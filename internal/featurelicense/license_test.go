package featurelicense

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func signed(t *testing.T, key ed25519.PrivateKey, c Claims) string {
	t.Helper()
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	s := "WTK1." + base64.RawURLEncoding.EncodeToString(raw)
	return s + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, []byte(s)))
}

func TestLicenseSignatureBindingExpiryAndPersistence(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	home := t.TempDir()
	m := NewWithKey(home, pub)
	now := time.Now().Unix()
	id := m.Status().Installation
	if m.Status().Unlocked || id == "" || m.Require() == nil {
		t.Fatal("must start locked")
	}
	c := Claims{1, "wishtoken-desktop", "fenjue", id, "fixture-license", now - 1, now + 3600}
	code := signed(t, priv, c)
	for _, tc := range []struct {
		name   string
		change func(*Claims)
		reason string
	}{
		{"expired", func(c *Claims) { c.IssuedAt = now - 120; c.ExpiresAt = now - 1 }, "expired"},
		{"other-installation", func(c *Claims) { c.Installation = "WT-" + strings.Repeat("A", 32) }, "wrong_installation"},
		{"other-feature", func(c *Claims) { c.Feature = "other" }, "invalid_claims"},
		{"future", func(c *Claims) { c.IssuedAt = now + 600 }, "not_yet_valid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := c
			tc.change(&v)
			s, e := m.Activate(signed(t, priv, v))
			if e == nil || s.Reason != tc.reason || m.Status().Unlocked {
				t.Fatalf("unexpected %v %v", s, e)
			}
		})
	}
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	for _, bad := range []string{"", code + "x", signed(t, other, c), strings.Repeat("A", 9000)} {
		if _, e := m.Activate(bad); e == nil {
			t.Fatal("bad code accepted")
		}
	}
	if s, e := m.Activate(code); e != nil || !s.Unlocked {
		t.Fatalf("valid code rejected: %v", e)
	}
	if _, e := m.Activate("invalid"); e == nil || !m.Status().Unlocked {
		t.Fatal("invalid entry replaced valid activation")
	}
	reloaded := NewWithKey(home, pub)
	if reloaded.Require() != nil || reloaded.Status().LicenseID != c.ID {
		t.Fatal("activation not persisted")
	}
	reloaded.now = func() time.Time { return time.Unix(now+3601, 0) }
	if reloaded.Require() == nil || reloaded.Status().Reason != "expired" {
		t.Fatal("expiry not enforced on next operation")
	}
	if s, e := m.Deactivate(); e != nil || s.Unlocked || s.Installation != id {
		t.Fatal("deactivation changed identity")
	}
}

func TestMissingIssuerFailsClosed(t *testing.T) {
	m := NewWithKey(t.TempDir(), nil)
	if m.Require() == nil {
		t.Fatal("missing key unlocked")
	}
}
