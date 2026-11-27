package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"github.com/xxx-holic/wishtoken-desktop/internal/account"
	"github.com/xxx-holic/wishtoken-desktop/internal/config"
	"github.com/xxx-holic/wishtoken-desktop/internal/featurelicense"
	"net/http"
	"strings"
	"testing"
	"time"
)

func fixtureLicense(t *testing.T, s *Server) string {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	s.featureLicense = featurelicense.NewWithKey(t.TempDir(), pub)
	now := time.Now().Unix()
	c := featurelicense.Claims{Version: 1, Product: "wishtoken-desktop", Feature: "fenjue", Installation: s.featureLicense.Status().Installation, ID: "fixture-license", IssuedAt: now - 1, ExpiresAt: now + 3600}
	raw, _ := json.Marshal(c)
	payload := "WTK1." + base64.RawURLEncoding.EncodeToString(raw)
	return payload + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, []byte(payload)))
}

func newLicensedFenjueFixture(t *testing.T, cfg *config.Config, accounts ...account.Account) *fixture {
	t.Helper()
	f := newFixture(t, cfg, accounts...)
	code := fixtureLicense(t, f.srv)
	if _, err := f.srv.featureLicense.Activate(code); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestFenjueLicenseGateBlocksAliasesAndImportWithoutUpstream(t *testing.T) {
	f := newFixture(t, nil, account.Account{ID: "acc-locked", Provider: "fenjue", AccountID: "fixture", AccessToken: "synthetic"}, testAccount("native-fixture", "fixture@example.test"))
	code := fixtureLicense(t, f.srv)
	legacy := string([]byte{107, 105, 108, 111})
	for _, route := range []string{"fenjue", legacy} {
		res, body := post(t, f.api.URL+"/v1/responses", object{"model": "gpt-6-astra", "input": "fixture"}, map[string]string{"X-GPTBridge-Channel": route, "X-GPTBridge-Account": "acc-locked"})
		if res.StatusCode != 403 || !strings.Contains(body, "feature_locked") {
			t.Fatalf("route unlocked: %d %s", res.StatusCode, body)
		}
	}
	for _, url := range []string{"/api/fenjue/login/start", "/api/" + legacy + "/login/start"} {
		res, _ := post(t, f.api.URL+url, object{}, nil)
		if res.StatusCode != 403 {
			t.Fatal(url, res.StatusCode)
		}
	}
	res, _ := post(t, f.api.URL+"/api/accounts/import", object{"type": "fenjue", "token": "synthetic-new-credential-for-test", "user_id": "blocked-new"}, nil)
	if res.StatusCode != 403 {
		t.Fatal("import not gated", res.StatusCode)
	}
	get, err := http.Get(f.api.URL + "/api/fenjue/subscription?account_id=acc-locked")
	if err != nil {
		t.Fatal(err)
	}
	get.Body.Close()
	if get.StatusCode != 403 {
		t.Fatal("subscription not gated")
	}
	if f.bpsHits.Load() != 0 || f.codexHit.Load() != 0 {
		t.Fatal("locked feature reached upstream")
	}
	res, _ = post(t, f.api.URL+"/api/features/fenjue/license", object{"code": code}, nil)
	if res.StatusCode != 200 {
		t.Fatal("valid code rejected", res.StatusCode)
	}
	res, _ = post(t, f.api.URL+"/api/accounts/import", object{"type": "fenjue", "token": "synthetic-new-credential-for-test", "user_id": "allowed-new"}, nil)
	if res.StatusCode != 200 {
		t.Fatal("unlocked import rejected", res.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodDelete, f.api.URL+"/api/features/fenjue/license", nil)
	locked, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	locked.Body.Close()
	if locked.StatusCode != 200 || f.srv.featureLicense.Require() == nil {
		t.Fatal("deactivation failed")
	}
	res, _ = post(t, f.api.URL+"/v1/responses", object{"model": "gpt-6-astra-fenjue", "input": "fixture"}, map[string]string{"X-GPTBridge-Account": "acc-locked"})
	if res.StatusCode != 403 {
		t.Fatal("model suffix bypass")
	}
	res, _ = post(t, f.api.URL+"/v1/responses", object{"model": "gpt-6-astra", "input": "fixture"}, map[string]string{"X-GPTBridge-Channel": "bps"})
	if res.StatusCode != 200 {
		t.Fatal("BPS should remain available", res.StatusCode)
	}
}
