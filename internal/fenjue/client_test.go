package fenjue

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
				if r.Header.Get("Authorization") != "Bearer synthetic-fenjue-credential" {
					t.Error("missing independent credential")
				}
				switch r.URL.Path {
				case "/api/trpc/openAiChatGpt.status":
					fmt.Fprintf(w, `{"result":{"data":{"state":%q,"usageLimit":{"code":"old_notice"}}}}`, tc.state)
				case "/models":
					fmt.Fprintf(w, `{"data":[{"id":"openai/gpt-6-astra","hasUserByokAvailable":%t}]}`, tc.byok)
				case "/responses":
					posts.Add(1)
					if r.Header.Get("User-Agent") != wireValue("S2lsby1Db2RlLzcuOC4yNyBhaS1zZGsvcHJvdmlkZXItdXRpbHMvNC4wLjQwIHJ1bnRpbWUvYnVuLzEuNC40") || r.Header.Get(wireValue("eC1raWxvY29kZS1tb2Rl")) != "code" || r.Header.Get(wireValue("WC1LSUxPQ09ERS1FRElUT1JOQU1F")) == "" {
						t.Error("request profile incomplete")
					}
					body, _ := io.ReadAll(r.Body)
					if string(body) != `{"model":"openai/gpt-6-astra","input":"hi"}` {
						t.Error("body mutated")
					}
					if r.Header.Get("\u0058\u002d\u004b\u0049\u004c\u004f\u0043\u004f\u0044\u0045\u002d\u004d\u0041\u0043\u0048\u0049\u004e\u0045\u0049\u0044") == "" || !strings.HasPrefix(r.Header.Get("X-Session-Id"), "ses_") || r.Header.Get("X-Session-Id") == "session-fixture" {
						t.Error("missing stable request identity")
					}
					if r.Header.Get(wireValue("WC1LSUxPQ09ERS1NQUNISU5FSUQ=")) != "9be0fd70455917d2130dc3799b3eae42" {
						t.Error("machine identity changed after branding")
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
			resp, err := c.Responses(context.Background(), Credential{Token: "synthetic-fenjue-credential", AccountID: "fixture"}, "gpt-6-astra", []byte(`{"model":"openai/gpt-6-astra","input":"hi"}`), "session-fixture")
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
			fmt.Fprint(w, `{"device_code":"private-device-secret","user_code":"DEMO-1234","verificationUrl":"https://\u0061\u0070\u0070\u002e\u006b\u0069\u006c\u006f\u002e\u0061\u0069/device-auth?code=DEMO-1234","expiresIn":300}`)
		case "/api/device-auth/token":
			fmt.Fprint(w, `{"status":"approved","token":"synthetic-fenjue-credential","userId":"fixture","userEmail":"fixture@example.test"}`)
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
	_, err = c.Status(context.Background(), Credential{Token: "synthetic-fenjue-credential"})
	if err == nil || hits.Load() != 0 {
		t.Fatal("redirect forwarded credentials")
	}
}

func TestSubscriptionCooldownPreservesCredentialAndStopsBeforeGeneration(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/trpc/openAiChatGpt.status" {
			t.Error("unexpected generation or catalog request")
		}
		fmt.Fprint(w, `{"result":{"data":{"state":"error","email":"fixture@example.test","errorMessage":"OpenAI doesn't allow this ChatGPT account or workspace to share its plan. Connect a different ChatGPT account; until then, requests use credits."}}}`)
	}))
	defer srv.Close()
	c := Client{APIURL: srv.URL, GatewayURL: srv.URL}
	cred := Credential{Token: "synthetic-independent-credential", AccountID: "fixture"}
	status, err := c.Status(context.Background(), cred)
	if err != nil || status.Reason != "subscription_cooldown" {
		t.Fatalf("status: %+v, %v", status, err)
	}
	_, err = c.Responses(context.Background(), cred, "gpt-6-astra", []byte(`{"input":"hi"}`), "fixture")
	e, ok := err.(*Error)
	if !ok || e.Code != "subscription_cooldown" || e.Status != 409 || calls.Load() != 2 {
		t.Fatalf("unexpected result: %v; calls %d", err, calls.Load())
	}
	if cred.Token != "synthetic-independent-credential" {
		t.Fatal("credential changed")
	}
}
