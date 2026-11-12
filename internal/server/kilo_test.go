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
	"github.com/xxx-holic/wishtoken-desktop/internal/kilo"
)

func TestKiloResponsesExplicitAccountAndPayload(t *testing.T) {
	f := newFixture(t, nil, account.Account{ID: "acc-kilo", Provider: "kilo", AccountID: "kilo-fixture", AccessToken: "synthetic-kilo-credential"}, testAccount("native-fixture", "fixture@example.test"))
	var posts atomic.Int32
	var failed atomic.Bool
	var sentMu sync.Mutex
	var sent object
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-kilo-credential" {
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
			sentMu.Lock()
			sent = decoded
			sentMu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			if failed.Load() {
				fmt.Fprint(w, "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"fixture_failure\"}}}\n\n")
				return
			}
			stream := textStream("kilo-fixture-response", "fixture output")
			stream = strings.ReplaceAll(stream, `"input_tokens":3`, `"input_tokens":3,"is_byok":true,"cost":0`)
			fmt.Fprint(w, stream)
		default:
			t.Error("unexpected endpoint")
		}
	}))
	defer up.Close()
	f.srv.Router.Kilo = &kilo.Client{APIURL: up.URL, GatewayURL: up.URL}
	input := []any{object{"role": "user", "content": []any{object{"type": "input_text", "text": "fixture"}, object{"type": "input_image", "image_url": "data:image/png;base64,fixture"}}}}
	tools := []any{object{"type": "function", "name": "read_fixture", "parameters": object{"type": "object"}}}
	body := object{"model": "gpt-6-astra", "input": input, "tools": tools, "reasoning": object{"effort": "medium"}, "instructions": "caller-owned", "stream": false}
	h := map[string]string{"X-GPTBridge-Account": "acc-kilo", "X-GPTBridge-Channel": "kilo"}
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
		if r.Route == "kilo" && r.KiloBYOK != nil && *r.KiloBYOK && r.KiloCost != nil && *r.KiloCost == 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("missing BYOK/cost observations")
	}
	before := posts.Load()
	for _, change := range []object{{"store": true}, {"background": true}, {"service_tier": "priority"}, {"reasoning": object{"effort": "max"}}, {"model": "kilo-auto/free"}} {
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
	if posts.Load() != before {
		t.Fatal("invalid request sent upstream")
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
		t.Fatal("Kilo credential used for native route")
	}
}

func TestKiloCancelledPollCannotPersistGrant(t *testing.T) {
	f := newFixture(t, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/device-auth/codes":
			fmt.Fprint(w, `{"device_code":"synthetic-device-secret","user_code":"DEMO-1234","verificationUrl":"https://app.kilo.ai/device-auth?code=DEMO-1234","expiresIn":300}`)
		case "/api/device-auth/token":
			close(entered)
			<-release
			fmt.Fprint(w, `{"status":"approved","token":"synthetic-cancelled-token","userId":"cancelled-fixture","userEmail":"cancelled@example.test"}`)
		}
	}))
	defer up.Close()
	defer close(release)
	f.srv.Router.Kilo = &kilo.Client{APIURL: up.URL, GatewayURL: up.URL}
	_, raw := post(t, f.api.URL+"/api/kilo/login/start", object{}, nil)
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
		resp, err := http.Get(f.api.URL + "/api/kilo/login/status?session=" + session)
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
	resp, _ := post(t, f.api.URL+"/api/kilo/login/cancel?session="+session, object{}, nil)
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
	f.srv.kiloMu.Lock()
	defer f.srv.kiloMu.Unlock()
	if !f.srv.kiloLogin.Done || f.srv.kiloLogin.AccountID != "" || f.srv.kiloLogin.Device.Secret != "" {
		t.Fatal("cancelled grant was retained")
	}
}

func TestKiloDeviceLoginPersistsWithoutExposingCredential(t *testing.T) {
	f := newFixture(t, nil)
	const secret = "synthetic-kilo-device-secret"
	const token = "synthetic-kilo-credential"
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/device-auth/codes" {
			fmt.Fprintf(w, `{"device_code":%q,"user_code":"DEMO-1234","verificationUrl":"https://app.kilo.ai/device-auth?code=DEMO-1234","expiresIn":300}`, secret)
			return
		}
		if r.URL.Path == "/api/device-auth/token" {
			fmt.Fprintf(w, `{"status":"approved","token":%q,"userId":"fixture","userEmail":"fixture@example.test"}`, token)
			return
		}
		t.Error("unexpected endpoint")
	}))
	defer up.Close()
	f.srv.Router.Kilo = &kilo.Client{APIURL: up.URL, GatewayURL: up.URL}
	resp, raw := post(t, f.api.URL+"/api/kilo/login/start", object{}, nil)
	if resp.StatusCode != 200 || strings.Contains(raw, secret) {
		t.Fatal("start response", raw)
	}
	var started object
	_ = json.Unmarshal([]byte(raw), &started)
	response, err := http.Get(f.api.URL + "/api/kilo/login/status?session=" + started["session"].(string))
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
	if !ok || saved.Provider != "kilo" || saved.AccessToken != token {
		t.Fatal("credential not persisted")
	}
	f.srv.kiloMu.Lock()
	remaining := f.srv.kiloLogin.Device.Secret
	f.srv.kiloMu.Unlock()
	if remaining != "" {
		t.Fatal("device secret retained after completion")
	}
	// Expired authorization is terminal and does not re-poll.
	_, raw = post(t, f.api.URL+"/api/kilo/login/start", object{}, nil)
	_ = json.Unmarshal([]byte(raw), &started)
	f.srv.kiloMu.Lock()
	f.srv.kiloLogin.Deadline = time.Now().Add(-time.Second)
	f.srv.kiloMu.Unlock()
	response, err = http.Get(f.api.URL + "/api/kilo/login/status?session=" + started["session"].(string))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	result, _ = io.ReadAll(response.Body)
	if !strings.Contains(string(result), "expired") {
		t.Fatal("expiry not reported")
	}
}
