package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xxx-holic/wishtoken-desktop/internal/account"
	"github.com/xxx-holic/wishtoken-desktop/internal/api"
	"github.com/xxx-holic/wishtoken-desktop/internal/config"
	"github.com/xxx-holic/wishtoken-desktop/internal/httpx"
	"github.com/xxx-holic/wishtoken-desktop/internal/testutil"
)

func TestPrimerFailureDiagnosticsDoNotExposeRawUpstreamText(t *testing.T) {
	for _, tc := range []struct{ name, stream, code string }{
		{"server", "data: {\"type\":\"error\",\"error\":{\"code\":\"server_error\",\"message\":\"private-fixture-secret\"}}\n\n", "server_error"},
		{"unknown", "data: {\"type\":\"error\",\"error\":{\"code\":\"private-fixture-secret\"}}\n\n", "upstream_failed"},
		{"incomplete", "data: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\"}}\n\n", "response_incomplete"},
		{"empty", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n", "empty_output"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := api.Collect(strings.NewReader(tc.stream))
			code, message := primerResponseFailure(c, err, false)
			if code != tc.code || strings.Contains(message, "private-fixture-secret") {
				t.Fatalf("unexpected diagnostic: %q %q", code, message)
			}
		})
	}
	if code, _ := primerResponseFailure(nil, io.ErrUnexpectedEOF, false); code != "stream_interrupted" {
		t.Fatal(code)
	}
	if code, _ := primerResponseFailure(nil, nil, true); code != "response_too_large" {
		t.Fatal(code)
	}
}

func primerFixture(t *testing.T) (*fixture, string, string) {
	t.Helper()
	t.Setenv("GPTBRIDGE_HOME", t.TempDir())
	cfg := config.Default()
	cfg.APIKey = "primer-fixture-key"
	cfg.DesktopMode = true
	cfg.RoutePolicy = config.PolicyBPSOnly
	cfg.NativeFallback = false
	makeAccount := func(client string) account.Account {
		claims := testutil.ChatGPTClaims("primer@example.test", "workspace", "pro", time.Now().Add(time.Hour))
		claims["client_id"] = client
		token := testutil.MakeJWT(claims)
		return account.Account{AccessToken: token, OAuthClientID: client}
	}
	f := newFixture(t, cfg, makeAccount(account.CodexOAuthClientID), makeAccount(account.BPSOAuthClientID))
	f.srv.bpsNativeDelay = func() time.Duration { return 0 }
	access := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Basispoints-Auth-Mode") != "chatgpt" || r.Header.Get("Chatgpt-Account-Id") != "workspace" {
			t.Error("catalog identity")
		}
		io.WriteString(w, `{"allowed":true,"model_catalog":{"models":[{"id":"gpt-6-astra","efforts":[{"value":"low"}]},{"id":"gpt-6.1-sol"}]}}`)
	}))
	t.Cleanup(access.Close)
	f.srv.Router.BPS.AccessURL = access.URL
	var native, bps string
	for _, a := range f.srv.Store.List() {
		if a.OAuthClientID == account.CodexOAuthClientID {
			native = a.ID
		} else {
			bps = a.ID
		}
	}
	t.Cleanup(func() {
		f.srv.bpsNativeMu.Lock()
		if f.srv.bpsNativeCancel != nil {
			f.srv.bpsNativeCancel()
		}
		f.srv.bpsNativeMu.Unlock()
		for i := 0; i < 200; i++ {
			f.srv.bpsNativeMu.Lock()
			active := f.srv.bpsNativeActive
			f.srv.bpsNativeMu.Unlock()
			if active == "" {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	})
	return f, native, bps
}

func primerWait(t *testing.T, f *fixture, id string) *bpsNativeJob {
	t.Helper()
	for i := 0; i < 500; i++ {
		f.srv.bpsNativeMu.Lock()
		job, err := f.srv.bpsNativeRead(id)
		active := f.srv.bpsNativeActive
		f.srv.bpsNativeMu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if active == "" && job.State != "idle" {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("primer did not finish")
	return nil
}

func TestBPSNativeSequenceGateResumeAndProfile(t *testing.T) {
	f, n, b := primerFixture(t)
	body := object{"model": "gpt-6-astra", "input": "actual user question", "reasoning": object{"effort": "high"}, "stream": false}
	headers := map[string]string{"X-GPTBridge-Account": n, "X-GPTBridge-Channel": "bps_native"}
	if r, _ := primerPost(t, f.api.URL+"/v1/responses", body, headers); r.StatusCode != 409 || f.codexHit.Load() != 0 || f.bpsHits.Load() != 0 {
		t.Fatal("native executed before primer")
	}
	start := object{"account_id": n, "bps_account_id": b, "total_requests": 3}
	if r, raw := primerPost(t, f.api.URL+"/api/bps-native/start", start, nil); r.StatusCode != 200 {
		t.Fatalf("%d %s", r.StatusCode, raw)
	}
	job := primerWait(t, f, n)
	if job.State != "ready" || job.Completed != 3 || f.bpsHits.Load() != 3 || f.codexHit.Load() != 0 {
		t.Fatalf("unexpected sequence %+v", job)
	}
	wire := f.bpsBody.Load().(object)
	if wire["reasoning_effort"] != "low" || wire["service_tier"] != nil || wire["model_selection"] != "explicit" || wire["instructions"] != nil {
		t.Fatal(wire)
	}
	if !strings.Contains(f.bpsHdr.Load().(http.Header).Get("Authorization"), "eyJ") {
		t.Fatal("missing independent grant")
	}
	if r, raw := primerPost(t, f.api.URL+"/v1/responses", body, headers); r.StatusCode != 200 || !strings.Contains(raw, "pong from codex") {
		t.Fatalf("%d %s", r.StatusCode, raw)
	}
	if f.codexHit.Load() != 1 || f.bpsHits.Load() != 3 {
		t.Fatal("native call re-primed or fell back")
	}
	native := f.codexBody.Load().(object)
	if native["model"] != "gpt-6-astra" || native["reasoning"].(map[string]any)["effort"] != "high" || !strings.Contains(string(mustJSON(native)), "actual user question") {
		t.Fatal("native request changed")
	}
	if r, _ := primerPost(t, f.api.URL+"/api/bps-native/start", start, nil); r.StatusCode != 200 || f.bpsHits.Load() != 3 {
		t.Fatal("ready record not reused")
	}
	launch := object{"account_id": n, "channel": "bps_native", "model": "gpt-6-astra", "effort": "high", "speed": "fast", "directory": t.TempDir(), "prepare_only": true}
	r, raw := primerPost(t, f.api.URL+"/api/codex/launch", launch, nil)
	if r.StatusCode != 200 {
		t.Fatalf("profile %d %s", r.StatusCode, raw)
	}
	var prepared struct {
		Home string `json:"home"`
	}
	json.Unmarshal([]byte(raw), &prepared)
	config, err := os.ReadFile(filepath.Join(prepared.Home, "config.toml"))
	if err != nil || !strings.Contains(string(config), `"X-GPTBridge-Channel" = "bps_native"`) || !strings.Contains(string(config), `service_tier = "priority"`) {
		t.Fatalf("profile not pinned: %s %v", config, err)
	}
	_ = f.srv.Store.Update(n, func(a *account.Account) { a.ProxyURL = "http://127.0.0.1:1" })
	if r, _ := primerPost(t, f.api.URL+"/v1/responses", body, headers); r.StatusCode != 409 || f.codexHit.Load() != 1 {
		t.Fatal("stale binding accepted")
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func TestBPSNativePartialFailureAndContinue(t *testing.T) {
	f, n, b := primerFixture(t)
	var hits atomic.Int32
	var fail atomic.Bool
	fail.Store(true)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := hits.Add(1)
		if i == 2 && fail.Load() {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, textStream("probe", "hi"))
	}))
	defer up.Close()
	f.srv.Router.BPS.ResponsesURL = up.URL
	start := object{"account_id": n, "bps_account_id": b, "total_requests": 3}
	primerPost(t, f.api.URL+"/api/bps-native/start", start, nil)
	job := primerWait(t, f, n)
	if job.State != "failed" || job.Completed != 1 || f.codexHit.Load() != 0 {
		t.Fatalf("failure incorrectly delivered %+v", job)
	}
	if f.srv.bpsNativeReady(n) == nil {
		t.Fatal("failed job ready")
	}
	fail.Store(false)
	primerPost(t, f.api.URL+"/api/bps-native/start", start, nil)
	job = primerWait(t, f, n)
	if job.State != "ready" || job.Completed != 3 || hits.Load() != 4 {
		t.Fatalf("did not resume remaining requests %+v hits=%d", job, hits.Load())
	}
}

func TestBPSNativeSSERateLimitKeepsProgressAndBacksOff(t *testing.T) {
	f, n, b := primerFixture(t)
	var hits atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Retry-After", "180")
		if hits.Add(1) == 1 {
			io.WriteString(w, textStream("probe", "hi"))
			return
		}
		io.WriteString(w, "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"rate_limit_exceeded\",\"message\":\"private-fixture-secret\"}}}\n\n")
	}))
	defer up.Close()
	f.srv.Router.BPS.ResponsesURL = up.URL
	start := object{"account_id": n, "bps_account_id": b, "total_requests": 3}
	primerPost(t, f.api.URL+"/api/bps-native/start", start, nil)
	job := primerWait(t, f, n)
	if job.State != "failed" || job.Completed != 1 || job.ErrorCode != "rate_limit_exceeded" || time.Until(job.RetryAt) < 170*time.Second || strings.Contains(job.Error, "private-fixture-secret") {
		t.Fatalf("SSE limit lost progress or retry deadline: %+v", job)
	}
	for _, restart := range []bool{false, true} {
		start["restart"] = restart
		if r, _ := primerPost(t, f.api.URL+"/api/bps-native/start", start, nil); r.StatusCode != 429 {
			t.Fatal("SSE cooldown bypassed")
		}
	}
	if hits.Load() != 2 || f.codexHit.Load() != 0 {
		t.Fatal("automatic replay or native fallback")
	}
}

func TestPrimerRetryAtWithoutServerReset(t *testing.T) {
	for _, value := range []string{"", "bad", "-1", "9223372036854775807"} {
		remaining := time.Until(primerRetryAt(http.Header{"Retry-After": []string{value}}))
		if remaining < 89*time.Second || remaining > 91*time.Second {
			t.Fatal("invalid retry header must retain local backoff")
		}
	}
}

func TestBPSNativeCancelProbeHoldAndRateLimit(t *testing.T) {
	f, n, b := primerFixture(t)
	entered := make(chan struct{}, 1)
	var mode atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode.Load() == 0 {
			io.Copy(io.Discard, r.Body)
			entered <- struct{}{}
			<-r.Context().Done()
			return
		}
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(429)
	}))
	defer up.Close()
	f.srv.Router.BPS.ResponsesURL = up.URL
	start := object{"account_id": n, "bps_account_id": b, "total_requests": 1}
	primerPost(t, f.api.URL+"/api/bps-native/start", start, nil)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("not started")
	}
	if _, err := f.srv.Pool.ProbeUsage(context.Background(), n, f.srv.Codex.Identity); err == nil {
		t.Fatal("native usage probe was not held")
	}
	primerPost(t, f.api.URL+"/api/bps-native/cancel", object{"account_id": n}, nil)
	job := primerWait(t, f, n)
	if job.State != "cancelled" || job.Completed != 0 || f.codexHit.Load() != 0 {
		t.Fatal(job)
	}
	mode.Store(1)
	primerPost(t, f.api.URL+"/api/bps-native/start", start, nil)
	job = primerWait(t, f, n)
	if job.State != "failed" || time.Until(job.RetryAt) < 110*time.Second {
		t.Fatal(job)
	}
	start["restart"] = true
	if r, _ := primerPost(t, f.api.URL+"/api/bps-native/start", start, nil); r.StatusCode != 429 {
		t.Fatal("restart bypassed Retry-After")
	}
}

func TestBPSNativeRejectsIdentityMismatchAndIncomplete(t *testing.T) {
	for _, scenario := range []string{"user", "workspace", "proxy", "incomplete", "deny"} {
		t.Run(scenario, func(t *testing.T) {
			f, n, b := primerFixture(t)
			if scenario == "user" || scenario == "workspace" || scenario == "proxy" {
				_ = f.srv.Store.Update(b, func(a *account.Account) {
					switch scenario {
					case "user":
						a.UserID = "another"
					case "workspace":
						a.AccountID = "another"
					case "proxy":
						a.ProxyURL = "http://127.0.0.1:1"
					}
				})
			} else {
				f.bpsMode.Store(scenario)
			}
			r, _ := primerPost(t, f.api.URL+"/api/bps-native/start", object{"account_id": n, "bps_account_id": b, "total_requests": 1}, nil)
			if scenario == "user" || scenario == "workspace" || scenario == "proxy" {
				if r.StatusCode != 400 || f.bpsHits.Load() != 0 {
					t.Fatal("mismatch accepted")
				}
			} else {
				if primerWait(t, f, n).State != "failed" {
					t.Fatal("failure delivered")
				}
			}
			if f.codexHit.Load() != 0 {
				t.Fatal("native fallback")
			}
		})
	}
}

func TestBPSNativeRefreshOccursOnlyAfterComplete(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(strconv.FormatBool(reject), func(t *testing.T) {
			f, n, b := primerFixture(t)
			proxy := "http://127.0.0.1:" + strings.Split(f.api.URL, ":")[2]
			for _, id := range []string{n, b} {
				_ = f.srv.Store.Update(id, func(a *account.Account) { a.ProxyURL = proxy })
			}
			_ = f.srv.Store.Update(n, func(a *account.Account) { a.RefreshToken = "synthetic-native-refresh" })
			var refreshes atomic.Int32
			transport := identityTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "auth.openai.com" {
					refreshes.Add(1)
					if f.bpsHits.Load() != 2 || f.codexHit.Load() != 0 {
						t.Error("native refresh preceded BPS completion")
					}
					r.ParseForm()
					if r.Form.Get("client_id") != account.CodexOAuthClientID {
						t.Error("wrong refresh client")
					}
					status := 200
					body := ""
					if reject {
						status = 400
						body = `{"error":"invalid_grant"}`
					} else {
						a, _ := f.srv.Store.Get(n)
						body = string(mustJSON(object{"access_token": a.AccessToken, "refresh_token": "synthetic-rotated", "expires_in": 3600}))
					}
					return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
				}
				if r.URL.Host != strings.TrimPrefix(f.srv.Router.BPS.ResponsesURL, "http://")[0:strings.Index(strings.TrimPrefix(f.srv.Router.BPS.ResponsesURL, "http://"), "/")] && r.URL.String() != f.srv.Router.BPS.AccessURL {
					return nil, fmt.Errorf("unexpected external request")
				}
				return http.DefaultTransport.RoundTrip(r)
			})
			for _, opts := range []httpx.Options{{ProxyURL: proxy, Timeout: 60 * time.Second}, {ProxyURL: proxy, ResponseHeaderTimeout: 30 * time.Second}, {ProxyURL: proxy, ResponseHeaderTimeout: 10 * time.Minute}} {
				client, err := httpx.NewClient(opts)
				if err != nil {
					t.Fatal(err)
				}
				old := client.Transport
				client.Transport = transport
				t.Cleanup(func() { client.Transport = old })
			}
			primerPost(t, f.api.URL+"/api/bps-native/start", object{"account_id": n, "bps_account_id": b, "total_requests": 2}, nil)
			job := primerWait(t, f, n)
			if refreshes.Load() != 1 || job.Completed != 2 {
				t.Fatal(job, refreshes.Load())
			}
			if reject && job.State != "failed" || !reject && job.State != "ready" {
				t.Fatal(job)
			}
			if !reject {
				a, _ := f.srv.Store.Get(n)
				if a.RefreshToken != "synthetic-rotated" {
					t.Fatal("rotated grant not saved")
				}
			}
			reloaded := New(f.srv.Config(), f.srv.cfgPath, f.srv.Store)
			if (reloaded.bpsNativeReady(n) == nil) == reject {
				t.Fatal("persistent gate lost result")
			}
		})
	}
}

func primerPost(t *testing.T, target string, body any, headers map[string]string) (*http.Response, string) {
	if headers == nil {
		headers = map[string]string{}
	}
	headers["Authorization"] = "Bearer primer-fixture-key"
	return post(t, target, body, headers)
}
