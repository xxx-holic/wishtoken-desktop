package pool

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/xxx-holic/wishtoken-desktop/internal/account"
	"github.com/xxx-holic/wishtoken-desktop/internal/config"
)

func TestBPSGrantNotPickedForNative(t *testing.T) {
	s, _ := account.Open("")
	bps, _ := s.Upsert(account.Account{Email: "fixture@example.test", AccountID: "fixture-workspace", AccessToken: "fixture-access", OAuthClientID: account.BPSOAuthClientID, ExpiresAt: time.Now().Add(time.Hour)})
	codex, _ := s.Upsert(account.Account{Email: "fixture@example.test", AccountID: "fixture-workspace", AccessToken: "fixture-native-access", ExpiresAt: time.Now().Add(time.Hour)})
	cfg := config.Default()
	p := New(s, func() *config.Config { return cfg })
	if _, err := p.Pick(context.Background(), PickOptions{AccountID: bps.ID, Route: "codex"}); err == nil || !strings.Contains(err.Error(), "separate Codex") {
		t.Fatal("BPS grant used for native", err)
	}
	a, err := p.Pick(context.Background(), PickOptions{Route: "codex"})
	if err != nil || a.ID != codex.ID {
		t.Fatal("native scheduler mixed grants", err)
	}
	a, err = p.Pick(context.Background(), PickOptions{AccountID: bps.ID, Route: "bps"})
	if err != nil || a.ID != bps.ID {
		t.Fatal("BPS grant not usable", err)
	}
}

func TestBPSFailedRefreshIsNotReplayed(t *testing.T) {
	s, _ := account.Open("")
	up, _ := s.Upsert(account.Account{AccountID: "fixture-workspace", RefreshToken: "fixture-refresh", OAuthClientID: account.BPSOAuthClientID})
	_ = s.Update(up.ID, func(a *account.Account) { a.RefreshFailures = 1 })
	cfg := config.Default()
	p := New(s, func() *config.Config { return cfg })
	for _, force := range []bool{false, true} {
		if _, err := p.Refresh(context.Background(), up.ID, force); err == nil || !strings.Contains(err.Error(), "previously failed") {
			t.Fatal("rotated grant could be replayed", err)
		}
	}
}
