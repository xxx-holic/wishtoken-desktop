package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/xxx-holic/wishtoken-desktop/internal/account"
	"github.com/xxx-holic/wishtoken-desktop/internal/api"
	"github.com/xxx-holic/wishtoken-desktop/internal/channelname"
	"github.com/xxx-holic/wishtoken-desktop/internal/config"
	"github.com/xxx-holic/wishtoken-desktop/internal/jwt"
	"github.com/xxx-holic/wishtoken-desktop/internal/localcodex"
)

var launchMu sync.Mutex

type launchRecord struct {
	ID            string    `json:"id"`
	AccountID     string    `json:"account_id"`
	Directory     string    `json:"directory"`
	Home          string    `json:"home"`
	Model         string    `json:"model"`
	Effort        string    `json:"effort"`
	ContextWindow int       `json:"context_window"`
	CompactLimit  int       `json:"compact_limit"`
	LastUsed      time.Time `json:"last_used"`
	PID           int       `json:"pid,omitempty"`
	Target        string    `json:"target,omitempty"`
	Channel       string    `json:"channel,omitempty"`
	Speed         string    `json:"speed,omitempty"`
	AppMode       string    `json:"app_mode,omitempty"`
}

func launchHistory() []launchRecord {
	var entries []launchRecord
	raw, err := os.ReadFile(filepath.Join(config.Home(), "launch-history.json"))
	if err == nil {
		_ = json.Unmarshal(raw, &entries)
	}
	if entries == nil {
		entries = []launchRecord{}
	}
	for i := range entries {
		entries[i].Channel = channelname.Normalize(entries[i].Channel)
	}
	return entries
}

func saveLaunch(rec launchRecord) error {
	entries := []launchRecord{rec}
	for _, old := range launchHistory() {
		if old.ID != rec.ID && len(entries) < 40 {
			entries = append(entries, old)
		}
	}
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(config.Home(), "launch-history.json")
	if err := os.WriteFile(path+".tmp", raw, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

func (s *Server) localOptions() localcodex.Options {
	cfg := s.Config()
	return localcodex.Options{Home: filepath.Join(config.Home(), "codex-home"), BaseURL: s.baseURL(), APIKey: cfg.APIKey, Model: cfg.DefaultModel, Effort: cfg.DefaultEffort, AccountID: cfg.ActiveAccountID}
}

func (s *Server) adminLocalCodex(w http.ResponseWriter, r *http.Request) {
	o := s.localOptions()
	if r.Method == http.MethodGet {
		bin, err := localcodex.Binary()
		message := ""
		if err != nil {
			message = err.Error()
		}
		launchMu.Lock()
		history := launchHistory()
		launchMu.Unlock()
		api.WriteJSON(w, 200, object{"installed": err == nil, "binary": bin, "home": o.Home, "model": o.Model, "effort": o.Effort, "active_account_id": o.AccountID, "error": message, "history": history})
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	if s.Config().RoutePolicy != config.PolicyBPSOnly || s.Config().NativeFallback {
		writeError(w, r, 400, "route_policy", "invalid_request_error", "请先保存 BPS 专用配置再启动 Codex")
		return
	}
	var req struct {
		Directory     string `json:"directory"`
		AccountID     string `json:"account_id"`
		Model         string `json:"model"`
		Effort        string `json:"effort"`
		ContextWindow int    `json:"context_window"`
		CompactLimit  int    `json:"compact_limit"`
		PrepareOnly   bool   `json:"prepare_only"`
		Resume        bool   `json:"resume"`
		Target        string `json:"target"`
		Channel       string `json:"channel"`
		Speed         string `json:"speed"`
		AppMode       string `json:"app_mode"`
		PrismURL      string `json:"prism_url"`
		PrismKey      string `json:"prism_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, 400, "invalid_json", "invalid_request_error", "invalid launch request")
		return
	}
	if req.AccountID != "" {
		o.AccountID = req.AccountID
	}
	if o.AccountID == "" {
		for _, a := range s.Store.List() {
			if !a.Disabled && a.HasCredentials() {
				o.AccountID = a.ID
				break
			}
		}
	}
	a, found := s.Store.Get(o.AccountID)
	if !found || a.Disabled || !a.HasCredentials() || (a.Expired() && a.RefreshToken == "") {
		writeError(w, r, 400, "account_unavailable", "invalid_request_error", "请选择凭据有效且已启用的账号")
		return
	}
	if req.Model != "" {
		o.Model = req.Model
	}
	channel, channelErr := localcodex.Channel(req.Channel)
	if channelErr != nil {
		writeError(w, r, 400, "invalid_channel", "invalid_request_error", channelErr.Error())
		return
	}
	o.Channel = channel
	if (a.Provider == "fenjue") != (channel == "fenjue") {
		writeError(w, r, 400, "credential_channel_mismatch", "invalid_request_error", "Fenjue requires an independent Fenjue credential")
		return
	}
	if channel == "fenjue" && req.Target == "app" && req.AppMode != "isolated" {
		writeError(w, r, 400, "fenjue_app_mode", "invalid_request_error", "Fenjue uses an isolated Codex instance; choose isolated mode")
		return
	}
	if channel == "codex" && a.OAuthClientID == account.BPSOAuthClientID {
		writeError(w, r, 400, "oauth_channel_mismatch", "invalid_request_error", "此记录是 BPS 授权，请选择同账号的 Codex 授权记录用于原生通道")
		return
	}
	o.Speed = req.Speed
	if channel == "prism" {
		o.PrismURL = req.PrismURL
		o.PrismKey = req.PrismKey
		o.AccessToken = ""
		o.AuthAPIURL = ""
	}
	if channel == "codex" && !s.Config().DesktopMode {
		writeError(w, r, 400, "route_policy", "invalid_request_error", "原生启动需要专用桌面客户端")
		return
	}
	if (channel == "bps" && !s.Config().BPSModelAllowed(o.Model)) || (channel == "codex" && !localcodex.NativeModelAllowed(o.Model)) || (channel == "work" && !localcodex.WorkModelAllowed(o.Model)) || (channel == "prism" && !localcodex.PrismModelAllowed(o.Model)) {
		writeError(w, r, 400, "model_unavailable", "invalid_request_error", "模型不在所选通道列表中")
		return
	}
	if req.Effort != "" {
		o.Effort = req.Effort
	}
	if req.Target == "" {
		req.Target = "cli"
	}
	if req.Target != "cli" && req.Target != "app" {
		writeError(w, r, 400, "invalid_target", "invalid_request_error", "启动目标无效")
		return
	}
	if req.Target == "app" && (!req.PrepareOnly || req.Resume) {
		writeError(w, r, 400, "invalid_target", "invalid_request_error", "App 由桌面客户端启动")
		return
	}
	if req.Target == "app" {
		if req.AppMode == "" {
			req.AppMode = "main"
		}
		if req.AppMode != "main" && req.AppMode != "isolated" {
			writeError(w, r, 400, "invalid_app_mode", "invalid_request_error", "App 工作空间无效")
			return
		}
		if channel == "prism" && req.AppMode == "main" {
			writeError(w, r, 400, "prism_app_mode", "invalid_request_error", "Prism 请使用独立实例，不能接管主 Codex 配置")
			return
		}
	}
	if req.Target == "cli" && strings.TrimSpace(req.Directory) == "" {
		writeError(w, r, 400, "directory_required", "invalid_request_error", "请先选择项目目录")
		return
	}
	directory := strings.TrimSpace(req.Directory)
	if req.Target == "app" {
		directory, _ = os.UserHomeDir()
	}
	dir, err := filepath.Abs(directory)
	if err != nil {
		writeError(w, r, 400, "directory_invalid", "invalid_request_error", err.Error())
		return
	}
	o.Directory, o.ContextWindow, o.CompactLimit, o.Resume = dir, req.ContextWindow, req.CompactLimit, req.Resume
	identityDir := filepath.Clean(dir)
	if req.Target == "app" {
		identityDir = "desktop-app"
		if req.AppMode == "main" {
			identityDir += "-main"
		}
	}
	if runtime.GOOS == "windows" {
		identityDir = strings.ToLower(identityDir)
	}
	identity := o.AccountID + "\n" + identityDir
	// Keep existing BPS histories; native sessions get a separate profile.
	if channel != "bps" {
		identity += "\n" + channelname.IdentityKey(channel)
	}
	key := sha256.Sum256([]byte(identity))
	instanceID := hex.EncodeToString(key[:10])
	o.Home = filepath.Join(config.Home(), "codex-instances", instanceID)
	if channel != "prism" && channel != "fenjue" && s.Config().DesktopMode && s.Config().CockpitKey != "" && jwt.IsJWT(a.AccessToken) {
		o.AccessToken = a.AccessToken
		o.AuthAPIURL = s.baseURL() + "/cockpit-auth/" + s.Config().CockpitKey
	}
	launchMu.Lock()
	defer launchMu.Unlock()
	o, err = localcodex.Prepare(o)
	if err != nil {
		writeError(w, r, 400, "codex_config", "invalid_request_error", err.Error())
		return
	}
	s.cfgMu.Lock()
	next := *s.cfg
	next.ActiveAccountID = o.AccountID
	if channel == "bps" {
		next.DefaultModel, next.DefaultEffort = o.Model, o.Effort
	}
	if s.cfgPath != "" {
		err = next.Save(s.cfgPath)
	}
	if err == nil {
		s.cfg = &next
	}
	s.cfgMu.Unlock()
	if err != nil {
		writeError(w, r, 500, "save_failed", "server_error", err.Error())
		return
	}
	pid := 0
	if !req.PrepareOnly {
		pid, err = localcodex.Launch(o)
		if err != nil {
			writeError(w, r, 400, "codex_launch", "invalid_request_error", fmt.Sprintf("账号已切换，终端启动失败：%v", err))
			return
		}
	}
	rec := launchRecord{ID: instanceID, AccountID: o.AccountID, Directory: dir, Home: o.Home, Model: o.Model, Effort: o.Effort, ContextWindow: o.ContextWindow, CompactLimit: o.CompactLimit, LastUsed: time.Now().UTC(), PID: pid, Target: req.Target}
	rec.Channel = channel
	rec.Speed = o.Speed
	rec.AppMode = req.AppMode
	warning := ""
	if err := saveLaunch(rec); err != nil {
		warning = "无法保存最近启动记录：" + err.Error()
	}
	api.WriteJSON(w, 200, object{"ok": true, "pid": pid, "home": o.Home, "directory": dir, "account_id": o.AccountID, "model": o.Model, "effort": o.Effort, "channel": channel, "prepared_only": req.PrepareOnly, "instance_id": instanceID, "started_at": rec.LastUsed, "warning": warning})
}
