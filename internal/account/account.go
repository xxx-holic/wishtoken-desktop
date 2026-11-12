// Package account models ChatGPT OAuth accounts and their on-disk store.
//
// Credentials are stored as plain JSON with owner-only permissions in the
// bridge home directory. They are never written anywhere else and never
// returned by the management API in full.
package account

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/xxx-holic/wishtoken-desktop/internal/jwt"
)

const (
	CodexOAuthClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	BPSOAuthClientID   = "app_fnr0pYvVwwFDocDumLG3H2Bp"
)

// Account is one ChatGPT login usable by the bridge.
type Account struct {
	Provider          string    `json:"provider,omitempty"`
	ID                string    `json:"id"`
	Name              string    `json:"name,omitempty"`
	Email             string    `json:"email,omitempty"`
	AccountID         string    `json:"account_id,omitempty"`
	UserID            string    `json:"user_id,omitempty"`
	PlanType          string    `json:"plan_type,omitempty"`
	AccessToken       string    `json:"access_token,omitempty"`
	RefreshToken      string    `json:"refresh_token,omitempty"`
	IDToken           string    `json:"id_token,omitempty"`
	OAuthClientID     string    `json:"oauth_client_id,omitempty"`
	ExpiresAt         time.Time `json:"expires_at"`
	LastRefresh       time.Time `json:"last_refresh"`
	SubscriptionUntil time.Time `json:"subscription_until"`
	Disabled          bool      `json:"disabled,omitempty"`
	ProxyURL          string    `json:"proxy_url,omitempty"`
	Source            string    `json:"source,omitempty"`
	Tags              []string  `json:"tags,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	LastUsedAt        time.Time `json:"last_used_at"`
	LastError         string    `json:"last_error,omitempty"`
	RefreshFailures   int       `json:"refresh_failures,omitempty"`
	Usage             *Usage    `json:"usage,omitempty"`
	Stats             Stats     `json:"stats"`
}

// Usage is a snapshot of the ChatGPT rate limit windows.
type Usage struct {
	Primary      *Window   `json:"primary,omitempty"`
	Secondary    *Window   `json:"secondary,omitempty"`
	LimitReached bool      `json:"limit_reached"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Window is one usage window (5 hours or 7 days).
type Window struct {
	UsedPercent   float64   `json:"used_percent"`
	WindowSeconds int       `json:"window_seconds"`
	ResetAt       time.Time `json:"reset_at"`
}

// Stats are lifetime counters kept for the dashboard.
type Stats struct {
	Requests     int64 `json:"requests"`
	Failures     int64 `json:"failures"`
	RateLimits   int64 `json:"rate_limits"`
	BPSRequests  int64 `json:"bps_requests"`
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// Identity returns the key used to detect duplicates.
func (a *Account) Identity() string {
	if a.Provider == "kilo" {
		if a.AccountID != "" {
			return "kilo:" + a.AccountID
		}
		sum := sha256.Sum256([]byte(a.AccessToken))
		return "kilo-key:" + hex.EncodeToString(sum[:16])
	}
	// Office and Codex issue independent grants for the same member/workspace.
	// Keep their credentials separate while retaining legacy Codex identities.
	if a.OAuthClientID != "" && a.OAuthClientID != CodexOAuthClientID {
		copy := *a
		copy.OAuthClientID = ""
		if identity := copy.Identity(); identity != "" {
			return identity + "|oauth:" + a.OAuthClientID
		}
		return ""
	}
	if id := strings.TrimSpace(a.AccountID); id != "" {
		// A Team workspace is shared by many members. Deduplicate by member
		// AND workspace so importing a second child cannot replace the first.
		member := strings.TrimSpace(a.UserID)
		if member == "" {
			member = strings.ToLower(strings.TrimSpace(a.Email))
		}
		if member == "" {
			credential := a.AccessToken
			if credential == "" {
				credential = a.RefreshToken
			}
			sum := sha256.Sum256([]byte(credential))
			member = hex.EncodeToString(sum[:8])
		}
		return "acct:" + strings.ToLower(id) + "|member:" + member
	}
	if email := strings.TrimSpace(a.Email); email != "" {
		return "email:" + strings.ToLower(email)
	}
	if rt := strings.TrimSpace(a.RefreshToken); rt != "" {
		sum := sha256.Sum256([]byte(rt))
		return "rt:" + hex.EncodeToString(sum[:8])
	}
	if at := strings.TrimSpace(a.AccessToken); at != "" {
		sum := sha256.Sum256([]byte(at))
		return "at:" + hex.EncodeToString(sum[:8])
	}
	return ""
}

// Label is a short display name.
func (a *Account) Label() string {
	if a.Name != "" {
		return a.Name
	}
	if a.Email != "" {
		return a.Email
	}
	if a.AccountID != "" {
		return MaskID(a.AccountID)
	}
	return a.ID
}

// HasCredentials reports whether the account can be refreshed or used.
func (a *Account) HasCredentials() bool {
	return strings.TrimSpace(a.RefreshToken) != "" || strings.TrimSpace(a.AccessToken) != ""
}

// Usable reports whether the account can serve a request right now.
func (a *Account) Usable() bool {
	return !a.Disabled && strings.TrimSpace(a.AccessToken) != "" && strings.TrimSpace(a.AccountID) != "" && !a.Expired()
}

// Expired reports whether the access token is past its expiry.
func (a *Account) Expired() bool {
	return !a.ExpiresAt.IsZero() && !time.Now().Before(a.ExpiresAt)
}

// ExpiringWithin reports whether the access token expires within d.
func (a *Account) ExpiringWithin(d time.Duration) bool {
	if a.ExpiresAt.IsZero() {
		return strings.TrimSpace(a.AccessToken) == ""
	}
	return time.Until(a.ExpiresAt) < d
}

// FillFromTokens completes missing identity fields from the JWT claims.
func (a *Account) FillFromTokens() {
	if a.Provider == "kilo" {
		// Kilo credentials must never be interpreted as a ChatGPT OAuth grant.
		if claims, err := jwt.Decode(a.AccessToken); err == nil {
			if a.AccountID == "" {
				a.AccountID, _ = claims.Raw["sub"].(string)
			}
			if a.Email == "" {
				a.Email, _ = claims.Raw["email"].(string)
			}
			if a.ExpiresAt.IsZero() {
				a.ExpiresAt = jwt.Expiry(a.AccessToken)
			}
		}
		if a.AccountID == "" && a.AccessToken != "" {
			sum := sha256.Sum256([]byte(a.AccessToken))
			a.AccountID = "key-" + hex.EncodeToString(sum[:16])
		}
		return
	}
	if claims, err := jwt.Decode(a.AccessToken); err == nil {
		if client, ok := claims.Raw["client_id"].(string); ok && client != "" {
			a.OAuthClientID = client
		}
	}
	if a.OAuthClientID == "" {
		if claims, err := jwt.Decode(a.IDToken); err == nil {
			switch audience := claims.Raw["aud"].(type) {
			case string:
				if audience == CodexOAuthClientID || audience == BPSOAuthClientID {
					a.OAuthClientID = audience
				}
			case []any:
				for _, item := range audience {
					if item == CodexOAuthClientID || item == BPSOAuthClientID {
						a.OAuthClientID, _ = item.(string)
						break
					}
				}
			}
		}
	}
	for _, token := range []string{a.IDToken, a.AccessToken} {
		if token == "" {
			continue
		}
		claims, err := jwt.Decode(token)
		if err != nil {
			continue
		}
		if a.Email == "" {
			a.Email = claims.Email
		}
		if a.AccountID == "" {
			a.AccountID = claims.AccountID
		}
		if a.UserID == "" {
			a.UserID = claims.UserID
		}
		if a.PlanType == "" {
			a.PlanType = claims.PlanType
		}
		if a.SubscriptionUntil.IsZero() {
			a.SubscriptionUntil = claims.SubscriptionUntil
		}
	}
	if a.ExpiresAt.IsZero() && a.AccessToken != "" {
		a.ExpiresAt = jwt.Expiry(a.AccessToken)
	}
}

// View is the redacted representation exposed to the dashboard.
type View struct {
	Provider          string    `json:"provider,omitempty"`
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Email             string    `json:"email"`
	AccountID         string    `json:"account_id"`
	PlanType          string    `json:"plan_type"`
	OAuthClientID     string    `json:"oauth_client_id,omitempty"`
	ExpiresAt         string    `json:"expires_at,omitempty"`
	LastRefresh       string    `json:"last_refresh,omitempty"`
	SubscriptionUntil string    `json:"subscription_until,omitempty"`
	Disabled          bool      `json:"disabled"`
	HasRefreshToken   bool      `json:"has_refresh_token"`
	HasAccessToken    bool      `json:"has_access_token"`
	Expired           bool      `json:"expired"`
	ProxyURL          string    `json:"proxy_url,omitempty"`
	Source            string    `json:"source,omitempty"`
	Tags              []string  `json:"tags,omitempty"`
	LastUsedAt        string    `json:"last_used_at,omitempty"`
	LastError         string    `json:"last_error,omitempty"`
	Usage             *Usage    `json:"usage,omitempty"`
	Stats             Stats     `json:"stats"`
	Status            string    `json:"status"`
	StatusDetail      string    `json:"status_detail,omitempty"`
	CooldownUntil     string    `json:"cooldown_until,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

// View returns the redacted representation.
func (a *Account) View() View {
	v := View{
		Provider: a.Provider,
		ID:       a.ID, Name: a.Name, Email: a.Email, AccountID: MaskID(a.AccountID), PlanType: a.PlanType,
		Disabled: a.Disabled, HasRefreshToken: a.RefreshToken != "", HasAccessToken: a.AccessToken != "",
		Expired: a.Expired(), ProxyURL: redactProxy(a.ProxyURL), Source: a.Source, Tags: a.Tags,
		LastError: a.LastError, Usage: a.Usage, Stats: a.Stats, CreatedAt: a.CreatedAt,
		OAuthClientID: a.OAuthClientID,
	}
	v.ExpiresAt = formatTime(a.ExpiresAt)
	v.LastRefresh = formatTime(a.LastRefresh)
	v.SubscriptionUntil = formatTime(a.SubscriptionUntil)
	v.LastUsedAt = formatTime(a.LastUsedAt)
	switch {
	case a.Disabled:
		v.Status = "disabled"
	case !a.HasCredentials():
		v.Status = "no_credentials"
	case a.AccountID == "":
		v.Status = "needs_refresh"
	case a.Expired():
		v.Status = "expired"
	case a.LastError != "":
		v.Status = "error"
	default:
		v.Status = "ready"
	}
	return v
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// MaskID keeps the first 8 characters of an identifier.
func MaskID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 8 {
		return id
	}
	return id[:8] + "..."
}

func redactProxy(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	if at := strings.LastIndex(u, "@"); at > 0 {
		if scheme := strings.Index(u, "://"); scheme > 0 && scheme < at {
			return u[:scheme+3] + "***@" + u[at+1:]
		}
	}
	return u
}

// NewID returns a random account identifier.
func NewID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("acc-%d", time.Now().UnixNano())
	}
	return "acc-" + hex.EncodeToString(b[:])
}

// Store is the JSON-backed account list.
type Store struct {
	path     string
	mu       sync.RWMutex
	accounts []*Account
}

type storeFile struct {
	Version  int        `json:"version"`
	Accounts []*Account `json:"accounts"`
}

// Open loads the store at path, creating an empty store when it is missing.
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, fmt.Errorf("read accounts %s: %w", path, err)
	}
	var f storeFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse accounts %s: %w", path, err)
	}
	for _, acc := range f.Accounts {
		if acc == nil {
			continue
		}
		if acc.ID == "" {
			acc.ID = NewID()
		}
		acc.FillFromTokens()
		s.accounts = append(s.accounts, acc)
	}
	return s, nil
}

// Path returns the backing file path.
func (s *Store) Path() string { return s.path }

// Save writes the store atomically with owner-only permissions.
func (s *Store) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(storeFile{Version: 1, Accounts: s.accounts}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// List returns copies of all accounts ordered by creation time.
func (s *Store) List() []Account {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Account, 0, len(s.accounts))
	for _, acc := range s.accounts {
		out = append(out, *acc)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Count returns the number of stored accounts.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.accounts)
}

// Get returns a copy of the account with id.
func (s *Store) Get(id string) (Account, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, acc := range s.accounts {
		if acc.ID == id {
			return *acc, true
		}
	}
	return Account{}, false
}

// UpsertResult describes what Upsert did.
type UpsertResult struct {
	ID      string
	Created bool
}

// Upsert inserts acc or merges it into an existing account with the same
// identity. Newer credentials win; metadata is only filled when missing.
func (s *Store) Upsert(acc Account) (UpsertResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	acc.FillFromTokens()
	identity := acc.Identity()
	now := time.Now()
	for _, existing := range s.accounts {
		if identity == "" || existing.Identity() != identity {
			continue
		}
		mergeInto(existing, &acc)
		existing.UpdatedAt = now
		return UpsertResult{ID: existing.ID}, s.saveLocked()
	}
	if acc.ID == "" {
		acc.ID = NewID()
	}
	if acc.CreatedAt.IsZero() {
		acc.CreatedAt = now
	}
	acc.UpdatedAt = now
	copyAcc := acc
	s.accounts = append(s.accounts, &copyAcc)
	return UpsertResult{ID: copyAcc.ID, Created: true}, s.saveLocked()
}

func mergeInto(dst, src *Account) {
	if src.Provider != "" {
		dst.Provider = src.Provider
	}
	if src.OAuthClientID != "" {
		dst.OAuthClientID = src.OAuthClientID
	}
	if src.AccessToken != "" {
		newer := dst.ExpiresAt.IsZero() || src.ExpiresAt.After(dst.ExpiresAt) || dst.AccessToken == ""
		if newer {
			dst.AccessToken = src.AccessToken
			dst.ExpiresAt = src.ExpiresAt
		}
	}
	if src.RefreshToken != "" && (dst.RefreshToken == "" || !src.LastRefresh.Before(dst.LastRefresh)) {
		dst.RefreshToken = src.RefreshToken
	}
	if src.IDToken != "" {
		dst.IDToken = src.IDToken
	}
	if dst.Email == "" {
		dst.Email = src.Email
	}
	if dst.AccountID == "" {
		dst.AccountID = src.AccountID
	}
	if dst.UserID == "" {
		dst.UserID = src.UserID
	}
	if src.PlanType != "" {
		dst.PlanType = src.PlanType
	}
	if dst.Name == "" {
		dst.Name = src.Name
	}
	if dst.ProxyURL == "" {
		dst.ProxyURL = src.ProxyURL
	}
	if src.LastRefresh.After(dst.LastRefresh) {
		dst.LastRefresh = src.LastRefresh
	}
	if !src.SubscriptionUntil.IsZero() {
		dst.SubscriptionUntil = src.SubscriptionUntil
	}
	if dst.Source == "" {
		dst.Source = src.Source
	}
	dst.LastError = ""
	dst.RefreshFailures = 0
}

// Update applies fn to the account with id under the store lock and saves.
func (s *Store) Update(id string, fn func(*Account)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, acc := range s.accounts {
		if acc.ID == id {
			fn(acc)
			acc.UpdatedAt = time.Now()
			return s.saveLocked()
		}
	}
	return ErrNotFound
}

// Delete removes the account with id.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, acc := range s.accounts {
		if acc.ID == id {
			s.accounts = append(s.accounts[:i], s.accounts[i+1:]...)
			return s.saveLocked()
		}
	}
	return ErrNotFound
}

// ErrNotFound is returned for unknown account ids.
var ErrNotFound = errors.New("account not found")
