// Package server exposes the bridge over HTTP: OpenAI Responses and Chat
// Completions, Anthropic Messages, a model list, health and a loopback-only
// management API backing the embedded dashboard.
package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/xxx-holic/wishtoken-desktop/internal/account"
	"github.com/xxx-holic/wishtoken-desktop/internal/api"
	"github.com/xxx-holic/wishtoken-desktop/internal/basispoints"
	"github.com/xxx-holic/wishtoken-desktop/internal/channelname"
	"github.com/xxx-holic/wishtoken-desktop/internal/config"
	"github.com/xxx-holic/wishtoken-desktop/internal/fenjue"
	"github.com/xxx-holic/wishtoken-desktop/internal/localcodex"
	"github.com/xxx-holic/wishtoken-desktop/internal/oauth"
	"github.com/xxx-holic/wishtoken-desktop/internal/pool"
	"github.com/xxx-holic/wishtoken-desktop/internal/router"
	"github.com/xxx-holic/wishtoken-desktop/internal/upstream"
	"github.com/xxx-holic/wishtoken-desktop/internal/version"
)

type object = map[string]any

const maxBodyBytes = 64 << 20

// Server is the HTTP front end.
type Server struct {
	cfgMu   sync.RWMutex
	cfg     *config.Config
	cfgPath string

	Store         *account.Store
	Pool          *pool.Pool
	Router        *router.Router
	Codex         *upstream.CodexClient
	cockpitRouter *router.Router
	cockpitStore  *account.Store

	logs      *recordLog
	startedAt time.Time
	stopMu    sync.Mutex
	stop      context.CancelFunc

	loginMu            sync.Mutex
	login              *oauth.LoginSession
	loginID            string
	loginAt            time.Time
	loginExpectedEmail string
	fenjueMu           sync.Mutex
	fenjueLogin        *fenjueLogin
}

// New wires the bridge components together.
func New(cfg *config.Config, cfgPath string, store *account.Store) *Server {
	s := &Server{cfg: cfg, cfgPath: cfgPath, Store: store, logs: newRecordLog(500), startedAt: time.Now()}
	s.Pool = pool.New(store, s.Config)
	s.Codex = &upstream.CodexClient{Identity: upstream.DefaultIdentity(cfg.CodexClientVersion)}
	s.Router = &router.Router{
		Config:      s.Config,
		Pool:        s.Pool,
		BPS:         &upstream.BPSClient{},
		Codex:       s.Codex,
		Fenjue:      &fenjue.Client{},
		Replay:      basispoints.NewReplayCache(),
		Attachments: &basispoints.AttachmentCache{},
		Observer:    s.logs.add,
	}
	s.initCockpit()
	s.Router.Work = s.workResponses
	return s
}

// Config returns the current configuration snapshot.
func (s *Server) Config() *config.Config {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg
}

func (s *Server) setConfig(cfg *config.Config) {
	s.cfgMu.Lock()
	s.cfg = cfg
	s.cfgMu.Unlock()
	s.Codex.Identity = upstream.DefaultIdentity(cfg.CodexClientVersion)
}

// Handler builds the HTTP mux.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/health", s.handleHealth)
	for _, prefix := range []string{"", "/v1"} {
		mux.HandleFunc(prefix+"/responses", s.apiAuth(s.handleResponses))
		mux.HandleFunc(prefix+"/chat/completions", s.apiAuth(s.handleChat))
		mux.HandleFunc(prefix+"/messages", s.apiAuth(s.handleMessages))
		mux.HandleFunc(prefix+"/messages/count_tokens", s.apiAuth(s.handleCountTokens))
		mux.HandleFunc(prefix+"/models", s.apiAuth(s.handleModels))
	}
	mux.HandleFunc("/api/", s.adminAuth(s.handleAdmin))
	mux.HandleFunc("/cockpit/", s.handleCockpit)
	mux.HandleFunc("/cockpit-auth/", s.handleCockpitIdentity)
	mux.HandleFunc("/desktop-api/", s.handleDesktopAPI)
	mux.HandleFunc("/", s.handleUI)
	return s.hostGuard(mux)
}

// ListenAndServe runs the server until ctx ends.
func (s *Server) ListenAndServe(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.stopMu.Lock()
	s.stop = cancel
	s.stopMu.Unlock()
	cfg := s.Config()
	if !config.IsLoopback(cfg.Listen) && (!cfg.AllowRemote || cfg.APIKey == "") {
		return fmt.Errorf("refusing to bind %q: set allow_remote=true and an api_key to expose the bridge", cfg.Listen)
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 30 * time.Second}
	go s.Pool.RunMaintenance(ctx, s.Codex.Identity)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	log.Printf("gptbridge %s listening on http://%s (dashboard at /)", version.Version, ln.Addr())
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
// middleware
// ---------------------------------------------------------------------------

func (s *Server) hostGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := s.Config()
		if config.IsLoopback(cfg.Listen) && !cfg.AllowRemote {
			host := r.Host
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			if !config.IsLoopback(host) {
				http.Error(w, "forbidden host", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) apiAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := s.Config().APIKey
		if key != "" && !presentsKey(r, key) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, r, http.StatusUnauthorized, "invalid_api_key", "authentication_error", "缺少或错误的 API Key。 / Missing or invalid API key.")
			return
		}
		next(w, r)
	}
}

func presentsKey(r *http.Request, key string) bool {
	if auth := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		if strings.TrimSpace(auth[7:]) == key {
			return true
		}
	}
	if strings.TrimSpace(r.Header.Get("x-api-key")) == key {
		return true
	}
	return strings.TrimSpace(r.Header.Get("X-GPTBridge-Key")) == key
}

func (s *Server) adminAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := s.Config()
		remoteHost, _, _ := net.SplitHostPort(r.RemoteAddr)
		fromLoopback := config.IsLoopback(remoteHost)
		if cfg.DesktopMode && !presentsKey(r, cfg.APIKey) {
			writeError(w, r, http.StatusUnauthorized, "admin_unauthorized", "authentication_error", "desktop management requires the local key")
			return
		}
		if !fromLoopback && (cfg.APIKey == "" || !presentsKey(r, cfg.APIKey)) {
			writeError(w, r, http.StatusUnauthorized, "admin_unauthorized", "authentication_error", "management API requires loopback access or the API key")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if site := strings.ToLower(r.Header.Get("Sec-Fetch-Site")); site == "cross-site" {
				writeError(w, r, http.StatusForbidden, "csrf", "permission_error", "cross-site requests are not allowed")
				return
			}
			if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" && !sameOrigin(origin, r.Host) {
				writeError(w, r, http.StatusForbidden, "csrf", "permission_error", "origin mismatch")
				return
			}
		}
		next(w, r)
	}
}

func sameOrigin(origin, host string) bool {
	origin = strings.TrimSpace(origin)
	for _, scheme := range []string{"http://", "https://"} {
		if strings.EqualFold(strings.TrimPrefix(origin, scheme), host) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// decodeJSONObject accepts one object, plus the double-encoded string form
// some clients emit after serializing a request twice.
func decodeJSONObject(raw []byte) (object, []byte, bool) {
	decode := func(value []byte) (object, bool) {
		dec := json.NewDecoder(bytes.NewReader(value))
		dec.UseNumber()
		var body object
		if err := dec.Decode(&body); err != nil || body == nil {
			return nil, false
		}
		var extra any
		if dec.Decode(&extra) != io.EOF {
			return nil, false
		}
		return body, true
	}
	if body, ok := decode(raw); ok {
		return body, raw, true
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err == nil {
		if nested, ok := decode([]byte(encoded)); ok {
			return nested, []byte(encoded), true
		}
	}
	return nil, nil, false
}

func readJSONBody(w http.ResponseWriter, r *http.Request) (object, []byte, bool) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_body", "invalid_request_error", "could not read request body")
		return nil, nil, false
	}
	if len(raw) > maxBodyBytes {
		writeError(w, r, http.StatusRequestEntityTooLarge, "body_too_large", "invalid_request_error", "request body exceeds 64 MiB")
		return nil, nil, false
	}
	encoding := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding")))
	switch encoding {
	case "", "identity":
	case "gzip":
		reader, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_body", "invalid_request_error", "request body is not valid gzip")
			return nil, nil, false
		}
		defer reader.Close()
		raw, err = io.ReadAll(io.LimitReader(reader, maxBodyBytes+1))
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_body", "invalid_request_error", "could not decompress request body")
			return nil, nil, false
		}
		if len(raw) > maxBodyBytes {
			writeError(w, r, http.StatusRequestEntityTooLarge, "body_too_large", "invalid_request_error", "request body exceeds 64 MiB")
			return nil, nil, false
		}
	default:
		writeError(w, r, http.StatusUnsupportedMediaType, "unsupported_encoding", "invalid_request_error", "unsupported request content-encoding: "+encoding)
		return nil, nil, false
	}
	if len(raw) >= 4 && raw[0] == 0x28 && raw[1] == 0xb5 && raw[2] == 0x2f && raw[3] == 0xfd {
		writeError(w, r, http.StatusUnsupportedMediaType, "unsupported_encoding", "invalid_request_error", "zstd-compressed request bodies are not supported; set [features] enable_request_compression = false")
		return nil, nil, false
	}
	body, raw, ok := decodeJSONObject(raw)
	if !ok {
		writeError(w, r, http.StatusBadRequest, "invalid_json", "invalid_request_error", "request body is not a JSON object")
		return nil, nil, false
	}
	return body, raw, true
}

func isAnthropic(r *http.Request) bool {
	return strings.HasSuffix(r.URL.Path, "/messages") || strings.HasSuffix(r.URL.Path, "/count_tokens")
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, kind, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	var payload any
	if isAnthropic(r) {
		anthropicType := "invalid_request_error"
		switch status {
		case http.StatusUnauthorized:
			anthropicType = "authentication_error"
		case http.StatusForbidden:
			anthropicType = "permission_error"
		case http.StatusTooManyRequests:
			anthropicType = "rate_limit_error"
		case http.StatusServiceUnavailable:
			anthropicType = "overloaded_error"
		default:
			if status >= 500 {
				anthropicType = "api_error"
			}
		}
		payload = object{"type": "error", "error": object{"type": anthropicType, "message": message, "code": code}}
	} else {
		payload = object{"error": object{"message": message, "type": kind, "code": code, "status": status}}
	}
	_ = json.NewEncoder(w).Encode(payload)
}

func writeRouterError(w http.ResponseWriter, r *http.Request, err *router.Error) {
	status := err.Status
	if status == 499 {
		status = http.StatusBadRequest
	}
	writeError(w, r, status, err.Code, err.Type, err.Message)
}

func clientKind(r *http.Request) string {
	if upstream.IsOfficialCodexClient(r.Header.Get("User-Agent"), r.Header.Get("Originator")) {
		return "codex"
	}
	ua := strings.ToLower(r.Header.Get("User-Agent"))
	switch {
	case strings.Contains(ua, "claude-cli"):
		return "claude-code"
	case strings.Contains(ua, "openai"):
		return "openai-sdk"
	}
	return "generic"
}

func wantsStream(body object, dflt bool) bool {
	if v, ok := body["stream"].(bool); ok {
		return v
	}
	return dflt
}

func applyResultHeaders(w http.ResponseWriter, res *router.Result) {
	for k, v := range res.Header {
		for _, value := range v {
			w.Header().Add(k, value)
		}
	}
	if len(res.Warnings) > 0 {
		w.Header().Set("X-GPTBridge-Warnings", strings.Join(res.Warnings, "; "))
	}
}

// ---------------------------------------------------------------------------
// /v1/responses
// ---------------------------------------------------------------------------

func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "invalid_request_error", "use POST")
		return
	}
	body, raw, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	stream := wantsStream(body, true)
	res, rerr := s.Router.Responses(r.Context(), &router.Request{Raw: raw, Body: body, Headers: r.Header, Client: clientKind(r)})
	if rerr != nil {
		writeRouterError(w, r, rerr)
		return
	}
	defer res.Body.Close()
	applyResultHeaders(w, res)
	if stream {
		_ = api.Pipe(w, res.Body)
		return
	}
	collected, err := api.Collect(res.Body)
	if err != nil {
		writeError(w, r, http.StatusBadGateway, "upstream_stream_error", "server_error", err.Error())
		return
	}
	if collected.Failed != nil {
		writeError(w, r, http.StatusBadGateway, str(collected.Failed["code"]), "server_error", str(collected.Failed["message"]))
		return
	}
	api.WriteJSON(w, http.StatusOK, collected.Response)
}

// ---------------------------------------------------------------------------
// /v1/chat/completions
// ---------------------------------------------------------------------------

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "invalid_request_error", "use POST")
		return
	}
	body, _, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	cfg := s.Config()
	converted, err := api.ChatToResponses(body, api.Defaults{Model: cfg.DefaultModel, Effort: cfg.DefaultEffort})
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid_request_error", err.Error())
		return
	}
	raw, _ := json.Marshal(converted)
	res, rerr := s.Router.Responses(r.Context(), &router.Request{Raw: raw, Body: converted, Headers: r.Header, Client: "chat:" + clientKind(r)})
	if rerr != nil {
		writeRouterError(w, r, rerr)
		return
	}
	defer res.Body.Close()
	applyResultHeaders(w, res)
	model := str(body["model"])
	if model == "" {
		model = res.Model
	}
	if wantsStream(body, false) {
		if err := api.ChatStream(w, res.Body, model); err != nil {
			var se *api.StreamError
			if errors.As(err, &se) {
				writeError(w, r, se.Status, se.Code, "server_error", se.Message)
			}
		}
		return
	}
	completion, serr := api.ChatCompletion(res.Body, model)
	if serr != nil {
		writeError(w, r, serr.Status, serr.Code, "server_error", serr.Message)
		return
	}
	api.WriteJSON(w, http.StatusOK, completion)
}

// ---------------------------------------------------------------------------
// /v1/messages (Anthropic)
// ---------------------------------------------------------------------------

func (s *Server) anthropicOptions() api.AnthropicOptions {
	cfg := s.Config()
	return api.AnthropicOptions{ModelMap: cfg.AnthropicModelMap, DefaultModel: cfg.DefaultModel, DefaultEffort: cfg.DefaultEffort}
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "invalid_request_error", "use POST")
		return
	}
	body, _, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	converted, err := api.MessagesToResponses(body, s.anthropicOptions())
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid_request_error", err.Error())
		return
	}
	raw, _ := json.Marshal(converted)
	res, rerr := s.Router.Responses(r.Context(), &router.Request{Raw: raw, Body: converted, Headers: r.Header, Client: "anthropic:" + clientKind(r)})
	if rerr != nil {
		writeRouterError(w, r, rerr)
		return
	}
	defer res.Body.Close()
	applyResultHeaders(w, res)
	model := str(body["model"])
	if wantsStream(body, false) {
		if err := api.AnthropicStream(w, res.Body, model); err != nil {
			var se *api.StreamError
			if errors.As(err, &se) {
				writeError(w, r, se.Status, se.Code, "api_error", se.Message)
			}
		}
		return
	}
	message, serr := api.AnthropicMessage(res.Body, model)
	if serr != nil {
		writeError(w, r, serr.Status, serr.Code, "api_error", serr.Message)
		return
	}
	api.WriteJSON(w, http.StatusOK, message)
}

func (s *Server) handleCountTokens(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "invalid_request_error", "use POST")
		return
	}
	body, _, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	api.WriteJSON(w, http.StatusOK, object{"input_tokens": api.EstimateTokens(body)})
}

// ---------------------------------------------------------------------------
// /v1/models
// ---------------------------------------------------------------------------

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "invalid_request_error", "use GET")
		return
	}
	// Codex clients ask for the manifest with ?client_version=; proxy the
	// upstream manifest so the model picker stays current.
	if clientVersion := r.URL.Query().Get("client_version"); clientVersion != "" {
		if s.Config().DesktopMode {
			channel, err := localcodex.Channel(r.Header.Get("X-GPTBridge-Channel"))
			if err != nil {
				writeError(w, r, 400, "invalid_channel", "invalid_request_error", err.Error())
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(localcodex.ChannelCatalog(channel))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if acc, err := s.Pool.Pick(ctx, pool.PickOptions{Route: router.RouteCodex}); err == nil {
			if body, status, err := s.Codex.Models(ctx, s.Pool.Credentials(acc), clientVersion); err == nil && status == http.StatusOK {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-GPTBridge-Upstream", "codex")
				_, _ = w.Write(body)
				return
			}
		}
	}
	cfg := s.Config()
	now := time.Now().Unix()
	data := make([]any, 0, len(basispoints.Catalog)*2)
	if channelname.Normalize(r.Header.Get("X-GPTBridge-Channel")) == "fenjue" {
		for _, id := range fenjue.Models {
			data = append(data, object{"id": id, "object": "model", "owned_by": "fenjue", "availability": "checked before generation"})
		}
		api.WriteJSON(w, 200, object{"object": "list", "data": data})
		return
	}
	if cfg.DesktopMode {
		for _, model := range localcodex.WorkModels() {
			data = append(data, object{"id": model["id"], "object": "model", "owned_by": "chatgpt-work", "display_name": model["display_name"], "reasoning_efforts": localcodex.WorkEfforts})
		}
	}
	for _, m := range basispoints.Catalog {
		entry := object{"id": m.ID, "object": "model", "created": now, "owned_by": "openai", "display_name": m.DisplayName,
			"context_window": m.ContextWindow, "bps_allowed": cfg.BPSModelAllowed(m.ID), "reasoning_efforts": basispoints.Efforts}
		data = append(data, entry)
		data = append(data, object{"id": m.ID + "-1m", "object": "model", "created": now, "owned_by": "openai", "display_name": m.DisplayName + " 1M",
			"context_window": basispoints.LongContextWindow, "bps_allowed": cfg.BPSModelAllowed(m.ID), "reasoning_efforts": basispoints.Efforts})
	}
	api.WriteJSON(w, http.StatusOK, object{"object": "list", "data": data})
}

// ---------------------------------------------------------------------------
// /healthz
// ---------------------------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	accounts := s.Store.List()
	ready := 0
	for _, acc := range accounts {
		if !acc.Disabled && acc.HasCredentials() {
			ready++
		}
	}
	api.WriteJSON(w, http.StatusOK, object{
		"app": "gptbridge-team", "ok": true, "version": version.Version, "listen": s.Config().Listen,
		"cockpit_oauth": s.Config().CockpitKey != "",
		"accounts":      len(accounts), "usable_accounts": ready, "uptime_seconds": int(time.Since(s.startedAt).Seconds()),
	})
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
