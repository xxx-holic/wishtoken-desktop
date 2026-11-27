package fenjue

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
)

// ConversationKey prefers explicit affinity. The fallback includes the first
// user turn, not only a shared system prefix, and survives appended tool turns.
func ConversationKey(headers http.Header, body map[string]any) string {
	for _, name := range []string{"session_id", "session-id", "x-session-id", "conversation_id", "thread-id", "x-conversation-id", "x-session-affinity"} {
		if value := strings.TrimSpace(headers.Get(name)); value != "" {
			return value
		}
	}
	if value, ok := body["prompt_cache_key"].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	metadata, _ := body["client_metadata"].(map[string]any)
	for _, name := range []string{"thread_id", "session_id"} {
		for _, source := range []map[string]any{metadata, body} {
			if value, ok := source[name].(string); ok && strings.TrimSpace(value) != "" {
				return name + ":" + strings.TrimSpace(value)
			}
		}
	}
	var anchor any
	prefix := []any{}
	switch input := body["input"].(type) {
	case string:
		if strings.TrimSpace(input) != "" {
			anchor = input
		}
	case []any:
		prefixOpen := true
		for _, raw := range input {
			item, ok := raw.(map[string]any)
			if !ok {
				prefixOpen = false
				continue
			}
			role, _ := item["role"].(string)
			if prefixOpen && (role == "system" || role == "developer") {
				prefix = append(prefix, map[string]any{"role": role, "content": item["content"]})
			} else {
				prefixOpen = false
			}
			if anchor == nil {
				if role == "user" {
					anchor = item["content"]
				} else if item["type"] == "input_text" {
					anchor = item["text"]
				}
			}
		}
	}
	if anchor == nil {
		return ""
	}
	raw, err := json.Marshal([]any{body["model"], body["instructions"], body["tools"], prefix, anchor})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return "conv-" + hex.EncodeToString(sum[:16])
}

// SessionID retains the existing account-bound identity across upgrades.
func SessionID(accountID, session string) string {
	if session == "" {
		return ""
	}
	key := sha256.Sum256([]byte(accountID + "\n" + session))
	return "ses_" + hex.EncodeToString(key[:16])
}

// PrepareRequest adapts local transport metadata, never prompt/effort/tool content.
// Stateless references are rejected rather than silently losing conversation history.
func PrepareRequest(source map[string]any, sessionID string) (map[string]any, error) {
	body := make(map[string]any, len(source))
	for key, value := range source {
		body[key] = value
	}
	if value, ok := body["previous_response_id"]; ok && value != nil && value != "" {
		return nil, &Error{400, "full_history_required", "request"}
	}
	delete(body, "previous_response_id")
	for _, key := range []string{"client_metadata", "thread_id", "session_id"} {
		delete(body, key)
	}
	if opts, ok := body["stream_options"].(map[string]any); ok {
		copy := make(map[string]any, len(opts))
		for key, value := range opts {
			if key != "include_usage" {
				copy[key] = value
			}
		}
		if len(copy) == 0 {
			delete(body, "stream_options")
		} else {
			body["stream_options"] = copy
		}
	}
	if items, ok := body["input"].([]any); ok {
		out := make([]any, len(items))
		for i, item := range items {
			value, ok := item.(map[string]any)
			if !ok {
				out[i] = item
				continue
			}
			if value["type"] == "item_reference" {
				return nil, &Error{400, "full_history_required", "request"}
			}
			copy := make(map[string]any, len(value))
			for key, field := range value {
				if key != "id" && key != "synthetic" {
					copy[key] = field
				}
			}
			if copy["type"] == "function_call" && copy["arguments"] == "" {
				copy["arguments"] = "{}"
			}
			out[i] = copy
		}
		body["input"] = out
	}
	if _, exists := body["prompt_cache_key"]; !exists && sessionID != "" {
		body["prompt_cache_key"] = sessionID
	}
	if _, exists := body["max_output_tokens"]; !exists {
		body["max_output_tokens"] = 32000
	}
	body["store"] = false
	body["stream"] = true
	return body, nil
}
