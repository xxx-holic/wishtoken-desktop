package server

import (
	"encoding/json"
	"github.com/xxx-holic/wishtoken-desktop/internal/api"
	"io"
	"net/http"
)

func (s *Server) requireFenjue(w http.ResponseWriter, r *http.Request) bool {
	if err := s.featureLicense.Require(); err != nil {
		writeError(w, r, 403, "feature_locked", "permission_error", err.Error())
		return false
	}
	return true
}

func (s *Server) adminFeatureLicense(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		api.WriteJSON(w, 200, s.featureLicense.Status())
	case http.MethodPost:
		var input struct {
			Code string `json:"code"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 10000)).Decode(&input) != nil || len(input.Code) > 8192 {
			writeError(w, r, 400, "invalid_license", "invalid_request_error", "请输入有效授权码")
			return
		}
		status, err := s.featureLicense.Activate(input.Code)
		if err != nil {
			writeError(w, r, 400, status.Reason, "invalid_request_error", err.Error())
			return
		}
		api.WriteJSON(w, 200, status)
	case http.MethodDelete:
		status, err := s.featureLicense.Deactivate()
		if err != nil {
			writeError(w, r, 500, "license_storage", "server_error", "无法移除本地功能授权")
			return
		}
		api.WriteJSON(w, 200, status)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
