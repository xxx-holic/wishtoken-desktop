package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/xxx-holic/wishtoken-desktop/internal/account"
	"github.com/xxx-holic/wishtoken-desktop/internal/api"
	"github.com/xxx-holic/wishtoken-desktop/internal/basispoints"
	"github.com/xxx-holic/wishtoken-desktop/internal/channelname"
	"github.com/xxx-holic/wishtoken-desktop/internal/codexcfg"
	"github.com/xxx-holic/wishtoken-desktop/internal/config"
	"github.com/xxx-holic/wishtoken-desktop/internal/httpx"
	"github.com/xxx-holic/wishtoken-desktop/internal/localcodex"
	"github.com/xxx-holic/wishtoken-desktop/internal/oauth"
	"github.com/xxx-holic/wishtoken-desktop/internal/router"
	"github.com/xxx-holic/wishtoken-desktop/internal/version"
)

// recordLog keeps the most recent upstream attempts for the dashboard.
type recordLog struct {
	mu      sync.Mutex
	entries []router.Record
	max     int
}

func newRecordLog(max int) *recordLog { return &recordLog{max: max} }

func qualitySummary(records []router.Record) object {
	summary := object{"checked": 0, "model_mismatches": 0, "last_model_mismatch": "", "accounts": object{}}
	accounts := summary["accounts"].(object)
	for _, rec := range records {
		if rec.Quality != "model_match" && rec.Quality != "model_mismatch" {
			continue
		}
		label := rec.Account
		if label == "" {
			label = "unknown"
		}
		summary["checked"] = summary["checked"].(int) + 1
		entry, _ := accounts[label].(object)
		if entry == nil {
			entry = object{"checked": 0, "model_mismatches": 0, "last_quality": ""}
			accounts[label] = entry
		}
		entry["checked"] = entry["checked"].(int) + 1
		// Records arrive newest first; keep the newest signal per account.
		if entry["last_quality"] == "" {
			entry["last_quality"] = rec.Quality
			if rec.TurnStateLength > 0 {
				entry["last_turn_state_length"] = rec.TurnStateLength
			}
			if rec.SafetyBuffering != "" {
				entry["safety_buffering"] = rec.SafetyBuffering
			}
			if rec.SafetyFasterModel != "" {
				entry["safety_faster_model"] = rec.SafetyFasterModel
			}
		}
		if rec.ModelMismatch {
			stamp := rec.Time.UTC().Format(time.RFC3339)
			summary["model_mismatches"] = summary["model_mismatches"].(int) + 1
			entry["model_mismatches"] = entry["model_mismatches"].(int) + 1
			if entry["last_model_mismatch"] == nil {
				entry["last_model_mismatch"] = stamp
			}
			if summary["last_model_mismatch"] == "" {
				summary["last_model_mismatch"] = stamp
			}
		}
	}
	return summary
}

func (l *recordLog) add(rec router.Record) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := len(l.entries) - 1; i >= 0; i-- {
		prior := l.entries[i]
		if prior.Time.Equal(rec.Time) && prior.Route == rec.Route && prior.Account == rec.Account {
			l.entries[i] = rec
			return
		}
	}
	l.entries = append(l.entries, rec)
	if len(l.entries) > l.max {
		l.entries = l.entries[len(l.entries)-l.max:]
	}
}

func (l *recordLog) list(limit int) []router.Record {
	l.mu.Lock()
	defer l.mu.Unlock()
	if limit <= 0 || limit > len(l.entries) {
		limit = len(l.entries)
	}
	out := make([]router.Record, limit)
	copy(out, l.entries[len(l.entries)-limit:])
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	path := channelname.AdminPath(strings.TrimPrefix(r.URL.Path, "/api"))
	switch {
	case path == "/features/fenjue/license":
		s.adminFeatureLicense(w, r)
	case strings.HasPrefix(path, "/bps-native/"):
		s.adminBPSNative(w, r, path)
	case strings.HasPrefix(path, "/fenjue/"):
		s.adminFenjue(w, r, path)
	case path == "/shutdown" && r.Method == http.MethodPost:
		s.bpsNativeMu.Lock()
		if s.bpsNativeCancel != nil {
			s.bpsNativeCancel()
		}
		s.bpsNativeMu.Unlock()
		api.WriteJSON(w, http.StatusOK, object{"ok": true})
		s.stopMu.Lock()
		if s.stop != nil {
			time.AfterFunc(200*time.Millisecond, s.stop)
		}
		s.stopMu.Unlock()
	case path == "/codex/launch":
		s.adminLocalCodex(w, r)
	case path == "/codex/select" && r.Method == http.MethodPost:
		s.adminSelectAccount(w, r)
	case path == "/pelican/generate" && r.Method == http.MethodPost:
		s.adminPelican(w, r)
	case path == "/status" && r.Method == http.MethodGet:
		s.adminStatus(w, r)
	case path == "/accounts" && r.Method == http.MethodGet:
		s.adminAccounts(w, r)
	case path == "/accounts/import" && r.Method == http.MethodPost:
		s.adminImport(w, r)
	case path == "/accounts/import-codex" && r.Method == http.MethodPost:
		s.adminImportCodex(w, r)
	case path == "/accounts/import-cpa" && r.Method == http.MethodPost:
		s.adminImportCPA(w, r)
	case path == "/accounts/export" && r.Method == http.MethodGet:
		s.adminExport(w, r)
	case strings.HasPrefix(path, "/accounts/"):
		s.adminAccountAction(w, r, strings.TrimPrefix(path, "/accounts/"))
	case path == "/settings" && r.Method == http.MethodGet:
		api.WriteJSON(w, http.StatusOK, s.settingsView())
	case path == "/settings" && (r.Method == http.MethodPut || r.Method == http.MethodPost):
		s.adminUpdateSettings(w, r)
	case path == "/login/start" && r.Method == http.MethodPost:
		s.adminLoginStart(w, r)
	case path == "/login/status" && r.Method == http.MethodGet:
		s.adminLoginStatus(w, r)
	case path == "/login/cancel" && r.Method == http.MethodPost:
		s.adminLoginCancel(w, r)
	case path == "/logs" && r.Method == http.MethodGet:
		api.WriteJSON(w, http.StatusOK, object{"records": s.logs.list(200)})
	case path == "/test" && r.Method == http.MethodPost:
		s.adminTest(w, r)
	case path == "/snippets" && r.Method == http.MethodGet:
		api.WriteJSON(w, http.StatusOK, s.snippets())
	case path == "/codex/config" && r.Method == http.MethodGet:
		api.WriteJSON(w, http.StatusOK, codexcfg.Inspect(codexcfg.ConfigPath()))
	case path == "/codex/config" && r.Method == http.MethodPost:
		s.adminApplyCodexConfig(w, r)
	case path == "/codex/config" && r.Method == http.MethodDelete:
		backup, err := codexcfg.Remove(codexcfg.ConfigPath())
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "codex_config", "server_error", err.Error())
			return
		}
		api.WriteJSON(w, http.StatusOK, object{"ok": true, "backup": backup})
	case path == "/models" && r.Method == http.MethodGet:
		cfg := s.Config()
		api.WriteJSON(w, http.StatusOK, object{"catalog": basispoints.Catalog, "native_catalog": localcodex.NativeModels(), "work_catalog": localcodex.WorkModels(), "prism_catalog": localcodex.PrismModels(), "fenjue_catalog": localcodex.FenjueModels(), "bps_models": cfg.BPSModels, "efforts": basispoints.Efforts, "work_efforts": localcodex.WorkEfforts, "prism_efforts": localcodex.PrismEfforts})
	default:
		writeError(w, r, http.StatusNotFound, "not_found", "invalid_request_error", "unknown management endpoint")
	}
}

func (s *Server) adminStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.Config()
	accounts := s.Store.List()
	counts := object{"total": len(accounts), "ready": 0, "disabled": 0, "error": 0, "cooldown": 0}
	for _, acc := range accounts {
		if _, _, cooling := s.Pool.Cooldown(acc.ID); cooling {
			counts["cooldown"] = counts["cooldown"].(int) + 1
			continue
		}
		v := acc.View()
		switch v.Status {
		case "ready":
			counts["ready"] = counts["ready"].(int) + 1
		case "disabled":
			counts["disabled"] = counts["disabled"].(int) + 1
		default:
			counts["error"] = counts["error"].(int) + 1
		}
	}
	api.WriteJSON(w, http.StatusOK, object{
		"version": version.Version, "listen": cfg.Listen, "base_url": s.baseURL(),
		"desktop_mode":   cfg.DesktopMode,
		"work_bridge_id": workBridgeID(),
		"uptime_seconds": int(time.Since(s.startedAt).Seconds()), "accounts": counts,
		"route_policy": cfg.RoutePolicy, "bps_models": cfg.BPSModels, "native_fallback": cfg.NativeFallback,
		"proxy_url": httpx.Redact(cfg.ProxyURL), "api_key_set": cfg.APIKey != "", "home": config.Home(),
		"codex_config": codexcfg.Inspect(codexcfg.ConfigPath()), "default_model": cfg.DefaultModel,
		"quality": qualitySummary(s.logs.list(200)),
	})
}

func (s *Server) baseURL() string {
	listen := s.Config().Listen
	host, port, err := splitListen(listen)
	if err != nil {
		return "http://" + listen
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return "http://" + host + ":" + port
}

func splitListen(addr string) (string, string, error) {
	idx := strings.LastIndex(addr, ":")
	if idx < 0 {
		return "", "", errors.New("no port")
	}
	return strings.Trim(addr[:idx], "[]"), addr[idx+1:], nil
}

type accountEntry struct {
	account.View
	CooldownReason string `json:"cooldown_reason,omitempty"`
}

func (s *Server) adminAccounts(w http.ResponseWriter, r *http.Request) {
	list := s.Store.List()
	out := make([]accountEntry, 0, len(list))
	for _, acc := range list {
		entry := accountEntry{View: acc.View()}
		if until, reason, ok := s.Pool.Cooldown(acc.ID); ok {
			entry.Status = "cooldown"
			entry.CooldownUntil = until.UTC().Format(time.RFC3339)
			entry.CooldownReason = reason
		}
		out = append(out, entry)
	}
	api.WriteJSON(w, http.StatusOK, object{"accounts": out})
}

type importSummary struct {
	Imported   int      `json:"imported"`
	Merged     int      `json:"merged"`
	Skipped    int      `json:"skipped"`
	Warnings   []string `json:"warnings"`
	IDs        []string `json:"ids"`
	Refreshing int      `json:"refreshing"`
}

// importAccounts stores parsed accounts and refreshes incomplete ones in
// the background.
func (s *Server) importAccounts(res *account.ImportResult, refresh bool) importSummary {
	summary := importSummary{Skipped: res.Skipped, Warnings: res.Warnings}
	if summary.Warnings == nil {
		summary.Warnings = []string{}
	}
	for _, acc := range res.Accounts {
		if acc.Provider == "fenjue" {
			if err := s.featureLicense.Require(); err != nil {
				summary.Skipped++
				summary.Warnings = append(summary.Warnings, err.Error())
				continue
			}
		}
		up, err := s.Store.Upsert(acc)
		if err != nil {
			summary.Warnings = append(summary.Warnings, err.Error())
			continue
		}
		if up.Created {
			summary.Imported++
		} else {
			summary.Merged++
		}
		summary.IDs = append(summary.IDs, up.ID)
		stored, _ := s.Store.Get(up.ID)
		if refresh && stored.RefreshToken != "" && (stored.AccountID == "" || stored.AccessToken == "" || stored.ExpiringWithin(10*time.Minute)) {
			summary.Refreshing++
			go func(id string) {
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				_, _ = s.Pool.Refresh(ctx, id, true)
			}(up.ID)
		}
	}
	if summary.IDs == nil {
		summary.IDs = []string{}
	}
	return summary
}

func (s *Server) adminImport(w http.ResponseWriter, r *http.Request) {
	refresh := r.URL.Query().Get("refresh") != "0"
	var payloads [][]byte
	source := "dashboard"
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(maxBodyBytes); err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_multipart", "invalid_request_error", err.Error())
			return
		}
		for _, files := range r.MultipartForm.File {
			for _, fh := range files {
				f, err := fh.Open()
				if err != nil {
					continue
				}
				raw, _ := io.ReadAll(io.LimitReader(f, maxBodyBytes))
				f.Close()
				payloads = append(payloads, raw)
				source = fh.Filename
			}
		}
		if text := strings.TrimSpace(r.FormValue("text")); text != "" {
			payloads = append(payloads, []byte(text))
		}
	} else {
		raw, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_body", "invalid_request_error", err.Error())
			return
		}
		var wrapper struct {
			Text   string `json:"text"`
			Source string `json:"source"`
		}
		if json.Unmarshal(raw, &wrapper) == nil && wrapper.Text != "" {
			payloads = append(payloads, []byte(wrapper.Text))
			if wrapper.Source != "" {
				source = wrapper.Source
			}
		} else {
			payloads = append(payloads, raw)
		}
	}
	if len(payloads) == 0 {
		writeError(w, r, http.StatusBadRequest, "empty_import", "invalid_request_error", "nothing to import")
		return
	}
	merged := &account.ImportResult{}
	for _, payload := range payloads {
		res, err := account.Parse(payload, source)
		if err != nil {
			merged.Warnings = append(merged.Warnings, err.Error())
			continue
		}
		merged.Accounts = append(merged.Accounts, res.Accounts...)
		merged.Warnings = append(merged.Warnings, res.Warnings...)
		merged.Skipped += res.Skipped
	}
	if len(merged.Accounts) == 0 {
		msg := "no account recognised in the payload"
		if len(merged.Warnings) > 0 {
			msg += ": " + strings.Join(merged.Warnings, "; ")
		}
		writeError(w, r, http.StatusBadRequest, "no_accounts_found", "invalid_request_error", msg)
		return
	}
	for _, a := range merged.Accounts {
		if a.Provider == "fenjue" && !s.requireFenjue(w, r) {
			return
		}
	}
	api.WriteJSON(w, http.StatusOK, s.importAccounts(merged, refresh))
}

func (s *Server) adminImportCodex(w http.ResponseWriter, r *http.Request) {
	path := account.CodexAuthPath()
	var req struct {
		Path string `json:"path"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if strings.TrimSpace(req.Path) != "" {
		path = req.Path
	}
	res, err := account.ParseFile(path)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "codex_auth_unreadable", "invalid_request_error", fmt.Sprintf("cannot read %s: %v (run `codex login` first)", path, err))
		return
	}
	for i := range res.Accounts {
		res.Accounts[i].Source = "codex-cli"
	}
	summary := s.importAccounts(res, true)
	api.WriteJSON(w, http.StatusOK, object{"path": path, "result": summary})
}

func (s *Server) adminImportCPA(w http.ResponseWriter, r *http.Request) {
	dir := account.CPADir()
	var req struct {
		Path string `json:"path"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if strings.TrimSpace(req.Path) != "" {
		dir = req.Path
	}
	info, err := os.Stat(dir)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "cpa_dir_missing", "invalid_request_error", fmt.Sprintf("cannot read %s: %v", dir, err))
		return
	}
	var res *account.ImportResult
	if info.IsDir() {
		res, err = account.ParseDir(dir)
	} else {
		res, err = account.ParseFile(dir)
	}
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "cpa_import_failed", "invalid_request_error", err.Error())
		return
	}
	for i := range res.Accounts {
		res.Accounts[i].Source = "cpa:" + filepath.Base(dir)
	}
	api.WriteJSON(w, http.StatusOK, object{"path": dir, "result": s.importAccounts(res, true)})
}

func (s *Server) adminExport(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	ids := map[string]bool{}
	for _, id := range strings.Split(r.URL.Query().Get("ids"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids[id] = true
		}
	}
	var selected []account.Account
	for _, acc := range s.Store.List() {
		if len(ids) == 0 || ids[acc.ID] {
			selected = append(selected, acc)
		}
	}
	if len(selected) == 0 {
		writeError(w, r, http.StatusNotFound, "no_accounts", "invalid_request_error", "no matching accounts")
		return
	}
	raw, err := account.ExportJSON(selected, format)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "export_failed", "server_error", err.Error())
		return
	}
	name := "gptbridge-accounts.json"
	if format == "codex" && len(selected) == 1 {
		name = "auth.json"
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, name))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(raw)
}

func (s *Server) adminAccountAction(w http.ResponseWriter, r *http.Request, rest string) {
	id, action, _ := strings.Cut(rest, "/")
	if _, ok := s.Store.Get(id); !ok {
		writeError(w, r, http.StatusNotFound, "account_not_found", "invalid_request_error", "unknown account")
		return
	}
	switch {
	case action == "" && r.Method == http.MethodDelete:
		if err := s.Store.Delete(id); err != nil {
			writeError(w, r, http.StatusInternalServerError, "delete_failed", "server_error", err.Error())
			return
		}
		s.Pool.ClearCooldown(id)
		api.WriteJSON(w, http.StatusOK, object{"ok": true})
	case action == "" && (r.Method == http.MethodPut || r.Method == http.MethodPatch):
		var req struct {
			Name     *string  `json:"name"`
			ProxyURL *string  `json:"proxy_url"`
			Disabled *bool    `json:"disabled"`
			Tags     []string `json:"tags"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_json", "invalid_request_error", err.Error())
			return
		}
		if req.ProxyURL != nil && strings.TrimSpace(*req.ProxyURL) != "" {
			if _, err := httpx.ParseProxyURL(*req.ProxyURL); err != nil {
				writeError(w, r, http.StatusBadRequest, "invalid_proxy", "invalid_request_error", err.Error())
				return
			}
		}
		err := s.Store.Update(id, func(acc *account.Account) {
			if req.Name != nil {
				acc.Name = strings.TrimSpace(*req.Name)
			}
			if req.ProxyURL != nil {
				acc.ProxyURL = strings.TrimSpace(*req.ProxyURL)
			}
			if req.Disabled != nil {
				acc.Disabled = *req.Disabled
				if !acc.Disabled {
					acc.LastError = ""
					acc.RefreshFailures = 0
				}
			}
			if req.Tags != nil {
				acc.Tags = req.Tags
			}
		})
		if err != nil {
			writeError(w, r, 500, "save_failed", "server_error", err.Error())
			return
		}
		acc, _ := s.Store.Get(id)
		api.WriteJSON(w, http.StatusOK, acc.View())
	case action == "refresh" && r.Method == http.MethodPost:
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		acc, err := s.Pool.Refresh(ctx, id, true)
		if err != nil {
			writeError(w, r, http.StatusBadGateway, "refresh_failed", "server_error", err.Error())
			return
		}
		api.WriteJSON(w, http.StatusOK, acc.View())
	case action == "usage" && r.Method == http.MethodPost:
		usage, err := s.Pool.ProbeUsage(r.Context(), id, s.Codex.Identity)
		if err != nil {
			writeError(w, r, http.StatusBadGateway, "usage_failed", "server_error", err.Error())
			return
		}
		api.WriteJSON(w, http.StatusOK, usage)
	case action == "clear-cooldown" && r.Method == http.MethodPost:
		s.Pool.ClearCooldown(id)
		_ = s.Store.Update(id, func(acc *account.Account) { acc.LastError = "" })
		api.WriteJSON(w, http.StatusOK, object{"ok": true})
	default:
		writeError(w, r, http.StatusNotFound, "not_found", "invalid_request_error", "unknown account action")
	}
}

// settingsView returns the editable configuration (API key redacted).
func (s *Server) settingsView() object {
	cfg := s.Config()
	return object{
		"listen": cfg.Listen, "allow_remote": cfg.AllowRemote, "api_key_set": cfg.APIKey != "",
		"proxy_url": cfg.ProxyURL, "route_policy": cfg.RoutePolicy, "bps_models": cfg.BPSModels,
		"bps_transport":   cfg.BPSTransport,
		"native_fallback": cfg.NativeFallback, "default_model": cfg.DefaultModel, "default_effort": cfg.DefaultEffort,
		"active_account_id":   cfg.ActiveAccountID,
		"anthropic_model_map": cfg.AnthropicModelMap, "scheduler": cfg.Scheduler, "auto_refresh": cfg.AutoRefresh,
		"usage_probe": cfg.UsageProbe, "log_requests": cfg.LogRequests, "codex_client_version": cfg.CodexClientVersion,
		"web_ui": cfg.WebUI, "bps_compact_threshold": cfg.BPSCompactThreshold, "strip_unknown_fields": cfg.StripUnknownFields,
		"config_path": s.cfgPath, "restart_required_fields": []string{"listen", "allow_remote"},
	}
}

func (s *Server) adminUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var patch map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_json", "invalid_request_error", err.Error())
		return
	}
	current := *s.Config()
	next := current
	next.BPSModels = append([]string(nil), current.BPSModels...)
	next.AnthropicModelMap = map[string]string{}
	for k, v := range current.AnthropicModelMap {
		next.AnthropicModelMap[k] = v
	}
	raw, _ := json.Marshal(next)
	var merged map[string]json.RawMessage
	_ = json.Unmarshal(raw, &merged)
	for k, v := range patch {
		if k == "api_key_set" || k == "config_path" || k == "restart_required_fields" {
			continue
		}
		merged[k] = v
	}
	rawMerged, _ := json.Marshal(merged)
	updated := config.Default()
	if err := json.Unmarshal(rawMerged, updated); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_settings", "invalid_request_error", err.Error())
		return
	}
	if current.DesktopMode {
		updated.DesktopMode = true
		updated.APIKey, updated.Listen = current.APIKey, current.Listen
	}
	if err := updated.Normalize(); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_settings", "invalid_request_error", err.Error())
		return
	}
	if updated.ProxyURL != "" {
		if _, err := httpx.ParseProxyURL(updated.ProxyURL); err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_proxy", "invalid_request_error", err.Error())
			return
		}
	}
	if s.cfgPath != "" {
		if err := updated.Save(s.cfgPath); err != nil {
			writeError(w, r, http.StatusInternalServerError, "save_failed", "server_error", err.Error())
			return
		}
	}
	s.setConfig(updated)
	api.WriteJSON(w, http.StatusOK, s.settingsView())
}

// ---------------------------------------------------------------------------
// browser login
// ---------------------------------------------------------------------------

func (s *Server) adminLoginStart(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Channel       string `json:"channel"`
		ExpectedEmail string `json:"expected_email"`
		Fresh         bool   `json:"fresh"`
	}
	if r.Body != nil {
		decoder := json.NewDecoder(io.LimitReader(r.Body, 1024))
		if err := decoder.Decode(&input); err != nil && err != io.EOF {
			writeError(w, r, 400, "invalid_login", "invalid_request_error", "invalid login request")
			return
		}
	}
	if input.Channel == "" {
		input.Channel = "codex"
	}
	if input.Channel != "codex" && input.Channel != "bps" {
		writeError(w, r, 400, "invalid_login_channel", "invalid_request_error", "choose Codex or BPS authorization")
		return
	}
	input.ExpectedEmail = strings.ToLower(strings.TrimSpace(input.ExpectedEmail))
	if len(input.ExpectedEmail) > 254 || (input.ExpectedEmail != "" && (!strings.Contains(input.ExpectedEmail, "@") || strings.ContainsAny(input.ExpectedEmail, "\r\n\t "))) {
		writeError(w, r, 400, "invalid_login_identity", "invalid_request_error", "invalid expected login email")
		return
	}
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	if s.login != nil && !s.login.Done() {
		if input.Fresh || input.Channel != s.login.Channel || input.ExpectedEmail != s.loginExpectedEmail {
			writeError(w, r, 409, "login_in_progress", "invalid_request_error", "cancel the current login before changing authorization channel")
			return
		}
		api.WriteJSON(w, http.StatusOK, object{"auth_url": s.login.AuthURL, "session": s.loginID, "channel": s.login.Channel, "user_code": s.login.UserCode, "reused": true})
		return
	}
	client, err := httpx.NewClient(httpx.Options{ProxyURL: s.Config().ProxyURL, Timeout: 60 * time.Second})
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_proxy", "invalid_request_error", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	var session *oauth.LoginSession
	if input.Channel == "bps" {
		session, err = oauth.StartBPSLogin(ctx, client)
	} else {
		session, err = oauth.StartLogin(ctx, client)
	}
	if err != nil {
		cancel()
		writeError(w, r, http.StatusConflict, "login_unavailable", "server_error", err.Error())
		return
	}
	s.login = session
	s.loginExpectedEmail = input.ExpectedEmail
	s.loginID = fmt.Sprint(time.Now().UnixNano())
	s.loginAt = time.Now()
	id := s.loginID
	go func() {
		defer cancel()
		tokens, err := session.Wait(ctx)
		if err != nil {
			s.setLoginResult(id, "", err)
			return
		}
		acc := account.Account{Source: session.Channel + "-browser-login"}
		oauth.Apply(&acc, tokens)
		if err := validateLoginIdentity(acc, input.ExpectedEmail, session.Channel); err != nil {
			s.setLoginResult(id, "", err)
			return
		}
		up, err := s.Store.Upsert(acc)
		if err != nil {
			s.setLoginResult(id, "", err)
			return
		}
		s.setLoginResult(id, up.ID, nil)
	}()
	api.WriteJSON(w, http.StatusOK, object{"auth_url": session.AuthURL, "session": s.loginID, "channel": session.Channel, "user_code": session.UserCode})
}

func validateLoginIdentity(acc account.Account, email, channel string) error {
	if email != "" && !strings.EqualFold(strings.TrimSpace(acc.Email), email) {
		return fmt.Errorf("login identity mismatch; account was not saved")
	}
	client := account.CodexOAuthClientID
	if channel == "bps" {
		client = account.BPSOAuthClientID
	}
	if acc.OAuthClientID != "" && acc.OAuthClientID != client {
		return fmt.Errorf("login client identity mismatch; account was not saved")
	}
	return nil
}

var loginResults sync.Map // session id -> object

func (s *Server) setLoginResult(id, accountID string, err error) {
	res := object{"done": true, "account_id": accountID}
	if err != nil {
		res["error"] = err.Error()
	}
	loginResults.Store(id, res)
}

func (s *Server) adminLoginStatus(w http.ResponseWriter, r *http.Request) {
	s.loginMu.Lock()
	session, id := s.login, s.loginID
	s.loginMu.Unlock()
	if session == nil {
		api.WriteJSON(w, http.StatusOK, object{"active": false})
		return
	}
	if expected := r.URL.Query().Get("session"); expected != "" && expected != id {
		writeError(w, r, 409, "login_session_changed", "invalid_request_error", "login session changed")
		return
	}
	res := object{"active": true, "session": id, "auth_url": session.AuthURL, "channel": session.Channel, "user_code": session.UserCode, "done": false}
	if v, ok := loginResults.Load(id); ok {
		res["active"] = false
		for k, val := range v.(object) {
			res[k] = val
		}
	}
	api.WriteJSON(w, http.StatusOK, res)
}

func (s *Server) adminLoginCancel(w http.ResponseWriter, r *http.Request) {
	s.loginMu.Lock()
	if expected := r.URL.Query().Get("session"); expected != "" && expected != s.loginID {
		s.loginMu.Unlock()
		writeError(w, r, 409, "login_session_changed", "invalid_request_error", "login session changed")
		return
	}
	if s.login != nil {
		s.login.Close()
	}
	s.loginMu.Unlock()
	api.WriteJSON(w, http.StatusOK, object{"ok": true})
}

// ---------------------------------------------------------------------------
// connectivity test
// ---------------------------------------------------------------------------

func (s *Server) adminTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model     string `json:"model"`
		Effort    string `json:"effort"`
		Route     string `json:"route"`
		Prompt    string `json:"prompt"`
		AccountID string `json:"account_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	cfg := s.Config()
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = cfg.DefaultModel
	}
	switch req.Route {
	case "bps":
		model += ":bps"
	case "codex":
		model += ":codex"
	}
	prompt := req.Prompt
	if prompt == "" {
		prompt = "Reply with exactly the single word: pong"
	}
	if req.Effort == "" {
		req.Effort = cfg.DefaultEffort
	}
	body := object{"model": model, "input": prompt, "instructions": "You are a connectivity probe. Follow the user's instruction literally.", "reasoning": object{"effort": req.Effort}}
	raw, _ := json.Marshal(body)
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	start := time.Now()
	headers := http.Header{}
	if req.Route == "work" || req.Route == "fenjue" || req.Route == "bps_native" {
		headers.Set("X-GPTBridge-Channel", req.Route)
	}
	if req.AccountID != "" {
		headers.Set("X-GPTBridge-Account", req.AccountID)
	}
	res, rerr := s.Router.Responses(ctx, &router.Request{Raw: raw, Body: body, Headers: headers, Client: "test"})
	if rerr != nil {
		api.WriteJSON(w, http.StatusOK, object{"ok": false, "error": rerr.Message, "code": rerr.Code, "status": rerr.Status, "duration_ms": time.Since(start).Milliseconds()})
		return
	}
	defer res.Body.Close()
	collected, err := api.Collect(res.Body)
	result := object{"route": res.Route, "account": res.Account, "model": res.Model, "effort": res.Effort, "warnings": res.Warnings, "reason": res.Reason, "duration_ms": time.Since(start).Milliseconds()}
	if err != nil {
		result["ok"] = false
		result["error"] = err.Error()
	} else if collected.Failed != nil {
		result["ok"] = false
		result["error"] = str(collected.Failed["message"])
	} else {
		result["ok"] = true
		result["text"] = api.OutputText(collected.Response)
		if collected.Response["status"] != "completed" || strings.TrimSpace(api.OutputText(collected.Response)) == "" {
			result["ok"] = false
			result["error"] = "测试未完整返回文本，请重试；HTTP 200 本身不代表模型可用"
		}
		in, out, _, _ := api.Usage(collected.Response)
		result["usage"] = object{"input_tokens": in, "output_tokens": out}
	}
	api.WriteJSON(w, http.StatusOK, result)
}

// ---------------------------------------------------------------------------
// snippets and Codex config projection
// ---------------------------------------------------------------------------

func (s *Server) snippets() object {
	cfg := s.Config()
	base := s.baseURL()
	key := cfg.APIKey
	keyOrPlaceholder := key
	if keyOrPlaceholder == "" {
		keyOrPlaceholder = "gptbridge"
	}
	models := make([]string, 0, len(basispoints.Catalog))
	for _, m := range basispoints.Catalog {
		models = append(models, m.ID)
	}
	sort.Strings(models)
	codex := codexcfg.Snippet(codexcfg.Projection{BaseURL: base + "/v1", Model: cfg.DefaultModel, Effort: "high", APIKey: key})
	claude := fmt.Sprintf("export ANTHROPIC_BASE_URL=%s\nexport ANTHROPIC_AUTH_TOKEN=%s\nexport ANTHROPIC_MODEL=%s\nexport ANTHROPIC_DEFAULT_OPUS_MODEL=gpt-6-astra\nexport ANTHROPIC_DEFAULT_SONNET_MODEL=gpt-5.6-sol\nexport ANTHROPIC_DEFAULT_HAIKU_MODEL=gpt-5.6-luna\nexport CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1\nclaude", base, keyOrPlaceholder, cfg.DefaultModel)
	claudePS := fmt.Sprintf("$env:ANTHROPIC_BASE_URL=\"%s\"\n$env:ANTHROPIC_AUTH_TOKEN=\"%s\"\n$env:ANTHROPIC_MODEL=\"%s\"\n$env:ANTHROPIC_DEFAULT_OPUS_MODEL=\"gpt-6-astra\"\n$env:ANTHROPIC_DEFAULT_SONNET_MODEL=\"gpt-5.6-sol\"\n$env:ANTHROPIC_DEFAULT_HAIKU_MODEL=\"gpt-5.6-luna\"\n$env:CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=\"1\"\nclaude", base, keyOrPlaceholder, cfg.DefaultModel)
	curl := fmt.Sprintf("curl %s/v1/chat/completions -H \"Content-Type: application/json\" -H \"Authorization: Bearer %s\" -d '{\"model\":\"%s\",\"messages\":[{\"role\":\"user\",\"content\":\"你好\"}]}'", base, keyOrPlaceholder, cfg.DefaultModel)
	python := fmt.Sprintf("from openai import OpenAI\nclient = OpenAI(base_url=%q, api_key=%q)\nresp = client.responses.create(model=%q, input=\"你好\", reasoning={\"effort\": \"high\"})\nprint(resp.output_text)", base+"/v1", keyOrPlaceholder, cfg.DefaultModel)
	return object{
		"base_url": base, "openai_base_url": base + "/v1", "anthropic_base_url": base, "api_key_required": key != "",
		"models": models, "codex_config_toml": codex, "codex_config_path": codexcfg.ConfigPath(),
		"codex_cli_oneoff": fmt.Sprintf("codex -c model_provider=%s -c 'model_providers.%s={name=\"GPTBridge\",base_url=\"%s/v1\",wire_api=\"responses\",requires_openai_auth=false}' -c model=%s", codexcfg.ProviderID, codexcfg.ProviderID, base, cfg.DefaultModel),
		"claude_code_bash": claude, "claude_code_powershell": claudePS, "curl": curl, "python": python,
		"cherry_studio": object{"provider_type": "OpenAI", "api_host": base, "api_key": keyOrPlaceholder, "models": models},
	}
}

func (s *Server) adminApplyCodexConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model  string `json:"model"`
		Effort string `json:"effort"`
		Path   string `json:"path"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	cfg := s.Config()
	path := codexcfg.ConfigPath()
	if strings.TrimSpace(req.Path) != "" {
		path = req.Path
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = cfg.DefaultModel
	}
	backup, err := codexcfg.Apply(path, codexcfg.Projection{BaseURL: s.baseURL() + "/v1", Model: model, Effort: strings.TrimSpace(req.Effort), APIKey: cfg.APIKey})
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "codex_config_failed", "server_error", err.Error())
		return
	}
	api.WriteJSON(w, http.StatusOK, object{"ok": true, "path": path, "backup": backup, "status": codexcfg.Inspect(path)})
}
