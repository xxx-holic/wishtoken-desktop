package upstream

// The public BPS wire contract was cross-checked against bps-local-public
// (7134964). This bounded RFC 6455 client is independently implemented with
// the standard library. No browser signer, borrowed session or reconnection.
import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const maxBPSWSMessage = (16 << 20) - 1024

func bpsRequestID() string {
	var v [16]byte
	_, _ = rand.Read(v[:])
	v[6], v[8] = v[6]&15|64, v[8]&63|128
	return fmt.Sprintf("%x-%x-%x-%x-%x", v[:4], v[4:6], v[6:8], v[8:10], v[10:])
}

func bpsClientInfo() map[string]string {
	r, _ := http.NewRequest(http.MethodGet, "https://bps.openai.com", nil)
	ApplyBPSHeaders(r, Credentials{})
	profile := map[string]string{}
	for k := range r.Header {
		if strings.HasPrefix(strings.ToLower(k), "x-openai-internal-basispoints-") {
			profile[k] = r.Header.Get(k)
		}
	}
	if runtime.GOOS == "darwin" {
		profile["X-Openai-Internal-Basispoints-Client-Platform-Class"] = "Mac"
		profile["X-Openai-Internal-Basispoints-Office-Platform"] = "Mac"
	}
	profile["X-Openai-Internal-Basispoints-Browser-Name"] = "unknown"
	return profile
}

func wsAccept(key string) string {
	h := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(h[:])
}

func (c *BPSClient) responsesWebSocket(ctx context.Context, cred Credentials, body []byte) (*http.Response, error) {
	var payload map[string]json.RawMessage
	if json.Unmarshal(body, &payload) != nil || payload == nil {
		return nil, errors.New("invalid BPS WebSocket request")
	}
	id := bpsRequestID()
	payload["type"] = json.RawMessage(`"response.create"`)
	payload["basispoints_request_id"], _ = json.Marshal(id)
	var resume [16]byte
	_, _ = rand.Read(resume[:])
	payload["basispoints_resume_token"], _ = json.Marshal("resume_" + hex.EncodeToString(resume[:]))
	wire, err := json.Marshal(payload)
	if err != nil || len(wire) > maxBPSWSMessage {
		return nil, errors.New("BPS WebSocket request exceeds message limit")
	}
	u, err := url.Parse(c.responsesURL())
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Host == "" {
		return nil, errors.New("invalid BPS WebSocket endpoint")
	}
	profile := bpsClientInfo()
	info, _ := json.Marshal(profile)
	q := u.Query()
	q.Set("bps_client_info", string(info))
	q.Set("bps_auth_mode", "chatgpt")
	q.Set("bps_ws_affinity", id)
	q.Set("bps_control_frames", "upstream_sent,heartbeat")
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New("invalid BPS WebSocket request")
	}
	ApplyBPSHeaders(req, cred)
	for k, v := range profile {
		req.Header.Set(k, v)
	}
	var nonce [16]byte
	_, _ = rand.Read(nonce[:])
	key := base64.StdEncoding.EncodeToString(nonce[:])
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", key)
	req.Header.Set("Sec-WebSocket-Protocol", "responses, openai-bearer."+cred.AccessToken)
	base, err := client(cred, 60*time.Second)
	if err != nil {
		return nil, errors.New("invalid BPS proxy configuration")
	}
	// A scoped HTTP/1 transport preserves configured proxies and TLS validation.
	// The cached transport must never be mutated for an individual connection.
	transport := base.Transport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = false
	transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}}
	hc := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := hc.Do(req)
	if err != nil {
		transport.CloseIdleConnections()
		// Network errors and redirect URLs may contain the bearer subprotocol.
		return nil, errors.New("BPS WebSocket handshake failed; check credentials, proxy and transport support")
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		// Rejections may echo the bearer subprotocol. Preserve only HTTP status
		// and headers used for retry timing, never an untrusted error body.
		resp.Body.Close()
		transport.CloseIdleConnections()
		raw, _ := json.Marshal(map[string]any{"error": map[string]string{"code": "basispoints_ws_handshake_rejected", "message": fmt.Sprintf("BPS WebSocket handshake returned HTTP %d; verify authorization and selected transport", resp.StatusCode)}})
		resp.Body = io.NopCloser(bytes.NewReader(raw))
		return resp, nil
	}
	conn, ok := resp.Body.(io.ReadWriteCloser)
	protocol := resp.Header.Get("Sec-WebSocket-Protocol")
	if !ok || !strings.EqualFold(resp.Header.Get("Upgrade"), "websocket") || !headerToken(resp.Header.Get("Connection"), "upgrade") || resp.Header.Get("Sec-WebSocket-Accept") != wsAccept(key) || (protocol != "" && protocol != "responses") || resp.Header.Get("Sec-WebSocket-Extensions") != "" {
		resp.Body.Close()
		transport.CloseIdleConnections()
		return nil, errors.New("invalid BPS WebSocket handshake")
	}
	b := &bpsWSBody{conn: conn, ctx: ctx, idle: 2 * time.Minute, cleanup: transport.CloseIdleConnections}
	b.stop = context.AfterFunc(ctx, func() { _ = conn.Close() })
	timer := time.AfterFunc(30*time.Second, func() { _ = conn.Close() })
	err = writeWSFrame(conn, 1, wire)
	timer.Stop()
	if err != nil {
		b.Close()
		return nil, errors.New("could not send BPS response.create; request was not retried")
	}
	header := resp.Header.Clone()
	for _, k := range []string{"Connection", "Upgrade", "Sec-WebSocket-Accept", "Sec-WebSocket-Protocol", "Sec-WebSocket-Extensions"} {
		header.Del(k)
	}
	header.Set("Content-Type", "text/event-stream")
	header.Set("X-GPTBridge-BPS-Transport", "websocket")
	return &http.Response{StatusCode: http.StatusOK, Header: header, Body: b}, nil
}

func headerToken(value, want string) bool {
	for _, v := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(v), want) {
			return true
		}
	}
	return false
}

// Frames sent by a client MUST be masked, including pong control frames.
func writeWSFrame(w io.Writer, opcode byte, payload []byte) error {
	if len(payload) > maxBPSWSMessage {
		return errors.New("WebSocket message too large")
	}
	frame := make([]byte, 0, 14+len(payload))
	frame = append(frame, 0x80|opcode)
	n := len(payload)
	switch {
	case n < 126:
		frame = append(frame, 0x80|byte(n))
	case n <= 65535:
		frame = append(frame, 0x80|126, byte(n>>8), byte(n))
	default:
		frame = append(frame, 0x80|127)
		frame = binary.BigEndian.AppendUint64(frame, uint64(n))
	}
	var mask [4]byte
	_, _ = rand.Read(mask[:])
	frame = append(frame, mask[:]...)
	for i, v := range payload {
		frame = append(frame, v^mask[i%4])
	}
	for len(frame) > 0 {
		n, err := w.Write(frame)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return nil
}

// Only UTF-8 JSON text messages are supported; extension/compression frames
// were not offered. Fragmented text and interleaved ping/pong are supported.
func readWSMessage(rw io.ReadWriter) ([]byte, error) {
	var message []byte
	started := false
	for {
		var h [2]byte
		if _, err := io.ReadFull(rw, h[:]); err != nil {
			return nil, err
		}
		fin, op := h[0]&128 != 0, h[0]&15
		if h[0]&112 != 0 || h[1]&128 != 0 {
			return nil, errors.New("unexpected WebSocket flags")
		}
		n := uint64(h[1] & 127)
		if n == 126 {
			var b [2]byte
			if _, err := io.ReadFull(rw, b[:]); err != nil {
				return nil, err
			}
			n = uint64(binary.BigEndian.Uint16(b[:]))
			if n < 126 {
				return nil, errors.New("invalid frame length")
			}
		} else if n == 127 {
			var b [8]byte
			if _, err := io.ReadFull(rw, b[:]); err != nil {
				return nil, err
			}
			n = binary.BigEndian.Uint64(b[:])
			if n <= 65535 {
				return nil, errors.New("invalid frame length")
			}
		}
		control := op >= 8
		if n > maxBPSWSMessage || (!control && uint64(len(message))+n > maxBPSWSMessage) || (control && (!fin || n > 125)) {
			return nil, errors.New("invalid WebSocket frame length")
		}
		data := make([]byte, int(n))
		if _, err := io.ReadFull(rw, data); err != nil {
			return nil, err
		}
		switch op {
		case 8:
			return nil, io.ErrUnexpectedEOF
		case 9:
			if err := writeWSFrame(rw, 10, data); err != nil {
				return nil, err
			}
			continue
		case 10:
			continue
		case 1:
			if started {
				return nil, errors.New("interleaved WebSocket messages")
			}
			started = true
		case 0:
			if !started {
				return nil, errors.New("unexpected continuation")
			}
		default:
			return nil, errors.New("unsupported WebSocket opcode")
		}
		message = append(message, data...)
		if fin {
			if !utf8.Valid(message) {
				return nil, errors.New("invalid WebSocket UTF-8")
			}
			return message, nil
		}
	}
}

type bpsWSBody struct {
	conn     io.ReadWriteCloser
	ctx      context.Context
	idle     time.Duration
	stop     func() bool
	cleanup  func()
	once     sync.Once
	pending  bytes.Reader
	terminal bool
}

func (b *bpsWSBody) Close() error {
	b.once.Do(func() {
		if b.stop != nil {
			b.stop()
		}
		b.conn.Close()
		if b.cleanup != nil {
			b.cleanup()
		}
	})
	return nil
}
func (b *bpsWSBody) fail(code string) {
	raw, _ := json.Marshal(map[string]any{"type": "response.failed", "response": map[string]any{"status": "failed", "output": []any{}, "error": map[string]string{"code": code, "message": "BPS WebSocket ended without a valid terminal response; request was not retried"}}})
	b.pending.Reset(append(append([]byte("event: response.failed\ndata: "), raw...), '\n', '\n'))
	b.terminal = true
	b.Close()
}
func (b *bpsWSBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for b.pending.Len() == 0 {
		if b.terminal {
			b.Close()
			return 0, io.EOF
		}
		timedOut := make(chan struct{})
		timer := time.AfterFunc(b.idle, func() { close(timedOut); b.conn.Close() })
		raw, err := readWSMessage(b.conn)
		timer.Stop()
		if err != nil {
			if b.ctx.Err() != nil {
				b.Close()
				return 0, b.ctx.Err()
			}
			code := "basispoints_ws_disconnected"
			select {
			case <-timedOut:
				code = "basispoints_ws_timeout"
			default:
			}
			b.fail(code)
			continue
		}
		var event map[string]json.RawMessage
		if json.Unmarshal(raw, &event) != nil || event == nil {
			b.fail("basispoints_ws_protocol_error")
			continue
		}
		var kind string
		_ = json.Unmarshal(event["type"], &kind)
		switch kind {
		case "basispoints.response.resume_token", "basispoints.response.metadata", "basispoints.response.upstream_sent", "basispoints.response.heartbeat":
			continue
		case "basispoints.response.server_draining":
			b.fail("basispoints_ws_draining")
			continue
		}
		valid := len(kind) > 0 && len(kind) <= 128 && (kind == "error" || strings.HasPrefix(kind, "response."))
		for _, c := range kind {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '_') {
				valid = false
			}
		}
		if !valid {
			b.fail("basispoints_ws_protocol_error")
			continue
		}
		delete(event, "basispoints_replay_cursor")
		b.terminal = kind == "response.completed" || kind == "response.failed" || kind == "response.incomplete" || kind == "error"
		raw, _ = json.Marshal(event)
		b.pending.Reset([]byte("event: " + kind + "\ndata: " + string(raw) + "\n\n"))
	}
	return b.pending.Read(p)
}
