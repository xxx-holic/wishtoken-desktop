package account

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyProviderMigrationPreservesCredentialsAndIDs(t *testing.T) {
	old := string([]byte{107, 105, 108, 111})
	file := filepath.Join(t.TempDir(), "accounts.json")
	initial := map[string]any{"version": 1, "accounts": []map[string]any{{"id": "acc-existing", "provider": old, "account_id": "existing-user", "access_token": "synthetic-existing-credential", "source": old + "-device-login", "name": "Custom label"}}}
	raw, _ := json.Marshal(initial)
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	a, ok := store.Get("acc-existing")
	if !ok || a.Provider != "fenjue" || a.AccessToken != "synthetic-existing-credential" || a.Source != "fenjue-device-login" || a.Name != "Custom label" {
		t.Fatal("migration changed identity or credentials")
	}
	for _, kind := range []string{old, "fenjue"} {
		input, _ := json.Marshal(map[string]any{"type": kind, "user_id": "existing-user", "api_key": "synthetic-existing-credential"})
		parsed, err := Parse(input, "fixture")
		if err != nil || len(parsed.Accounts) != 1 {
			t.Fatal("legacy import failed", err)
		}
		result, err := store.Upsert(parsed.Accounts[0])
		if err != nil || result.Created || result.ID != "acc-existing" || store.Count() != 1 {
			t.Fatal("migration duplicated account", err)
		}
	}
	raw, err = ExportJSON(store.List(), "cpa")
	if err != nil || strings.Contains(string(raw), old) || !strings.Contains(string(raw), `"type": "fenjue"`) {
		t.Fatal("new export has legacy metadata", err)
	}
}

func TestFenjueImportIdentityAndExport(t *testing.T) {
	r, err := Parse([]byte(`[{"type":"fenjue","api_key":"synthetic-fenjue-credential","user_id":"workspace","email":"fixture@example.test"},{"type":"codex","access_token":"synthetic-codex-credential","account_id":"workspace","email":"fixture@example.test"}]`), "fixture")
	if err != nil || len(r.Accounts) != 2 {
		t.Fatal(err, r)
	}
	store, err := Open(filepath.Join(t.TempDir(), "accounts.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range r.Accounts {
		if _, err = store.Upsert(a); err != nil {
			t.Fatal(err)
		}
	}
	if store.Count() != 2 {
		t.Fatal("cross-provider merge")
	}
	a := r.Accounts[0]
	raw, _ := json.Marshal(a.View())
	if strings.Contains(string(raw), "synthetic-fenjue-credential") || !strings.Contains(string(raw), `"provider":"fenjue"`) {
		t.Fatal("management boundary")
	}
	if _, err = ExportJSON([]Account{a}, "codex"); err == nil {
		t.Fatal("Fenjue exported as Codex auth")
	}
	raw, err = ExportJSON([]Account{a}, "cpa")
	if err != nil {
		t.Fatal(err)
	}
	imported, err := Parse(raw, "roundtrip")
	if err != nil || len(imported.Accounts) != 1 || imported.Accounts[0].Identity() != a.Identity() {
		t.Fatal("roundtrip failed")
	}
}
