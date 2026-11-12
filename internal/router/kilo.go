package router

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/xxx-holic/wishtoken-desktop/internal/kilo"
	"github.com/xxx-holic/wishtoken-desktop/internal/pool"
)

func (r *Router) executeKilo(ctx context.Context, req *Request, accountID, requested string) (*Result, *Error) {
	channel := req.Headers.Get("X-GPTBridge-Channel")
	if channel != "" && channel != RouteKilo {
		return nil, apiErr(400, "channel_conflict", "Kilo model conflicts with the pinned channel")
	}
	if r.Kilo == nil {
		return nil, apiErr(503, "kilo_unavailable", "Kilo transport is not configured")
	}
	if accountID == "" {
		return nil, apiErr(400, "account_required", "Select an independent Kilo account")
	}
	model := strings.TrimPrefix(strings.TrimSuffix(requested, "-kilo"), "openai/")
	if !kilo.ModelAllowed(model) {
		return nil, apiErr(400, "kilo_model", "Model is not in the explicit Kilo candidate list")
	}
	body := cloneObject(req.Body)
	if body["store"] == true || body["background"] == true || body["conversation"] != nil {
		return nil, apiErr(400, "kilo_stateless", "Kilo subscription requests must be stateless: store=false, no conversation or background mode")
	}
	if tier := str(body["service_tier"]); tier != "" && tier != "auto" && tier != "default" {
		return nil, apiErr(400, "kilo_tier", "Kilo subscription requires standard speed")
	}
	effort := r.Config().DefaultEffort
	if reasoning, ok := body["reasoning"].(object); ok && str(reasoning["effort"]) != "" {
		effort = str(reasoning["effort"])
	}
	if effort == "" {
		effort = "medium"
	}
	if effort != "low" && effort != "medium" && effort != "high" && effort != "xhigh" {
		return nil, apiErr(400, "kilo_effort", "Use low, medium, high or xhigh for this Kilo integration")
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
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, apiErr(400, "invalid_json", "Invalid Kilo request")
	}
	acc, err := r.Pool.Pick(ctx, pool.PickOptions{AccountID: accountID, Route: RouteKilo, Model: model})
	if err != nil {
		return nil, apiErr(409, "kilo_account_required", "Select a ready Kilo credential; ChatGPT/BPS grants cannot authenticate to Kilo")
	}
	release, err := r.Pool.Acquire(ctx, acc.ID)
	if err != nil {
		return nil, apiErr(503, "account_busy", "Kilo account is busy")
	}
	rec := Record{Time: time.Now(), Route: RouteKilo, Model: model, Effort: effort, Account: acc.Label(), Client: req.Client, Transport: "http"}
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
	cred := kilo.Credential{Token: acc.AccessToken, AccountID: acc.AccountID, Proxy: r.Pool.ProxyFor(acc)}
	resp, err := r.Kilo.Responses(ctx, cred, model, raw, SessionKey(req.Headers, body))
	if err != nil {
		if e, ok := err.(*kilo.Error); ok {
			return fail(e.Status, e.Code, e.Error())
		}
		return fail(502, "kilo_request_failed", "Kilo request failed")
	}
	first, stream, err := peekFirstEvent(resp.Body)
	if err != nil {
		resp.Body.Close()
		return fail(502, "kilo_stream_incomplete", "Kilo stream ended before its first event")
	}
	if _, _, failed := failedEvent(first); failed {
		stream.Close()
		return fail(502, "kilo_generation_failed", "Kilo returned a failed generation event")
	}
	rec.Status = 200
	rec.Duration = ms(rec.Time)
	r.observe(rec)
	header := http.Header{"X-Gptbridge-Upstream": []string{RouteKilo}}
	return &Result{Body: &leasedBody{ReadCloser: &accounting{ReadCloser: stream, done: r.finishObserved(acc.ID, rec)}, release: release}, Route: RouteKilo, Account: acc.Label(), Model: model, Effort: effort, Header: header}, nil
}
