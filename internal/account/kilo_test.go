package account

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestKiloImportIdentityAndExport(t *testing.T) {
	r, err := Parse([]byte(`[{"type":"kilo","api_key":"synthetic-kilo-credential","user_id":"workspace","email":"fixture@example.test"},{"type":"codex","access_token":"synthetic-codex-credential","account_id":"workspace","email":"fixture@example.test"}]`), "fixture")
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
	if strings.Contains(string(raw), "synthetic-kilo-credential") || !strings.Contains(string(raw), `"provider":"kilo"`) {
		t.Fatal("management boundary")
	}
	if _, err = ExportJSON([]Account{a}, "codex"); err == nil {
		t.Fatal("Kilo exported as Codex auth")
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
