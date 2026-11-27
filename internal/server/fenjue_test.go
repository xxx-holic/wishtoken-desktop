package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xxx-holic/wishtoken-desktop/internal/account"
	"github.com/xxx-holic/wishtoken-desktop/internal/fenjue"
)

func TestFenjueResponsesExplicitAccountAndPayload(t *testing.T) {
	f := newLicensedFenjueFixture(t, nil, account.Account{ID: "acc-fenjue", Provider: "fenjue", AccountID: "fenjue-fixture", AccessToken: "synthetic-fenjue-credential"}, testAccount("native-fixture", "fixture@example.test"))
	var posts atomic.Int32
	var hits atomic.Int32
	var failed atomic.Bool
	var sentMu sync.Mutex
	var sent object
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Authorization") != "Bearer synthetic-fenjue-credential" {
			t.Error("wrong upstream credential")
		}
		switch r.URL.Path {
		case "/api/trpc/openAiChatGpt.status":
			fmt.Fprint(w, `{"result":{"data":{"state":"connected"}}}`)
		case "/models":
			fmt.Fprint(w, `{"data":[{"id":"openai/gpt-6-astra","hasUserByokAvailable":true}]}`)
		case "/responses":
			posts.Add(1)
			var decoded object
			_ = json.NewDecoder(r.Body).Decode(&decoded)
			if decoded["prompt_cache_key"] != r.Header.Get("X-Session-Id") || decoded["prompt_cache_key"] == "" {
				t.Error("body cache identity differs from request session")
			}
			sentMu.Lock()
			sent = decoded
			sentMu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			if failed.Load() {
				fmt.Fprint(w, "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"fixture_failure\"}}}\n\n")
				return
			}
			stream := textStream("fenjue-fixture-response", "fixture output")
			stream = strings.ReplaceAll(stream, `"input_tokens":3`, `"input_tokens":3,"is_byok":true,"cost":0`)
			fmt.Fprint(w, stream)
		default:
			t.Error("unexpected endpoint")
		}
	}))
	defer up.Close()
	f.srv.Router.Fenjue = &fenjue.Client{APIURL: up.URL, GatewayURL: up.URL}
	input := []any{object{"role": "user", "content": []any{object{"type": "input_text", "text": "fixture"}, object{"type": "input_image", "image_url": "data:image/png;base64,fixture"}}}}
	tools := []any{object{"type": "function", "name": "read_fixture", "parameters": object{"type": "object"}}}
	body := object{"model": "gpt-6-astra", "input": input, "tools": tools, "reasoning": object{"effort": "medium"}, "instructions": "caller-owned", "stream": false}
	h := map[string]string{"X-GPTBridge-Account": "acc-fenjue", "X-GPTBridge-Channel": "fenjue"}
	resp, raw := post(t, f.api.URL+"/v1/responses", body, h)
	if resp.StatusCode != 200 || !strings.Contains(raw, "fixture output") || posts.Load() != 1 {
		t.Fatalf("status=%d body=%s posts=%d", resp.StatusCode, raw, posts.Load())
	}
	sentMu.Lock()
	observed := sent
	sentMu.Unlock()
	for _, key := range []string{"input", "tools", "instructions", "reasoning"} {
		a, _ := json.Marshal(body[key])
		b, _ := json.Marshal(observed[key])
		if string(a) != string(b) {
			t.Fatalf("changed %s", key)
		}
	}
	if observed["model"] != "openai/gpt-6-astra" || observed["store"] != false || observed["stream"] != true {
		t.Fatal("wire shape")
	}
	records := f.srv.logs.list(10)
	found := false
	for _, r := range records {
		if r.Route == "fenjue" && r.FenjueBYOK != nil && *r.FenjueBYOK && r.FenjueCost != nil && *r.FenjueCost == 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("missing BYOK/cost observations")
	}
	before := posts.Load()
	legacy := string([]byte{107, 105, 108, 111})
	legacyHeaders := map[string]string{"X-GPTBridge-Account": "acc-fenjue", "X-GPTBridge-Channel": legacy}
	response, _ := post(t, f.api.URL+"/v1/responses", body, legacyHeaders)
	if response.StatusCode != 200 || posts.Load() != before+1 {
		t.Fatal("legacy header route broke")
	}
	legacyBody := object{}
	for key, value := range body {
		legacyBody[key] = value
	}
	legacyBody["model"] = "gpt-6-astra-" + legacy
	response, _ = post(t, f.api.URL+"/v1/responses", legacyBody, map[string]string{"X-GPTBridge-Account": "acc-fenjue"})
	if response.StatusCode != 200 || posts.Load() != before+2 {
		t.Fatal("legacy model route broke")
	}
	before = posts.Load()
	hitsBefore := hits.Load()
	for _, change := range []object{{"store": true}, {"background": true}, {"service_tier": "priority"}, {"reasoning": object{"effort": "max"}}, {"model": "fenjue-auto/free"}, {"previous_response_id": "resp_unavailable"}, {"input": []any{object{"type": "item_reference", "id": "msg_unavailable"}}}} {
		b := object{}
		for k, v := range body {
			b[k] = v
		}
		for k, v := range change {
			b[k] = v
		}
		response, _ := post(t, f.api.URL+"/v1/responses", b, h)
		if response.StatusCode != 400 {
			t.Fatal(response.StatusCode)
		}
	}
	if posts.Load() != before || hits.Load() != hitsBefore {
		t.Fatal("invalid request sent upstream or submitted credentials")
	}
	failed.Store(true)
	resp, _ = post(t, f.api.URL+"/v1/responses", body, h)
	if resp.StatusCode != 502 || posts.Load() != before+1 {
		t.Fatal("failed event masked or replayed")
	}
	if f.bpsHits.Load() != 0 || f.codexHit.Load() != 0 {
		t.Fatal("channel fallback")
	}
	h["X-GPTBridge-Channel"] = "codex"
	resp, _ = post(t, f.api.URL+"/v1/responses", body, h)
	if resp.StatusCode == 200 || f.codexHit.Load() != 0 {
		t.Fatal("Fenjue credential used for native route")
	}
}

func TestFenjueStatelessToolContinuationUsesSameIdentityAndEncryptedReasoning(t *testing.T) {
	f := newLicensedFenjueFixture(t, nil,
		account.Account{ID: "acc-fenjue-a", Provider: "fenjue", AccountID: "identity-a", AccessToken: "synthetic-token-a"},
		account.Account{ID: "acc-fenjue-b", Provider: "fenjue", AccountID: "identity-b", AccessToken: "synthetic-token-b"})
	var posts atomic.Int32
	var sessionsMu sync.Mutex
	var sessions []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/trpc/openAiChatGpt.status":
			fmt.Fprint(w, `{"result":{"data":{"state":"connected","email":"fixture@example.test"}}}`)
		case "/models":
			fmt.Fprint(w, `{"data":[{"id":"openai/gpt-6-astra","hasUserByokAvailable":true}]}`)
		case "/responses":
			n := posts.Add(1)
			var body object
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			session := r.Header.Get("X-Session-Id")
			sessionsMu.Lock()
			sessions = append(sessions, session)
			sessionsMu.Unlock()
			if session == "" || session != body["prompt_cache_key"] || session != r.Header.Get("X-Session-Affinity") {
				t.Error("session/cache mismatch")
			}
			if body["client_metadata"] != nil || body["thread_id"] != nil || body["instructions"] != "unchanged instruction" || body["reasoning"].(map[string]any)["effort"] != "medium" {
				t.Error("metadata or caller semantics changed")
			}
			if n == 2 {
				items := body["input"].([]any)
				if len(items) != 4 {
					t.Error("lost full tool history")
				} else {
					reasoning, call, result := items[1].(map[string]any), items[2].(map[string]any), items[3].(map[string]any)
					if reasoning["encrypted_content"] != "fixture-encrypted-reasoning" || reasoning["id"] != nil || call["id"] != nil || call["synthetic"] != nil || call["arguments"] != "{}" || call["call_id"] != result["call_id"] || result["output"] != "fixture-nonce-111" {
						t.Error("stateless reasoning or tool result changed")
					}
				}
			}
			w.Header().Set("Content-Type", "text/event-stream")
			if n == 1 {
				fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_fixture\",\"status\":\"completed\",\"model\":\"openai/gpt-6-astra\",\"output\":[{\"type\":\"reasoning\",\"id\":\"rs_fixture\",\"summary\":[],\"encrypted_content\":\"fixture-encrypted-reasoning\"},{\"type\":\"function_call\",\"id\":\"fc_fixture\",\"call_id\":\"call_fixture\",\"name\":\"read_fixture\",\"arguments\":\"\"}]}}\n\n")
			} else {
				fmt.Fprint(w, textStream("resp_final", "fixture-nonce-111"))
			}
		default:
			t.Error("unexpected endpoint")
		}
	}))
	defer up.Close()
	f.srv.Router.Fenjue = &fenjue.Client{APIURL: up.URL, GatewayURL: up.URL}
	body := object{"model": "gpt-6-astra", "reasoning": object{"effort": "medium"}, "instructions": "unchanged instruction", "stream": false,
		"client_metadata": object{"thread_id": "thread-a"}, "include": []any{"reasoning.encrypted_content"},
		"tools": []any{object{"type": "function", "name": "read_fixture", "parameters": object{"type": "object"}}},
		"input": []any{object{"role": "user", "content": "read the fixture"}}}
	headers := map[string]string{"X-GPTBridge-Account": "acc-fenjue-a", "X-GPTBridge-Channel": "fenjue"}
	response, raw := post(t, f.api.URL+"/v1/responses", body, headers)
	var first object
	if response.StatusCode != 200 || json.Unmarshal([]byte(raw), &first) != nil {
		t.Fatal("first tool turn did not complete")
	}
	output, ok := first["output"].([]any)
	if !ok || len(output) != 2 {
		t.Fatal("response did not retain reasoning and function call")
	}
	output[1].(map[string]any)["synthetic"] = true
	items := append(body["input"].([]any), output...)
	body["input"] = append(items, object{"type": "function_call_output", "call_id": "call_fixture", "output": "fixture-nonce-111"})
	response, raw = post(t, f.api.URL+"/v1/responses", body, headers)
	if response.StatusCode != 200 || !strings.Contains(raw, "fixture-nonce-111") {
		t.Fatal("tool continuation failed")
	}
	body["client_metadata"] = object{"thread_id": "thread-b"}
	response, _ = post(t, f.api.URL+"/v1/responses", body, headers)
	if response.StatusCode != 200 {
		t.Fatal("second conversation failed")
	}
	body["client_metadata"] = object{"thread_id": "thread-a"}
	headers["X-GPTBridge-Account"] = "acc-fenjue-b"
	response, _ = post(t, f.api.URL+"/v1/responses", body, headers)
	if response.StatusCode != 200 {
		t.Fatal("second account failed")
	}
	sessionsMu.Lock()
	defer sessionsMu.Unlock()
	if len(sessions) != 4 || sessions[0] != sessions[1] || sessions[0] == sessions[2] || sessions[0] == sessions[3] {
		t.Fatal("conversation or account isolation lost", sessions)
	}
	if f.bpsHits.Load() != 0 || f.codexHit.Load() != 0 {
		t.Fatal("unexpected channel fallback")
	}
}

func TestFenjueCancelledPollCannotPersistGrant(t *testing.T) {
	f := newLicensedFenjueFixture(t, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/device-auth/codes":
			fmt.Fprint(w, `{"device_code":"synthetic-device-secret","user_code":"DEMO-1234","verificationUrl":"https://\u0061\u0070\u0070\u002e\u006b\u0069\u006c\u006f\u002e\u0061\u0069/device-auth?code=DEMO-1234","expiresIn":300}`)
		case "/api/device-auth/token":
			close(entered)
			<-release
			fmt.Fprint(w, `{"status":"approved","token":"synthetic-cancelled-token","userId":"cancelled-fixture","userEmail":"cancelled@example.test"}`)
		}
	}))
	defer up.Close()
	defer close(release)
	f.srv.Router.Fenjue = &fenjue.Client{APIURL: up.URL, GatewayURL: up.URL}
	_, raw := post(t, f.api.URL+"/api/fenjue/login/start", object{}, nil)
	var started object
	if err := json.Unmarshal([]byte(raw), &started); err != nil {
		t.Fatal(err)
	}
	session, ok := started["session"].(string)
	if !ok {
		t.Fatal("missing session")
	}
	finished := make(chan error, 1)
	go func() {
		resp, err := http.Get(f.api.URL + "/api/fenjue/login/status?session=" + session)
		if err == nil {
			_, err = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		finished <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("poll not started")
	}
	resp, _ := post(t, f.api.URL+"/api/fenjue/login/cancel?session="+session, object{}, nil)
	if resp.StatusCode != 200 {
		t.Fatal("cancel failed")
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not stop poll")
	}
	f.srv.fenjueMu.Lock()
	defer f.srv.fenjueMu.Unlock()
	if !f.srv.fenjueLogin.Done || f.srv.fenjueLogin.AccountID != "" || f.srv.fenjueLogin.Device.Secret != "" {
		t.Fatal("cancelled grant was retained")
	}
}

func TestFenjueDeviceLoginPersistsWithoutExposingCredential(t *testing.T) {
	f := newLicensedFenjueFixture(t, nil)
	const secret = "synthetic-fenjue-device-secret"
	const token = "synthetic-fenjue-credential"
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/device-auth/codes" {
			fmt.Fprintf(w, `{"device_code":%q,"user_code":"DEMO-1234","verificationUrl":"https://\u0061\u0070\u0070\u002e\u006b\u0069\u006c\u006f\u002e\u0061\u0069/device-auth?code=DEMO-1234","expiresIn":300}`, secret)
			return
		}
		if r.URL.Path == "/api/device-auth/token" {
			fmt.Fprintf(w, `{"status":"approved","token":%q,"userId":"fixture","userEmail":"fixture@example.test"}`, token)
			return
		}
		t.Error("unexpected endpoint")
	}))
	defer up.Close()
	f.srv.Router.Fenjue = &fenjue.Client{APIURL: up.URL, GatewayURL: up.URL}
	resp, raw := post(t, f.api.URL+"/api/fenjue/login/start", object{}, nil)
	if resp.StatusCode != 200 || strings.Contains(raw, secret) {
		t.Fatal("start response", raw)
	}
	var started object
	_ = json.Unmarshal([]byte(raw), &started)
	response, err := http.Get(f.api.URL + "/api/fenjue/login/status?session=" + started["session"].(string))
	if err != nil {
		t.Fatal(err)
	}
	result, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if strings.Contains(string(result), token) || strings.Contains(string(result), secret) {
		t.Fatal("private credential returned")
	}
	var state object
	_ = json.Unmarshal(result, &state)
	if state["done"] != true || state["account_id"] == "" {
		t.Fatal(string(result))
	}
	saved, ok := f.srv.Store.Get(state["account_id"].(string))
	if !ok || saved.Provider != "fenjue" || saved.AccessToken != token {
		t.Fatal("credential not persisted")
	}
	f.srv.fenjueMu.Lock()
	remaining := f.srv.fenjueLogin.Device.Secret
	f.srv.fenjueMu.Unlock()
	if remaining != "" {
		t.Fatal("device secret retained after completion")
	}
	// Expired authorization is terminal and does not re-poll.
	_, raw = post(t, f.api.URL+"/api/fenjue/login/start", object{}, nil)
	_ = json.Unmarshal([]byte(raw), &started)
	f.srv.fenjueMu.Lock()
	f.srv.fenjueLogin.Deadline = time.Now().Add(-time.Second)
	f.srv.fenjueMu.Unlock()
	response, err = http.Get(f.api.URL + "/api/fenjue/login/status?session=" + started["session"].(string))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	result, _ = io.ReadAll(response.Body)
	if !strings.Contains(string(result), "expired") {
		t.Fatal("expiry not reported")
	}
}
