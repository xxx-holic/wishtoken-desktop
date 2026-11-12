package account

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xxx-holic/wishtoken-desktop/internal/testutil"
)

func TestOAuthGrantsRemainSeparateAcrossImportAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	claims := testutil.ChatGPTClaims("same@example.test", "fixture-workspace", "pro", time.Now().Add(time.Hour))
	legacy, _ := s.Upsert(Account{AccessToken: testutil.MakeJWT(claims), RefreshToken: "fixture-codex"})
	claims["client_id"] = CodexOAuthClientID
	codex, _ := s.Upsert(Account{AccessToken: testutil.MakeJWT(claims), RefreshToken: "fixture-codex"})
	if codex.ID != legacy.ID || codex.Created {
		t.Fatal("Codex migration changed legacy identity")
	}
	claims["client_id"] = BPSOAuthClientID
	raw, _ := json.Marshal(map[string]any{"type": "bps", "access_token": testutil.MakeJWT(claims), "refresh_token": "fixture-bps", "client_id": BPSOAuthClientID})
	parsed, err := Parse(raw, "fixture")
	if err != nil || len(parsed.Accounts) != 1 {
		t.Fatal("BPS import failed", err)
	}
	bps, err := s.Upsert(parsed.Accounts[0])
	if err != nil || bps.ID == codex.ID || !bps.Created {
		t.Fatal("independent grants merged", err)
	}
	duplicate, _ := s.Upsert(parsed.Accounts[0])
	if duplicate.ID != bps.ID || duplicate.Created {
		t.Fatal("BPS duplicate not merged")
	}
	reloaded, err := Open(path)
	if err != nil || reloaded.Count() != 2 {
		t.Fatal("reload lost grant identity", err)
	}
	for _, a := range reloaded.List() {
		v, _ := json.Marshal(a.View())
		if strings.Contains(string(v), "fixture-bps") || strings.Contains(string(v), "fixture-codex") || strings.Contains(string(v), "access_token\":\"") {
			t.Fatal("credential exposed in view")
		}
	}
}

func TestBPSRefreshOnlyImportAndIDTokenClient(t *testing.T) {
	r, err := Parse([]byte(`{"type":"bps","refresh_token":"fixture-refresh-only"}`), "fixture")
	if err != nil || len(r.Accounts) != 1 || r.Accounts[0].OAuthClientID != BPSOAuthClientID {
		t.Fatal("BPS type not retained", err)
	}
	for _, aud := range []any{BPSOAuthClientID, []any{BPSOAuthClientID}} {
		c := testutil.ChatGPTClaims("fixture@example.test", "fixture-workspace", "pro", time.Now().Add(time.Hour))
		c["aud"] = aud
		a := Account{IDToken: testutil.MakeJWT(c), RefreshToken: "fixture-refresh-only"}
		a.FillFromTokens()
		if a.OAuthClientID != BPSOAuthClientID {
			t.Fatal("ID token audience not recognized")
		}
	}
	if (&Account{OAuthClientID: BPSOAuthClientID}).Identity() != "" {
		t.Fatal("empty identity must not merge")
	}
}
