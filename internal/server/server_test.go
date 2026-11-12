package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xxx-holic/wishtoken-desktop/internal/account"
	"github.com/xxx-holic/wishtoken-desktop/internal/basispoints"
	"github.com/xxx-holic/wishtoken-desktop/internal/config"
	"github.com/xxx-holic/wishtoken-desktop/internal/testutil"
)

type fixture struct {
	srv       *Server
	api       *httptest.Server
	bpsHits   atomic.Int32
	codexHit  atomic.Int32
	bpsBody   atomic.Value // last decoded BPS body
	bpsHdr    atomic.Value
	codexHdr  atomic.Value
	codexBody atomic.Value
	codexMode atomic.Value
	bpsMode   atomic.Value // "ok" | "deny" | "rate_first"
}

func textStream(id, text string) string {
	return testutil.SSE(
		object{"type": "response.created", "response": object{"id": id, "status": "in_progress", "output": []any{}}},
		object{"type": "response.output_text.delta", "item_id": "msg", "output_index": 0, "delta": text},
		object{"type": "response.completed", "response": object{"id": id, "status": "completed", "output": []any{
			object{"type": "message", "id": "msg", "role": "assistant", "content": []any{object{"type": "output_text", "text": text}}},
		}, "usage": object{"input_tokens": 3, "output_tokens": 2}}},
	)
}

func newFixture(t *testing.T, cfg *config.Config, accounts ...account.Account) *fixture {
	t.Helper()
	f := &fixture{}
	f.bpsMode.Store("ok")
	f.codexMode.Store("ok")
	bps := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.bpsHits.Add(1)
		f.bpsHdr.Store(r.Header.Clone())
		raw, _ := io.ReadAll(r.Body)
		var body object
		_ = json.Unmarshal(raw, &body)
		f.bpsBody.Store(body)
		switch f.bpsMode.Load().(string) {
		case "policy_denied":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":"server_error","message":"403: This request was blocked by our usage policy."}}`))
			return
		case "incomplete":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, testutil.SSE(object{"type": "response.incomplete", "response": object{"status": "incomplete", "output": []any{}}}))
			return
		case "deny":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":"basispoints_model_access_changed","message":"model access changed"}}`))
			return
		case "rate_first":
			if r.Header.Get("Chatgpt-Account-Id") == "acct_one" {
				w.Header().Set("Retry-After", "120")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":{"code":"rate_limit_exceeded","message":"slow down"}}`))
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, textStream("resp_bps", "pong from bps"))
	}))
	t.Cleanup(bps.Close)
	codex := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.codexHit.Add(1)
		f.codexHdr.Store(r.Header.Clone())
		var body object
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.codexBody.Store(body)
		if f.codexMode.Load() == "deny" {
			w.WriteHeader(403)
			_, _ = io.WriteString(w, `{"error":{"message":"native unavailable"}}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/models") {
			_, _ = w.Write([]byte(`{"models":[{"slug":"gpt-6-astra"}]}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Codex-Turn-State", "fixture-healthy-state")
		w.Header().Set("X-Codex-Safety-Buffering-Enabled", "false")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, textStream("resp_codex", "pong from codex"))
	}))
	t.Cleanup(codex.Close)

	store, err := account.Open(filepath.Join(t.TempDir(), "accounts.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, acc := range accounts {
		if _, err := store.Upsert(acc); err != nil {
			t.Fatal(err)
		}
	}
	if cfg == nil {
		cfg = config.Default()
	}
	cfg.LogRequests = false
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	f.srv = New(cfg, filepath.Join(t.TempDir(), "config.json"), store)
	f.srv.Router.BPS.ResponsesURL = bps.URL + "/basispoints/api/responses"
	f.srv.Router.BPS.AttachmentsURL = bps.URL + "/basispoints/api/attachments"
	f.srv.Codex.BaseURL = codex.URL
	f.api = httptest.NewServer(f.srv.Handler())
	t.Cleanup(f.api.Close)
	return f
}

func testAccount(id, email string) account.Account {
	token := testutil.MakeJWT(testutil.ChatGPTClaims(email, id, "pro", time.Now().Add(2*time.Hour)))
	return account.Account{Email: email, AccountID: id, AccessToken: token, IDToken: token, RefreshToken: "rt_" + id + "_1234567890"}
}

func post(t *testing.T, url string, body any, headers map[string]string) (*http.Response, string) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp, string(out)
}

func TestChatCompletionsThroughBasispoints(t *testing.T) {
	f := newFixture(t, nil, testAccount("acct_one", "one@example.com"))
	resp, body := post(t, f.api.URL+"/v1/chat/completions", object{"model": "gpt-6-astra", "messages": []any{object{"role": "user", "content": "ping"}}}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if resp.Header.Get("X-GPTBridge-Upstream") != "basispoints" {
		t.Fatalf("expected basispoints upstream, headers=%v", resp.Header)
	}
	if !strings.Contains(body, "pong from bps") || !strings.Contains(body, `"object":"chat.completion"`) {
		t.Fatalf("body: %s", body)
	}
	hdr := f.bpsHdr.Load().(http.Header)
	if hdr.Get("X-Basispoints-Auth-Mode") != "chatgpt" || hdr.Get("Chatgpt-Account-Id") != "acct_one" || !strings.HasPrefix(hdr.Get("Authorization"), "Bearer eyJ") {
		t.Fatalf("bps headers: %v", hdr)
	}
	sent := f.bpsBody.Load().(object)
	if sent["model_selection"] != "explicit" || sent["reasoning_effort"] != "xhigh" {
		t.Fatalf("bps body: %v", sent)
	}
	if _, has := sent["tools"]; has {
		t.Fatalf("tools leaked natively: %v", sent)
	}
	acc := f.srv.Store.List()[0]
	if acc.Stats.Requests != 1 || acc.Stats.BPSRequests != 1 || acc.Stats.InputTokens != 3 {
		t.Fatalf("stats not recorded: %+v", acc.Stats)
	}
}

func TestFallbackToCodexOnModelDenial(t *testing.T) {
	legacy := config.Default()
	legacy.RoutePolicy = config.PolicyBPSPrefer
	legacy.NativeFallback = true
	f := newFixture(t, legacy, testAccount("acct_one", "one@example.com"))
	f.bpsMode.Store("deny")
	resp, body := post(t, f.api.URL+"/v1/chat/completions", object{"model": "gpt-6-astra", "messages": []any{object{"role": "user", "content": "ping"}}}, nil)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("X-GPTBridge-Upstream") != "codex" || !strings.Contains(body, "pong from codex") {
		t.Fatalf("fallback failed: %d %v %s", resp.StatusCode, resp.Header, body)
	}
	if f.bpsHits.Load() != 1 || f.codexHit.Load() != 1 {
		t.Fatalf("hits bps=%d codex=%d", f.bpsHits.Load(), f.codexHit.Load())
	}
	// The denial is remembered: the next request goes straight to Codex.
	f.bpsMode.Store("ok")
	resp, _ = post(t, f.api.URL+"/v1/chat/completions", object{"model": "gpt-6-astra", "messages": []any{object{"role": "user", "content": "ping"}}}, nil)
	if resp.Header.Get("X-GPTBridge-Upstream") != "codex" || f.bpsHits.Load() != 1 {
		t.Fatalf("denial not remembered: %v bps=%d", resp.Header, f.bpsHits.Load())
	}
	// bps_only refuses fallback.
	cfg := config.Default()
	cfg.RoutePolicy = config.PolicyBPSOnly
	g := newFixture(t, cfg, testAccount("acct_two", "two@example.com"))
	g.bpsMode.Store("deny")
	resp, body = post(t, g.api.URL+"/v1/chat/completions", object{"model": "gpt-6-astra", "messages": []any{object{"role": "user", "content": "ping"}}}, nil)
	if resp.StatusCode != http.StatusForbidden || g.codexHit.Load() != 0 {
		t.Fatalf("bps_only should fail without fallback: %d %s codex=%d", resp.StatusCode, body, g.codexHit.Load())
	}
}

func TestRateLimitedAccountIsSkipped(t *testing.T) {
	cfg := config.Default()
	cfg.Scheduler = config.SchedulerLeastUsed
	f := newFixture(t, cfg, testAccount("acct_one", "one@example.com"), testAccount("acct_two", "two@example.com"))
	f.bpsMode.Store("rate_first")
	resp, body := post(t, f.api.URL+"/v1/chat/completions", object{"model": "gpt-6-astra", "messages": []any{object{"role": "user", "content": "ping"}}}, nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "pong from bps") {
		t.Fatalf("expected second account to serve: %d %s", resp.StatusCode, body)
	}
	var one account.Account
	for _, acc := range f.srv.Store.List() {
		if acc.AccountID == "acct_one" {
			one = acc
		}
	}
	if _, _, cooling := f.srv.Pool.Cooldown(one.ID); !cooling || one.Stats.RateLimits != 1 {
		t.Fatalf("first account should be cooling down: cooling=%v stats=%+v", cooling, one.Stats)
	}
}

func TestResponsesNativePassThroughWithCodexClient(t *testing.T) {
	cfg := config.Default()
	cfg.RoutePolicy = config.PolicyCodexOnly
	f := newFixture(t, cfg, testAccount("acct_one", "one@example.com"))
	headers := map[string]string{"User-Agent": "codex-tui/0.153.3 (Mac OS 15.5.0; arm64) xterm-256color (codex-tui; 0.153.3)", "Originator": "codex-tui", "session-id": "sess-123", "X-Codex-Turn-State": "fixture-client-state"}
	resp, body := post(t, f.api.URL+"/v1/responses", object{"model": "gpt-6-astra", "input": "ping", "stream": true, "instructions": "x"}, headers)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "pong from codex") || !strings.Contains(body, "event: response.completed") {
		t.Fatalf("native stream: %d %s", resp.StatusCode, body)
	}
	if resp.Header.Get("X-Codex-Turn-State") != "fixture-healthy-state" {
		t.Fatalf("turn-state not forwarded downstream: %v", resp.Header)
	}
	hdr := f.codexHdr.Load().(http.Header)
	if hdr.Get("Originator") != "codex-tui" || hdr.Get("Session-Id") != "sess-123" || hdr.Get("Chatgpt-Account-Id") != "acct_one" || hdr.Get("X-Codex-Beta-Features") == "" {
		t.Fatalf("codex headers: %v", hdr)
	}
	if hdr.Get("X-Codex-Turn-State") != "fixture-client-state" {
		t.Fatalf("turn-state not forwarded upstream: %v", hdr)
	}
	if f.bpsHits.Load() != 0 {
		t.Fatal("codex_only must not touch basispoints")
	}
	// Non-streaming Responses aggregate to JSON.
	resp, body = post(t, f.api.URL+"/v1/responses", object{"model": "gpt-6-astra", "input": "ping", "stream": false}, nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"status":"completed"`) || strings.Contains(body, "event:") {
		t.Fatalf("aggregate: %d %s", resp.StatusCode, body)
	}
}

func TestAnthropicMessagesStreaming(t *testing.T) {
	f := newFixture(t, nil, testAccount("acct_one", "one@example.com"))
	resp, body := post(t, f.api.URL+"/v1/messages", object{"model": "claude-sonnet-4-5", "max_tokens": 100, "stream": true, "messages": []any{object{"role": "user", "content": "ping"}}}, map[string]string{"x-api-key": "anything", "anthropic-version": "2023-06-01"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	for _, needle := range []string{"event: message_start", `"text":"pong from bps"`, `"stop_reason":"end_turn"`, "event: message_stop"} {
		if !strings.Contains(body, needle) {
			t.Fatalf("missing %s: %s", needle, body)
		}
	}
	sent := f.bpsBody.Load().(object)
	if sent["model"] != "gpt-5.6-sol" {
		t.Fatalf("claude sonnet should map to gpt-5.6-sol, got %v", sent["model"])
	}
	resp, body = post(t, f.api.URL+"/v1/messages/count_tokens", object{"model": "claude-sonnet-4-5", "messages": []any{object{"role": "user", "content": "ping"}}}, nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "input_tokens") {
		t.Fatalf("count_tokens: %d %s", resp.StatusCode, body)
	}
}

func TestAPIKeyAndNoAccounts(t *testing.T) {
	cfg := config.Default()
	cfg.APIKey = "secret-key"
	f := newFixture(t, cfg)
	resp, body := post(t, f.api.URL+"/v1/chat/completions", object{"model": "gpt-6-astra", "messages": []any{object{"role": "user", "content": "ping"}}}, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without key, got %d %s", resp.StatusCode, body)
	}
	resp, body = post(t, f.api.URL+"/v1/chat/completions", object{"model": "gpt-6-astra", "messages": []any{object{"role": "user", "content": "ping"}}}, map[string]string{"Authorization": "Bearer secret-key"})
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, "no_accounts") {
		t.Fatalf("expected no_accounts 503, got %d %s", resp.StatusCode, body)
	}
}

func TestManagementImportAndStatus(t *testing.T) {
	f := newFixture(t, nil)
	token := testutil.MakeJWT(testutil.ChatGPTClaims("imp@example.com", "acct_imp", "plus", time.Now().Add(time.Hour)))
	payload := object{"text": `{"tokens":{"access_token":"` + token + `","refresh_token":"rt_imp_1234567890","account_id":"acct_imp"}}`}
	resp, body := post(t, f.api.URL+"/api/accounts/import?refresh=0", payload, nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"imported":1`) {
		t.Fatalf("import: %d %s", resp.StatusCode, body)
	}
	r, err := http.Get(f.api.URL + "/api/accounts")
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if strings.Contains(string(out), "rt_imp_1234567890") || strings.Contains(string(out), token) {
		t.Fatalf("management API leaked secrets: %s", out)
	}
	if !strings.Contains(string(out), "imp@example.com") || !strings.Contains(string(out), `"status":"ready"`) {
		t.Fatalf("accounts view: %s", out)
	}
	r, _ = http.Get(f.api.URL + "/api/status")
	out, _ = io.ReadAll(r.Body)
	r.Body.Close()
	if !strings.Contains(string(out), `"total":1`) {
		t.Fatalf("status: %s", out)
	}
	r, _ = http.Get(f.api.URL + "/v1/models")
	out, _ = io.ReadAll(r.Body)
	r.Body.Close()
	if !strings.Contains(string(out), basispoints.Catalog[0].ID) {
		t.Fatalf("models: %s", out)
	}
	r, _ = http.Get(f.api.URL + "/")
	out, _ = io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != http.StatusOK || !strings.Contains(string(out), "GPTBridge") {
		t.Fatalf("dashboard: %d", r.StatusCode)
	}
}

func TestToolCallRoundTripViaChat(t *testing.T) {
	f := newFixture(t, nil, testAccount("acct_one", "one@example.com"))
	// Replace the BPS handler behaviour: emit a run_officejs call.
	args, _ := json.Marshal(object{"code": `{"name":"get_weather","arguments":{"city":"Tokyo"}}`, "summary": "weather", "extended_summary": "", "destructive": false, "references": []any{}})
	native := object{"type": "function_call", "id": "fc_n", "call_id": "call_n", "name": basispoints.TransportTool, "arguments": string(args), "status": "completed"}
	stream := testutil.SSE(
		object{"type": "response.created", "response": object{"id": "r", "status": "in_progress", "output": []any{}}},
		object{"type": "response.output_item.done", "output_index": 0, "item": native},
		object{"type": "response.completed", "response": object{"id": "r", "status": "completed", "output": []any{native}, "usage": object{"input_tokens": 1, "output_tokens": 1}}},
	)
	toolServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, stream)
	}))
	defer toolServer.Close()
	f.srv.Router.BPS.ResponsesURL = toolServer.URL
	tools := []any{object{"type": "function", "function": object{"name": "get_weather", "parameters": object{"type": "object", "properties": object{"city": object{"type": "string"}}}}}}
	resp, body := post(t, f.api.URL+"/v1/chat/completions", object{"model": "gpt-6-astra", "tools": tools, "messages": []any{object{"role": "user", "content": "weather?"}}}, nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"name":"get_weather"`) || !strings.Contains(body, `"finish_reason":"tool_calls"`) || strings.Contains(body, basispoints.TransportTool) {
		t.Fatalf("tool round trip: %d %s", resp.StatusCode, body)
	}
}
