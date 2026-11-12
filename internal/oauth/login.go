package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// LoginSession is one in-flight PKCE browser login. It binds the Codex
// callback port (1455) on localhost because the OAuth client only accepts
// that exact redirect URI.
type LoginSession struct {
	AuthURL  string
	UserCode string
	Channel  string
	cancel   context.CancelFunc

	state    string
	verifier string
	client   *http.Client
	server   *http.Server
	listener net.Listener

	mu     sync.Mutex
	done   chan struct{}
	tokens *Tokens
	err    error
}

// StartLogin opens the callback listener and returns the URL to open in a
// browser. Call Wait to obtain the tokens.
func StartLogin(ctx context.Context, client *http.Client) (*LoginSession, error) {
	verifier, err := randomString(64)
	if err != nil {
		return nil, err
	}
	state, err := randomString(32)
	if err != nil {
		return nil, err
	}
	redirect, err := url.Parse(RedirectURI)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:"+redirect.Port())
	if err != nil {
		return nil, fmt.Errorf("cannot bind %s (is another login or Codex CLI login running?): %w", redirect.Host, err)
	}
	s := &LoginSession{state: state, verifier: verifier, client: client, listener: listener, done: make(chan struct{}), Channel: "codex"}
	sum := sha256.Sum256([]byte(verifier))
	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", ClientID)
	params.Set("redirect_uri", RedirectURI)
	params.Set("scope", LoginScope)
	params.Set("state", state)
	params.Set("code_challenge", base64.RawURLEncoding.EncodeToString(sum[:]))
	params.Set("code_challenge_method", "S256")
	params.Set("id_token_add_organizations", "true")
	params.Set("codex_cli_simplified_flow", "true")
	s.AuthURL = AuthorizeURL + "?" + params.Encode()

	mux := http.NewServeMux()
	mux.HandleFunc(redirect.Path, s.handleCallback)
	s.server = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = s.server.Serve(listener) }()
	go func() {
		select {
		case <-ctx.Done():
			s.finish(nil, ctx.Err())
		case <-s.done:
		}
	}()
	return s, nil
}

func (s *LoginSession) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if errCode := q.Get("error"); errCode != "" {
		http.Error(w, "登录失败 / login failed: "+errCode+" "+q.Get("error_description"), http.StatusBadRequest)
		s.finish(nil, fmt.Errorf("authorization failed: %s %s", errCode, q.Get("error_description")))
		return
	}
	if q.Get("state") != s.state {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	code := q.Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	tokens, err := ExchangeCode(ctx, s.client, code, s.verifier, RedirectURI)
	if err != nil {
		http.Error(w, "token exchange failed: "+err.Error(), http.StatusBadGateway)
		s.finish(nil, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8"><title>GPTBridge</title></head><body style="font-family:system-ui;padding:40px;text-align:center"><h2>登录成功 / Signed in</h2><p>账号已导入 GPTBridge，可以关闭此页面。<br>The account was imported into GPTBridge. You can close this page.</p></body></html>`))
	s.finish(tokens, nil)
}

func (s *LoginSession) finish(t *Tokens, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.done:
		return
	default:
	}
	s.tokens, s.err = t, err
	close(s.done)
	if s.cancel != nil {
		s.cancel()
	}
	if s.server == nil {
		return
	}
	go func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.server.Shutdown(shutdownCtx)
	}()
}

// Wait blocks until the browser completes the flow or ctx ends.
func (s *LoginSession) Wait(ctx context.Context) (*Tokens, error) {
	select {
	case <-ctx.Done():
		s.finish(nil, ctx.Err())
		return nil, ctx.Err()
	case <-s.done:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	if s.tokens == nil {
		return nil, errors.New("login cancelled")
	}
	return s.tokens, nil
}

// Done reports whether the session finished.
func (s *LoginSession) Done() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// Close aborts the session.
func (s *LoginSession) Close() { s.finish(nil, errors.New("login cancelled")) }

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return strings.TrimRight(base64.RawURLEncoding.EncodeToString(b), "="), nil
}
