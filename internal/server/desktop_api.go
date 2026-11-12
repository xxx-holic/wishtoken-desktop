package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strings"

	"github.com/xxx-holic/wishtoken-desktop/internal/config"
)

// The built-in OpenAI provider cannot be redefined. Its supported base URL
// override uses this loopback-only, account/channel-scoped capability URL.
// The URL never contains the management key or an upstream OAuth credential.
func desktopScope(key, accountID, channel string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte("desktop-api\n" + accountID + "\n" + channel))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) handleDesktopAPI(w http.ResponseWriter, r *http.Request) {
	cfg := s.Config()
	remote, _, _ := net.SplitHostPort(r.RemoteAddr)
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/desktop-api/"), "/")
	if !cfg.DesktopMode || cfg.APIKey == "" || !config.IsLoopback(remote) || len(parts) != 5 || parts[3] != "v1" || (parts[2] != "bps" && parts[2] != "codex" && parts[2] != "work") || !hmac.Equal([]byte(parts[0]), []byte(desktopScope(cfg.APIKey, parts[1], parts[2]))) || r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeError(w, r, 401, "desktop_auth", "authentication_error", "invalid local desktop request")
		return
	}
	if parts[4] != "responses" && parts[4] != "models" {
		http.NotFound(w, r)
		return
	}
	if parts[4] == "responses" && r.Method == http.MethodGet {
		// Codex may probe WebSockets before using HTTP SSE on the same route.
		w.WriteHeader(http.StatusUpgradeRequired)
		return
	}
	if (parts[4] == "responses" && r.Method != http.MethodPost) || (parts[4] == "models" && r.Method != http.MethodGet) {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	request := r.Clone(r.Context())
	request.Header = r.Header.Clone()
	request.Header.Set("X-GPTBridge-Account", parts[1])
	request.Header.Set("X-GPTBridge-Channel", parts[2])
	request.Header.Del("Authorization")
	request.Header.Del("X-Api-Key")
	request.Header.Del("X-GPTBridge-Key")
	if parts[4] == "responses" {
		s.handleResponses(w, request)
	} else {
		s.handleModels(w, request)
	}
}
