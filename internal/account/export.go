package account

import (
	"encoding/json"
	"fmt"
	"time"
)

// ToCPA renders the account in the CLIProxyAPI / codex2api export shape.
func ToCPA(a Account) map[string]any {
	if a.Provider == "fenjue" {
		return map[string]any{"type": "fenjue", "name": a.Name, "email": a.Email, "access_token": a.AccessToken, "user_id": a.AccountID, "proxy_url": a.ProxyURL}
	}
	out := map[string]any{
		"type":          "codex",
		"email":         a.Email,
		"id_token":      a.IDToken,
		"access_token":  a.AccessToken,
		"refresh_token": a.RefreshToken,
		"account_id":    a.AccountID,
		"expired":       formatTime(a.ExpiresAt),
		"last_refresh":  formatTime(a.LastRefresh),
	}
	if a.PlanType != "" {
		out["plan_type"] = a.PlanType
	}
	if a.ProxyURL != "" {
		out["proxy_url"] = a.ProxyURL
	}
	if a.Disabled {
		out["disabled"] = true
	}
	return out
}

// ToCodexAuth renders the account as a Codex CLI auth.json document.
func ToCodexAuth(a Account) map[string]any {
	last := a.LastRefresh
	if last.IsZero() {
		last = time.Now()
	}
	return map[string]any{
		"auth_mode":      "chatgpt",
		"OPENAI_API_KEY": nil,
		"tokens": map[string]any{
			"id_token":      a.IDToken,
			"access_token":  a.AccessToken,
			"refresh_token": a.RefreshToken,
			"account_id":    a.AccountID,
		},
		"last_refresh": last.UTC().Format(time.RFC3339Nano),
	}
}

// ExportJSON renders accounts in the requested format ("cpa" or "codex").
func ExportJSON(accounts []Account, format string) ([]byte, error) {
	switch format {
	case "codex", "auth.json":
		for _, a := range accounts {
			if a.Provider == "fenjue" {
				return nil, fmt.Errorf("Fenjue credentials cannot be exported as ChatGPT auth.json")
			}
		}
		if len(accounts) == 1 {
			return json.MarshalIndent(ToCodexAuth(accounts[0]), "", "  ")
		}
		list := make([]map[string]any, 0, len(accounts))
		for _, a := range accounts {
			list = append(list, ToCodexAuth(a))
		}
		return json.MarshalIndent(list, "", "  ")
	default:
		list := make([]map[string]any, 0, len(accounts))
		for _, a := range accounts {
			list = append(list, ToCPA(a))
		}
		return json.MarshalIndent(list, "", "  ")
	}
}
