// Package pool selects accounts for requests, keeps their tokens fresh and
// tracks temporary failures (rate limits, denials) in memory.
package pool

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/xxx-holic/wishtoken-desktop/internal/account"
	"github.com/xxx-holic/wishtoken-desktop/internal/config"
	"github.com/xxx-holic/wishtoken-desktop/internal/httpx"
	"github.com/xxx-holic/wishtoken-desktop/internal/oauth"
	"github.com/xxx-holic/wishtoken-desktop/internal/upstream"
)

// Errors returned by Pick.
var (
	ErrNoAccounts     = errors.New("no usable account: import a ChatGPT login first")
	ErrAllUnavailable = errors.New("every account is cooling down or unusable")
)

const (
	refreshAhead    = 5 * time.Minute
	stickyTTL       = 2 * time.Hour
	stickyMax       = 4096
	deniedTTL       = 30 * time.Minute
	defaultCooldown = 15 * time.Second
)

type cooldown struct {
	until  time.Time
	reason string
}

type stickyEntry struct {
	accountID string
	seen      time.Time
}

// Pool wraps the account store with scheduling state.
type Pool struct {
	store *account.Store
	cfg   func() *config.Config

	mu        sync.Mutex
	cooldowns map[string]cooldown
	sticky    map[string]stickyEntry
	denied    map[string]map[string]time.Time
	rr        int

	refreshing sync.Map // account id -> *sync.Mutex
	slots      sync.Map // account id -> buffered channel
}

// Acquire reserves one of five per-member slots until the stream is closed.
func (p *Pool) Acquire(ctx context.Context, id string) (func(), error) {
	v, _ := p.slots.LoadOrStore(id, make(chan struct{}, 5))
	ch := v.(chan struct{})
	wait, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	select {
	case ch <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-ch }) }, nil
	case <-wait.Done():
		return nil, wait.Err()
	}
}

// New builds a pool over store. cfg is consulted on every call so settings
// can change at runtime.
func New(store *account.Store, cfg func() *config.Config) *Pool {
	return &Pool{store: store, cfg: cfg, cooldowns: map[string]cooldown{}, sticky: map[string]stickyEntry{}, denied: map[string]map[string]time.Time{}}
}

// Store exposes the underlying account store.
func (p *Pool) Store() *account.Store { return p.store }

// PickOptions describe the request needing an account.
type PickOptions struct {
	// AccountID pins this request. Never substitute another member on failure.
	AccountID string
	// SessionKey keeps a conversation on the same account when possible.
	SessionKey string
	// Route is "bps" or "codex"; BPS model denials exclude accounts.
	Route string
	// Model is the upstream model name.
	Model string
	// Exclude lists account ids already tried for this request.
	Exclude map[string]bool
}

// Pick returns a copy of the chosen account with a fresh access token.
func (p *Pool) Pick(ctx context.Context, opts PickOptions) (*account.Account, error) {
	if opts.AccountID != "" && opts.Route == "codex" {
		if acc, ok := p.store.Get(opts.AccountID); ok && acc.OAuthClientID == account.BPSOAuthClientID {
			return nil, errors.New("selected account has BPS authorization; select its separate Codex login for the native channel")
		}
	}
	candidates := p.candidates(opts)
	if len(candidates) == 0 {
		if p.store.Count() == 0 {
			return nil, ErrNoAccounts
		}
		return nil, ErrAllUnavailable
	}
	var lastErr error
	for _, acc := range candidates {
		fresh, err := p.Ensure(ctx, acc.ID)
		if err != nil {
			lastErr = err
			continue
		}
		if !fresh.Usable() {
			lastErr = fmt.Errorf("account %s is not usable", fresh.Label())
			continue
		}
		p.remember(opts.SessionKey, fresh.ID)
		return fresh, nil
	}
	if lastErr != nil {
		return nil, fmt.Errorf("%w (%v)", ErrAllUnavailable, lastErr)
	}
	return nil, ErrAllUnavailable
}

func (p *Pool) candidates(opts PickOptions) []account.Account {
	all := p.store.List()
	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	usable := make([]account.Account, 0, len(all))
	for _, acc := range all {
		if opts.Route == "codex" && acc.OAuthClientID == account.BPSOAuthClientID {
			continue
		}
		if opts.AccountID != "" && acc.ID != opts.AccountID {
			continue
		}
		if acc.Disabled || !acc.HasCredentials() || opts.Exclude[acc.ID] {
			continue
		}
		if acc.RefreshToken == "" && acc.Expired() {
			continue
		}
		if cd, ok := p.cooldowns[acc.ID]; ok {
			if now.Before(cd.until) {
				continue
			}
			delete(p.cooldowns, acc.ID)
		}
		if opts.Route == "bps" {
			if until, ok := p.denied[acc.ID][strings.ToLower(opts.Model)]; ok && now.Before(until) {
				continue
			}
		}
		usable = append(usable, acc)
	}
	if len(usable) == 0 {
		return nil
	}
	cfg := p.cfg()
	// Sticky sessions first.
	if opts.SessionKey != "" && cfg.Scheduler == config.SchedulerSticky {
		if entry, ok := p.sticky[opts.SessionKey]; ok && now.Sub(entry.seen) < stickyTTL {
			for i, acc := range usable {
				if acc.ID == entry.accountID {
					usable[0], usable[i] = usable[i], usable[0]
					return usable
				}
			}
		}
	}
	switch cfg.Scheduler {
	case config.SchedulerLeastUsed:
		sort.SliceStable(usable, func(i, j int) bool {
			return usable[i].Stats.Requests < usable[j].Stats.Requests
		})
	default:
		if len(usable) > 1 {
			p.rr = (p.rr + 1) % len(usable)
			rotated := append([]account.Account{}, usable[p.rr:]...)
			rotated = append(rotated, usable[:p.rr]...)
			usable = rotated
		}
	}
	return usable
}

func (p *Pool) remember(sessionKey, accountID string) {
	if sessionKey == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.sticky) >= stickyMax {
		var oldestKey string
		var oldest time.Time
		for k, v := range p.sticky {
			if oldest.IsZero() || v.seen.Before(oldest) {
				oldestKey, oldest = k, v.seen
			}
		}
		delete(p.sticky, oldestKey)
	}
	p.sticky[sessionKey] = stickyEntry{accountID: accountID, seen: time.Now()}
}

func (p *Pool) refreshLock(id string) *sync.Mutex {
	mu, _ := p.refreshing.LoadOrStore(id, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

// Ensure returns account id with a valid access token, refreshing when the
// token is missing or expires within five minutes.
func (p *Pool) Ensure(ctx context.Context, id string) (*account.Account, error) {
	acc, ok := p.store.Get(id)
	if !ok {
		return nil, account.ErrNotFound
	}
	if acc.AccessToken != "" && acc.AccountID != "" && !acc.ExpiringWithin(refreshAhead) {
		return &acc, nil
	}
	return p.Refresh(ctx, id, false)
}

// Refresh exchanges the refresh token of account id. With force=false a token
// that is still fresh is returned unchanged.
func (p *Pool) Refresh(ctx context.Context, id string, force bool) (*account.Account, error) {
	mu := p.refreshLock(id)
	mu.Lock()
	defer mu.Unlock()
	acc, ok := p.store.Get(id)
	if !ok {
		return nil, account.ErrNotFound
	}
	if !force && acc.AccessToken != "" && acc.AccountID != "" && !acc.ExpiringWithin(refreshAhead) {
		return &acc, nil
	}
	if acc.OAuthClientID == account.BPSOAuthClientID && acc.RefreshFailures > 0 {
		return nil, errors.New("BPS refresh previously failed; authorize BPS again to avoid replaying a rotated refresh grant")
	}
	if strings.TrimSpace(acc.RefreshToken) == "" {
		if acc.AccessToken != "" && acc.AccountID != "" && !acc.Expired() {
			return &acc, nil
		}
		return nil, fmt.Errorf("account %s has no refresh token and its access token is unusable", acc.Label())
	}
	client, err := httpx.NewClient(httpx.Options{ProxyURL: p.proxyFor(&acc), Timeout: 60 * time.Second})
	if err != nil {
		return nil, err
	}
	err = oauth.RefreshAccount(ctx, client, &acc)
	saveErr := p.store.Update(id, func(stored *account.Account) {
		stored.LastError = acc.LastError
		stored.RefreshFailures = acc.RefreshFailures
		if err == nil {
			stored.OAuthClientID = acc.OAuthClientID
			stored.AccessToken, stored.RefreshToken, stored.IDToken = acc.AccessToken, acc.RefreshToken, acc.IDToken
			stored.ExpiresAt, stored.LastRefresh = acc.ExpiresAt, acc.LastRefresh
			stored.Email, stored.AccountID, stored.UserID, stored.PlanType = acc.Email, acc.AccountID, acc.UserID, acc.PlanType
			stored.SubscriptionUntil = acc.SubscriptionUntil
		} else if oauth.IsPermanent(err) && stored.RefreshFailures >= 2 {
			stored.Disabled = true
			stored.LastError = "disabled after repeated refresh rejections: " + err.Error()
		}
	})
	if err != nil {
		log.Printf("[pool] refresh failed for %s: %v", acc.Label(), err)
		return nil, err
	}
	if saveErr != nil {
		log.Printf("[pool] saving refreshed tokens for %s failed: %v", acc.Label(), saveErr)
	}
	return &acc, nil
}

func (p *Pool) proxyFor(acc *account.Account) string {
	if acc != nil && strings.TrimSpace(acc.ProxyURL) != "" {
		return acc.ProxyURL
	}
	return p.cfg().ProxyURL
}

// ProxyFor returns the outbound proxy for acc.
func (p *Pool) ProxyFor(acc *account.Account) string { return p.proxyFor(acc) }

// Credentials builds upstream credentials for acc.
func (p *Pool) Credentials(acc *account.Account) upstream.Credentials {
	return upstream.Credentials{AccessToken: acc.AccessToken, AccountID: acc.AccountID, ProxyURL: p.proxyFor(acc)}
}

// ReportSuccess records a served request.
func (p *Pool) ReportSuccess(id, route string, inputTokens, outputTokens int64) {
	_ = p.store.Update(id, func(acc *account.Account) {
		acc.Stats.Requests++
		if route == "bps" {
			acc.Stats.BPSRequests++
		}
		acc.Stats.InputTokens += inputTokens
		acc.Stats.OutputTokens += outputTokens
		acc.LastUsedAt = time.Now()
		acc.LastError = ""
	})
	p.mu.Lock()
	delete(p.cooldowns, id)
	p.mu.Unlock()
}

// ReportFailure records a failed request without cooling the account down.
func (p *Pool) ReportFailure(id, message string) {
	_ = p.store.Update(id, func(acc *account.Account) {
		acc.Stats.Failures++
		acc.LastError = truncate(message, 300)
	})
}

// ReportRateLimited cools the account down for d (default fifteen seconds).
func (p *Pool) ReportRateLimited(id string, d time.Duration, reason string) {
	if d <= 0 {
		d = defaultCooldown
	}
	if d > 24*time.Hour {
		d = 24 * time.Hour
	}
	p.mu.Lock()
	p.cooldowns[id] = cooldown{until: time.Now().Add(d), reason: reason}
	p.mu.Unlock()
	_ = p.store.Update(id, func(acc *account.Account) {
		acc.Stats.RateLimits++
		acc.LastError = fmt.Sprintf("rate limited (%s); cooling down %s", reason, d.Round(time.Second))
	})
}

// ReportBPSModelDenied excludes the account from Basispoints for model.
func (p *Pool) ReportBPSModelDenied(id, model string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.denied[id] == nil {
		p.denied[id] = map[string]time.Time{}
	}
	p.denied[id][strings.ToLower(model)] = time.Now().Add(deniedTTL)
}

// Cooldown reports the active cooldown of account id.
func (p *Pool) Cooldown(id string) (time.Time, string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cd, ok := p.cooldowns[id]
	if !ok || !time.Now().Before(cd.until) {
		return time.Time{}, "", false
	}
	return cd.until, cd.reason, true
}

// ClearCooldown removes a cooldown (dashboard action).
func (p *Pool) ClearCooldown(id string) {
	p.mu.Lock()
	delete(p.cooldowns, id)
	delete(p.denied, id)
	p.mu.Unlock()
}

// RetryAfter parses the Retry-After / reset headers of a 429 answer.
func RetryAfter(h http.Header) time.Duration {
	for _, name := range []string{"Retry-After", "X-Codex-Primary-Reset-After-Seconds", "x-ratelimit-reset-after"} {
		v := strings.TrimSpace(h.Get(name))
		if v == "" {
			continue
		}
		var seconds float64
		if _, err := fmt.Sscanf(v, "%f", &seconds); err == nil && seconds > 0 {
			return time.Duration(seconds * float64(time.Second))
		}
		if t, err := http.ParseTime(v); err == nil {
			if d := time.Until(t); d > 0 {
				return d
			}
		}
	}
	return 0
}

// RunMaintenance refreshes expiring tokens and probes usage in the
// background until ctx ends.
func (p *Pool) RunMaintenance(ctx context.Context, identity upstream.Identity) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	lastProbe := map[string]time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		cfg := p.cfg()
		for _, acc := range p.store.List() {
			if acc.Disabled || !acc.HasCredentials() {
				continue
			}
			if cfg.AutoRefresh && acc.RefreshToken != "" && acc.ExpiringWithin(10*time.Minute) {
				refreshCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
				_, _ = p.Refresh(refreshCtx, acc.ID, false)
				cancel()
			}
			if cfg.UsageProbe && time.Since(lastProbe[acc.ID]) > 30*time.Minute {
				lastProbe[acc.ID] = time.Now()
				p.ProbeUsage(ctx, acc.ID, identity)
			}
		}
	}
}

// ProbeUsage refreshes the usage snapshot of account id.
func (p *Pool) ProbeUsage(ctx context.Context, id string, identity upstream.Identity) (*account.Usage, error) {
	acc, err := p.Ensure(ctx, id)
	if err != nil {
		return nil, err
	}
	client, err := httpx.NewClient(httpx.Options{ProxyURL: p.proxyFor(acc), Timeout: 30 * time.Second})
	if err != nil {
		return nil, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	usage, err := oauth.QueryUsage(probeCtx, client, acc, oauth.UsageIdentity{UserAgent: identity.UserAgent, Originator: identity.Originator, Version: identity.Version})
	if err != nil {
		return nil, err
	}
	_ = p.store.Update(id, func(stored *account.Account) {
		stored.Usage = usage
		if stored.PlanType == "" {
			stored.PlanType = acc.PlanType
		}
	})
	return usage, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
