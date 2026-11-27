package basispoints

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"regexp"
)

// PrimerModels uses the authenticated catalog, never the static picker or guesses.
func PrimerModels(raw []byte) ([]string, error) {
	var data struct {
		Allowed bool `json:"allowed"`
		Catalog *struct {
			Models     []json.RawMessage `json:"models"`
			Restricted []string          `json:"restricted_models"`
		} `json:"model_catalog"`
		Models []json.RawMessage `json:"models"`
	}
	if json.Unmarshal(raw, &data) != nil || !data.Allowed {
		return nil, fmt.Errorf("BPS 未允许此账号访问")
	}
	rows := data.Models
	restricted := map[string]bool{}
	if data.Catalog != nil {
		rows = data.Catalog.Models
		for _, id := range data.Catalog.Restricted {
			restricted[id] = true
		}
	}
	models := []string{}
	seen := map[string]bool{}
	for _, raw := range rows {
		var m struct {
			ID        string `json:"id"`
			Available *bool  `json:"available"`
			Enabled   *bool  `json:"enabled"`
			Picker    *bool  `json:"model_picker_enabled"`
			Policy    struct {
				State string `json:"state"`
			} `json:"policy"`
			Efforts          []json.RawMessage `json:"efforts"`
			ReasoningEfforts []string          `json:"reasoning_efforts"`
		}
		if json.Unmarshal(raw, &m) != nil || !regexp.MustCompile(`^[A-Za-z0-9._/-]{1,128}$`).MatchString(m.ID) {
			return nil, fmt.Errorf("BPS 模型列表格式无效")
		}
		if seen[m.ID] || restricted[m.ID] || (m.Available != nil && !*m.Available) || (m.Enabled != nil && !*m.Enabled) || (m.Picker != nil && !*m.Picker) || m.Policy.State == "disabled" || m.Policy.State == "unconfigured" {
			continue
		}
		low := len(m.Efforts) == 0 && len(m.ReasoningEfforts) == 0
		for _, value := range m.ReasoningEfforts {
			low = low || value == "low"
		}
		for _, item := range m.Efforts {
			var value string
			if json.Unmarshal(item, &value) != nil {
				var e struct {
					Value string `json:"value"`
				}
				_ = json.Unmarshal(item, &e)
				value = e.Value
			}
			low = low || value == "low"
		}
		if !low {
			return nil, fmt.Errorf("BPS 模型 %s 未提供 low 档位，未开始预热", m.ID)
		}
		seen[m.ID] = true
		models = append(models, m.ID)
	}
	if len(models) == 0 || len(models) > 100 {
		return nil, fmt.Errorf("BPS 未返回可用模型列表")
	}
	return models, nil
}

func PrimerPlan(models []string, total int) []string {
	if len(models) == 0 || total < 0 {
		return nil
	}
	if total < len(models) {
		total = len(models)
	}
	plan := make([]string, total)
	for i := range plan {
		plan[i] = models[i%len(models)]
	}
	return plan
}

// UUID v5 with the URL namespace matches the supplied reference vectors.
func primerUUID(name string) string {
	ns := []byte{0x6b, 0xa7, 0xb8, 0x11, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}
	sum := sha1.Sum(append(ns, []byte(name)...))
	sum[6] = sum[6]&0x0f | 0x50
	sum[8] = sum[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

func PrimerBody(model, scope string, turn int) []byte {
	raw, _ := json.Marshal(map[string]any{
		"model": model, "model_selection": "explicit", "stream": true, "store": false,
		"input":            []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "hi"}}}},
		"reasoning_effort": "low",
		"metadata":         map[string]string{"task_id": primerUUID("cpa-oai-basispoints/" + scope), "turn_id": primerUUID(fmt.Sprintf("cpa-oai-basispoints/%s/turn/%d", scope, turn)), "agent_iteration": "1"},
	})
	return raw
}
