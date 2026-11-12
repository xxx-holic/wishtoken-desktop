package server

import (
	"context"
	"net/http"
	"time"

	"github.com/xxx-holic/wishtoken-desktop/internal/account"
	"github.com/xxx-holic/wishtoken-desktop/internal/api"
	"github.com/xxx-holic/wishtoken-desktop/internal/fenjue"
)

type fenjueLogin struct {
	ID               string
	Device           *fenjue.Device
	Proxy            string
	Deadline         time.Time
	NextPoll         time.Time
	Busy, Done       bool
	AccountID, Error string
	Ctx              context.Context
	Cancel           context.CancelFunc
}

func fenjueLoginView(login *fenjueLogin) object {
	return object{"session": login.ID, "active": !login.Done, "done": login.Done, "auth_url": login.Device.URL, "user_code": login.Device.UserCode, "channel": "fenjue", "account_id": login.AccountID, "error": login.Error, "byok_url": fenjue.BYOKURL}
}

func (s *Server) adminFenjue(w http.ResponseWriter, r *http.Request, path string) {
	if path == "/fenjue/subscription" && r.Method == "GET" {
		a, ok := s.Store.Get(r.URL.Query().Get("account_id"))
		if !ok || a.Provider != "fenjue" {
			writeError(w, r, 400, "fenjue_account_required", "invalid_request_error", "Select a Fenjue account")
			return
		}
		status, err := s.Router.Fenjue.Status(r.Context(), fenjue.Credential{Token: a.AccessToken, Proxy: s.Pool.ProxyFor(&a)})
		if err != nil {
			fenjueError(w, r, err)
			return
		}
		api.WriteJSON(w, 200, status)
		return
	}
	if path == "/fenjue/login/start" && r.Method == "POST" {
		s.fenjueMu.Lock()
		if s.fenjueLogin != nil && !s.fenjueLogin.Done && time.Now().Before(s.fenjueLogin.Deadline) {
			s.fenjueMu.Unlock()
			writeError(w, r, 409, "fenjue_login_busy", "invalid_request_error", "Finish or cancel the current Fenjue authorization")
			return
		}
		if previous := s.fenjueLogin; previous != nil {
			previous.Cancel()
			previous.Done = true
			previous.Device.Secret = ""
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
		login := &fenjueLogin{ID: "fenjue-" + account.NewID(), Device: &fenjue.Device{}, Proxy: s.Config().ProxyURL, Deadline: time.Now().Add(10 * time.Minute), Ctx: ctx, Cancel: cancel, Busy: true}
		s.fenjueLogin = login
		s.fenjueMu.Unlock()
		device, err := s.Router.Fenjue.Start(ctx, login.Proxy)
		s.fenjueMu.Lock()
		defer s.fenjueMu.Unlock()
		login.Busy = false
		if err != nil || login.Done {
			cancel()
			login.Done = true
			if err != nil {
				login.Error = err.Error()
				fenjueError(w, r, err)
			} else {
				writeError(w, r, 409, "fenjue_login_cancelled", "invalid_request_error", "Fenjue authorization cancelled")
			}
			return
		}
		login.Device = device
		login.Deadline = time.Now().Add(time.Duration(device.Expires) * time.Second)
		api.WriteJSON(w, 200, fenjueLoginView(login))
		return
	}
	if (path != "/fenjue/login/status" || r.Method != "GET") && (path != "/fenjue/login/cancel" || r.Method != "POST") {
		http.NotFound(w, r)
		return
	}
	s.fenjueMu.Lock()
	login := s.fenjueLogin
	if login == nil || login.ID != r.URL.Query().Get("session") {
		s.fenjueMu.Unlock()
		writeError(w, r, 404, "login_missing", "invalid_request_error", "Fenjue authorization session is missing")
		return
	}
	if path == "/fenjue/login/cancel" {
		login.Cancel()
		login.Done = true
		login.Device.Secret = ""
		api.WriteJSON(w, 200, object{"ok": true})
		s.fenjueMu.Unlock()
		return
	}
	if time.Now().After(login.Deadline) && !login.Done {
		login.Cancel()
		login.Done = true
		login.Error = "Fenjue authorization expired"
		login.Device.Secret = ""
	}
	if login.Done || login.Busy || time.Now().Before(login.NextPoll) {
		api.WriteJSON(w, 200, fenjueLoginView(login))
		s.fenjueMu.Unlock()
		return
	}
	login.Busy = true
	login.NextPoll = time.Now().Add(3 * time.Second)
	secret := login.Device.Secret
	s.fenjueMu.Unlock()
	grant, err := s.Router.Fenjue.Poll(login.Ctx, login.Proxy, secret)
	s.fenjueMu.Lock()
	defer s.fenjueMu.Unlock()
	login.Busy = false
	if s.fenjueLogin != login || login.Done || time.Now().After(login.Deadline) {
		login.Cancel()
		login.Done = true
		login.Device.Secret = ""
		api.WriteJSON(w, 200, fenjueLoginView(login))
		return
	}
	if err != nil {
		fenjueError(w, r, err)
		return
	}
	if grant.Status == "approved" {
		a := account.Account{Provider: "fenjue", AccessToken: grant.Token, AccountID: grant.UserID, Email: grant.Email, Source: "fenjue-device-login"}
		a.FillFromTokens()
		stored, saveErr := s.Store.Upsert(a)
		login.Done = true
		login.Device.Secret = ""
		login.Cancel()
		if saveErr != nil {
			login.Error = "Fenjue authorization succeeded but could not be saved; authorize again"
		} else {
			login.AccountID = stored.ID
		}
	} else if grant.Status != "pending" {
		login.Done = true
		login.Error = "Fenjue authorization " + grant.Status
		login.Device.Secret = ""
		login.Cancel()
	}
	api.WriteJSON(w, 200, fenjueLoginView(login))
}

func fenjueError(w http.ResponseWriter, r *http.Request, err error) {
	if e, ok := err.(*fenjue.Error); ok {
		writeError(w, r, e.Status, e.Code, "upstream_error", e.Error())
		return
	}
	writeError(w, r, 502, "fenjue_request_failed", "server_error", "Fenjue request failed")
}
