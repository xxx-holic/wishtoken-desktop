// Package fenjue implements the independent Fenjue device and Responses protocols.
package fenjue

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/xxx-holic/wishtoken-desktop/internal/httpx"
)

var API = wireValue("aHR0cHM6Ly9hcGkua2lsby5haQ==")
var Gateway = wireValue("aHR0cHM6Ly9haS1nYXRld2F5LmtpbG8uYWkvYXBpL3Yx")
var BYOKURL = wireValue("aHR0cHM6Ly9hcHAua2lsby5haS9ieW9r")

// Candidates are explicit model IDs from the supplied reference. The authenticated
// gateway catalog is checked again before sending any generation request.
var Models = []string{"gpt-6-astra", "gpt-6.1-sol", "gpt-6.1-terra", "gpt-6-luna", "gpt-5.6-sol", "gpt-5.6-luna", "gpt-6.2-sol", "gpt-6.2-astra"}

func ModelAllowed(model string) bool {
	for _, candidate := range Models {
		if model == candidate {
			return true
		}
	}
	return false
}

type Client struct{ APIURL, GatewayURL string }
type Credential struct{ Token, AccountID, Proxy string }
type Device struct {
	Secret, UserCode, URL string
	Expires               int
}
type Grant struct {
	Status string `json:"status"`
	Token  string `json:"token"`
	UserID string `json:"userId"`
	Email  string `json:"userEmail"`
}
type Subscription struct {
	State       string          `json:"state"`
	Email       string          `json:"email,omitempty"`
	Subject     string          `json:"subject,omitempty"`
	ConnectedAt string          `json:"connectedAt,omitempty"`
	UsageLimit  json.RawMessage `json:"usageLimit,omitempty"`
	Reason      string          `json:"reason,omitempty"`
}
type Error struct {
	Status      int
	Code, Stage string
}

func (e *Error) Error() string {
	return fmt.Sprintf("Fenjue %s: %s (HTTP %d)", e.Stage, e.Code, e.Status)
}
func (c *Client) api() string {
	if c.APIURL != "" {
		return strings.TrimRight(c.APIURL, "/")
	}
	return API
}
func (c *Client) gateway() string {
	if c.GatewayURL != "" {
		return strings.TrimRight(c.GatewayURL, "/")
	}
	return Gateway
}

var safeCode = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)

func Failure(resp *http.Response, stage string) error {
	defer resp.Body.Close()
	var data struct {
		Error json.RawMessage `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&data)
	var nested struct {
		Code string `json:"code"`
		Type string `json:"type"`
	}
	_ = json.Unmarshal(data.Error, &nested)
	code := nested.Code
	if code == "" {
		code = nested.Type
	}
	if !safeCode.MatchString(code) {
		code = "upstream_rejected"
	}
	return &Error{Status: resp.StatusCode, Code: code, Stage: stage}
}

func (c *Client) send(ctx context.Context, cred Credential, method, target string, body []byte, stream bool, session string) (*http.Response, error) {
	base, err := httpx.NewClient(httpx.Options{ProxyURL: cred.Proxy, ResponseHeaderTimeout: 120 * time.Second})
	if err != nil {
		return nil, &Error{502, "proxy_invalid", "network"}
	}
	hc := *base
	// Never forward credentials or a request body through an unexpected redirect.
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if !stream {
		hc.Timeout = 30 * time.Second
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "WishToken-Desktop/0.9")
	req.Header.Set("Accept", "application/json")
	if cred.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cred.Token)
	}
	if stream {
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set(wireValue("WC1LSUxPQ09ERS1GRUFUVVJF"), "cli")
		req.Header.Set("User-Agent", wireValue("S2lsby1Db2RlLzcuOC4yNyBhaS1zZGsvcHJvdmlkZXItdXRpbHMvNC4wLjQwIHJ1bnRpbWUvYnVuLzEuNC40"))
		req.Header.Set(wireValue("WC1LSUxPQ09ERS1FRElUT1JOQU1F"), wireValue("S2lsbyBDTEkgNy44LjI3"))
		req.Header.Set(wireValue("eC1raWxvY29kZS1tb2Rl"), "code")
		req.Header.Set("HTTP-Referer", wireValue("aHR0cHM6Ly9raWxvY29kZS5haQ=="))
		req.Header.Set("X-Title", wireValue("S2lsbyBDb2Rl"))
		if session != "" {
			session = SessionID(cred.AccountID, session)
			req.Header.Set("X-Session-Id", session)
			req.Header.Set(wireValue("WC1LSUxPQ09ERS1UQVNLSUQ="), session)
			req.Header.Set("X-Session-Affinity", session)
		}
		sum := sha256.Sum256([]byte(wireValue("d2lzaHRva2VuLWtpbG8tbWFjaGluZQo=") + cred.AccountID))
		req.Header.Set(wireValue("WC1LSUxPQ09ERS1NQUNISU5FSUQ="), hex.EncodeToString(sum[:16]))
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, &Error{502, "network_error", "network"}
	}
	return resp, nil
}

func decode(resp *http.Response, out any) error {
	defer resp.Body.Close()
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out); err != nil {
		return &Error{502, "invalid_json", "response"}
	}
	return nil
}

func (c *Client) Start(ctx context.Context, proxy string) (*Device, error) {
	resp, err := c.send(ctx, Credential{Proxy: proxy}, "POST", c.api()+"/api/device-auth/codes", []byte(`{}`), false, "")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, Failure(resp, "device_start")
	}
	var wire struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
		URL        string `json:"verificationUrl"`
		Expires    int    `json:"expiresIn"`
	}
	if err = decode(resp, &wire); err != nil {
		return nil, err
	}
	u, e := url.Parse(wire.URL)
	if e != nil || u.Scheme != "https" || u.Host != wireValue("YXBwLmtpbG8uYWk=") || u.Path != "/device-auth" || u.User != nil || u.Fragment != "" || wire.DeviceCode == "" || wire.UserCode == "" || u.Query().Get("code") != wire.UserCode || wire.Expires < 1 || wire.Expires > 3600 {
		return nil, &Error{502, "invalid_device_response", "device_start"}
	}
	return &Device{wire.DeviceCode, wire.UserCode, wire.URL, wire.Expires}, nil
}

func (c *Client) Poll(ctx context.Context, proxy, secret string) (*Grant, error) {
	body, _ := json.Marshal(map[string]any{"deviceCode": secret})
	resp, err := c.send(ctx, Credential{Proxy: proxy}, "POST", c.api()+"/api/device-auth/token", body, false, "")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == 202 {
		resp.Body.Close()
		return &Grant{Status: "pending"}, nil
	}
	if resp.StatusCode == 403 || resp.StatusCode == 410 {
		defer resp.Body.Close()
		return &Grant{Status: map[int]string{403: "denied", 410: "expired"}[resp.StatusCode]}, nil
	}
	if resp.StatusCode != 200 {
		return nil, Failure(resp, "device_poll")
	}
	var grant Grant
	if err = decode(resp, &grant); err != nil {
		return nil, err
	}
	if grant.Status != "approved" || len(grant.Token) < 20 || len(grant.Token) > 16384 || strings.ContainsAny(grant.Token, " \r\n\t") || grant.UserID == "" || grant.Email == "" {
		return nil, &Error{502, "invalid_grant", "device_poll"}
	}
	return &grant, nil
}

func (c *Client) Status(ctx context.Context, cred Credential) (*Subscription, error) {
	query := url.QueryEscape(`{"json":{}}`)
	resp, err := c.send(ctx, cred, "GET", c.api()+"/api/trpc/openAiChatGpt.status?input="+query, nil, false, "")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, Failure(resp, "subscription")
	}
	var wire struct {
		Result struct {
			Data json.RawMessage `json:"data"`
		} `json:"result"`
	}
	if err = decode(resp, &wire); err != nil {
		return nil, err
	}
	var status Subscription
	_ = json.Unmarshal(wire.Result.Data, &status)
	if status.State == "" {
		var wrapped struct {
			JSON Subscription `json:"json"`
		}
		_ = json.Unmarshal(wire.Result.Data, &wrapped)
		status = wrapped.JSON
	}
	if status.State != "connected" && status.State != "disconnected" && status.State != "error" {
		return nil, &Error{502, "unknown_subscription_state", "subscription"}
	}
	status.Reason = ""
	// This condition is a temporary sharing hold, not an invalid credential.
	// Expose a stable category without returning arbitrary upstream text.
	if status.State == "error" {
		var detail struct {
			ErrorMessage string `json:"errorMessage"`
			JSON         struct {
				ErrorMessage string `json:"errorMessage"`
			} `json:"json"`
		}
		_ = json.Unmarshal(wire.Result.Data, &detail)
		if strings.Contains(detail.ErrorMessage+detail.JSON.ErrorMessage, "doesn't allow this ChatGPT account or workspace to share its plan") {
			status.Reason = "subscription_cooldown"
		}
	}
	return &status, nil
}

func (c *Client) CheckModel(ctx context.Context, cred Credential, model string) error {
	resp, err := c.send(ctx, cred, "GET", c.gateway()+"/models", nil, false, "")
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return Failure(resp, "models")
	}
	var wire struct {
		Data []struct {
			ID   string `json:"id"`
			Byok bool   `json:"hasUserByokAvailable"`
		} `json:"data"`
	}
	if err = decode(resp, &wire); err != nil {
		return err
	}
	for _, m := range wire.Data {
		if m.ID == "openai/"+model && m.Byok {
			return nil
		}
	}
	return &Error{400, "model_not_subscription_ready", "models"}
}

// Responses checks the personal subscription and model before the POST. This
// cannot guarantee Fenjue's internal routing: preserve usage.is_byok and cost as
// observations, and never claim the preflight proves a particular served model.
func (c *Client) Responses(ctx context.Context, cred Credential, model string, body []byte, session string) (*http.Response, error) {
	status, err := c.Status(ctx, cred)
	if err != nil {
		return nil, err
	}
	if status.State != "connected" {
		if status.Reason == "subscription_cooldown" {
			return nil, &Error{409, "subscription_cooldown", "subscription"}
		}
		return nil, &Error{409, "connect_chatgpt_subscription", "subscription"}
	}
	// usageLimit is a historical notice, not a current gate. The server clears
	// it after successful generation; blocking here would prevent recovery.
	if err = c.CheckModel(ctx, cred, model); err != nil {
		return nil, err
	}
	resp, err := c.send(ctx, cred, "POST", c.gateway()+"/responses", body, true, session)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, Failure(resp, "generation")
	}
	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		resp.Body.Close()
		return nil, &Error{502, "expected_event_stream", "generation"}
	}
	return resp, nil
}
