package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xxx-holic/wishtoken-desktop/internal/oauth"
)

type loginFixtureTransport struct{}

func (loginFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"device_auth_id":"private-device-id","user_code":"FIXTURE-CODE","interval":300}`)), Header: make(http.Header)}, nil
}

func TestLoginChannelAndSessionBinding(t *testing.T) {
	f := newFixture(t, nil)
	r := httptest.NewRequest("POST", "/api/login/start", strings.NewReader(`{"channel":"work"}`))
	w := httptest.NewRecorder()
	f.srv.adminLoginStart(w, r)
	if w.Code != 400 {
		t.Fatal("invalid channel accepted")
	}
	s, err := oauth.StartBPSLogin(context.Background(), &http.Client{Transport: loginFixtureTransport{}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	f.srv.login = s
	f.srv.loginID = "fixture-session"
	w = httptest.NewRecorder()
	f.srv.adminLoginStart(w, httptest.NewRequest("POST", "/api/login/start", strings.NewReader(`{"channel":"codex"}`)))
	if w.Code != 409 {
		t.Fatal("different client reused existing grant")
	}
	w = httptest.NewRecorder()
	f.srv.adminLoginStart(w, httptest.NewRequest("POST", "/api/login/start", strings.NewReader(`{"channel":"bps"}`)))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"reused":true`) || strings.Contains(w.Body.String(), "private-device-id") {
		t.Fatal("BPS prompt leaked private device state")
	}
	for _, path := range []string{"/api/login/status?session=stale", "/api/login/cancel?session=stale"} {
		w = httptest.NewRecorder()
		r = httptest.NewRequest("POST", path, nil)
		if strings.Contains(path, "cancel") {
			f.srv.adminLoginCancel(w, r)
		} else {
			f.srv.adminLoginStatus(w, r)
		}
		if w.Code != 409 || s.Done() {
			t.Fatal("stale session changed active authorization")
		}
	}
	w = httptest.NewRecorder()
	f.srv.adminLoginCancel(w, httptest.NewRequest("POST", "/api/login/cancel?session=fixture-session", nil))
	if w.Code != 200 || !s.Done() {
		t.Fatal("cancel did not stop authorization")
	}
}
