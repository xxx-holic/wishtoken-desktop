// Package oauth talks to the OpenAI OAuth endpoints used by Codex CLI:
// refresh-token exchange, PKCE login and the ChatGPT usage probe.
package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/xxx-holic/wishtoken-desktop/internal/account"
	"github.com/xxx-holic/wishtoken-desktop/internal/jwt"
	"github.com/xxx-holic/wishtoken-desktop/internal/version"
)

const (
	// TokenURL is the OAuth token endpoint.
	TokenURL = "https://auth.openai.com/oauth/token"
	// AuthorizeURL is the OAuth authorization endpoint.
	AuthorizeURL = "https://auth.openai.com/oauth/authorize"
	// ClientID is the public Codex CLI OAuth client.
	ClientID = account.CodexOAuthClientID
	// RefreshScope is sent on refresh; it never widens the original grant.
	RefreshScope = "openid profile email"
	// LoginScope is requested by the PKCE login flow.
	LoginScope = "openid profile email offline_access"
	// RedirectURI must match the client registration exactly.
	RedirectURI = "http://localhost:1455/auth/callback"
)

// Tokens is the result of a token exchange.
type Tokens struct {
	AccessToken   string
	RefreshToken  string
	IDToken       string
	ExpiresAt     time.Time
	OAuthClientID string
}

// HTTPError is a non-200 answer from the token endpoint.
type HTTPError struct {
	Status      int
	Code        string
	Description string
}

func (e *HTTPError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("oauth token endpoint returned %d: %s (%s)", e.Status, e.Code, e.Description)
	}
	return fmt.Sprintf("oauth token endpoint returned %d", e.Status)
}

// Permanent reports whether retrying with the same refresh token is futile.
func (e *HTTPError) Permanent() bool {
	switch e.Code {
	case "invalid_grant", "invalid_client", "unauthorized_client", "invalid_request", "unsupported_grant_type", "token_revoked", "token_invalidated":
		return true
	}
	return e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden
}

// IsPermanent reports whether err is a permanent refresh failure.
func IsPermanent(err error) bool {
	var he *HTTPError
	return errors.As(err, &he) && he.Permanent()
}

// Refresh exchanges a refresh token for fresh tokens.
func Refresh(ctx context.Context, client *http.Client, refreshToken string) (*Tokens, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return nil, errors.New("refresh token is empty")
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {ClientID},
		"refresh_token": {refreshToken},
		"scope":         {RefreshScope},
	}
	return exchange(ctx, client, form, refreshToken)
}

// ExchangeCode completes a PKCE authorization code flow.
func ExchangeCode(ctx context.Context, client *http.Client, code, verifier, redirectURI string) (*Tokens, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {ClientID},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
	}
	return exchange(ctx, client, form, "")
}

func exchange(ctx context.Context, client *http.Client, form url.Values, previousRefresh string) (*Tokens, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", version.UserAgent())
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read oauth response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		he := &HTTPError{Status: resp.StatusCode}
		var payload struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		if json.Unmarshal(body, &payload) == nil {
			he.Code = payload.Error
			he.Description = payload.ErrorDescription
		}
		return nil, he
	}
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse oauth response: %w", err)
	}
	payload.AccessToken = strings.TrimSpace(payload.AccessToken)
	if payload.AccessToken == "" {
		return nil, errors.New("oauth response has no access_token")
	}
	if payload.ExpiresIn <= 0 {
		payload.ExpiresIn = 3600
	}
	t := &Tokens{
		OAuthClientID: form.Get("client_id"),
		AccessToken:   payload.AccessToken,
		RefreshToken:  strings.TrimSpace(payload.RefreshToken),
		IDToken:       strings.TrimSpace(payload.IDToken),
		ExpiresAt:     time.Now().Add(time.Duration(payload.ExpiresIn) * time.Second),
	}
	if exp := jwt.Expiry(t.AccessToken); !exp.IsZero() && exp.Before(t.ExpiresAt) {
		t.ExpiresAt = exp
	}
	if t.RefreshToken == "" {
		t.RefreshToken = previousRefresh
	}
	return t, nil
}

// RefreshWithRetry retries transient failures with exponential backoff.
func RefreshWithRetry(ctx context.Context, client *http.Client, refreshToken string, attempts int) (*Tokens, error) {
	if attempts < 1 {
		attempts = 1
	}
	var last error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(1<<uint(i-1)) * time.Second):
			}
		}
		t, err := Refresh(ctx, client, refreshToken)
		if err == nil {
			return t, nil
		}
		last = err
		if IsPermanent(err) || errors.Is(err, context.Canceled) {
			return nil, err
		}
	}
	return nil, last
}

// Apply writes fresh tokens into acc and completes identity fields.
func Apply(acc *account.Account, t *Tokens) {
	if t.OAuthClientID != "" {
		acc.OAuthClientID = t.OAuthClientID
	}
	acc.AccessToken = t.AccessToken
	if t.RefreshToken != "" {
		acc.RefreshToken = t.RefreshToken
	}
	if t.IDToken != "" {
		acc.IDToken = t.IDToken
	}
	acc.ExpiresAt = t.ExpiresAt
	acc.LastRefresh = time.Now()
	acc.LastError = ""
	acc.RefreshFailures = 0
	// Identity claims of the new tokens are authoritative.
	if claims, err := jwt.Decode(t.IDToken); err == nil {
		if claims.Email != "" {
			acc.Email = claims.Email
		}
		// Keep the explicitly imported Team workspace. A refreshed ID token
		// may describe the member's personal default workspace.
		if claims.AccountID != "" && acc.AccountID == "" {
			acc.AccountID = claims.AccountID
		}
		if claims.UserID != "" {
			acc.UserID = claims.UserID
		}
		if claims.PlanType != "" && (acc.PlanType == "" || claims.AccountID == acc.AccountID) {
			acc.PlanType = claims.PlanType
		}
		if !claims.SubscriptionUntil.IsZero() {
			acc.SubscriptionUntil = claims.SubscriptionUntil
		}
	}
	acc.FillFromTokens()
}

// RefreshAccount refreshes acc in place. The caller persists the result.
func RefreshAccount(ctx context.Context, client *http.Client, acc *account.Account) error {
	if strings.TrimSpace(acc.RefreshToken) == "" {
		return errors.New("account has no refresh token")
	}
	acc.FillFromTokens()
	var t *Tokens
	var err error
	switch acc.OAuthClientID {
	case account.BPSOAuthClientID:
		// Rotating Office refresh grants are never retried automatically.
		t, err = refreshBPS(ctx, client, acc)
	case "", ClientID:
		t, err = RefreshWithRetry(ctx, client, acc.RefreshToken, 3)
	default:
		err = errors.New("unsupported OAuth client; reauthorize using Codex or BPS login")
	}
	if err != nil {
		acc.RefreshFailures++
		acc.LastError = err.Error()
		if IsPermanent(err) {
			acc.LastError = "refresh token rejected; log in again and re-import: " + err.Error()
		}
		return err
	}
	Apply(acc, t)
	return nil
}
