package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/xxx-holic/wishtoken-desktop/internal/account"
	"github.com/xxx-holic/wishtoken-desktop/internal/api"
	"github.com/xxx-holic/wishtoken-desktop/internal/basispoints"
	"github.com/xxx-holic/wishtoken-desktop/internal/config"
	"github.com/xxx-holic/wishtoken-desktop/internal/router"
	"github.com/xxx-holic/wishtoken-desktop/internal/upstream"
)

type bpsNativeJob struct {
	AccountID     string    `json:"account_id"`
	BPSAccountID  string    `json:"bps_account_id"`
	State         string    `json:"state"`
	Binding       string    `json:"binding"`
	Run           string    `json:"run"`
	Requested     int       `json:"requested"`
	Plan          []string  `json:"plan"`
	Completed     int       `json:"completed"`
	Current       string    `json:"current_model,omitempty"`
	Error         string    `json:"error,omitempty"`
	ErrorCode     string    `json:"error_code,omitempty"`
	RetryAt       time.Time `json:"retry_at,omitempty"`
	Updated       time.Time `json:"updated_at"`
	NativeRefresh string    `json:"native_refresh,omitempty"`
}

func (s *Server) bpsNativeMaintenancePaused(id string) bool {
	s.bpsNativeMu.Lock()
	defer s.bpsNativeMu.Unlock()
	if s.bpsNativeActive == "" {
		return false
	}
	if id == s.bpsNativeActive {
		return true
	}
	job, err := s.bpsNativeRead(s.bpsNativeActive)
	return err == nil && id == job.BPSAccountID
}

func (s *Server) bpsNativePath(id string) string {
	home := config.Home()
	if s.cfgPath != "" {
		home = filepath.Dir(s.cfgPath)
	}
	return filepath.Join(home, "bps-native", id+".json")
}

func (s *Server) bpsNativeRead(id string) (*bpsNativeJob, error) {
	if _, ok := s.Store.Get(id); !ok {
		return nil, fmt.Errorf("请选择原生授权记录")
	}
	raw, err := os.ReadFile(s.bpsNativePath(id))
	if os.IsNotExist(err) {
		return &bpsNativeJob{AccountID: id, State: "idle"}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("无法读取预热进度")
	}
	var job bpsNativeJob
	if json.Unmarshal(raw, &job) != nil || job.AccountID != id || job.Completed < 0 || job.Completed > len(job.Plan) {
		return nil, fmt.Errorf("预热进度无效，请检查本地记录")
	}
	if (job.State == "running" || job.State == "catalog" || job.State == "refreshing") && s.bpsNativeActive != id {
		job.State = "interrupted"
	}
	return &job, nil
}

func (s *Server) bpsNativeSave(job *bpsNativeJob) error {
	job.Updated = time.Now().UTC()
	file := s.bpsNativePath(job.AccountID)
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(job, "", "  ")
	if err := os.WriteFile(file+".tmp", raw, 0600); err != nil {
		return err
	}
	return os.Rename(file+".tmp", file)
}

func (s *Server) bpsNativePair(nativeID, bpsID string) (account.Account, account.Account, string, error) {
	n, nok := s.Store.Get(nativeID)
	b, bok := s.Store.Get(bpsID)
	if !nok || !bok || n.Provider == "fenjue" || b.Provider == "fenjue" || n.Disabled || b.Disabled || !n.HasCredentials() || !b.HasCredentials() || n.OAuthClientID != account.CodexOAuthClientID || b.OAuthClientID != account.BPSOAuthClientID {
		return n, b, "", fmt.Errorf("需要有效的 Codex 和 BPS 两套独立授权")
	}
	if n.AccountID == "" || n.UserID == "" || n.Email == "" || n.AccountID != b.AccountID || n.UserID != b.UserID || !strings.EqualFold(n.Email, b.Email) {
		return n, b, "", fmt.Errorf("两套授权必须属于同一邮箱、用户和工作区")
	}
	if strings.EqualFold(n.PlanType, "free") || strings.EqualFold(b.PlanType, "free") {
		return n, b, "", fmt.Errorf("Free 账号不使用此 BPS 预热通道")
	}
	proxy := func(a account.Account) string {
		if a.ProxyURL != "" {
			return a.ProxyURL
		}
		return s.Config().ProxyURL
	}
	if proxy(n) != proxy(b) {
		return n, b, "", fmt.Errorf("两套授权必须绑定相同代理；未发出预热请求")
	}
	sum := sha256.Sum256([]byte(n.ID + "\n" + b.ID + "\n" + n.AccountID + "\n" + n.UserID + "\n" + strings.ToLower(n.Email) + "\n" + proxy(n)))
	return n, b, hex.EncodeToString(sum[:]), nil
}

func (s *Server) bpsNativeReady(id string) error {
	if !s.Config().DesktopMode {
		return fmt.Errorf("此通道仅供本地桌面客户端使用")
	}
	s.bpsNativeMu.Lock()
	defer s.bpsNativeMu.Unlock()
	job, err := s.bpsNativeRead(id)
	if err != nil {
		return err
	}
	if job.State != "ready" || len(job.Plan) == 0 || job.Completed != len(job.Plan) {
		return fmt.Errorf("请先完成 BPS → 原生预热；未发送原生请求")
	}
	_, _, binding, err := s.bpsNativePair(id, job.BPSAccountID)
	if err != nil {
		return err
	}
	if binding != job.Binding {
		return fmt.Errorf("账号身份或代理已改变，请重新预热")
	}
	return nil
}

func (s *Server) adminBPSNative(w http.ResponseWriter, r *http.Request, path string) {
	if !s.Config().DesktopMode {
		writeError(w, r, 403, "desktop_required", "invalid_request_error", "此通道仅供桌面客户端使用")
		return
	}
	s.bpsNativeMu.Lock()
	defer s.bpsNativeMu.Unlock()
	if path == "/bps-native/status" && r.Method == http.MethodGet {
		id := r.URL.Query().Get("account_id")
		job, err := s.bpsNativeRead(id)
		if err != nil {
			writeError(w, r, 400, "primer_status", "invalid_request_error", err.Error())
			return
		}
		candidates := []string{}
		natives := []string{}
		for _, a := range s.Store.List() {
			if _, _, _, err := s.bpsNativePair(id, a.ID); err == nil {
				candidates = append(candidates, a.ID)
			}
			if _, _, _, err := s.bpsNativePair(a.ID, id); err == nil {
				natives = append(natives, a.ID)
			}
		}
		if job.State == "ready" {
			_, _, binding, pairErr := s.bpsNativePair(id, job.BPSAccountID)
			if pairErr != nil || binding != job.Binding {
				job.State = "stale"
				job.Error = "账号身份或代理已改变，请重新预热"
			}
		}
		api.WriteJSON(w, 200, object{"job": job, "bps_candidates": candidates, "native_candidates": natives, "active_account_id": s.bpsNativeActive})
		return
	}
	if r.Method != http.MethodPost || (path != "/bps-native/start" && path != "/bps-native/cancel") {
		http.NotFound(w, r)
		return
	}
	var req struct {
		AccountID    string `json:"account_id"`
		BPSAccountID string `json:"bps_account_id"`
		Total        int    `json:"total_requests"`
		Restart      bool   `json:"restart"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req) != nil {
		writeError(w, r, 400, "invalid_json", "invalid_request_error", "预热参数无效")
		return
	}
	if path == "/bps-native/cancel" {
		if s.bpsNativeActive == req.AccountID && s.bpsNativeCancel != nil {
			s.bpsNativeCancel()
		}
		api.WriteJSON(w, 200, object{"ok": true})
		return
	}
	if s.bpsNativeActive != "" {
		writeError(w, r, 409, "primer_busy", "invalid_request_error", "已有预热任务，请等待完成或停止")
		return
	}
	if req.Total == 0 {
		req.Total = 40
	}
	if req.Total < 1 || req.Total > 100 {
		writeError(w, r, 400, "invalid_count", "invalid_request_error", "预热总数须为 1–100")
		return
	}
	_, _, binding, err := s.bpsNativePair(req.AccountID, req.BPSAccountID)
	if err != nil {
		writeError(w, r, 400, "primer_pair", "invalid_request_error", err.Error())
		return
	}
	job, err := s.bpsNativeRead(req.AccountID)
	if err != nil {
		writeError(w, r, 400, "primer_state", "invalid_request_error", err.Error())
		return
	}
	if time.Now().Before(job.RetryAt) {
		writeError(w, r, 429, "primer_cooldown", "rate_limit_error", "上游限流等待中，请在页面显示的时间后继续")
		return
	}
	if !req.Restart && job.State != "idle" {
		if job.Binding != binding || job.BPSAccountID != req.BPSAccountID || job.Requested != req.Total {
			writeError(w, r, 409, "primer_changed", "invalid_request_error", "授权、代理或总数已改变，请选择重新预热")
			return
		}
		if time.Now().Before(job.RetryAt) {
			writeError(w, r, 429, "primer_cooldown", "rate_limit_error", "上游限流等待中，请在页面显示的时间后继续")
			return
		}
		if job.State == "ready" {
			api.WriteJSON(w, 200, job)
			return
		}
	} else {
		buf := make([]byte, 16)
		if _, err := rand.Read(buf); err != nil {
			writeError(w, r, 500, "random_failed", "server_error", "无法创建任务")
			return
		}
		job = &bpsNativeJob{AccountID: req.AccountID, BPSAccountID: req.BPSAccountID, Binding: binding, Requested: req.Total, Run: hex.EncodeToString(buf)}
	}
	job.State = "catalog"
	job.Error = ""
	job.ErrorCode = ""
	job.RetryAt = time.Time{}
	if err := s.bpsNativeSave(job); err != nil {
		writeError(w, r, 500, "save_failed", "server_error", "无法保存预热进度")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	s.bpsNativeActive = req.AccountID
	s.bpsNativeCancel = cancel
	copy := *job
	go s.runBPSNative(ctx, cancel, &copy)
	api.WriteJSON(w, 200, job)
}

func (s *Server) runBPSNative(ctx context.Context, cancel context.CancelFunc, job *bpsNativeJob) {
	defer cancel()
	var failure error
	save := func() error { s.bpsNativeMu.Lock(); defer s.bpsNativeMu.Unlock(); return s.bpsNativeSave(job) }
	defer func() {
		if ctx.Err() != nil {
			job.State = "cancelled"
			job.Error = "预热已停止；已完成的请求保留，可继续"
		} else if failure != nil {
			job.State = "failed"
			job.Error = failure.Error()
		}
		s.bpsNativeMu.Lock()
		defer s.bpsNativeMu.Unlock()
		if err := s.bpsNativeSave(job); err != nil { /* A missing ready receipt keeps native gated. */
		}
		s.bpsNativeActive = ""
		s.bpsNativeCancel = nil
	}()
	failure = s.bpsNativeRun(ctx, job, save)
}

func (s *Server) bpsNativeRun(ctx context.Context, job *bpsNativeJob, save func() error) error {
	check := func() (account.Account, account.Account, error) {
		n, b, binding, err := s.bpsNativePair(job.AccountID, job.BPSAccountID)
		if err == nil && binding != job.Binding {
			err = fmt.Errorf("账号身份或代理已改变，预热已停止")
		}
		return n, b, err
	}
	if _, _, err := check(); err != nil {
		return err
	}
	if _, err := s.Pool.Ensure(ctx, job.BPSAccountID); err != nil {
		return fmt.Errorf("BPS 凭据未就绪，请重新授权后继续")
	}
	_, b, err := check()
	if err != nil {
		return err
	}
	cred := upstream.Credentials{AccessToken: b.AccessToken, AccountID: b.AccountID, ProxyURL: b.ProxyURL}
	if cred.ProxyURL == "" {
		cred.ProxyURL = s.Config().ProxyURL
	}
	client := *s.Router.BPS
	client.Transport = "http"
	resp, err := client.ModelAccess(ctx, cred)
	if err != nil {
		return fmt.Errorf("BPS 模型列表网络请求失败")
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return primerHTTPError(resp, job)
	}
	if readErr != nil {
		return fmt.Errorf("BPS 模型列表读取失败")
	}
	models, err := basispoints.PrimerModels(raw)
	if err != nil {
		return err
	}
	plan := basispoints.PrimerPlan(models, job.Requested)
	if len(job.Plan) > 0 {
		if strings.Join(job.Plan, "\n") != strings.Join(plan, "\n") {
			return fmt.Errorf("BPS 模型列表发生变化，请重新预热")
		}
	} else {
		job.Plan = plan
	}
	job.State = "running"
	if err := save(); err != nil {
		return fmt.Errorf("无法保存模型计划，已停止")
	}
	for job.Completed < len(job.Plan) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, _, err := check(); err != nil {
			return err
		}
		job.Current = job.Plan[job.Completed]
		if err := save(); err != nil {
			return fmt.Errorf("无法保存进度，已停止")
		}
		requestCtx, requestCancel := context.WithTimeout(ctx, 120*time.Second)
		started := time.Now()
		resp, err := client.Responses(requestCtx, cred, basispoints.PrimerBody(job.Current, "wishtoken-bps-native/"+job.Run+"/"+b.AccountID, job.Completed+1))
		if err != nil {
			requestCancel()
			return fmt.Errorf("BPS 请求网络失败，已停止；未切换原生")
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			requestCancel()
			return primerHTTPError(resp, job)
		}
		limited := &io.LimitedReader{R: resp.Body, N: 4 << 20}
		collected, collectErr := api.Collect(limited)
		resp.Body.Close()
		requestCancel()
		if code, detail := primerResponseFailure(collected, collectErr, limited.N <= 0); code != "" {
			job.ErrorCode = code
			if code == "rate_limit_exceeded" {
				job.RetryAt = primerRetryAt(resp.Header)
			}
			s.logs.add(router.Record{Time: time.Now().UTC(), Route: "bps", Account: b.Label(), Model: job.Current, Effort: "low", Status: resp.StatusCode, Duration: float64(time.Since(started).Milliseconds()), Client: "bps-native-primer", StreamStatus: "failed", Error: code, Transport: "http"})
			return fmt.Errorf("%s：%s；已完成 %d 条保留，可继续当前条（%s）", job.Current, detail, job.Completed, code)
		}
		job.ErrorCode = ""
		in, out, reasoning, cached := api.Usage(collected.Response)
		s.logs.add(router.Record{Time: time.Now().UTC(), Route: "bps", Account: b.Label(), Model: job.Current, Effort: "low", Status: 200, Duration: float64(time.Since(started).Milliseconds()), Client: "bps-native-primer", StreamStatus: "completed", InputTokens: in, OutputTokens: out, ReasoningTokens: reasoning, CachedInputTokens: cached, Transport: "http"})
		job.Completed++
		if err := save(); err != nil {
			return fmt.Errorf("响应已完成但进度保存失败，请检查存储")
		}
		if job.Completed < len(job.Plan) {
			delay := 2 * time.Second
			if jitter, err := rand.Int(rand.Reader, big.NewInt(3001)); err == nil {
				delay += time.Duration(jitter.Int64()) * time.Millisecond
			}
			if s.bpsNativeDelay != nil {
				delay = s.bpsNativeDelay()
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	job.State = "refreshing"
	job.Current = ""
	if err := save(); err != nil {
		return fmt.Errorf("无法保存预热结果")
	}
	n, _, err := check()
	if err != nil {
		return err
	}
	if n.RefreshToken != "" {
		if _, err := s.Pool.Refresh(ctx, n.ID, true); err != nil {
			return fmt.Errorf("BPS 已完成；原生续期失败，未启用原生通道")
		}
		job.NativeRefresh = "completed"
	} else if !n.Usable() {
		return fmt.Errorf("BPS 已完成；原生凭据不可用，请补充授权")
	} else {
		job.NativeRefresh = "not_needed_no_refresh_token"
	}
	if _, _, err := check(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	job.State = "ready"
	return nil
}

func primerHTTPError(resp *http.Response, job *bpsNativeJob) error {
	job.ErrorCode = fmt.Sprintf("http_%d", resp.StatusCode)
	if resp.StatusCode == 429 {
		job.RetryAt = primerRetryAt(resp.Header)
		return fmt.Errorf("BPS 限流，已暂停；等待后可再尝试，是否解除以上游为准")
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return fmt.Errorf("BPS 授权或访问受限（HTTP %d），请检查授权；未切换原生", resp.StatusCode)
	}
	return fmt.Errorf("BPS 返回 HTTP %d，已停止；未切换原生", resp.StatusCode)
}

func primerRetryAt(header http.Header) time.Time {
	// Match HTTP and SSE rate limits. This is a minimum local backoff when
	// the server does not supply Retry-After, not a promised quota reset.
	delay := 90 * time.Second
	if seconds, err := strconv.ParseInt(header.Get("Retry-After"), 10, 64); err == nil && seconds > 0 && seconds <= int64((1<<63-1)/time.Second) {
		delay = time.Duration(seconds) * time.Second
	} else if date, err := http.ParseTime(header.Get("Retry-After")); err == nil && time.Until(date) > 0 {
		delay = time.Until(date)
	}
	return time.Now().Add(delay).UTC()
}

// Only stable categories leave the response boundary; arbitrary upstream text
// can contain echoed credentials and must never enter the desktop job or logs.
func primerResponseFailure(c *api.Collected, readErr error, tooLarge bool) (string, string) {
	if tooLarge {
		return "response_too_large", "响应超过读取上限"
	}
	if readErr != nil {
		return "stream_interrupted", "响应流读取中断"
	}
	if c == nil {
		return "missing_terminal", "未收到完整结束事件"
	}
	if c.Failed != nil {
		code, _ := c.Failed["code"].(string)
		switch code {
		case "rate_limit_exceeded":
			return code, "上游限流，已暂停；等待后可再尝试，是否解除以上游为准"
		case "server_error", "model_not_found", "invalid_request_error", "insufficient_quota", "usage_limit_reached", "request_timeout", "model_overloaded":
			return code, "上游返回失败事件"
		default:
			return "upstream_failed", "上游返回失败事件"
		}
	}
	if c.Response["status"] == "incomplete" {
		return "response_incomplete", "上游未完整生成响应"
	}
	if c.Response["status"] != "completed" {
		return "missing_terminal", "未收到成功结束事件"
	}
	if strings.TrimSpace(api.OutputText(c.Response)) == "" {
		return "empty_output", "响应结束但未返回文本"
	}
	return "", ""
}
