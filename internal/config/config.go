// Package config holds the persistent bridge configuration.
//
// The file lives at $GPTBRIDGE_HOME/config.json (default ~/.gptbridge). Every
// field has a safe default so a missing file is not an error.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// Route policies decide which upstream serves a request.
const (
	PolicyBPSPrefer = "bps_prefer" // Basispoints when possible, native Codex otherwise
	PolicyBPSOnly   = "bps_only"   // Basispoints only; unsupported requests fail
	PolicyCodexOnly = "codex_only" // never use Basispoints
)

// Scheduler strategies for multi-account pools.
const (
	SchedulerSticky     = "sticky"      // same conversation keeps its account, otherwise round robin
	SchedulerRoundRobin = "round_robin" // rotate on every request
	SchedulerLeastUsed  = "least_used"  // lowest recent request count first
)

// DefaultBPSModels are the models verified to be served by the Basispoints
// gateway. Others are rejected upstream with basispoints_model_access_changed,
// so they are routed to the native Codex channel unless the list is overridden.
var DefaultBPSModels = []string{"gpt-5.6-sol", "gpt-6-astra"}

// Config is the persisted bridge configuration.
type Config struct {
	// DesktopMode fixes BPS as the default, disables automatic fallback, and
	// requires local admin authentication. Explicit channel pins are permitted.
	DesktopMode bool `json:"desktop_mode,omitempty"`
	// Listen is the address of the local API server. Non-loopback addresses
	// are refused unless AllowRemote is set and APIKey is non-empty.
	Listen string `json:"listen"`
	// AllowRemote permits binding to a non-loopback interface.
	AllowRemote bool `json:"allow_remote"`
	// APIKey, when set, is required as a Bearer token on /v1 endpoints and on
	// the management API. Always required when AllowRemote is true.
	APIKey string `json:"api_key,omitempty"`
	// ProxyURL is the default outbound proxy (http://, https://, socks5://).
	ProxyURL string `json:"proxy_url,omitempty"`
	// RoutePolicy is one of the Policy* constants.
	RoutePolicy string `json:"route_policy"`
	// BPSModels lists the models Basispoints may serve. "*" allows all.
	BPSModels []string `json:"bps_models"`
	// BPSTransport selects http or the opt-in WebSocket protocol. No fallback.
	BPSTransport string `json:"bps_transport"`
	// NativeFallback lets a Basispoints request retry on the native channel
	// when Basispoints cannot serve it (protocol rejection, model denial).
	NativeFallback bool `json:"native_fallback"`
	// DefaultModel is used when a client omits the model.
	DefaultModel string `json:"default_model"`
	// DefaultEffort is the reasoning effort applied when the client omits one.
	DefaultEffort string `json:"default_effort"`
	// ActiveAccountID pins unscoped API clients to the selected local account.
	ActiveAccountID string `json:"active_account_id,omitempty"`
	// CockpitKey protects the local OAuth-passthrough endpoint. It is never an upstream key.
	CockpitKey string `json:"cockpit_key,omitempty"`
	// AnthropicModelMap maps Claude model names (prefix match) to GPT models
	// for the /v1/messages endpoint.
	AnthropicModelMap map[string]string `json:"anthropic_model_map"`
	// Scheduler is one of the Scheduler* constants.
	Scheduler string `json:"scheduler"`
	// AutoRefresh refreshes access tokens in the background before expiry.
	AutoRefresh bool `json:"auto_refresh"`
	// UsageProbe periodically queries the ChatGPT usage endpoint so the
	// dashboard can display 5h/7d quota windows.
	UsageProbe bool `json:"usage_probe"`
	// LogRequests writes one line per request to the log (never prompts).
	LogRequests bool `json:"log_requests"`
	// CodexClientVersion is the Codex CLI version the bridge impersonates on
	// the native channel when the downstream client is not Codex itself.
	CodexClientVersion string `json:"codex_client_version,omitempty"`
	// WebUI enables the embedded dashboard at /.
	WebUI bool `json:"web_ui"`
	// BPSCompactThreshold overrides the compaction threshold sent to Basispoints.
	BPSCompactThreshold int `json:"bps_compact_threshold,omitempty"`
	// StripUnknownFields removes request fields the upstream is known to reject.
	StripUnknownFields bool `json:"strip_unknown_fields"`
}

// Default returns the built in configuration.
func Default() *Config {
	return &Config{
		Listen:         "127.0.0.1:8791",
		RoutePolicy:    PolicyBPSOnly,
		BPSModels:      append([]string(nil), DefaultBPSModels...),
		BPSTransport:   "http",
		NativeFallback: false,
		DefaultModel:   "gpt-6-astra",
		DefaultEffort:  "xhigh",
		AnthropicModelMap: map[string]string{
			"claude-opus":   "gpt-6-astra",
			"claude-sonnet": "gpt-5.6-sol",
			"claude-haiku":  "gpt-5.6-luna",
		},
		Scheduler:          SchedulerSticky,
		AutoRefresh:        true,
		UsageProbe:         true,
		LogRequests:        true,
		WebUI:              true,
		StripUnknownFields: true,
	}
}

// Home returns the data directory ($GPTBRIDGE_HOME or ~/.gptbridge).
func Home() string {
	if home := strings.TrimSpace(os.Getenv("GPTBRIDGE_HOME")); home != "" {
		return home
	}
	dir, err := os.UserHomeDir()
	if err != nil || dir == "" {
		return ".gptbridge"
	}
	return filepath.Join(dir, ".gptbridge-team")
}

// Path returns the config file path inside the home directory.
func Path() string { return filepath.Join(Home(), "config.json") }

// AccountsPath returns the accounts file path inside the home directory.
func AccountsPath() string { return filepath.Join(Home(), "accounts.json") }

// Load reads path, falling back to defaults when the file does not exist.
func Load(path string) (*Config, error) {
	cfg := Default()
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, cfg); err != nil {
			return nil, fmt.Errorf("parse config %s: %w", path, err)
		}
	}
	cfg.ApplyEnv()
	if err := cfg.Normalize(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Save writes the configuration atomically with owner-only permissions.
func (c *Config) Save(path string) error {
	if err := c.Normalize(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ApplyEnv overlays GPTBRIDGE_* environment variables.
func (c *Config) ApplyEnv() {
	if v := strings.TrimSpace(os.Getenv("GPTBRIDGE_LISTEN")); v != "" {
		c.Listen = v
	}
	if v := strings.TrimSpace(os.Getenv("GPTBRIDGE_API_KEY")); v != "" {
		c.APIKey = v
	}
	if v := strings.TrimSpace(os.Getenv("GPTBRIDGE_PROXY")); v != "" {
		c.ProxyURL = v
	}
	if v := strings.TrimSpace(os.Getenv("GPTBRIDGE_ROUTE_POLICY")); v != "" {
		c.RoutePolicy = v
	}
	if v := strings.TrimSpace(os.Getenv("GPTBRIDGE_BPS_MODELS")); v != "" {
		c.BPSModels = splitList(v)
	}
	if v := strings.TrimSpace(os.Getenv("GPTBRIDGE_ALLOW_REMOTE")); v != "" {
		c.AllowRemote = isTrue(v)
	}
	if v := strings.TrimSpace(os.Getenv("GPTBRIDGE_NATIVE_FALLBACK")); v != "" {
		c.NativeFallback = isTrue(v)
	}
	if v := strings.TrimSpace(os.Getenv("GPTBRIDGE_DEFAULT_MODEL")); v != "" {
		c.DefaultModel = v
	}
}

// Normalize validates and canonicalizes the configuration.
func (c *Config) Normalize() error {
	if c.BPSTransport == "" {
		c.BPSTransport = "http"
	}
	if c.BPSTransport != "http" && c.BPSTransport != "websocket" {
		return errors.New("bps_transport must be http or websocket")
	}
	if c.DesktopMode {
		c.RoutePolicy, c.NativeFallback, c.WebUI, c.AllowRemote = PolicyBPSOnly, false, false, false
		if c.APIKey == "" {
			return errors.New("desktop mode requires a local API key")
		}
	}
	c.Listen = strings.TrimSpace(c.Listen)
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8791"
	}
	switch strings.ToLower(strings.TrimSpace(c.RoutePolicy)) {
	case "", PolicyBPSPrefer, "bps", "prefer", "basispoints_prefer":
		c.RoutePolicy = PolicyBPSPrefer
	case PolicyBPSOnly, "basispoints_only", "only":
		c.RoutePolicy = PolicyBPSOnly
	case PolicyCodexOnly, "codex", "native", "native_only":
		c.RoutePolicy = PolicyCodexOnly
	default:
		return fmt.Errorf("unknown route_policy %q (use bps_prefer, bps_only or codex_only)", c.RoutePolicy)
	}
	switch strings.ToLower(strings.TrimSpace(c.Scheduler)) {
	case "", SchedulerSticky:
		c.Scheduler = SchedulerSticky
	case SchedulerRoundRobin, "rr":
		c.Scheduler = SchedulerRoundRobin
	case SchedulerLeastUsed:
		c.Scheduler = SchedulerLeastUsed
	default:
		return fmt.Errorf("unknown scheduler %q", c.Scheduler)
	}
	if len(c.BPSModels) == 0 {
		c.BPSModels = append([]string(nil), DefaultBPSModels...)
	}
	for i, m := range c.BPSModels {
		c.BPSModels[i] = strings.ToLower(strings.TrimSpace(m))
	}
	if c.DefaultModel == "" {
		c.DefaultModel = "gpt-6-astra"
	}
	if c.DefaultEffort == "" {
		c.DefaultEffort = "xhigh"
	}
	if c.AnthropicModelMap == nil {
		c.AnthropicModelMap = Default().AnthropicModelMap
	}
	if !IsLoopback(c.Listen) {
		if !c.AllowRemote {
			return fmt.Errorf("listen address %q is not loopback; set allow_remote=true and an api_key to expose the bridge", c.Listen)
		}
		if strings.TrimSpace(c.APIKey) == "" {
			return errors.New("allow_remote requires a non-empty api_key")
		}
	}
	return nil
}

// BPSModelAllowed reports whether model may be served by Basispoints.
func (c *Config) BPSModelAllowed(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return false
	}
	for _, allowed := range c.BPSModels {
		if allowed == "*" || allowed == "all" || allowed == m {
			return true
		}
		// Date snapshots of an allowed family, e.g. gpt-6-astra-2026-08-01.
		if strings.HasPrefix(m, allowed+"-") && len(m) == len(allowed)+11 {
			return true
		}
	}
	return false
}

// IsLoopback reports whether addr (host, host:port or bare IP) is loopback.
func IsLoopback(addr string) bool {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return true
	}
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func splitList(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func isTrue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on", "y":
		return true
	}
	return false
}
