package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xxx-holic/wishtoken-desktop/internal/config"
)

func TestPrismLaunchPreparesExternalProvider(t *testing.T) {
	t.Setenv("GPTBRIDGE_HOME", t.TempDir())
	cfg := config.Default()
	f := newFixture(t, cfg, testAccount("acc_prism", "prism@example.test"))
	account := f.srv.Store.List()[0]
	body := object{
		"account_id": account.ID, "directory": t.TempDir(), "model": "gpt-6.1-sol",
		"effort": "ultra", "channel": "prism", "prism_url": "http://127.0.0.1:18765",
		"prism_key": "fixture-prism-key", "prepare_only": true, "target": "cli",
	}
	response, raw := post(t, f.api.URL+"/api/codex/launch", body, map[string]string{"Authorization": "Bearer fixture-local-key"})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("prism launch: %d %s", response.StatusCode, raw)
	}
	var out struct {
		Home string `json:"home"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out.Home == "" {
		t.Fatalf("launch response: %s", raw)
	}
	prepared, err := os.ReadFile(filepath.Join(out.Home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`model_provider = "prism"`, `model = "gpt-6.1-sol"`, `model_reasoning_effort = "ultra"`, `base_url = "http://127.0.0.1:18765/v1"`, `experimental_bearer_token = "fixture-prism-key"`} {
		if !strings.Contains(string(prepared), want) {
			t.Fatalf("missing %s in %s", want, prepared)
		}
	}
	if _, err := os.Stat(filepath.Join(out.Home, "auth.json")); !os.IsNotExist(err) {
		t.Fatal("Prism profile received local identity auth")
	}
}
