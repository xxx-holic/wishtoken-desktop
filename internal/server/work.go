package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/xxx-holic/wishtoken-desktop/internal/router"
)

func workBridgeID() string {
	key := os.Getenv("GPTBRIDGE_WORK_KEY")
	if key == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(key))
	return hex.EncodeToString(digest[:])
}

// Browser credentials remain in the browser. Only the expected identity and
// request are sent to the private, loopback Electron worker, once, without retry.
func (s *Server) workResponses(ctx context.Context, req *router.Request, accountID string) (*router.Result, *router.Error) {
	fail := func(status int, code, message string) (*router.Result, *router.Error) {
		return nil, &router.Error{Status: status, Code: code, Type: "work_error", Message: message}
	}
	a, ok := s.Store.Get(accountID)
	if !ok || a.Disabled || a.Email == "" {
		return fail(400, "work_account", "请选择已启用且有邮箱的账号，并绑定其 Work 浏览器")
	}
	u, err := url.Parse(os.Getenv("GPTBRIDGE_WORK_URL"))
	key := os.Getenv("GPTBRIDGE_WORK_KEY")
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || key == "" {
		return fail(503, "work_unavailable", "请使用新版 WishToken Desktop 启动 Work 通道")
	}
	body := map[string]any{}
	for k, v := range req.Body {
		body[k] = v
	}
	// Work stores web conversations. Do not claim API store=false guarantees it.
	body["stream"] = true
	raw, err := json.Marshal(object{"account_id": a.ID, "email": a.Email, "workspace": a.AccountID, "request": body})
	if err != nil || len(raw) > 24*1024*1024 {
		return fail(400, "work_size", "Work 请求体超过 24 MiB")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String()+"/responses", bytes.NewReader(raw))
	if err != nil {
		return fail(500, "work_request", "无法创建 Work 请求")
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, Timeout: 330 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return fail(502, "work_connection", "Work 浏览器桥未连接或请求中断；未重试")
	}
	if response.StatusCode != 200 {
		defer response.Body.Close()
		var payload struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&payload)
		if payload.Error.Message == "" {
			payload.Error.Message = "Work 桥拒绝了请求"
		}
		return fail(response.StatusCode, "work_rejected", payload.Error.Message)
	}
	if response.Header.Get("Content-Type") != "text/event-stream" {
		response.Body.Close()
		return fail(502, "work_protocol", "Work 桥未返回 Responses 事件流")
	}
	model := "gpt-6-astra-wm"
	if value, ok := req.Body["model"].(string); ok && value != "" {
		model = value
	}
	effort := "medium"
	if reasoning, ok := req.Body["reasoning"].(map[string]any); ok {
		if value, ok := reasoning["effort"].(string); ok && value != "" {
			effort = value
		}
	}
	if value, ok := req.Body["reasoning_effort"].(string); ok && value != "" {
		effort = value
	}
	return &router.Result{Body: response.Body, Route: "work", Account: a.ID, Model: model, Effort: effort, Header: response.Header}, nil
}
