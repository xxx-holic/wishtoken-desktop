package localcodex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAccountIdentityAndSpeed(t *testing.T) {
	root := t.TempDir()
	o := Options{Home: filepath.Join(root, "profile"), Directory: root, BaseURL: "http://127.0.0.1:8792", Model: "gpt-6-astra", Effort: "high", Channel: "codex", Speed: "fast", AccountID: "acc-test", AccessToken: "real-token-sentinel", AuthAPIURL: "http://127.0.0.1:8792/cockpit-auth/private-key"}
	o, err := Prepare(o)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(o.Home, "config.toml"))
	for _, want := range []string{`service_tier = "priority"`, `default-service-tier = "priority"`, `requires_openai_auth = true`, `env_key = "GPTBRIDGE_CODEX_KEY"`, `"X-GPTBridge-Account" = "acc-test"`, "[features]\nenable_request_compression = false"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("missing %s", want)
		}
	}
	if strings.Contains(string(raw), o.AccessToken) {
		t.Fatal("token leaked into config")
	}
	auth, _ := os.ReadFile(filepath.Join(o.Home, "auth.json"))
	var doc map[string]any
	if json.Unmarshal(auth, &doc) != nil || doc["personal_access_token"] != o.AccessToken || len(doc) != 2 {
		t.Fatal("auth projection changed or refresh token included")
	}
	if !strings.Contains(strings.Join(Environment(o), ";"), "CODEX_AUTHAPI_BASE_URL="+o.AuthAPIURL) {
		t.Fatal("identity adapter unavailable to CLI")
	}
	o.Speed = "standard"
	if _, err = Prepare(o); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(filepath.Join(o.Home, "config.toml"))
	if strings.Contains(string(raw), "priority") || !strings.Contains(string(raw), `service_tier = "default"`) {
		t.Fatal("fast setting persisted after standard selection")
	}
	o.Channel = "bps"
	o.Speed = "fast"
	if _, err = Prepare(o); err == nil {
		t.Fatal("BPS silently accepted unsupported speed")
	}
}

func TestIsolatedConfig(t *testing.T) {
	root := t.TempDir()
	global := filepath.Join(root, "existing-codex")
	if err := os.MkdirAll(global, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(global, "config.toml"), []byte("sentinel"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", global)
	o, err := Prepare(Options{Home: filepath.Join(root, "bridge", "codex-home"), BaseURL: "http://127.0.0.1:8790", Model: "gpt-6-astra", Effort: "xhigh", APIKey: "secret-local-key", Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(o.Home, "config.toml"))
	for _, want := range []string{`model = "gpt-6-astra"`, `model_reasoning_effort = "xhigh"`, `wire_api = "responses"`, `supports_websockets = false`, `env_key = "GPTBRIDGE_CODEX_KEY"`, "[features]\nenable_request_compression = false"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("missing %s", want)
		}
	}
	if strings.Contains(string(raw), o.APIKey) {
		t.Fatal("key persisted in config")
	}
	original, _ := os.ReadFile(filepath.Join(global, "config.toml"))
	if string(original) != "sentinel" || os.Getenv("CODEX_HOME") != global {
		t.Fatal("global Codex configuration changed")
	}
	env := Environment(o)
	count := 0
	for _, s := range env {
		if strings.HasPrefix(s, "CODEX_HOME=") {
			count++
			if s != "CODEX_HOME="+o.Home {
				t.Fatal("wrong isolated home")
			}
		}
	}
	if count != 1 {
		t.Fatal("duplicate CODEX_HOME")
	}
	o.Effort = "ultra"
	if _, err := Prepare(o); err == nil {
		t.Fatal("effort was silently changed")
	}
}

// TestChannelCatalogsParseInInstalledCodex pins the apply_patch enum accepted
// by the Codex build the app launches. Codex 0.163 rejects "function" while
// parsing model_catalog_json, which blocks Codex from starting at all.
func TestChannelCatalogsParseInInstalledCodex(t *testing.T) {
	for _, channel := range []string{"bps", "codex", "work", "fenjue"} {
		var manifest struct {
			Models []struct {
				Slug                     string   `json:"slug"`
				ApplyPatchToolType       string   `json:"apply_patch_tool_type"`
				InputModalities          []string `json:"input_modalities"`
				SupportedReasoningLevels []struct {
					Effort string `json:"effort"`
				} `json:"supported_reasoning_levels"`
			} `json:"models"`
		}
		if err := json.Unmarshal(ChannelCatalog(channel), &manifest); err != nil {
			t.Fatalf("%s catalog: %v", channel, err)
		}
		if len(manifest.Models) == 0 {
			t.Fatalf("%s catalog has no models", channel)
		}
		for _, model := range manifest.Models {
			image := false
			for _, modality := range model.InputModalities {
				image = image || modality == "image"
			}
			if !image {
				t.Errorf("%s/%s: image input not declared", channel, model.Slug)
			}
			if model.ApplyPatchToolType != "freeform" {
				t.Errorf("%s/%s: apply_patch_tool_type = %q, want freeform", channel, model.Slug, model.ApplyPatchToolType)
			}
			if channel == "work" {
				seen := map[string]bool{}
				for _, level := range model.SupportedReasoningLevels {
					seen[level.Effort] = true
				}
				for _, effort := range []string{"low", "medium", "high", "xhigh", "max"} {
					if !seen[effort] {
						t.Errorf("work/%s: missing reasoning level %q", model.Slug, effort)
					}
				}
				image := false
				for _, modality := range model.InputModalities {
					image = image || modality == "image"
				}
				if !image {
					t.Errorf("work/%s: image input not declared", model.Slug)
				}
			}
		}
	}
}

func TestPrismPrepareUsesExternalBridge(t *testing.T) {
	root := t.TempDir()
	o := Options{Home: filepath.Join(root, "profile"), Directory: root, BaseURL: "http://127.0.0.1:8792", Model: "gpt-6.1-sol", Effort: "ultra", Channel: "prism", Speed: "standard", PrismURL: "http://127.0.0.1:18765", PrismKey: "fixture-prism-key", AccountID: "acc-test"}
	o, err := Prepare(o)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(o.Home, "config.toml"))
	for _, want := range []string{`model_provider = "prism"`, `model = "gpt-6.1-sol"`, `model_reasoning_effort = "ultra"`, `[model_providers.prism]`, `base_url = "http://127.0.0.1:18765/v1"`, `experimental_bearer_token = "fixture-prism-key"`, `requires_openai_auth = false`, "[features]\nenable_request_compression = false"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("missing %s", want)
		}
	}
	if strings.Contains(string(raw), "GPTBRIDGE_CODEX_KEY") {
		t.Fatal("local bridge credential leaked into Prism config")
	}
	if _, err := os.Stat(filepath.Join(o.Home, "auth.json")); !os.IsNotExist(err) {
		t.Fatal("Prism profile received local identity auth")
	}
	env := strings.Join(Environment(o), "\n")
	if strings.Contains(env, "GPTBRIDGE_CODEX_KEY") || strings.Contains(env, "PRISM_BRIDGE_KEY") {
		t.Fatalf("Prism environment: %s", env)
	}
	catalog, _ := os.ReadFile(filepath.Join(o.Home, "models.json"))
	if !strings.Contains(string(catalog), "gpt-6.1-sol") {
		t.Fatal("Prism catalog not written")
	}
	if _, err := PrismURL("http://example.test:18765"); err == nil {
		t.Fatal("non-loopback Prism URL accepted")
	}
	if _, err := Prepare(Options{Home: filepath.Join(root, "bad"), Directory: root, BaseURL: "http://127.0.0.1:8792", Model: "gpt-6.1-sol", Effort: "ultra", Channel: "bps", Speed: "standard"}); err == nil {
		t.Fatal("ultra accepted outside Prism")
	}
}

func TestWorkModelsAndEfforts(t *testing.T) {
	if got := len(WorkModels()); got != 7 {
		t.Fatalf("Work models = %d, want 7", got)
	}
	for _, slug := range []string{"gpt-6.1-sol-wm", "gpt-6-astra-wm", "gpt-5.6-luna-wm"} {
		if !WorkModelAllowed(slug) {
			t.Errorf("Work model %q not allowed", slug)
		}
	}
	if WorkModelAllowed("gpt-6-astra") {
		t.Error("BPS model accepted as Work model")
	}
	for _, effort := range []string{"low", "medium", "high", "xhigh", "max"} {
		if !WorkEffortAllowed(effort) {
			t.Errorf("Work effort %q not allowed", effort)
		}
	}
	if WorkEffortAllowed("ultra") {
		t.Error("unsupported effort accepted")
	}
}
