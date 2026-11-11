package upstream

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Independent test peer: checks the client mask and decodes all length forms.
func readClientFrame(r io.Reader) (byte, []byte, error) {
	var h [2]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return 0, nil, err
	}
	if h[0]&128 == 0 || h[1]&128 == 0 {
		return 0, nil, fmt.Errorf("client frame is not FIN+masked")
	}
	n := uint64(h[1] & 127)
	if n == 126 {
		var b [2]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, nil, err
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	} else if n == 127 {
		var b [8]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, nil, err
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	if n > maxBPSWSMessage {
		return 0, nil, fmt.Errorf("oversize")
	}
	var mask [4]byte
	if _, err := io.ReadFull(r, mask[:]); err != nil {
		return 0, nil, err
	}
	p := make([]byte, int(n))
	if _, err := io.ReadFull(r, p); err != nil {
		return 0, nil, err
	}
	for i := range p {
		p[i] ^= mask[i%4]
	}
	return h[0] & 15, p, nil
}

func serverFrame(w io.Writer, first byte, data string) {
	h := []byte{first}
	if len(data) < 126 {
		h = append(h, byte(len(data)))
	} else {
		h = append(h, 126, byte(len(data)>>8), byte(len(data)))
	}
	_, _ = w.Write(append(h, []byte(data)...))
}

func upgradeFixture(t *testing.T, w http.ResponseWriter, r *http.Request) (net.Conn, *bytes.Buffer) {
	t.Helper()
	c, rw, err := w.(http.Hijacker).Hijack()
	if err != nil {
		t.Error(err)
		return nil, nil
	}
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: %s\r\nSec-WebSocket-Protocol: responses\r\n\r\n", wsAccept(r.Header.Get("Sec-WebSocket-Key")))
	_ = rw.Flush()
	op, data, err := readClientFrame(rw)
	if err != nil || op != 1 {
		t.Errorf("request frame: %d %v", op, err)
		c.Close()
		return nil, nil
	}
	return c, bytes.NewBuffer(data)
}

func TestBPSWebSocketHandshakeStreamingAndControlFrames(t *testing.T) {
	checks := make(chan bool, 1)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer fixture" || r.Header.Get("Chatgpt-Account-Id") != "fixture-account" || r.Header.Get("Origin") != "https://bps.openai.com" || r.URL.Query().Get("bps_auth_mode") != "chatgpt" || strings.Contains(r.URL.String(), "fixture") {
			t.Error("wrong handshake identity or token leaked into URL")
		}
		var profile map[string]string
		_ = json.Unmarshal([]byte(r.URL.Query().Get("bps_client_info")), &profile)
		if profile["X-Openai-Internal-Basispoints-Client-Editor"] != "excel" {
			t.Error("missing client profile")
		}
		c, body := upgradeFixture(t, w, r)
		if c == nil {
			return
		}
		defer c.Close()
		var request map[string]any
		_ = json.Unmarshal(body.Bytes(), &request)
		if request["type"] != "response.create" || request["model"] != "gpt-6-astra" || request["reasoning_effort"] != "medium" || request["input"] != "unchanged" || request["basispoints_request_id"] != r.URL.Query().Get("bps_ws_affinity") {
			t.Error("prepared request changed")
		}
		serverFrame(c, 129, `{"type":"basispoints.response.resume_token","token":"private-control"}`)
		serverFrame(c, 129, `{"type":"basispoints.response.heartbeat"}`)
		serverFrame(c, 1, `{"type":"response.output_text.delta",`)
		serverFrame(c, 137, "ping")
		op, pong, err := readClientFrame(c)
		if err != nil || op != 10 || string(pong) != "ping" {
			t.Errorf("pong: %d %q %v", op, pong, err)
		}
		serverFrame(c, 128, `"delta":"hello","basispoints_replay_cursor":"private-cursor"}`)
		serverFrame(c, 129, `{"type":"response.completed","response":{"status":"completed","model":"gpt-6-astra","output":[]}}`)
		checks <- true
	}))
	defer s.Close()
	c := BPSClient{ResponsesURL: s.URL, Transport: "websocket"}
	r, err := c.Responses(context.Background(), Credentials{AccessToken: "fixture", AccountID: "fixture-account"}, []byte(`{"model":"gpt-6-astra","reasoning_effort":"medium","input":"unchanged"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if r.StatusCode != 200 || r.Header.Get("X-GPTBridge-BPS-Transport") != "websocket" || !bytes.Contains(data, []byte("event: response.completed")) || !bytes.Contains(data, []byte(`"delta":"hello"`)) || bytes.Contains(data, []byte("private-")) {
		t.Fatalf("invalid translated stream: %s", data)
	}
	select {
	case <-checks:
	case <-time.After(time.Second):
		t.Fatal("fixture did not finish")
	}
}

func TestBPSWebSocketRejectionDoesNotRetryOrFollowRedirect(t *testing.T) {
	for _, status := range []int{401, 403, 429, 302} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", "http://invalid.test/")
				w.Header().Set("Retry-After", "9")
				w.WriteHeader(status)
				fmt.Fprint(w, `{"error":{"code":"fixture_rejected","message":"openai-bearer.fixture-secret"}}`)
			}))
			defer s.Close()
			c := BPSClient{ResponsesURL: s.URL, Transport: "websocket"}
			r, err := c.Responses(context.Background(), Credentials{AccessToken: "fixture", AccountID: "fixture-account"}, []byte(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			defer r.Body.Close()
			body, _ := io.ReadAll(r.Body)
			if bytes.Contains(body, []byte("fixture-secret")) {
				t.Fatal("handshake rejection echoed credentials")
			}
			if r.StatusCode != status || calls.Load() != 1 || r.Header.Get("Retry-After") != "9" {
				t.Fatal("rejection changed or request replayed")
			}
		})
	}
}

func TestBPSWebSocketUsesConfiguredHTTPProxy(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "no-direct-route.invalid" {
			t.Error("proxy target lost")
		}
		c, _ := upgradeFixture(t, w, r)
		if c == nil {
			return
		}
		defer c.Close()
		serverFrame(c, 129, `{"type":"response.completed","response":{"status":"completed"}}`)
	}))
	defer proxy.Close()
	c := BPSClient{ResponsesURL: "http://no-direct-route.invalid/responses", Transport: "websocket"}
	r, err := c.Responses(context.Background(), Credentials{AccessToken: "fixture", AccountID: "fixture-account", ProxyURL: proxy.URL}, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	data, err := io.ReadAll(r.Body)
	if err != nil || !bytes.Contains(data, []byte("response.completed")) {
		t.Fatalf("proxy result %s %v", data, err)
	}
}

func TestWebSocketMaskAndLengthBoundaries(t *testing.T) {
	for _, size := range []int{0, 125, 126, 127, 65535, 65536} {
		var b bytes.Buffer
		want := bytes.Repeat([]byte("a"), size)
		if err := writeWSFrame(&b, 1, want); err != nil {
			t.Fatal(err)
		}
		op, got, err := readClientFrame(&b)
		if err != nil || op != 1 || !bytes.Equal(got, want) {
			t.Fatalf("length %d: %v", size, err)
		}
	}
}

type fixtureRW struct{ *bytes.Buffer }

func (fixtureRW) Close() error { return nil }

func TestWebSocketMalformedAndPrematureCloseNeverComplete(t *testing.T) {
	cases := [][]byte{{0x81, 0x80}, {0x82, 0}, {0x80, 0}, {0x89, 126, 0, 126}, {0x81, 127, 0x80, 0, 0, 0, 0, 0, 0, 0}, {0x81, 2, 0xff, 0xff}, {0x88, 0}}
	for _, frame := range cases {
		b := &bpsWSBody{conn: fixtureRW{bytes.NewBuffer(frame)}, ctx: context.Background(), idle: time.Second}
		data, err := io.ReadAll(b)
		if err != nil || !bytes.Contains(data, []byte("response.failed")) || bytes.Contains(data, []byte("response.completed")) {
			t.Fatalf("bad frame accepted: %x %s %v", frame, data, err)
		}
	}
}

func TestWebSocketCancellationAndIdleTimeout(t *testing.T) {
	for _, cancelRequest := range []bool{false, true} {
		c, s := net.Pipe()
		ctx, cancel := context.WithCancel(context.Background())
		b := &bpsWSBody{conn: c, ctx: ctx, idle: 25 * time.Millisecond}
		b.stop = context.AfterFunc(ctx, func() { c.Close() })
		if cancelRequest {
			cancel()
		}
		data, err := io.ReadAll(b)
		if cancelRequest {
			if err != context.Canceled {
				t.Errorf("cancellation: %v", err)
			}
		} else if err != nil || !bytes.Contains(data, []byte("basispoints_ws_timeout")) {
			t.Errorf("idle: %s %v", data, err)
		}
		b.Close()
		cancel()
		s.Close()
	}
}

func TestWebSocketRejectsInvalidHandshakeWithoutCredentialEcho(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, rw, _ := w.(http.Hijacker).Hijack()
		defer c.Close()
		fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: invalid\r\nSec-WebSocket-Protocol: openai-bearer.fixture-secret\r\n\r\n")
		rw.Flush()
	}))
	defer s.Close()
	c := BPSClient{ResponsesURL: s.URL, Transport: "websocket"}
	_, err := c.Responses(context.Background(), Credentials{AccessToken: "fixture-secret", AccountID: "fixture-account"}, []byte(`{}`))
	if err == nil || strings.Contains(err.Error(), "fixture-secret") {
		t.Fatalf("invalid handshake or secret echo: %v", err)
	}
}
