package account

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xxx-holic/wishtoken-desktop/internal/jwt"
)

// ImportResult lists the accounts recognised in an import payload.
type ImportResult struct {
	Accounts []Account `json:"accounts"`
	Warnings []string  `json:"warnings,omitempty"`
	Skipped  int       `json:"skipped"`
}

// Parse recognises every credential format used in the Codex ecosystem:
//
//   - Codex CLI ~/.codex/auth.json ({"tokens":{...}})
//   - CLIProxyAPI / codex2api exports ({"type":"codex","access_token":...})
//   - sub2api exports ({"accounts":[{"name":..,"credentials":{...}}]})
//   - arrays of any of the above, camelCase variants, nested "session_info"
//   - plain text: one refresh token or access token per line, optionally
//     "email----refresh_token" or "email----password----refresh_token"
func Parse(data []byte, source string) (*ImportResult, error) {
	text := strings.TrimSpace(strings.TrimPrefix(string(data), "\uFEFF"))
	if text == "" {
		return nil, fmt.Errorf("empty import payload")
	}
	res := &ImportResult{}
	if strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") {
		var value any
		dec := json.NewDecoder(strings.NewReader(text))
		dec.UseNumber()
		if err := dec.Decode(&value); err != nil {
			// Some exports concatenate objects (JSON lines). Try line by line.
			if lines := parseJSONLines(text, source, res); lines > 0 {
				return finish(res), nil
			}
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		walk(value, source, res, 0)
		return finish(res), nil
	}
	parseTextLines(text, source, res)
	return finish(res), nil
}

func finish(res *ImportResult) *ImportResult {
	// Deduplicate inside the payload itself.
	seen := make(map[string]int)
	var out []Account
	for _, acc := range res.Accounts {
		key := acc.Identity()
		if idx, ok := seen[key]; ok && key != "" {
			mergeInto(&out[idx], &acc)
			res.Skipped++
			continue
		}
		seen[key] = len(out)
		out = append(out, acc)
	}
	res.Accounts = out
	return res
}

func parseJSONLines(text, source string, res *ImportResult) int {
	count := 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var value any
		dec := json.NewDecoder(strings.NewReader(line))
		dec.UseNumber()
		if err := dec.Decode(&value); err != nil {
			continue
		}
		count++
		walk(value, source, res, 0)
	}
	return count
}

func parseTextLines(text, source string, res *ImportResult) {
	for n, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		acc := Account{Source: source}
		fields := strings.Split(line, "----")
		if len(fields) == 1 {
			fields = strings.Fields(line)
		}
		token := strings.TrimSpace(fields[len(fields)-1])
		for _, f := range fields[:len(fields)-1] {
			f = strings.TrimSpace(f)
			if strings.Contains(f, "@") && acc.Email == "" {
				acc.Email = f
			}
		}
		if token == "" {
			continue
		}
		if jwt.IsJWT(token) {
			acc.AccessToken = token
		} else if len(token) >= 20 && !strings.ContainsAny(token, " \t\"'{}") {
			acc.RefreshToken = token
		} else {
			res.Warnings = append(res.Warnings, fmt.Sprintf("line %d: not a token", n+1))
			res.Skipped++
			continue
		}
		acc.FillFromTokens()
		res.Accounts = append(res.Accounts, acc)
	}
}

func walk(value any, source string, res *ImportResult, depth int) {
	if depth > 6 {
		return
	}
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			walk(item, source, res, depth+1)
		}
	case map[string]any:
		if list, ok := v["accounts"].([]any); ok {
			for _, item := range list {
				walk(item, source, res, depth+1)
			}
			return
		}
		if list, ok := v["data"].([]any); ok && !looksLikeCredential(v) {
			for _, item := range list {
				walk(item, source, res, depth+1)
			}
			return
		}
		if creds, ok := v["credentials"].(map[string]any); ok {
			merged := make(map[string]any, len(creds)+4)
			for k, val := range creds {
				merged[k] = val
			}
			for _, key := range []string{"name", "proxy_url", "proxy_label", "proxy_enabled", "email", "disabled", "enabled", "status", "tags"} {
				if _, exists := merged[key]; !exists {
					if val, ok := v[key]; ok {
						merged[key] = val
					}
				}
			}
			addEntry(merged, source, res)
			return
		}
		if tokens, ok := v["tokens"].(map[string]any); ok {
			merged := make(map[string]any, len(tokens)+4)
			for k, val := range tokens {
				merged[k] = val
			}
			for _, key := range []string{"last_refresh", "email", "name", "plan_type", "account_id", "proxy_url", "disabled", "auth_mode"} {
				if _, exists := merged[key]; !exists {
					if val, ok := v[key]; ok {
						merged[key] = val
					}
				}
			}
			addEntry(merged, source, res)
			return
		}
		if session, ok := v["session_info"].(map[string]any); ok {
			merged := make(map[string]any, len(session)+4)
			for k, val := range v {
				merged[k] = val
			}
			for k, val := range session {
				merged[k] = val
			}
			delete(merged, "session_info")
			addEntry(merged, source, res)
			return
		}
		if _, ok := v["agent_identity"]; ok && !looksLikeCredential(v) {
			res.Warnings = append(res.Warnings, "skipped an agent-identity credential: Basispoints requires a ChatGPT login")
			res.Skipped++
			return
		}
		if looksLikeCredential(v) {
			addEntry(v, source, res)
			return
		}
		// Unknown object: descend into nested containers (e.g. keyed by email).
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			switch v[k].(type) {
			case map[string]any, []any:
				walk(v[k], source, res, depth+1)
			}
		}
	}
}

func looksLikeCredential(m map[string]any) bool {
	for _, key := range []string{"access_token", "accessToken", "refresh_token", "refreshToken", "id_token", "idToken"} {
		if s, ok := m[key].(string); ok && strings.TrimSpace(s) != "" {
			return true
		}
	}
	return false
}

func addEntry(m map[string]any, source string, res *ImportResult) {
	if kind := strings.ToLower(getString(m, "type", "provider", "channel")); kind != "" && kind != "codex" && kind != "openai" && kind != "chatgpt" && kind != "bps" {
		res.Warnings = append(res.Warnings, fmt.Sprintf("skipped a %q credential (only Codex/ChatGPT accounts are supported)", kind))
		res.Skipped++
		return
	}
	if mode := strings.ToLower(getString(m, "auth_mode", "authMode")); mode != "" && mode != "chatgpt" && mode != "oauth" {
		if mode == "apikey" || mode == "api_key" || mode == "agentidentity" || mode == "agent_identity" {
			res.Warnings = append(res.Warnings, fmt.Sprintf("skipped an auth_mode=%s credential: Basispoints requires a ChatGPT login", mode))
			res.Skipped++
			return
		}
	}
	acc := Account{
		Source:        source,
		AccessToken:   getString(m, "access_token", "accessToken", "token"),
		RefreshToken:  getString(m, "refresh_token", "refreshToken"),
		IDToken:       getString(m, "id_token", "idToken"),
		OAuthClientID: getString(m, "oauth_client_id", "client_id"),
		AccountID:     getString(m, "workspace_id", "account_id", "chatgpt_account_id", "accountId", "chatgptAccountId"),
		Email:         getString(m, "email", "user_email"),
		Name:          getString(m, "name", "label", "remark", "note"),
		PlanType:      getString(m, "plan_type", "planType", "chatgpt_plan_type"),
		UserID:        getString(m, "user_id", "chatgpt_user_id", "userId"),
		ProxyURL:      getString(m, "proxy_url", "proxyUrl", "proxy"),
	}
	if acc.OAuthClientID == "" && strings.EqualFold(getString(m, "type", "provider", "channel"), "bps") {
		acc.OAuthClientID = BPSOAuthClientID
	}
	if user, ok := m["user"].(map[string]any); ok {
		if acc.Email == "" {
			acc.Email = getString(user, "email")
		}
		if acc.UserID == "" {
			acc.UserID = getString(user, "id")
		}
	}
	if accountObj, ok := m["account"].(map[string]any); ok {
		if acc.AccountID == "" {
			acc.AccountID = getString(accountObj, "id", "account_id")
		}
		if acc.PlanType == "" {
			acc.PlanType = getString(accountObj, "plan_type", "planType")
		}
	}
	if !jwt.IsJWT(acc.AccessToken) && acc.AccessToken != "" {
		// A non-JWT "token" field is more likely a refresh token.
		if acc.RefreshToken == "" {
			acc.RefreshToken = acc.AccessToken
		}
		acc.AccessToken = ""
	}
	if acc.AccessToken == "" && acc.RefreshToken == "" {
		res.Warnings = append(res.Warnings, "skipped an entry without access_token or refresh_token")
		res.Skipped++
		return
	}
	if t := parseTime(firstPresent(m, "expired", "expires_at", "expiresAt", "expires", "expiry", "expire_at")); !t.IsZero() {
		acc.ExpiresAt = t
	}
	if t := parseTime(firstPresent(m, "last_refresh", "lastRefresh", "updated_at", "refreshed_at")); !t.IsZero() {
		acc.LastRefresh = t
	}
	if t := parseTime(firstPresent(m, "subscription_expires_at", "subscription_until")); !t.IsZero() {
		acc.SubscriptionUntil = t
	}
	if disabled, ok := m["disabled"].(bool); ok && disabled {
		acc.Disabled = true
	}
	if enabled, ok := m["enabled"].(bool); ok && !enabled {
		acc.Disabled = true
	}
	if status := strings.ToLower(getString(m, "status")); status == "disabled" || status == "banned" {
		acc.Disabled = true
	}
	if tags, ok := m["tags"].([]any); ok {
		for _, t := range tags {
			if s, ok := t.(string); ok && strings.TrimSpace(s) != "" {
				acc.Tags = append(acc.Tags, strings.TrimSpace(s))
			}
		}
	}
	acc.FillFromTokens()
	if acc.AccountID == "" && acc.RefreshToken == "" {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%s: no account id and no refresh token; the access token alone cannot be renewed", acc.Label()))
	}
	res.Accounts = append(res.Accounts, acc)
}

func getString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		switch v := m[k].(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				return s
			}
		case json.Number:
			return v.String()
		}
	}
	return ""
}

func firstPresent(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			return v
		}
	}
	return nil
}

// parseTime accepts RFC3339 strings, "2006-01-02 15:04:05", and epoch
// seconds or milliseconds (numbers or numeric strings).
func parseTime(v any) time.Time {
	switch t := v.(type) {
	case nil:
		return time.Time{}
	case json.Number:
		if f, err := t.Float64(); err == nil {
			return epoch(f)
		}
	case float64:
		return epoch(t)
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return time.Time{}
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return epoch(f)
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
			if parsed, err := time.Parse(layout, s); err == nil {
				return parsed
			}
		}
	}
	return time.Time{}
}

func epoch(f float64) time.Time {
	if f <= 0 {
		return time.Time{}
	}
	if f > 1e12 { // milliseconds
		return time.UnixMilli(int64(f))
	}
	return time.Unix(int64(f), 0)
}

// ParseFile reads and parses one file.
func ParseFile(path string) (*ImportResult, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(raw, filepath.Base(path))
}

// ParseDir parses every *.json and *.txt file in dir (non recursive).
func ParseDir(dir string) (*ImportResult, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	res := &ImportResult{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".json" && ext != ".txt" {
			continue
		}
		part, err := ParseFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: %v", entry.Name(), err))
			continue
		}
		res.Accounts = append(res.Accounts, part.Accounts...)
		res.Warnings = append(res.Warnings, part.Warnings...)
		res.Skipped += part.Skipped
	}
	return finish(res), nil
}

// CodexAuthPath returns the Codex CLI credential file path.
func CodexAuthPath() string {
	if home := strings.TrimSpace(os.Getenv("CODEX_HOME")); home != "" {
		return filepath.Join(home, "auth.json")
	}
	dir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, ".codex", "auth.json")
}

// CPADir returns the default CLIProxyAPI credential directory.
func CPADir() string {
	dir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, ".cli-proxy-api")
}
