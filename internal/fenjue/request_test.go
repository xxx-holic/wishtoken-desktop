package fenjue

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

func TestStatelessHistoryPreservesReasoningAndToolPairing(t *testing.T) {
	var input map[string]any
	const raw = `{"model":"openai/gpt-6-astra","instructions":"caller instruction, unchanged","reasoning":{"effort":"medium","summary":"auto"},"include":["reasoning.encrypted_content"],"tools":[{"type":"function","name":"f","parameters":{"type":"object"}}],"tool_choice":"auto","input":[{"type":"reasoning","id":"rs_x","encrypted_content":"opaque-private-reasoning","summary":[]},{"type":"function_call","id":"fc_x","call_id":"call_1","name":"f","arguments":"","synthetic":true},{"type":"function_call_output","call_id":"call_1","output":"unchanged"},{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,untouched","detail":"high"}]}],"client_metadata":{"trace":"local"},"thread_id":"thread","session_id":"session","stream_options":{"include_usage":true,"other":true}}`
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(input)
	prepared, err := PrepareRequest(input, "ses_fixture")
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(input)
	if string(before) != string(after) {
		t.Fatal("caller input mutated")
	}
	for _, key := range []string{"model", "instructions", "reasoning", "include", "tools", "tool_choice"} {
		if !reflect.DeepEqual(prepared[key], input[key]) {
			t.Fatalf("changed semantic field %s", key)
		}
	}
	items := prepared["input"].([]any)
	reasoning := items[0].(map[string]any)
	if reasoning["encrypted_content"] != "opaque-private-reasoning" || reasoning["id"] != nil {
		t.Fatal("reasoning lost or server-side item id retained")
	}
	call := items[1].(map[string]any)
	result := items[2].(map[string]any)
	if call["arguments"] != "{}" || call["synthetic"] != nil || call["call_id"] != result["call_id"] || result["output"] != "unchanged" {
		t.Fatal("tool history changed")
	}
	if !reflect.DeepEqual(items[3], input["input"].([]any)[3]) {
		t.Fatal("image content changed")
	}
	for _, key := range []string{"client_metadata", "thread_id", "session_id"} {
		if _, ok := prepared[key]; ok {
			t.Fatal("local metadata forwarded", key)
		}
	}
	if prepared["stream_options"].(map[string]any)["include_usage"] != nil || prepared["prompt_cache_key"] != "ses_fixture" || prepared["max_output_tokens"] != 32000 || prepared["store"] != false || prepared["stream"] != true {
		t.Fatal("request profile mismatch")
	}
}

func TestRequestShapeRetainsExplicitLimitsAndCacheKey(t *testing.T) {
	body := map[string]any{"input": "unchanged", "max_output_tokens": json.Number("64"), "prompt_cache_key": "caller-cache", "stream_options": map[string]any{"include_usage": true}}
	prepared, err := PrepareRequest(body, "other")
	if err != nil || prepared["max_output_tokens"] != json.Number("64") || prepared["prompt_cache_key"] != "caller-cache" || prepared["input"] != "unchanged" || prepared["stream_options"] != nil {
		t.Fatal("explicit caller settings changed")
	}
}

func TestUnavailableReferencesFailInsteadOfDroppingHistory(t *testing.T) {
	for _, body := range []map[string]any{
		{"previous_response_id": "resp_private"},
		{"input": []any{map[string]any{"type": "item_reference", "id": "msg_private"}}},
	} {
		_, err := PrepareRequest(body, "")
		failure, ok := err.(*Error)
		if !ok || failure.Status != 400 || failure.Code != "full_history_required" {
			t.Fatal("reference was accepted without its actual history")
		}
	}
}

func TestSessionIdentityIsStableAndAccountBound(t *testing.T) {
	if SessionID("a", "t") != SessionID("a", "t") || SessionID("a", "t") == SessionID("b", "t") || SessionID("a", "t") == SessionID("a", "other") || SessionID("a", "") != "" {
		t.Fatal("unstable or shared identity")
	}
}

func TestConversationAffinityKeepsDistinctUserTurnsAndFullHistory(t *testing.T) {
	first := map[string]any{"model": "openai/gpt-6-astra", "instructions": "same instruction", "input": []any{
		map[string]any{"role": "system", "content": "shared prefix"},
		map[string]any{"role": "user", "content": "first question"},
	}}
	key := ConversationKey(nil, first)
	other := map[string]any{"model": first["model"], "instructions": first["instructions"], "input": []any{
		map[string]any{"role": "system", "content": "shared prefix"},
		map[string]any{"role": "user", "content": "different question"},
	}}
	if key == "" || key == ConversationKey(nil, other) {
		t.Fatal("shared system prefix collapsed distinct user conversations")
	}
	first["input"] = append(first["input"].([]any), map[string]any{"type": "function_call_output", "call_id": "call-1", "output": "tool result"})
	if ConversationKey(nil, first) != key {
		t.Fatal("tool result changed conversation affinity")
	}
	first["client_metadata"] = map[string]any{"thread_id": "explicit-thread"}
	if ConversationKey(nil, first) != "thread_id:explicit-thread" {
		t.Fatal("lost client metadata affinity")
	}
	first["prompt_cache_key"] = "caller-cache"
	if ConversationKey(nil, first) != "caller-cache" || ConversationKey(http.Header{"X-Session-Affinity": {"header-session"}}, first) != "header-session" {
		t.Fatal("explicit session precedence changed")
	}
	if ConversationKey(nil, map[string]any{"input": "hello"}) == "" || ConversationKey(nil, map[string]any{"input": "hello"}) == ConversationKey(nil, map[string]any{"input": "other"}) {
		t.Fatal("string input affinity missing")
	}
	if ConversationKey(nil, map[string]any{}) != "" {
		t.Fatal("unanchored request must receive its own random identity")
	}
}
