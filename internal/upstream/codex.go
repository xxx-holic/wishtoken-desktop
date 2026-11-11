// Package upstream sends prepared requests to the two ChatGPT backends: the
// native Codex channel and the Basispoints (Excel add-in) gateway.
package upstream

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/xxx-holic/wishtoken-desktop/internal/httpx"
)

const (
	// CodexBaseURL is the ChatGPT backend used by Codex clients.
	CodexBaseURL = "https://chatgpt.com/backend-api/codex"
	// DefaultCodexVersion is the Codex CLI version impersonated for
	// non-Codex downstream clients.
	DefaultCodexVersion = "0.153.3"
	// DefaultOriginator is the originator of the Codex TUI.
	DefaultOriginator = "codex-tui"

	betaFeaturesHeader  = "X-Codex-Beta-Features"
	defaultBetaFeatures = "remote_compaction_v2"
)

// Credentials identify the account used for one upstream call.
type Credentials struct {
	AccessToken string
	AccountID   string
	ProxyURL    string
}

// Identity is the client identity presented on the native channel.
type Identity struct {
	UserAgent  string
	Originator string
	Version    string
}

// DefaultIdentity builds a Codex TUI identity for this platform.
func DefaultIdentity(version string) Identity {
	version = strings.TrimSpace(version)
	if version == "" {
		version = DefaultCodexVersion
	}
	osName, terminal := "Linux 6.8.0", "xterm-256color"
	switch runtime.GOOS {
	case "darwin":
		osName = "Mac OS 15.5.0"
	case "windows":
		osName, terminal = "Windows 10.0.26100", "unknown"
	}
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x86_64"
	}
	return Identity{
		UserAgent:  fmt.Sprintf("%s/%s (%s; %s) %s (%s; %s)", DefaultOriginator, version, osName, arch, terminal, DefaultOriginator, version),
		Originator: DefaultOriginator,
		Version:    version,
	}
}

var officialUserAgentPrefixes = []string{
	"codex-tui/", "codex_cli_rs/", "codex_vscode/", "codex_app/", "codex_chatgpt_desktop/",
	"codex_atlas/", "codex_exec/", "codex_sdk_ts/", "codex ", "opencode/",
}

// IsOfficialCodexClient reports whether the downstream headers come from a
// Codex client whose identity should be preserved on the wire.
func IsOfficialCodexClient(userAgent, originator string) bool {
	ua := strings.ToLower(strings.TrimSpace(userAgent))
	for _, prefix := range officialUserAgentPrefixes {
		if strings.HasPrefix(ua, prefix) {
			return true
		}
	}
	o := strings.ToLower(strings.TrimSpace(originator))
	return strings.HasPrefix(o, "codex") || o == "opencode"
}

// CodexClient talks to the native Codex channel.
type CodexClient struct {
	// Identity is used when the downstream client is not an official Codex client.
	Identity Identity
	// BaseURL overrides CodexBaseURL (tests).
	BaseURL string
}

func (c *CodexClient) base() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return CodexBaseURL
}

func (c *CodexClient) identity() Identity {
	if c.Identity.UserAgent == "" {
		return DefaultIdentity("")
	}
	return c.Identity
}

// SessionHeaders are the conversation identifiers of one request.
type SessionHeaders struct {
	SessionID string
	ThreadID  string
}

func client(cred Credentials, headerTimeout time.Duration) (*http.Client, error) {
	return httpx.NewClient(httpx.Options{ProxyURL: cred.ProxyURL, ResponseHeaderTimeout: headerTimeout})
}

// Responses posts a Responses request to the native channel.
func (c *CodexClient) Responses(ctx context.Context, cred Credentials, body []byte, downstream http.Header, session SessionHeaders) (*http.Response, error) {
	return c.post(ctx, cred, c.base()+"/responses", body, downstream, session, "text/event-stream")
}

// Compact posts to the compaction endpoint.
func (c *CodexClient) Compact(ctx context.Context, cred Credentials, body []byte, downstream http.Header, session SessionHeaders) (*http.Response, error) {
	return c.post(ctx, cred, c.base()+"/responses/compact", body, downstream, session, "application/json")
}

func (c *CodexClient) post(ctx context.Context, cred Credentials, endpoint string, body []byte, downstream http.Header, session SessionHeaders, accept string) (*http.Response, error) {
	hc, err := client(cred, 120*time.Second)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	c.applyHeaders(req, cred, downstream, session)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", accept)
	return hc.Do(req)
}

func (c *CodexClient) applyHeaders(req *http.Request, cred Credentials, downstream http.Header, session SessionHeaders) {
	ident := c.identity()
	ua, originator, version := ident.UserAgent, ident.Originator, ident.Version
	if downstream != nil && IsOfficialCodexClient(downstream.Get("User-Agent"), downstream.Get("Originator")) {
		if v := strings.TrimSpace(downstream.Get("User-Agent")); v != "" {
			ua = v
		}
		if v := strings.TrimSpace(downstream.Get("Originator")); v != "" {
			originator = v
		}
		if v := strings.TrimSpace(downstream.Get("Version")); v != "" {
			version = v
		}
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Originator", originator)
	if version != "" {
		req.Header.Set("Version", version)
	}
	req.Header.Set("Authorization", "Bearer "+cred.AccessToken)
	if cred.AccountID != "" {
		req.Header.Set("Chatgpt-Account-Id", cred.AccountID)
	}
	if downstream != nil {
		// Preserve the server-issued sticky-routing state end to end. Dropping
		// it makes chatgpt.com treat every bridged turn as a cold request.
		if state := strings.TrimSpace(downstream.Get("X-Codex-Turn-State")); state != "" {
			req.Header.Set("X-Codex-Turn-State", state)
		}
	}
	beta := ""
	if downstream != nil {
		beta = strings.TrimSpace(downstream.Get(betaFeaturesHeader))
	}
	if beta == "" {
		beta = defaultBetaFeatures
	}
	req.Header.Set(betaFeaturesHeader, beta)
	sessionID, threadID := session.SessionID, session.ThreadID
	if downstream != nil {
		for _, name := range []string{"session-id", "session_id", "Session_id", "x-session-id"} {
			if v := strings.TrimSpace(downstream.Get(name)); v != "" {
				sessionID = v
				break
			}
		}
		for _, name := range []string{"thread-id", "thread_id", "conversation_id", "x-client-request-id"} {
			if v := strings.TrimSpace(downstream.Get(name)); v != "" {
				threadID = v
				break
			}
		}
	}
	if threadID == "" {
		threadID = sessionID
	}
	if sessionID != "" {
		req.Header.Set("session-id", sessionID)
	}
	if threadID != "" {
		req.Header.Set("thread-id", threadID)
		req.Header.Set("x-client-request-id", threadID)
	}
}

// Models fetches the Codex model manifest for the account.
func (c *CodexClient) Models(ctx context.Context, cred Credentials, clientVersion string) ([]byte, int, error) {
	hc, err := client(cred, 30*time.Second)
	if err != nil {
		return nil, 0, err
	}
	ident := c.identity()
	if strings.TrimSpace(clientVersion) == "" {
		clientVersion = ident.Version
	}
	endpoint := c.base() + "/models?client_version=" + url.QueryEscape(clientVersion)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+cred.AccessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", ident.UserAgent)
	req.Header.Set("Originator", ident.Originator)
	req.Header.Set("Version", clientVersion)
	if cred.AccountID != "" {
		req.Header.Set("Chatgpt-Account-Id", cred.AccountID)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}
