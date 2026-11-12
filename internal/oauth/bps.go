package oauth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/xxx-holic/wishtoken-desktop/internal/account"
	"github.com/xxx-holic/wishtoken-desktop/internal/jwt"
)

const bpsAuthBase = "https://auth.openai.com"
const bpsRedirectURI = bpsAuthBase + "/basispoints/deviceauth/callback"

// StartBPSLogin starts the independent Office device grant. Browser approval
// remains in the user's browser; passwords and MFA secrets never enter the app.
func StartBPSLogin(ctx context.Context, client *http.Client) (*LoginSession, error) {
	return startBPSLogin(ctx, client, waitBPSPoll)
}

func startBPSLogin(ctx context.Context, client *http.Client, wait func(context.Context, time.Duration) error) (*LoginSession, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	var code struct {
		ID       string          `json:"device_auth_id"`
		UserCode string          `json:"user_code"`
		Interval json.RawMessage `json:"interval"`
	}
	raw, _ := json.Marshal(map[string]string{"client_id": account.BPSOAuthClientID})
	err := bpsPost(ctx, client, "/api/accounts/deviceauth/usercode", "application/json", bytes.NewReader(raw), &code)
	if err != nil {
		cancel()
		return nil, err
	}
	interval := 5
	if len(code.Interval) != 0 {
		interval, err = strconv.Atoi(strings.Trim(string(code.Interval), `"`))
	}
	if err != nil || interval < 1 || interval > 300 || code.ID == "" || len(code.ID) > 2048 || code.UserCode == "" || len(code.UserCode) > 128 {
		cancel()
		return nil, errors.New("invalid BPS device-code response")
	}
	s := &LoginSession{Channel: "bps", UserCode: code.UserCode, AuthURL: bpsAuthBase + "/api/accounts/deviceauth/authorize?client_id=" + account.BPSOAuthClientID, cancel: cancel, done: make(chan struct{})}
	go func() {
		var grant struct {
			Code      string `json:"authorization_code"`
			Verifier  string `json:"code_verifier"`
			Challenge string `json:"code_challenge"`
		}
		poll, _ := json.Marshal(map[string]string{"client_id": account.BPSOAuthClientID, "device_auth_id": code.ID, "user_code": code.UserCode})
		for {
			if err := wait(ctx, time.Duration(interval)*time.Second); err != nil {
				s.finish(nil, err)
				return
			}
			err := bpsPost(ctx, client, "/api/accounts/deviceauth/token", "application/json", bytes.NewReader(poll), &grant)
			if err == nil {
				break
			}
			var he *HTTPError
			if errors.As(err, &he) {
				if (he.Status == 400 || he.Status == 403) && (he.Code == "deviceauth_authorization_pending" || he.Code == "authorization_pending") {
					continue
				}
				if (he.Status == 400 || he.Status == 429) && he.Code == "slow_down" {
					interval = min(interval+5, 300)
					continue
				}
			}
			s.finish(nil, err)
			return
		}
		if grant.Code == "" || len(grant.Verifier) < 43 || len(grant.Verifier) > 128 {
			s.finish(nil, errors.New("BPS authorization missing code or PKCE verifier"))
			return
		}
		digest := sha256.Sum256([]byte(grant.Verifier))
		if base64.RawURLEncoding.EncodeToString(digest[:]) != grant.Challenge {
			s.finish(nil, errors.New("BPS authorization PKCE mismatch"))
			return
		}
		form := url.Values{"grant_type": {"authorization_code"}, "client_id": {account.BPSOAuthClientID}, "code": {grant.Code}, "code_verifier": {grant.Verifier}, "redirect_uri": {bpsRedirectURI}}
		tokens, err := bpsExchange(ctx, client, form, "")
		s.finish(tokens, err)
	}()
	return s, nil
}

func waitBPSPoll(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// OAuth error bodies can contain credentials or request echoes. Only protocol
// codes from this allowlist leave the BPS boundary; redirects are not followed.
func bpsPost(ctx context.Context, client *http.Client, path, contentType string, body io.Reader, out any) error {
	if client == nil {
		client = http.DefaultClient
	}
	scoped := *client
	scoped.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, bpsAuthBase+path, body)
	if err != nil {
		return errors.New("cannot construct BPS OAuth request")
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	resp, err := scoped.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("BPS OAuth transport failed; check proxy and connection")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return errors.New("cannot read bounded BPS OAuth response")
	}
	if resp.StatusCode != http.StatusOK {
		var payload struct {
			Error json.RawMessage `json:"error"`
		}
		_ = json.Unmarshal(raw, &payload)
		var code string
		if json.Unmarshal(payload.Error, &code) != nil {
			var nested struct {
				Code string `json:"code"`
			}
			_ = json.Unmarshal(payload.Error, &nested)
			code = nested.Code
		}
		switch code {
		case "invalid_grant", "invalid_client", "invalid_request", "unauthorized_client", "unsupported_grant_type", "invalid_scope", "access_denied", "authorization_pending", "deviceauth_authorization_pending", "slow_down", "expired_token", "server_error", "temporarily_unavailable":
		default:
			code = ""
		}
		return &HTTPError{Status: resp.StatusCode, Code: code}
	}
	if json.Unmarshal(raw, out) != nil {
		return errors.New("invalid BPS OAuth JSON response")
	}
	return nil
}

func bpsExchange(ctx context.Context, client *http.Client, form url.Values, previousRefresh string) (*Tokens, error) {
	var data struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := bpsPost(ctx, client, "/oauth/token?unified=true", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()), &data); err != nil {
		return nil, err
	}
	if strings.TrimSpace(data.AccessToken) == "" || !strings.EqualFold(data.TokenType, "Bearer") || data.ExpiresIn <= 0 || data.ExpiresIn > 31536000 {
		return nil, errors.New("invalid BPS OAuth token response")
	}
	if claims, err := jwt.Decode(data.AccessToken); err == nil {
		if id, ok := claims.Raw["client_id"].(string); ok && id != account.BPSOAuthClientID {
			return nil, errors.New("BPS OAuth returned a different client grant")
		}
	}
	t := &Tokens{AccessToken: data.AccessToken, RefreshToken: data.RefreshToken, IDToken: data.IDToken, OAuthClientID: account.BPSOAuthClientID, ExpiresAt: time.Now().Add(time.Duration(data.ExpiresIn) * time.Second)}
	if exp := jwt.Expiry(t.AccessToken); !exp.IsZero() && exp.Before(t.ExpiresAt) {
		t.ExpiresAt = exp
	}
	if t.RefreshToken == "" {
		t.RefreshToken = previousRefresh
	}
	return t, nil
}

func refreshBPS(ctx context.Context, client *http.Client, acc *account.Account) (*Tokens, error) {
	form := url.Values{"grant_type": {"refresh_token"}, "client_id": {account.BPSOAuthClientID}, "refresh_token": {acc.RefreshToken}}
	t, err := bpsExchange(ctx, client, form, acc.RefreshToken)
	if err != nil {
		return nil, err
	}
	oldClaims, oldErr := jwt.Decode(acc.AccessToken)
	newClaims, newErr := jwt.Decode(t.AccessToken)
	if oldErr == nil && (oldClaims.AccountID != "" || oldClaims.UserID != "") {
		if newErr != nil || (oldClaims.AccountID != "" && oldClaims.AccountID != newClaims.AccountID) || (oldClaims.UserID != "" && oldClaims.UserID != newClaims.UserID) {
			return nil, fmt.Errorf("BPS refresh changed member or workspace; reauthorize this account")
		}
	}
	return t, nil
}
