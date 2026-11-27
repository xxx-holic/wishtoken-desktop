// Package router decides which upstream serves a Responses request, runs it
// against an account from the pool and applies controlled fallbacks.
package router

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/xxx-holic/wishtoken-desktop/internal/account"
	"github.com/xxx-holic/wishtoken-desktop/internal/basispoints"
	"github.com/xxx-holic/wishtoken-desktop/internal/channelname"
	"github.com/xxx-holic/wishtoken-desktop/internal/config"
	"github.com/xxx-holic/wishtoken-desktop/internal/fenjue"
	"github.com/xxx-holic/wishtoken-desktop/internal/localcodex"
	"github.com/xxx-holic/wishtoken-desktop/internal/pool"
	"github.com/xxx-holic/wishtoken-desktop/internal/upstream"
)

type object = map[string]any

// Routes.
const (
	RouteBPS    = "bps"
	RouteCodex  = "codex"
	RouteWork   = "work"
	RouteFenjue = "fenjue"
)

// Router executes Responses requests.
type Router struct {
	FenjueAccess   func() error
	BPSNativeReady func(string) error
	Work           func(context.Context, *Request, string) (*Result, *Error)
	Config         func() *config.Config
	Pool           *pool.Pool
	BPS            *upstream.BPSClient
	Codex          *upstream.CodexClient
	Fenjue         *fenjue.Client
	Replay         *basispoints.ReplayCache
	Attachments    *basispoints.AttachmentCache
	// Observer receives one record per attempt (dashboard log). Optional.
	Observer func(Record)
}

// Record describes one upstream attempt.
type Record struct {
	Time                   time.Time `json:"time"`
	Route                  string    `json:"route"`
	Model                  string    `json:"model"`
	Effort                 string    `json:"effort,omitempty"`
	Account                string    `json:"account"`
	Status                 int       `json:"status"`
	Duration               float64   `json:"duration_ms"`
	Error                  string    `json:"error,omitempty"`
	Fallback               string    `json:"fallback,omitempty"`
	Client                 string    `json:"client,omitempty"`
	ServiceTier            string    `json:"service_tier,omitempty"`
	ResponseTier           string    `json:"response_service_tier,omitempty"`
	ResponseModel          string    `json:"response_model,omitempty"`
	StreamStatus           string    `json:"stream_status,omitempty"`
	RequestTurnStateLength int       `json:"request_turn_state_length,omitempty"`
	TurnStateLength        int       `json:"turn_state_length,omitempty"`
	SafetyBuffering        string    `json:"safety_buffering,omitempty"`
	SafetyFasterModel      string    `json:"safety_faster_model,omitempty"`
	ModelMismatch          bool      `json:"model_mismatch,omitempty"`
	Quality                string    `json:"quality,omitempty"`
	Transport              string    `json:"transport,omitempty"`
	InputTokens            int64     `json:"input_tokens,omitempty"`
	CachedInputTokens      int64     `json:"cached_input_tokens,omitempty"`
	OutputTokens           int64     `json:"output_tokens,omitempty"`
	ReasoningTokens        int64     `json:"reasoning_tokens,omitempty"`
	FenjueBYOK             *bool     `json:"fenjue_byok,omitempty"`
	FenjueCost             *float64  `json:"fenjue_cost,omitempty"`
}

func copyQualityHeaders(dst, src http.Header) {
	for _, name := range []string{"X-Codex-Turn-State", "X-Codex-Safety-Buffering-Enabled", "X-Codex-Safety-Buffering-Faster-Model"} {
		if value := strings.TrimSpace(src.Get(name)); value != "" {
			dst.Set(name, value)
		}
	}
}

func requestTurnStateLength(header http.Header) int {
	if header == nil {
		return 0
	}
	return len(strings.TrimSpace(header.Get("X-Codex-Turn-State")))
}

func noteQualityHeaders(rec *Record, header http.Header) {
	if header == nil {
		return
	}
	rec.TurnStateLength = len(strings.TrimSpace(header.Get("X-Codex-Turn-State")))
	rec.SafetyBuffering = strings.TrimSpace(header.Get("X-Codex-Safety-Buffering-Enabled"))
	rec.SafetyFasterModel = strings.TrimSpace(header.Get("X-Codex-Safety-Buffering-Faster-Model"))
}

func normalizeModelName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.TrimPrefix(name, "openai/")
	for _, suffix := range []string{":bps", ":codex", "-bps", "-excel", "-1m"} {
		name = strings.TrimSuffix(name, suffix)
	}
	return name
}

// modelsEquivalent tolerates date/snapshot suffixes but never treats a
// degrade/luna substitution as the requested model.
func modelsEquivalent(requested, served string) bool {
	a, b := normalizeModelName(requested), normalizeModelName(served)
	if a == "" || b == "" {
		return true
	}
	if a == b {
		return true
	}
	if strings.Contains(b, "degrade") || (strings.Contains(b, "luna") && !strings.Contains(a, "luna")) {
		return false
	}
	for _, pair := range [][2]string{{a, b}, {b, a}} {
		if prefix := pair[0] + "-"; strings.HasPrefix(pair[1], prefix) {
			tail := strings.TrimPrefix(pair[1], prefix)
			if tail != "" && strings.Trim(tail, "0123456789-") == "" {
				return true
			}
		}
	}
	return false
}

func applyQuality(rec *Record) {
	rec.ModelMismatch = false
	if rec.ResponseModel == "" || rec.Model == "" {
		rec.Quality = "unknown"
		return
	}
	// Headers and model names are observable signals, never an intelligence test.
	rec.ModelMismatch = !modelsEquivalent(rec.Model, rec.ResponseModel)
	if rec.ModelMismatch {
		rec.Quality = "model_mismatch"
	} else {
		rec.Quality = "model_match"
	}
}

// Request is a Responses request from any front end.
type Request struct {
	Raw     []byte
	Body    object
	Headers http.Header
	// Client is a short label of the downstream client kind (log only).
	Client string
}

// Result is a streaming Responses answer.
type Result struct {
	Body     io.ReadCloser
	Route    string
	Account  string
	Model    string
	Effort   string
	Warnings []string
	Header   http.Header
	// Reason explains a native route when Basispoints was not used.
	Reason string
}

// Error is an API error returned to the client.
type Error struct {
	Status  int
	Code    string
	Type    string
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("%d %s: %s", e.Status, e.Code, e.Message) }

func apiErr(status int, code, message string) *Error {
	kind := "invalid_request_error"
	switch {
	case status == http.StatusUnauthorized:
		kind = "authentication_error"
	case status == http.StatusTooManyRequests:
		kind = "rate_limit_error"
	case status >= 500:
		kind = "server_error"
	}
	return &Error{Status: status, Code: code, Type: kind, Message: message}
}

// Responses routes and executes req.
func (r *Router) Responses(ctx context.Context, req *Request) (*Result, *Error) {
	cfg := r.Config()
	accountID := strings.TrimSpace(req.Headers.Get("X-GPTBridge-Account"))
	if accountID == "" {
		accountID = cfg.ActiveAccountID
	}
	if req.Headers.Get("X-GPTBridge-Channel") == "bps_native" {
		if !cfg.DesktopMode || r.BPSNativeReady == nil {
			return nil, apiErr(403, "primer_required", "BPS → 原生通道需要桌面预热记录")
		}
		if err := r.BPSNativeReady(accountID); err != nil {
			return nil, apiErr(409, "primer_required", err.Error())
		}
		copy := *req
		copy.Headers = req.Headers.Clone()
		copy.Headers.Set("X-GPTBridge-Channel", RouteCodex)
		copy.Headers.Set("X-GPTBridge-Account", accountID)
		copy.Client = req.Client + "/bps-native"
		req = &copy
	}
	body := cloneObject(req.Body)
	requested := strings.TrimSpace(str(body["model"]))
	if requested == "" {
		requested = cfg.DefaultModel
	}
	requested = channelname.Model(requested)
	if channelname.Normalize(req.Headers.Get("X-GPTBridge-Channel")) == RouteFenjue || strings.HasSuffix(requested, "-fenjue") {
		return r.executeFenjue(ctx, req, accountID, requested)
	}
	// Work has a separate browser identity and transport. Never enter pool retry,
	// token refresh, BPS normalization or native fallback for this explicit route.
	workChannel := strings.TrimSpace(req.Headers.Get("X-GPTBridge-Channel"))
	if workChannel == RouteWork || strings.HasSuffix(requested, "-wm") {
		if workChannel != "" && workChannel != RouteWork {
			return nil, apiErr(400, "channel_conflict", "Work model conflicts with pinned channel")
		}
		if !cfg.DesktopMode || r.Work == nil {
			return nil, apiErr(503, "work_unavailable", "Work requires the running desktop browser bridge")
		}
		if !localcodex.WorkModelAllowed(requested) {
			return nil, apiErr(400, "work_model", "Work 不支持该模型，请选择 GPT-6 / GPT-5.6 的 Work 模型")
		}
		effort := "medium"
		if reasoning, ok := body["reasoning"].(object); ok {
			if value := strings.TrimSpace(str(reasoning["effort"])); value != "" {
				effort = value
			}
		}
		if value := strings.TrimSpace(str(body["reasoning_effort"])); value != "" {
			effort = value
		}
		if !localcodex.WorkEffortAllowed(effort) {
			return nil, apiErr(400, "work_effort", "Work 推理档位无效，请选择 low / medium / high / xhigh / max")
		}
		rec := Record{Time: time.Now(), Route: RouteWork, Model: requested, Effort: effort, Account: accountID, Client: req.Client, Status: 200}
		res, failure := r.Work(ctx, req, accountID)
		if failure != nil {
			rec.Status = failure.Status
			rec.Error = failure.Message
			rec.StreamStatus = "failed"
			rec.Duration = ms(rec.Time)
			r.observe(rec)
			return nil, failure
		}
		res.Body = &accounting{ReadCloser: res.Body, done: func(response object, err error) {
			rec.Duration = ms(rec.Time)
			if response != nil {
				rec.ResponseModel = str(response["model"])
				rec.StreamStatus = "completed"
				applyQuality(&rec)
			}
			if err != nil {
				rec.Error = err.Error()
				rec.StreamStatus = "failed"
			}
			r.observe(rec)
		}}
		return res, nil
	}
	resolved := basispoints.ResolveModel(requested)
	channel := strings.TrimSpace(req.Headers.Get("X-GPTBridge-Channel"))
	if channel != "" {
		if channel != RouteBPS && channel != RouteCodex {
			return nil, apiErr(400, "invalid_channel", "channel must be bps or codex")
		}
		if resolved.ForceRoute != "" && resolved.ForceRoute != channel {
			return nil, apiErr(400, "channel_conflict", "model suffix conflicts with the pinned channel")
		}
		resolved.ForceRoute = channel
	}
	// Desktop remains BPS by default, but permits an explicit per-request choice.
	if cfg.RoutePolicy == config.PolicyBPSOnly && resolved.ForceRoute == RouteCodex && !cfg.DesktopMode {
		return nil, apiErr(http.StatusBadRequest, "route_policy", "BPS-only mode does not allow a native Codex route")
	}
	body["model"] = resolved.Upstream
	body["stream"] = true
	body["store"] = false
	if resolved.Effort != "" {
		reasoning, _ := body["reasoning"].(object)
		reasoning = cloneObject(reasoning)
		reasoning["effort"] = resolved.Effort
		body["reasoning"] = reasoning
	}
	if reasoning, ok := body["reasoning"].(object); ok {
		if str(reasoning["effort"]) == "" && cfg.DefaultEffort != "" {
			reasoning = cloneObject(reasoning)
			reasoning["effort"] = cfg.DefaultEffort
			body["reasoning"] = reasoning
		}
	} else if cfg.DefaultEffort != "" {
		body["reasoning"] = object{"effort": cfg.DefaultEffort, "summary": "auto"}
	}

	route, reason := r.decide(cfg, resolved, body)
	sessionKey := SessionKey(req.Headers, body)
	warnings := []string{}
	if reason != "" {
		warnings = append(warnings, "native route: "+reason)
	}

	tried := map[string]bool{}
	fallbackAllowed := cfg.NativeFallback && !cfg.DesktopMode && channel == "" && resolved.ForceRoute != RouteBPS && cfg.RoutePolicy != config.PolicyBPSOnly
	var lastErr *Error
	for attempt := 0; attempt < 4; attempt++ {
		acc, err := r.Pool.Pick(ctx, pool.PickOptions{AccountID: accountID, SessionKey: sessionKey, Route: route, Model: resolved.Upstream, Exclude: tried})
		if err != nil && route == RouteBPS && fallbackAllowed && errors.Is(err, pool.ErrAllUnavailable) {
			// Every account may simply be denied for this model on Basispoints;
			// the same accounts can still serve it natively.
			if native, nerr := r.Pool.Pick(ctx, pool.PickOptions{AccountID: accountID, SessionKey: sessionKey, Route: RouteCodex, Model: resolved.Upstream, Exclude: tried}); nerr == nil {
				route = RouteCodex
				warnings = append(warnings, "fallback to native: no account can use Basispoints for this model right now")
				acc, err = native, nil
			}
		}
		if err != nil {
			if lastErr != nil {
				return nil, lastErr
			}
			if errors.Is(err, pool.ErrNoAccounts) {
				return nil, apiErr(http.StatusServiceUnavailable, "no_accounts", "没有可用账号：请先在面板或用 `gptbridge import` 导入 ChatGPT 登录凭据。 / No usable account; import a ChatGPT login first.")
			}
			return nil, apiErr(http.StatusServiceUnavailable, "no_available_account", err.Error())
		}
		tried[acc.ID] = true
		release, acquireErr := r.Pool.Acquire(ctx, acc.ID)
		if acquireErr != nil {
			return nil, apiErr(http.StatusServiceUnavailable, "account_busy", "账号已有 5 个并发请求，等待超时，请稍后重试")
		}
		var res *Result
		var outcome outcome
		if route == RouteBPS {
			res, outcome = r.executeBPS(ctx, cfg, acc, body, resolved, sessionKey, req)
		} else {
			res, outcome = r.executeCodex(ctx, cfg, acc, body, resolved, sessionKey, req)
		}
		if res != nil {
			res.Body = &leasedBody{ReadCloser: res.Body, release: release}
			res.Warnings = append(warnings, res.Warnings...)
			res.Reason = reason
			return res, nil
		}
		release()
		lastErr = outcome.err
		switch outcome.action {
		case actionNextAccount:
			continue
		case actionFallbackNative:
			if route == RouteBPS && fallbackAllowed {
				route = RouteCodex
				warnings = append(warnings, "fallback to native: "+outcome.reason)
				delete(tried, acc.ID) // the same account may serve natively
				continue
			}
			return nil, outcome.err
		default:
			return nil, outcome.err
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, apiErr(http.StatusServiceUnavailable, "exhausted", "all accounts failed")
}

// decide picks the initial route.
func (r *Router) decide(cfg *config.Config, resolved basispoints.Resolved, body object) (string, string) {
	switch resolved.ForceRoute {
	case RouteBPS:
		return RouteBPS, ""
	case RouteCodex:
		return RouteCodex, "model suffix"
	}
	switch cfg.RoutePolicy {
	case config.PolicyCodexOnly:
		return RouteCodex, "policy codex_only"
	case config.PolicyBPSOnly:
		return RouteBPS, ""
	}
	if !cfg.BPSModelAllowed(resolved.Upstream) {
		return RouteCodex, "model not in bps_models allowlist"
	}
	if reason := basispoints.NativeReason(body); reason != "" {
		return RouteCodex, reason
	}
	return RouteBPS, ""
}

type action int

const (
	actionFail action = iota
	actionNextAccount
	actionFallbackNative
)

type outcome struct {
	action action
	reason string
	err    *Error
}

func (r *Router) observe(rec Record) {
	if r.Observer != nil {
		r.Observer(rec)
	}
	if r.Config().LogRequests {
		msg := fmt.Sprintf("[%s] model=%s account=%s status=%d %.0fms", rec.Route, rec.Model, rec.Account, rec.Status, rec.Duration)
		if rec.Error != "" {
			msg += " error=" + rec.Error
		}
		log.Print(msg)
	}
}

func (r *Router) executeBPS(ctx context.Context, cfg *config.Config, acc *account.Account, body object, resolved basispoints.Resolved, sessionKey string, req *Request) (*Result, outcome) {
	if tier := str(body["service_tier"]); tier != "" && tier != "default" && tier != "auto" {
		return nil, outcome{action: actionFail, err: apiErr(400, "unsupported_service_tier", "BPS 暂不支持快速模式；请选择标准速度，或明确切换到原生通道")}
	}
	start := time.Now()
	working := cloneObject(body)
	removed := basispoints.StripHostedTools(working)
	raw, err := json.Marshal(working)
	if err != nil {
		return nil, outcome{err: apiErr(http.StatusBadRequest, "invalid_request", err.Error())}
	}
	cred := r.Pool.Credentials(acc)
	scope := acc.ID + "|" + acc.AccountID + "|" + sessionKey
	rec := Record{Time: start, Route: RouteBPS, Model: resolved.Upstream, Account: acc.Label(), Client: req.Client, RequestTurnStateLength: requestTurnStateLength(req.Headers)}

	// Inline images become gateway attachments.
	plan, err := basispoints.PlanImages(raw)
	if err != nil {
		rec.Error = err.Error()
		rec.Status = 400
		r.observe(rec)
		return nil, outcome{action: actionFallbackNative, reason: "image_input", err: apiErr(http.StatusBadRequest, "basispoints_request_invalid", basispoints.UserMessage(err))}
	}
	if plan.HasUploads() {
		raw, err = plan.Apply(ctx, r.Attachments, scope, func(ctx context.Context, att basispoints.Attachment) (string, error) {
			return r.BPS.UploadAttachment(ctx, cred, att)
		})
		if err != nil {
			rec.Error = err.Error()
			rec.Status = 502
			r.observe(rec)
			return nil, outcome{action: actionFallbackNative, reason: "image_upload", err: apiErr(http.StatusBadGateway, "basispoints_upload_failed", err.Error())}
		}
	}
	threshold := resolved.CompactThreshold()
	if cfg.BPSCompactThreshold > 0 {
		threshold = cfg.BPSCompactThreshold
	}
	prepared, err := basispoints.Prepare(raw, basispoints.Options{
		Scope:             scope,
		DefaultEffort:     cfg.DefaultEffort,
		CompactThreshold:  threshold,
		Replay:            r.Replay,
		ToolImages:        plan.ToolImages(),
		ParallelToolCalls: req.Client == "codex",
	})
	if prepared != nil {
		rec.Effort = prepared.Effort
	}
	if err != nil {
		rec.Error = err.Error()
		rec.Status = 400
		r.observe(rec)
		category := basispoints.Category(err)
		fallback := category != basispoints.CategoryToolHistory && category != basispoints.CategoryModel && category != basispoints.CategoryRequestJSON
		act := actionFail
		if fallback {
			act = actionFallbackNative
		}
		return nil, outcome{action: act, reason: category, err: apiErr(http.StatusBadRequest, "basispoints_request_invalid", basispoints.UserMessage(err))}
	}
	bps := *r.BPS
	bps.Transport = cfg.BPSTransport
	rec.Transport = cfg.BPSTransport
	resp, err := bps.Responses(ctx, cred, prepared.Body)
	if err != nil {
		rec.Error = err.Error()
		rec.Duration = ms(start)
		r.observe(rec)
		if ctx.Err() != nil {
			return nil, outcome{err: apiErr(499, "client_closed", "client closed request")}
		}
		r.Pool.ReportFailure(acc.ID, err.Error())
		return nil, outcome{action: actionFallbackNative, reason: "transport", err: apiErr(http.StatusBadGateway, "basispoints_transport_error", "无法连接 bps.openai.com（请检查代理设置）。 / Cannot reach bps.openai.com: "+err.Error())}
	}
	rec.Status = resp.StatusCode
	rec.Duration = ms(start)
	noteQualityHeaders(&rec, resp.Header)
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		he := &upstream.HTTPError{Status: resp.StatusCode, Body: string(bodyBytes), Upstream: "basispoints"}
		rec.Error = he.Error()
		r.observe(rec)
		return nil, r.classifyFailure(acc, RouteBPS, resp, he, resolved.Upstream)
	}
	// Inspect the first event: a failure before any output may fall back.
	peek, reader, perr := peekFirstEvent(resp.Body)
	if perr != nil {
		resp.Body.Close()
		rec.Error = perr.Error()
		rec.StreamStatus = "failed"
		rec.Duration = ms(start)
		r.observe(rec)
		return nil, outcome{action: actionFallbackNative, reason: "empty_stream", err: apiErr(http.StatusBadGateway, "basispoints_empty_stream", perr.Error())}
	}
	if code, message, failed := failedEvent(peek); failed {
		reader.Close()
		rec.Error = code + ": " + message
		rec.StreamStatus = "failed"
		rec.Duration = ms(start)
		r.observe(rec)
		// The HTTP/WS handshake succeeded, but generation failed before any
		// output. Preserve upstream status in the record, not in the API error.
		he := &upstream.HTTPError{Status: http.StatusBadGateway, Body: fmt.Sprintf(`{"error":{"code":%q,"message":%q}}`, code, message), Upstream: "basispoints"}
		return nil, r.classifyFailure(acc, RouteBPS, resp, he, resolved.Upstream)
	}
	r.observe(rec)
	stream := prepared.Bridge.Stream(reader)
	header := http.Header{}
	header.Set("X-GPTBridge-Upstream", "basispoints")
	header.Set("X-GPTBridge-BPS-Transport", cfg.BPSTransport)
	header.Set("X-GPTBridge-Reasoning-Effort", prepared.Effort)
	header.Set("X-GPTBridge-Account", account.MaskID(acc.AccountID))
	copyQualityHeaders(header, resp.Header)
	if len(removed) > 0 {
		header.Set("X-GPTBridge-Stripped-Tools", strings.Join(removed, ","))
	}
	res := &Result{Body: &accounting{ReadCloser: stream, done: r.finishObserved(acc.ID, rec)}, Route: RouteBPS, Account: acc.Label(), Model: resolved.Upstream, Effort: prepared.Effort, Warnings: prepared.Warnings, Header: header}
	return res, outcome{}
}

func (r *Router) executeCodex(ctx context.Context, cfg *config.Config, acc *account.Account, body object, resolved basispoints.Resolved, sessionKey string, req *Request) (*Result, outcome) {
	start := time.Now()
	working := cloneObject(body)
	if input, ok := working["input"].(string); ok {
		working["input"] = []any{object{"role": "user", "content": []any{object{"type": "input_text", "text": input}}}}
	}
	if cfg.StripUnknownFields {
		stripNativeUnsupported(working)
	}
	raw, err := json.Marshal(working)
	if err != nil {
		return nil, outcome{err: apiErr(http.StatusBadRequest, "invalid_request", err.Error())}
	}
	cred := r.Pool.Credentials(acc)
	rec := Record{Time: start, Route: RouteCodex, Model: resolved.Upstream, Account: acc.Label(), Client: req.Client, ServiceTier: str(working["service_tier"]), RequestTurnStateLength: requestTurnStateLength(req.Headers)}
	resp, err := r.Codex.Responses(ctx, cred, raw, req.Headers, upstream.SessionHeaders{SessionID: sessionKey})
	if err != nil {
		rec.Error = err.Error()
		rec.Duration = ms(start)
		r.observe(rec)
		if ctx.Err() != nil {
			return nil, outcome{err: apiErr(499, "client_closed", "client closed request")}
		}
		r.Pool.ReportFailure(acc.ID, err.Error())
		return nil, outcome{action: actionNextAccount, err: apiErr(http.StatusBadGateway, "codex_transport_error", "无法连接 chatgpt.com（请检查代理设置）。 / Cannot reach chatgpt.com: "+err.Error())}
	}
	rec.Status = resp.StatusCode
	rec.Duration = ms(start)
	noteQualityHeaders(&rec, resp.Header)
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		he := &upstream.HTTPError{Status: resp.StatusCode, Body: string(bodyBytes), Upstream: "codex"}
		rec.Error = he.Error()
		r.observe(rec)
		return nil, r.classifyFailure(acc, RouteCodex, resp, he, resolved.Upstream)
	}
	r.observe(rec)
	header := http.Header{}
	header.Set("X-GPTBridge-Upstream", "codex")
	header.Set("X-GPTBridge-Account", account.MaskID(acc.AccountID))
	copyQualityHeaders(header, resp.Header)
	effort := ""
	if reasoning, ok := working["reasoning"].(object); ok {
		effort = str(reasoning["effort"])
	}
	rec.Effort = effort
	res := &Result{Body: &accounting{ReadCloser: resp.Body, done: r.finishObserved(acc.ID, rec)}, Route: RouteCodex, Account: acc.Label(), Model: resolved.Upstream, Effort: effort, Header: header}
	return res, outcome{}
}

// classifyFailure maps an upstream error to the next action.
func (r *Router) classifyFailure(acc *account.Account, route string, resp *http.Response, he *upstream.HTTPError, model string) outcome {
	code := he.ErrorCode()
	message := he.Message()
	switch he.Status {
	case http.StatusUnauthorized:
		// Force a refresh; the next attempt uses a different account, a later
		// request will pick this one up again with fresh tokens.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			_, _ = r.Pool.Refresh(ctx, acc.ID, true)
		}()
		r.Pool.ReportFailure(acc.ID, "unauthorized: "+message)
		return outcome{action: actionNextAccount, err: apiErr(http.StatusUnauthorized, "upstream_unauthorized", message)}
	case http.StatusTooManyRequests:
		retry := pool.RetryAfter(resp.Header)
		if retry == 0 && strings.Contains(strings.ToLower(message), "usage_limit") {
			retry = 30 * time.Minute
		}
		r.Pool.ReportRateLimited(acc.ID, retry, code)
		return outcome{action: actionNextAccount, err: apiErr(http.StatusTooManyRequests, "upstream_rate_limited", message)}
	case http.StatusForbidden, http.StatusPaymentRequired:
		lower := strings.ToLower(code + " " + message)
		if route == RouteBPS && (strings.Contains(lower, "model_access") || strings.Contains(lower, "model access") || strings.Contains(lower, "not available") || strings.Contains(lower, "unsupported model")) {
			r.Pool.ReportBPSModelDenied(acc.ID, model)
			return outcome{action: actionFallbackNative, reason: "bps_model_denied", err: apiErr(http.StatusForbidden, "basispoints_model_denied", message)}
		}
		if strings.Contains(lower, "usage policy") || strings.Contains(lower, "access_restricted") || strings.Contains(lower, "access denied") {
			if route == RouteBPS {
				message = "BPS 通道被上游策略拒绝（403）；额度正常不代表此通道可用。可手动选择原生通道测试，或切换具有 BPS 权限的账号；不会自动换通道。原始信息：" + message
				r.Pool.ReportFailure(acc.ID, "bps_access_restricted: "+message)
				return outcome{action: actionFallbackNative, reason: "bps_access_restricted", err: apiErr(http.StatusForbidden, "basispoints_access_restricted", message)}
			}
			r.Pool.ReportRateLimited(acc.ID, 10*time.Minute, "forbidden")
			return outcome{action: actionNextAccount, err: apiErr(http.StatusForbidden, "upstream_forbidden", message)}
		}
		r.Pool.ReportFailure(acc.ID, message)
		return outcome{action: actionNextAccount, err: apiErr(http.StatusForbidden, "upstream_forbidden", message)}
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		r.Pool.ReportFailure(acc.ID, message)
		if route == RouteBPS {
			return outcome{action: actionFallbackNative, reason: "bps_rejected_request", err: apiErr(http.StatusBadRequest, "upstream_bad_request", message)}
		}
		return outcome{action: actionFail, err: apiErr(http.StatusBadRequest, "upstream_bad_request", message)}
	default:
		r.Pool.ReportFailure(acc.ID, message)
		if he.Status >= 500 {
			if route == RouteBPS {
				return outcome{action: actionFallbackNative, reason: fmt.Sprintf("bps_%d", he.Status), err: apiErr(http.StatusBadGateway, "upstream_error", message)}
			}
			return outcome{action: actionNextAccount, err: apiErr(http.StatusBadGateway, "upstream_error", message)}
		}
		return outcome{action: actionFail, err: apiErr(he.Status, "upstream_error", message)}
	}
}

// finish returns a callback recording the stream outcome.
func (r *Router) finishObserved(accountID string, rec Record) func(object, error) {
	finish := r.finish(accountID, rec.Route)
	return func(response object, err error) {
		finish(response, err)
		rec.Duration = ms(rec.Time)
		if response != nil {
			rec.ResponseModel = str(response["model"])
			rec.ResponseTier = str(response["service_tier"])
			rec.StreamStatus = "completed"
			if usage, ok := response["usage"].(object); ok {
				if rec.Route == RouteFenjue {
					if v, ok := usage["is_byok"].(bool); ok {
						rec.FenjueBYOK = &v
					}
					if v, ok := usage["cost"].(float64); ok {
						rec.FenjueCost = &v
					}
				}
				rec.InputTokens, rec.OutputTokens = toInt(usage["input_tokens"]), toInt(usage["output_tokens"])
				if details, ok := usage["input_tokens_details"].(object); ok {
					rec.CachedInputTokens = toInt(details["cached_tokens"])
				}
				if details, ok := usage["output_tokens_details"].(object); ok {
					rec.ReasoningTokens = toInt(details["reasoning_tokens"])
				}
			}
			applyQuality(&rec)
		}
		if err != nil {
			rec.StreamStatus = "failed"
			rec.Error = err.Error()
		}
		r.observe(rec)
	}
}

func (r *Router) finish(accountID, route string) func(response object, err error) {
	return func(response object, err error) {
		if response != nil {
			usage, _ := response["usage"].(object)
			var in, out int64
			if usage != nil {
				in, out = toInt(usage["input_tokens"]), toInt(usage["output_tokens"])
			}
			r.Pool.ReportSuccess(accountID, route, in, out)
			return
		}
		if err != nil {
			r.Pool.ReportFailure(accountID, err.Error())
		}
	}
}

// accounting wraps a stream to observe its terminal event.
type accounting struct {
	io.ReadCloser
	done        func(response object, err error)
	buf         bytes.Buffer
	finished    bool
	discardLine bool
	response    object
	terminalErr error
}

func (a *accounting) Read(p []byte) (int, error) {
	n, err := a.ReadCloser.Read(p)
	if n > 0 && !a.finished {
		a.inspect(p[:n])
	}
	if err != nil && !a.finished {
		a.finished = true
		a.report(err)
	}
	return n, err
}

func (a *accounting) Close() error {
	if !a.finished {
		a.finished = true
		a.report(nil)
	}
	return a.ReadCloser.Close()
}

func (a *accounting) report(readErr error) {
	if a.done == nil {
		return
	}
	if a.buf.Len() > 0 && !a.discardLine {
		a.inspectLine(a.buf.Bytes())
	}
	if a.response != nil {
		a.done(a.response, nil)
		return
	}
	if a.terminalErr != nil {
		a.done(nil, a.terminalErr)
		return
	}
	if readErr != nil && readErr != io.EOF {
		a.done(nil, readErr)
		return
	}
	a.done(nil, errors.New("upstream stream ended without response.completed"))
}

// Responses SSE frames contain one JSON data line. Keep only the unfinished
// line and terminal metadata; large preceding output must not hide completion.
func (a *accounting) inspect(data []byte) {
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		part := data
		if i >= 0 {
			part = data[:i]
		}
		if !a.discardLine {
			if a.buf.Len()+len(part) <= 16<<20 {
				a.buf.Write(part)
			} else {
				a.buf.Reset()
				a.discardLine = true
			}
		}
		if i < 0 {
			return
		}
		if !a.discardLine {
			a.inspectLine(a.buf.Bytes())
		}
		a.buf.Reset()
		a.discardLine = false
		data = data[i+1:]
	}
}

func (a *accounting) inspectLine(line []byte) {
	line = bytes.TrimSpace(line)
	if !bytes.HasPrefix(line, []byte("data:")) {
		return
	}
	var event object
	if json.Unmarshal(bytes.TrimSpace(line[5:]), &event) != nil {
		return
	}
	switch str(event["type"]) {
	case "response.completed":
		if response, ok := event["response"].(object); ok {
			a.response = object{"model": response["model"], "service_tier": response["service_tier"], "usage": response["usage"]}
		}
	case "response.failed", "response.incomplete", "error":
		a.terminalErr = errors.New("upstream stream failed or was incomplete")
	}
}

// peekFirstEvent reads the first SSE event without consuming the stream.
func peekFirstEvent(body io.ReadCloser) ([]byte, io.ReadCloser, error) {
	reader := bufio.NewReaderSize(body, 64<<10)
	var buf bytes.Buffer
	for buf.Len() < 256<<10 {
		line, err := reader.ReadBytes('\n')
		buf.Write(line)
		if err != nil {
			if err == io.EOF && buf.Len() > 0 {
				break
			}
			return nil, nil, fmt.Errorf("upstream stream ended before the first event: %w", err)
		}
		if bytes.Equal(bytes.TrimRight(line, "\r\n"), []byte("")) && buf.Len() > 2 {
			break
		}
	}
	return buf.Bytes(), &replayReader{Reader: io.MultiReader(bytes.NewReader(buf.Bytes()), reader), closer: body}, nil
}

type replayReader struct {
	io.Reader
	closer io.Closer
}

func (r *replayReader) Close() error { return r.closer.Close() }

// failedEvent reports whether the first event is a terminal failure.
func failedEvent(block []byte) (code, message string, failed bool) {
	for _, line := range bytes.Split(block, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		var payload object
		if json.Unmarshal(bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:"))), &payload) != nil {
			continue
		}
		kind := str(payload["type"])
		var errObj object
		switch kind {
		case "error":
			errObj, _ = payload["error"].(object)
			if errObj == nil {
				errObj = object{"code": str(payload["code"]), "message": str(payload["message"])}
			}
		case "response.failed":
			if resp, ok := payload["response"].(object); ok {
				errObj, _ = resp["error"].(object)
			}
			if errObj == nil {
				errObj = object{"message": "response failed"}
			}
		default:
			return "", "", false
		}
		return str(errObj["code"]), str(errObj["message"]), true
	}
	return "", "", false
}

// stripNativeUnsupported removes fields the ChatGPT backend rejects.
func stripNativeUnsupported(body object) {
	for _, key := range []string{"temperature", "top_p", "max_output_tokens", "max_tokens", "user", "metadata", "seed", "frequency_penalty", "presence_penalty", "logprobs", "top_logprobs", "n", "stop", "truncation", "background", "safety_identifier", "prompt_cache_retention", "max_tool_calls"} {
		delete(body, key)
	}
	if _, ok := body["instructions"]; !ok {
		body["instructions"] = "You are a helpful assistant."
	}
	if reasoning, ok := body["reasoning"].(object); ok {
		if _, ok := reasoning["summary"]; !ok {
			reasoning["summary"] = "auto"
		}
	}
	if _, ok := body["include"]; !ok {
		body["include"] = []any{"reasoning.encrypted_content"}
	}
}

// SessionKey derives a stable conversation identifier from headers/body.
func SessionKey(h http.Header, body object) string {
	if h != nil {
		for _, name := range []string{"session_id", "session-id", "x-session-id", "conversation_id", "thread-id", "x-conversation-id"} {
			if v := strings.TrimSpace(h.Get(name)); v != "" {
				return v
			}
		}
	}
	if key := strings.TrimSpace(str(body["prompt_cache_key"])); key != "" {
		return key
	}
	if input, ok := body["input"].([]any); ok && len(input) > 0 {
		raw, _ := json.Marshal([]any{body["instructions"], input[0]})
		sum := sha256.Sum256(raw)
		return "conv-" + hex.EncodeToString(sum[:8])
	}
	return ""
}

func cloneObject(src object) object {
	dst := make(object, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func toInt(v any) int64 {
	switch n := v.(type) {
	case json.Number:
		i, _ := n.Int64()
		return i
	case float64:
		return int64(n)
	}
	return 0
}

func ms(start time.Time) float64 {
	return float64(time.Since(start).Microseconds()) / 1000
}
