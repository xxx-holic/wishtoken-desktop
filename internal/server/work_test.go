package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xxx-holic/wishtoken-desktop/internal/config"
)

func TestWorkIdentityChannelAndBenchmark(t *testing.T) {
	cfg := config.Default()
	cfg.DesktopMode = true
	cfg.APIKey = "fixture-local-key"
	f := newFixture(t, cfg, testAccount("workspace_fixture", "fixture@example.test"))
	a := f.srv.Store.List()[0]
	calls := 0
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer fixture-worker-key" {
			t.Error("worker auth lost")
		}
		var envelope object
		_ = json.NewDecoder(r.Body).Decode(&envelope)
		if envelope["account_id"] != a.ID || envelope["email"] != a.Email || envelope["workspace"] != a.AccountID {
			t.Error("identity not pinned")
		}
		body := envelope["request"].(map[string]any)
		if body["input"] != "original candy fixture" || body["instructions"] != nil {
			t.Error("benchmark modified")
		}
		if reasoning, _ := body["reasoning"].(map[string]any); reasoning["effort"] != "max" {
			t.Error("Work effort not forwarded")
		}
		if envelope["access_token"] != nil || envelope["refresh_token"] != nil {
			t.Error("credential leaked to browser worker")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `event: response.completed`+"\n"+`data: {"type":"response.completed","response":{"id":"fixture","status":"completed","model":"gpt-6-astra-wm","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"21"}]}]}}`+"\n\n")
	}))
	defer worker.Close()
	t.Setenv("GPTBRIDGE_WORK_URL", worker.URL)
	t.Setenv("GPTBRIDGE_WORK_KEY", "fixture-worker-key")
	headers := map[string]string{"Authorization": "Bearer fixture-local-key"}
	body := object{"account_id": a.ID, "channel": "work", "model": "gpt-6-astra-wm", "effort": "max", "benchmark": "candy", "prompt": "original candy fixture"}
	response, raw := post(t, f.api.URL+"/api/pelican/generate", body, headers)
	if response.StatusCode != 200 || !strings.Contains(raw, `"text":"21"`) || calls != 1 {
		t.Fatalf("%d %s calls=%d", response.StatusCode, raw, calls)
	}
	body["model"] = "gpt-6-astra"
	response, _ = post(t, f.api.URL+"/api/pelican/generate", body, headers)
	if response.StatusCode != 400 || calls != 1 {
		t.Fatal("wrong Work model not rejected")
	}
	if f.codexHit.Load() != 0 || f.bpsHits.Load() != 0 {
		t.Fatal("Work crossed channels")
	}
}
