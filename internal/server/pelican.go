package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/xxx-holic/wishtoken-desktop/internal/api"
	"github.com/xxx-holic/wishtoken-desktop/internal/localcodex"
	"github.com/xxx-holic/wishtoken-desktop/internal/router"
)

func (s *Server) adminSelectAccount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccountID string `json:"account_id"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		writeError(w, r, 400, "invalid_json", "invalid_request_error", "切换参数无效")
		return
	}
	a, ok := s.Store.Get(req.AccountID)
	if !ok || a.Disabled || !a.HasCredentials() || (a.Expired() && a.RefreshToken == "") {
		writeError(w, r, 400, "account_unavailable", "invalid_request_error", "请选择有效且启用的账号")
		return
	}
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	next := *s.cfg
	next.ActiveAccountID = req.AccountID
	if s.cfgPath != "" {
		if err := next.Save(s.cfgPath); err != nil {
			writeError(w, r, 500, "save_failed", "server_error", "保存切换失败")
			return
		}
	}
	s.cfg = &next
	api.WriteJSON(w, 200, object{"account_id": req.AccountID})
}

func (s *Server) adminPelican(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccountID string `json:"account_id"`
		Model     string `json:"model"`
		Effort    string `json:"effort"`
		Prompt    string `json:"prompt"`
		Channel   string `json:"channel"`
		Benchmark string `json:"benchmark"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 65537)).Decode(&req) != nil || len(req.Prompt) > 32000 || strings.TrimSpace(req.Prompt) == "" {
		writeError(w, r, 400, "invalid_request", "invalid_request_error", "测试提示词为空或过长")
		return
	}
	if _, ok := s.Store.Get(req.AccountID); !ok || req.AccountID == "" {
		writeError(w, r, 400, "account_required", "invalid_request_error", "请选择测试账号")
		return
	}
	channel, channelErr := localcodex.Channel(req.Channel)
	if channelErr != nil {
		writeError(w, r, 400, "invalid_channel", "invalid_request_error", channelErr.Error())
		return
	}
	if (channel == "bps" && !s.Config().BPSModelAllowed(req.Model)) || ((channel == "codex" || channel == "bps_native") && !localcodex.NativeModelAllowed(req.Model)) || (channel == "work" && !localcodex.WorkModelAllowed(req.Model)) {
		writeError(w, r, 400, "model_unavailable", "invalid_request_error", "模型不在所选通道列表中")
		return
	}
	switch req.Effort {
	case "low", "medium", "high", "xhigh":
	case "max":
		if channel != "work" {
			writeError(w, r, 400, "invalid_effort", "invalid_request_error", "仅 Work 通道支持 max 推理档位")
			return
		}
	default:
		writeError(w, r, 400, "invalid_effort", "invalid_request_error", "推理档位无效")
		return
	}
	if req.Benchmark != "" && req.Benchmark != "pelican" && req.Benchmark != "candy" {
		writeError(w, r, 400, "invalid_benchmark", "invalid_request_error", "测试题类型无效")
		return
	}
	// The benchmark is sent verbatim, without an HTML instruction or answer hint.
	body := object{"model": req.Model, "input": req.Prompt, "reasoning": object{"effort": req.Effort}}
	raw, _ := json.Marshal(body)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	start := time.Now()
	headers := http.Header{}
	headers.Set("X-GPTBridge-Account", req.AccountID)
	headers.Set("X-GPTBridge-Channel", channel)
	res, rerr := s.Router.Responses(ctx, &router.Request{Raw: raw, Body: body, Headers: headers, Client: "pelican"})
	if rerr != nil {
		writeError(w, r, rerr.Status, rerr.Code, "upstream_error", rerr.Message)
		return
	}
	defer res.Body.Close()
	limited := &io.LimitedReader{R: res.Body, N: 16*1024*1024 + 1}
	collected, err := api.Collect(limited)
	if err != nil || collected.Failed != nil || collected.Response["status"] != "completed" || limited.N <= 0 {
		writeError(w, r, 502, "generation_incomplete", "upstream_error", "生成未完整结束，请重试；不将中断结果记为成功")
		return
	}
	text := api.OutputText(collected.Response)
	if strings.TrimSpace(text) == "" {
		writeError(w, r, 502, "empty_output", "upstream_error", "模型没有返回文本内容")
		return
	}
	in, out, reasoning, cached := api.Usage(collected.Response)
	var usage any
	if collected.Response["usage"] != nil {
		usage = object{"input_tokens": in, "output_tokens": out, "reasoning_tokens": reasoning, "cached_tokens": cached}
		if channel == "fenjue" {
			if raw, ok := collected.Response["usage"].(object); ok {
				usage.(object)["is_byok"] = raw["is_byok"]
				usage.(object)["cost"] = raw["cost"]
			}
		}
	}
	api.WriteJSON(w, 200, object{"text": text, "model": res.Model, "effort": res.Effort, "route": res.Route, "transport": res.Header.Get("X-GPTBridge-BPS-Transport"), "response_id": collected.Response["id"], "response_model": collected.Response["model"], "duration_ms": time.Since(start).Milliseconds(), "usage": usage})
}
