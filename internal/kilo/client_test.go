package kilo

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSubscriptionPreflightAndNoReplay(t *testing.T) {
	for _, tc := range []struct {
		name, state string
		byok        bool
		code, want  int
	}{
		{"connected", "connected", true, 200, 200},
		{"disconnected", "disconnected", true, 200, 409},
		{"unavailable-model", "connected", false, 200, 400},
		{"upstream-quota", "connected", true, 429, 429},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var posts atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer synthetic-kilo-credential" {
					t.Error("missing independent credential")
				}
				switch r.URL.Path {
				case "/api/trpc/openAiChatGpt.status":
					fmt.Fprintf(w, `{"result":{"data":{"state":%q,"usageLimit":{"code":"old_notice"}}}}`, tc.state)
				case "/models":
					fmt.Fprintf(w, `{"data":[{"id":"openai/gpt-6-astra","hasUserByokAvailable":%t}]}`, tc.byok)
				case "/responses":
					posts.Add(1)
					body, _ := io.ReadAll(r.Body)
					if string(body) != `{"model":"openai/gpt-6-astra","input":"hi"}` {
						t.Error("body mutated")
					}
					if r.Header.Get("X-KILOCODE-MACHINEID") == "" || !strings.HasPrefix(r.Header.Get("X-Session-Id"), "ses_") || r.Header.Get("X-Session-Id") == "session-fixture" {
						t.Error("missing stable request identity")
					}
					if tc.code != 200 {
						w.WriteHeader(tc.code)
						fmt.Fprint(w, `{"error":{"code":"subscription_sharing_usage_limit_exceeded","message":"secret-marker"}}`)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {}\n\n")
				default:
					t.Error("unexpected path")
					w.WriteHeader(404)
				}
			}))
			defer srv.Close()
			c := Client{APIURL: srv.URL, GatewayURL: srv.URL}
			resp, err := c.Responses(context.Background(), Credential{Token: "synthetic-kilo-credential", AccountID: "fixture"}, "gpt-6-astra", []byte(`{"model":"openai/gpt-6-astra","input":"hi"}`), "session-fixture")
			if tc.want == 200 {
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
				if posts.Load() != 1 {
					t.Fatal(posts.Load())
				}
			} else {
				e, ok := err.(*Error)
				if !ok || e.Status != tc.want {
					t.Fatalf("unexpected error %v", err)
				}
				if strings.Contains(err.Error(), "secret-marker") {
					t.Fatal("upstream message leaked")
				}
				if posts.Load() > 1 || (tc.want != 429 && posts.Load() != 0) {
					t.Fatal("unexpected generation or retry", posts.Load())
				}
			}
		})
	}
}

func TestDeviceProtocolAndRedirection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/device-auth/codes":
			fmt.Fprint(w, `{"device_code":"private-device-secret","user_code":"DEMO-1234","verificationUrl":"https://app.kilo.ai/device-auth?code=DEMO-1234","expiresIn":300}`)
		case "/api/device-auth/token":
			fmt.Fprint(w, `{"status":"approved","token":"synthetic-kilo-credential","userId":"fixture","userEmail":"fixture@example.test"}`)
		default:
			t.Error("unexpected route")
		}
	}))
	defer srv.Close()
	c := Client{APIURL: srv.URL}
	d, err := c.Start(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	g, err := c.Poll(context.Background(), "", d.Secret)
	if err != nil || g.UserID != "fixture" {
		t.Fatal(err)
	}
	var hits atomic.Int32
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer sink.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, sink.URL, 307) }))
	defer redirect.Close()
	c.APIURL = redirect.URL
	_, err = c.Status(context.Background(), Credential{Token: "synthetic-kilo-credential"})
	if err == nil || hits.Load() != 0 {
		t.Fatal("redirect forwarded credentials")
	}
}
