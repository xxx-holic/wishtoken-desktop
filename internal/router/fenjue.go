package router

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/xxx-holic/wishtoken-desktop/internal/channelname"
	"github.com/xxx-holic/wishtoken-desktop/internal/fenjue"
	"github.com/xxx-holic/wishtoken-desktop/internal/pool"
)

func (r *Router) executeFenjue(ctx context.Context, req *Request, accountID, requested string) (*Result, *Error) {
	if r.FenjueAccess == nil {
		return nil, apiErr(403, "feature_locked", "焚诀通道需要有效功能授权码")
	}
	if err := r.FenjueAccess(); err != nil {
		return nil, apiErr(403, "feature_locked", err.Error())
	}
	channel := channelname.Normalize(req.Headers.Get("X-GPTBridge-Channel"))
	if channel != "" && channel != RouteFenjue {
		return nil, apiErr(400, "channel_conflict", "Fenjue model conflicts with the pinned channel")
	}
	if r.Fenjue == nil {
		return nil, apiErr(503, "fenjue_unavailable", "Fenjue transport is not configured")
	}
	if accountID == "" {
		return nil, apiErr(400, "account_required", "Select an independent Fenjue account")
	}
	model := strings.TrimPrefix(strings.TrimSuffix(requested, "-fenjue"), "openai/")
	if !fenjue.ModelAllowed(model) {
		return nil, apiErr(400, "fenjue_model", "Model is not in the explicit Fenjue candidate list")
	}
	body := cloneObject(req.Body)
	if body["store"] == true || body["background"] == true || body["conversation"] != nil {
		return nil, apiErr(400, "fenjue_stateless", "Fenjue subscription requests must be stateless: store=false, no conversation or background mode")
	}
	if tier := str(body["service_tier"]); tier != "" && tier != "auto" && tier != "default" {
		return nil, apiErr(400, "fenjue_tier", "Fenjue subscription requires standard speed")
	}
	effort := r.Config().DefaultEffort
	if reasoning, ok := body["reasoning"].(object); ok && str(reasoning["effort"]) != "" {
		effort = str(reasoning["effort"])
	}
	if effort == "" {
		effort = "medium"
	}
	if effort != "low" && effort != "medium" && effort != "high" && effort != "xhigh" {
		return nil, apiErr(400, "fenjue_effort", "Use low, medium, high or xhigh for this Fenjue integration")
	}
	if reasoning, ok := body["reasoning"].(object); ok {
		copy := cloneObject(reasoning)
		copy["effort"] = effort
		body["reasoning"] = copy
	} else {
		body["reasoning"] = object{"effort": effort}
	}
	// No system-prompt insertion, tool replacement or effort substitution.
	body["model"] = "openai/" + model
	body["stream"] = true
	body["store"] = false
	session := fenjue.ConversationKey(req.Headers, body)
	if session == "" {
		session = rand.Text()
	}
	// Reject unsupported server-side history before selecting an account or sending auth.
	prepared, err := fenjue.PrepareRequest(body, "")
	if err != nil {
		return nil, apiErr(400, "full_history_required", "Fenjue requires complete input history; previous_response_id and item_reference are unavailable")
	}
	acc, err := r.Pool.Pick(ctx, pool.PickOptions{AccountID: accountID, Route: RouteFenjue, Model: model})
	if err != nil {
		return nil, apiErr(409, "fenjue_account_required", "Select a ready Fenjue credential; ChatGPT/BPS grants cannot authenticate to Fenjue")
	}
	if _, exists := prepared["prompt_cache_key"]; !exists && session != "" {
		prepared["prompt_cache_key"] = fenjue.SessionID(acc.AccountID, session)
	}
	raw, err := json.Marshal(prepared)
	if err != nil {
		return nil, apiErr(400, "invalid_json", "Invalid Fenjue request")
	}
	release, err := r.Pool.Acquire(ctx, acc.ID)
	if err != nil {
		return nil, apiErr(503, "account_busy", "Fenjue account is busy")
	}
	rec := Record{Time: time.Now(), Route: RouteFenjue, Model: model, Effort: effort, Account: acc.Label(), Client: req.Client, Transport: "http"}
	fail := func(status int, code, message string) (*Result, *Error) {
		release()
		rec.Status = status
		rec.Error = message
		rec.StreamStatus = "failed"
		rec.Duration = ms(rec.Time)
		r.observe(rec)
		r.Pool.ReportFailure(acc.ID, message)
		return nil, apiErr(status, code, message)
	}
	cred := fenjue.Credential{Token: acc.AccessToken, AccountID: acc.AccountID, Proxy: r.Pool.ProxyFor(acc)}
	resp, err := r.Fenjue.Responses(ctx, cred, model, raw, session)
	if err != nil {
		if e, ok := err.(*fenjue.Error); ok {
			return fail(e.Status, e.Code, e.Error())
		}
		return fail(502, "fenjue_request_failed", "Fenjue request failed")
	}
	first, stream, err := peekFirstEvent(resp.Body)
	if err != nil {
		resp.Body.Close()
		return fail(502, "fenjue_stream_incomplete", "Fenjue stream ended before its first event")
	}
	if _, _, failed := failedEvent(first); failed {
		stream.Close()
		return fail(502, "fenjue_generation_failed", "Fenjue returned a failed generation event")
	}
	rec.Status = 200
	rec.Duration = ms(rec.Time)
	r.observe(rec)
	header := http.Header{"X-Gptbridge-Upstream": []string{RouteFenjue}}
	return &Result{Body: &leasedBody{ReadCloser: &accounting{ReadCloser: stream, done: r.finishObserved(acc.ID, rec)}, release: release}, Route: RouteFenjue, Account: acc.Label(), Model: model, Effort: effort, Header: header}, nil
}
