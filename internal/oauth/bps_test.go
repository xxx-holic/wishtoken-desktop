package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xxx-holic/wishtoken-desktop/internal/account"
	"github.com/xxx-holic/wishtoken-desktop/internal/testutil"
)

func bpsToken(workspace string) string {
	c := testutil.ChatGPTClaims("fixture@example.test", workspace, "pro", time.Now().Add(time.Hour))
	c["client_id"] = account.BPSOAuthClientID
	return testutil.MakeJWT(c)
}

func TestBPSDeviceLoginAndPending(t *testing.T) {
	for _, interval := range []any{1, "1"} {
		var polls atomic.Int32
		verifier := strings.Repeat("v", 43)
		digest := sha256.Sum256([]byte(verifier))
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/accounts/deviceauth/usercode":
				var body map[string]string
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body["client_id"] != account.BPSOAuthClientID {
					t.Error("wrong device client")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"device_auth_id": "fixture-device", "user_code": "FIXTURE-CODE", "interval": interval})
			case "/api/accounts/deviceauth/token":
				var body map[string]string
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body["client_id"] != account.BPSOAuthClientID || body["device_auth_id"] != "fixture-device" || body["user_code"] != "FIXTURE-CODE" {
					t.Error("poll did not preserve device identity")
				}
				if polls.Add(1) == 1 {
					w.WriteHeader(403)
					_, _ = w.Write([]byte(`{"error":{"code":"deviceauth_authorization_pending"}}`))
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"authorization_code": "fixture-code", "code_verifier": verifier, "code_challenge": base64.RawURLEncoding.EncodeToString(digest[:])})
			case "/oauth/token":
				_ = r.ParseForm()
				if r.URL.RawQuery != "unified=true" || r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("client_id") != account.BPSOAuthClientID || r.Form.Get("code_verifier") != verifier || r.Form.Get("redirect_uri") != bpsRedirectURI || r.Form.Has("scope") {
					t.Error("incorrect BPS exchange")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": bpsToken("fixture-workspace"), "refresh_token": "fixture-refresh", "token_type": "Bearer", "expires_in": 3600})
			default:
				t.Error("unexpected path")
				w.WriteHeader(404)
			}
		}))
		target, _ := url.Parse(srv.URL)
		s, err := startBPSLogin(context.Background(), &http.Client{Transport: rewriteTransport{target}}, func(ctx context.Context, d time.Duration) error {
			if d != time.Second {
				t.Error("poll interval not honored")
			}
			return ctx.Err()
		})
		if err != nil {
			t.Fatal(err)
		}
		if s.Channel != "bps" || s.UserCode != "FIXTURE-CODE" || !strings.HasPrefix(s.AuthURL, bpsAuthBase+"/api/accounts/deviceauth/authorize?") {
			t.Fatal("missing public prompt")
		}
		tokens, err := s.Wait(context.Background())
		if err != nil || tokens.OAuthClientID != account.BPSOAuthClientID || polls.Load() != 2 {
			t.Fatalf("device flow failed: %v", err)
		}
		srv.Close()
	}
}

func TestBPSCancelAndRejectPKCE(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		var exchanges atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/accounts/deviceauth/usercode":
				_, _ = w.Write([]byte(`{"device_auth_id":"fixture","user_code":"fixture","interval":1}`))
			case "/api/accounts/deviceauth/token":
				_ = json.NewEncoder(w).Encode(map[string]string{"authorization_code": "fixture", "code_verifier": strings.Repeat("v", 43), "code_challenge": "wrong"})
			case "/oauth/token":
				exchanges.Add(1)
			}
		}))
		target, _ := url.Parse(srv.URL)
		s, err := startBPSLogin(context.Background(), &http.Client{Transport: rewriteTransport{target}}, func(ctx context.Context, _ time.Duration) error {
			if cancel {
				<-ctx.Done()
			}
			return ctx.Err()
		})
		if err != nil {
			t.Fatal(err)
		}
		if cancel {
			s.Close()
		}
		_, err = s.Wait(context.Background())
		if err == nil || exchanges.Load() != 0 {
			t.Fatal("invalid/cancelled grant reached exchange")
		}
		srv.Close()
	}
}

func TestBPSRefreshClientRotationIdentityAndRedaction(t *testing.T) {
	for _, mode := range []string{"ok", "rejected", "other-workspace", "other-client", "redirect", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				_ = r.ParseForm()
				if r.URL.RawQuery != "unified=true" || r.Form.Get("client_id") != account.BPSOAuthClientID || r.Form.Get("grant_type") != "refresh_token" || r.Form.Has("scope") {
					t.Error("BPS refresh sent Codex fields")
				}
				switch mode {
				case "rejected":
					w.WriteHeader(400)
					_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"fixture-secret-never-echo"}`))
					return
				case "redirect":
					w.Header().Set("Location", "https://example.invalid/fixture-secret-never-echo")
					w.WriteHeader(307)
					return
				case "malformed":
					_, _ = w.Write([]byte("fixture-secret-never-echo"))
					return
				}
				token := bpsToken("fixture-workspace")
				if mode == "other-workspace" {
					token = bpsToken("different-workspace")
				}
				if mode == "other-client" {
					c := testutil.ChatGPTClaims("fixture@example.test", "fixture-workspace", "pro", time.Now().Add(time.Hour))
					c["client_id"] = ClientID
					token = testutil.MakeJWT(c)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": token, "refresh_token": "fixture-new-refresh", "token_type": "Bearer", "expires_in": 3600})
			}))
			defer srv.Close()
			target, _ := url.Parse(srv.URL)
			a := account.Account{AccessToken: bpsToken("fixture-workspace"), RefreshToken: "fixture-old-refresh"}
			err := RefreshAccount(context.Background(), &http.Client{Transport: rewriteTransport{target}}, &a)
			if hits.Load() != 1 {
				t.Fatal("refresh was retried or redirected")
			}
			if mode == "ok" {
				if err != nil || a.RefreshToken != "fixture-new-refresh" || a.OAuthClientID != account.BPSOAuthClientID {
					t.Fatalf("refresh failed: %v", err)
				}
			} else {
				if err == nil || a.RefreshToken != "fixture-old-refresh" || strings.Contains(err.Error(), "fixture-secret") {
					t.Fatal("bad token committed or secret exposed")
				}
				if mode == "rejected" && !IsPermanent(err) {
					t.Fatal("invalid_grant was not classified")
				}
			}
		})
	}
}

func TestBPSInvalidIntervalAndContext(t *testing.T) {
	for _, interval := range []any{0, 301, "invalid"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"device_auth_id": "fixture", "user_code": "fixture", "interval": interval})
		}))
		target, _ := url.Parse(srv.URL)
		_, err := StartBPSLogin(context.Background(), &http.Client{Transport: rewriteTransport{target}})
		srv.Close()
		if err == nil {
			t.Fatal("invalid interval accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitBPSPoll(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel did not stop poll")
	}
}
