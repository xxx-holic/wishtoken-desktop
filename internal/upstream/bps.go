package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"

	"github.com/xxx-holic/wishtoken-desktop/internal/basispoints"
)

// BPSClient talks to the Basispoints gateway.
type BPSClient struct {
	// Transport is explicitly selected. Empty retains the HTTP/SSE route.
	Transport string
	// ResponsesURL / AttachmentsURL override the defaults (tests).
	ResponsesURL   string
	AccessURL      string
	AttachmentsURL string
}

// ModelAccess uses the same credential and proxy as the subsequent BPS calls.
func (c *BPSClient) ModelAccess(ctx context.Context, cred Credentials) (*http.Response, error) {
	target := c.AccessURL
	if target == "" {
		target = basispoints.ResponsesURL + "/access?include_models=true"
	}
	hc, err := client(cred, 30*time.Second)
	if err != nil {
		return nil, err
	}
	copy := *hc
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	ApplyBPSHeaders(req, cred)
	req.Header.Set("Accept", "application/json")
	return copy.Do(req)
}

func (c *BPSClient) responsesURL() string {
	if c.ResponsesURL != "" {
		return c.ResponsesURL
	}
	return basispoints.ResponsesURL
}

func (c *BPSClient) attachmentsURL() string {
	if c.AttachmentsURL != "" {
		return c.AttachmentsURL
	}
	return basispoints.AttachmentsURL
}

// ApplyBPSHeaders sets the headers the gateway expects from the Excel add-in.
func ApplyBPSHeaders(req *http.Request, cred Credentials) {
	req.Header.Set("Authorization", "Bearer "+cred.AccessToken)
	req.Header.Set("Chatgpt-Account-Id", cred.AccountID)
	req.Header.Set("X-OpenAI-Account-Id", cred.AccountID)
	req.Header.Set("X-Basispoints-Auth-Mode", "chatgpt")
	req.Header.Set("Origin", "https://bps.openai.com")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36 Edg/140.0.0.0")
	req.Header.Set("X-OpenAI-Internal-Basispoints-Client-Product", "basispoints-excel-plugin")
	req.Header.Set("X-OpenAI-Internal-Basispoints-Client-Agent-Profile", "excel")
	req.Header.Set("X-OpenAI-Internal-Basispoints-Client-Editor", "excel")
	req.Header.Set("X-OpenAI-Internal-Basispoints-Client-Host", "office")
	req.Header.Set("X-OpenAI-Internal-Basispoints-Client-Platform", "excel")
	req.Header.Set("X-OpenAI-Internal-Basispoints-Client-Platform-Class", "PC")
	req.Header.Set("X-OpenAI-Internal-Basispoints-Client-Runtime", "desktop")
	req.Header.Set("X-OpenAI-Internal-Basispoints-Office-Host", "Excel")
	req.Header.Set("X-OpenAI-Internal-Basispoints-Office-Platform", "PC")
}

// Responses posts a prepared gateway request and returns the raw response.
func (c *BPSClient) Responses(ctx context.Context, cred Credentials, body []byte) (*http.Response, error) {
	if strings.TrimSpace(cred.AccessToken) == "" || strings.TrimSpace(cred.AccountID) == "" {
		return nil, fmt.Errorf("basispoints requires a ChatGPT access token and account id")
	}
	if c.Transport == "websocket" {
		return c.responsesWebSocket(ctx, cred, body)
	}
	if c.Transport != "" && c.Transport != "http" {
		return nil, fmt.Errorf("unknown BPS transport")
	}
	hc, err := client(cred, 10*time.Minute)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.responsesURL(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	ApplyBPSHeaders(req, cred)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	return hc.Do(req)
}

// UploadAttachment uploads one image the way the add-in's upload button does
// and returns the gateway file id.
func (c *BPSClient) UploadAttachment(ctx context.Context, cred Credentials, att basispoints.Attachment) (string, error) {
	hc, err := client(cred, 120*time.Second)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="image.%s"`, att.Extension()))
	header.Set("Content-Type", att.MIME)
	part, err := writer.CreatePart(header)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(att.Data); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.attachmentsURL(), &buf)
	if err != nil {
		return "", err
	}
	ApplyBPSHeaders(req, cred)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("attachment upload failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &HTTPError{Status: resp.StatusCode, Body: string(body), Upstream: "basispoints-attachments"}
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("attachment upload returned invalid JSON")
	}
	for _, key := range []string{"openai_file_id", "file_id", "id"} {
		if id, ok := payload[key].(string); ok && strings.TrimSpace(id) != "" {
			return strings.TrimSpace(id), nil
		}
	}
	return "", fmt.Errorf("attachment upload returned no file id")
}

// HTTPError is a non-2xx upstream answer with its (bounded) body.
type HTTPError struct {
	Status   int
	Body     string
	Upstream string
}

func (e *HTTPError) Error() string {
	body := strings.Join(strings.Fields(e.Body), " ")
	if len(body) > 400 {
		body = body[:400] + "…"
	}
	return fmt.Sprintf("%s returned %d: %s", e.Upstream, e.Status, body)
}

// ErrorCode extracts error.code (or error.type) from a JSON error body.
func (e *HTTPError) ErrorCode() string {
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
		Detail any `json:"detail"`
	}
	if json.Unmarshal([]byte(e.Body), &payload) != nil {
		return ""
	}
	if payload.Error.Code != "" {
		return payload.Error.Code
	}
	if detail, ok := payload.Detail.(map[string]any); ok {
		if code, ok := detail["code"].(string); ok {
			return code
		}
	}
	return payload.Error.Type
}

// Message extracts a human readable message from a JSON error body.
func (e *HTTPError) Message() string {
	lower := strings.ToLower(strings.TrimSpace(e.Body))
	if strings.HasPrefix(lower, "<!doctype html") || strings.HasPrefix(lower, "<html") {
		return fmt.Sprintf("%s upstream returned HTTP %d (gateway error; retry the same model later)", e.Upstream, e.Status)
	}
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Detail any `json:"detail"`
	}
	if json.Unmarshal([]byte(e.Body), &payload) == nil {
		if payload.Error.Message != "" {
			return payload.Error.Message
		}
		switch d := payload.Detail.(type) {
		case string:
			return d
		case map[string]any:
			if m, ok := d["message"].(string); ok {
				return m
			}
		}
	}
	return strings.TrimSpace(e.Body)
}
