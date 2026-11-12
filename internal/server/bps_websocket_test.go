package server

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xxx-holic/wishtoken-desktop/internal/config"
)

func TestDesktopBPSWebSocketThroughResponsesAPI(t *testing.T) {
	cfg := config.Default()
	cfg.DesktopMode, cfg.APIKey, cfg.BPSTransport = true, "fixture-local-key", "websocket"
	f := newFixture(t, cfg, testAccount("fixture-account", "fixture@example.test"))
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Method != http.MethodGet || r.Header.Get("Chatgpt-Account-Id") != "fixture-account" {
			t.Error("lost explicit identity")
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		digest := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: %s\r\nSec-WebSocket-Protocol: responses\r\n\r\n", base64.StdEncoding.EncodeToString(digest[:]))
		rw.Flush()
		var h [2]byte
		if _, err = io.ReadFull(rw, h[:]); err != nil {
			t.Error(err)
			return
		}
		n := uint64(h[1] & 127)
		if n == 126 {
			var v [2]byte
			io.ReadFull(rw, v[:])
			n = uint64(binary.BigEndian.Uint16(v[:]))
		} else if n == 127 {
			var v [8]byte
			io.ReadFull(rw, v[:])
			n = binary.BigEndian.Uint64(v[:])
		}
		if n > 65535 || h[0] != 129 || h[1]&128 == 0 {
			t.Error("invalid request frame")
			return
		}
		var mask [4]byte
		io.ReadFull(rw, mask[:])
		raw := make([]byte, int(n))
		io.ReadFull(rw, raw)
		for i := range raw {
			raw[i] ^= mask[i%4]
		}
		var prepared object
		if json.Unmarshal(raw, &prepared) != nil || prepared["model"] != "gpt-6-astra" || prepared["reasoning_effort"] != "medium" || prepared["type"] != "response.create" {
			t.Error("request not prepared correctly")
			return
		}
		response := object{"type": "response.completed", "response": object{"id": "resp_fixture", "model": "gpt-6-astra", "status": "completed", "output": []any{object{"id": "msg_fixture", "type": "message", "role": "assistant", "status": "completed", "content": []any{object{"type": "output_text", "text": "fixture result"}}}}, "usage": object{"input_tokens": 100, "input_tokens_details": object{"cached_tokens": 80}, "output_tokens": 12, "output_tokens_details": object{"reasoning_tokens": 7}}}}
		data, _ := json.Marshal(response)
		conn.Write(append([]byte{129, 126, byte(len(data) >> 8), byte(len(data))}, data...))
	}))
	defer upstream.Close()
	f.srv.Router.BPS.ResponsesURL = upstream.URL
	id := f.srv.Store.List()[0].ID
	resp, body := post(t, f.api.URL+"/v1/responses", object{"model": "gpt-6-astra", "input": "fixture request", "reasoning": object{"effort": "medium"}, "stream": false}, map[string]string{"Authorization": "Bearer fixture-local-key", "X-GPTBridge-Account": id, "X-GPTBridge-Channel": "bps"})
	if resp.StatusCode != 200 || !strings.Contains(body, "fixture result") || resp.Header.Get("X-GPTBridge-BPS-Transport") != "websocket" {
		t.Fatalf("API result: %d %s", resp.StatusCode, body)
	}
	records := f.srv.logs.list(5)
	if len(records) != 1 {
		t.Fatalf("records=%d", len(records))
	}
	r := records[0]
	if r.Transport != "websocket" || r.StreamStatus != "completed" || r.Quality != "model_match" || r.InputTokens != 100 || r.CachedInputTokens != 80 || r.OutputTokens != 12 || r.ReasoningTokens != 7 {
		t.Fatalf("observations lost: %+v", r)
	}
	if hits.Load() != 1 || f.codexHit.Load() != 0 || f.bpsHits.Load() != 0 {
		t.Fatal("unexpected retry or fallback")
	}
}

func TestDesktopBPSEarlyFailureIsNotHTTPSuccess(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			cfg := config.Default()
			cfg.DesktopMode, cfg.APIKey = true, "fixture-local-key"
			f := newFixture(t, cfg, testAccount("fixture-account", "fixture@example.test"))
			var hits atomic.Int32
			// This is the first SSE event emitted by the WebSocket adapter when
			// the upgraded peer disconnects before any response output arrives.
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"basispoints_ws_disconnected\",\"message\":\"fixture disconnected before completion\"}}}\n\n")
			}))
			defer upstream.Close()
			f.srv.Router.BPS.ResponsesURL = upstream.URL
			id := f.srv.Store.List()[0].ID
			resp, body := post(t, f.api.URL+"/v1/responses", object{"model": "gpt-6-astra", "input": "fixture request", "stream": stream}, map[string]string{"Authorization": "Bearer fixture-local-key", "X-GPTBridge-Account": id, "X-GPTBridge-Channel": "bps"})
			if resp.StatusCode != http.StatusBadGateway || !strings.Contains(body, "fixture disconnected") {
				t.Fatalf("early failure reported as success: %d %s", resp.StatusCode, body)
			}
			records := f.srv.logs.list(5)
			if len(records) != 1 || records[0].StreamStatus != "failed" || records[0].Error == "" {
				t.Fatalf("failure observation missing: %+v", records)
			}
			if hits.Load() != 1 || f.codexHit.Load() != 0 {
				t.Fatal("failed request retried or changed channel")
			}
		})
	}
}
