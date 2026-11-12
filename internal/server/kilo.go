package server

import (
	"context"
	"net/http"
	"time"

	"github.com/xxx-holic/wishtoken-desktop/internal/account"
	"github.com/xxx-holic/wishtoken-desktop/internal/api"
	"github.com/xxx-holic/wishtoken-desktop/internal/kilo"
)

type kiloLogin struct {
	ID               string
	Device           *kilo.Device
	Proxy            string
	Deadline         time.Time
	NextPoll         time.Time
	Busy, Done       bool
	AccountID, Error string
	Ctx              context.Context
	Cancel           context.CancelFunc
}

func kiloLoginView(login *kiloLogin) object {
	return object{"session": login.ID, "active": !login.Done, "done": login.Done, "auth_url": login.Device.URL, "user_code": login.Device.UserCode, "channel": "kilo", "account_id": login.AccountID, "error": login.Error, "byok_url": kilo.BYOKURL}
}

func (s *Server) adminKilo(w http.ResponseWriter, r *http.Request, path string) {
	if path == "/kilo/subscription" && r.Method == "GET" {
		a, ok := s.Store.Get(r.URL.Query().Get("account_id"))
		if !ok || a.Provider != "kilo" {
			writeError(w, r, 400, "kilo_account_required", "invalid_request_error", "Select a Kilo account")
			return
		}
		status, err := s.Router.Kilo.Status(r.Context(), kilo.Credential{Token: a.AccessToken, Proxy: s.Pool.ProxyFor(&a)})
		if err != nil {
			kiloError(w, r, err)
			return
		}
		api.WriteJSON(w, 200, status)
		return
	}
	if path == "/kilo/login/start" && r.Method == "POST" {
		s.kiloMu.Lock()
		if s.kiloLogin != nil && !s.kiloLogin.Done && time.Now().Before(s.kiloLogin.Deadline) {
			s.kiloMu.Unlock()
			writeError(w, r, 409, "kilo_login_busy", "invalid_request_error", "Finish or cancel the current Kilo authorization")
			return
		}
		if previous := s.kiloLogin; previous != nil {
			previous.Cancel()
			previous.Done = true
			previous.Device.Secret = ""
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
		login := &kiloLogin{ID: "kilo-" + account.NewID(), Device: &kilo.Device{}, Proxy: s.Config().ProxyURL, Deadline: time.Now().Add(10 * time.Minute), Ctx: ctx, Cancel: cancel, Busy: true}
		s.kiloLogin = login
		s.kiloMu.Unlock()
		device, err := s.Router.Kilo.Start(ctx, login.Proxy)
		s.kiloMu.Lock()
		defer s.kiloMu.Unlock()
		login.Busy = false
		if err != nil || login.Done {
			cancel()
			login.Done = true
			if err != nil {
				login.Error = err.Error()
				kiloError(w, r, err)
			} else {
				writeError(w, r, 409, "kilo_login_cancelled", "invalid_request_error", "Kilo authorization cancelled")
			}
			return
		}
		login.Device = device
		login.Deadline = time.Now().Add(time.Duration(device.Expires) * time.Second)
		api.WriteJSON(w, 200, kiloLoginView(login))
		return
	}
	if (path != "/kilo/login/status" || r.Method != "GET") && (path != "/kilo/login/cancel" || r.Method != "POST") {
		http.NotFound(w, r)
		return
	}
	s.kiloMu.Lock()
	login := s.kiloLogin
	if login == nil || login.ID != r.URL.Query().Get("session") {
		s.kiloMu.Unlock()
		writeError(w, r, 404, "login_missing", "invalid_request_error", "Kilo authorization session is missing")
		return
	}
	if path == "/kilo/login/cancel" {
		login.Cancel()
		login.Done = true
		login.Device.Secret = ""
		api.WriteJSON(w, 200, object{"ok": true})
		s.kiloMu.Unlock()
		return
	}
	if time.Now().After(login.Deadline) && !login.Done {
		login.Cancel()
		login.Done = true
		login.Error = "Kilo authorization expired"
		login.Device.Secret = ""
	}
	if login.Done || login.Busy || time.Now().Before(login.NextPoll) {
		api.WriteJSON(w, 200, kiloLoginView(login))
		s.kiloMu.Unlock()
		return
	}
	login.Busy = true
	login.NextPoll = time.Now().Add(3 * time.Second)
	secret := login.Device.Secret
	s.kiloMu.Unlock()
	grant, err := s.Router.Kilo.Poll(login.Ctx, login.Proxy, secret)
	s.kiloMu.Lock()
	defer s.kiloMu.Unlock()
	login.Busy = false
	if s.kiloLogin != login || login.Done || time.Now().After(login.Deadline) {
		login.Cancel()
		login.Done = true
		login.Device.Secret = ""
		api.WriteJSON(w, 200, kiloLoginView(login))
		return
	}
	if err != nil {
		kiloError(w, r, err)
		return
	}
	if grant.Status == "approved" {
		a := account.Account{Provider: "kilo", AccessToken: grant.Token, AccountID: grant.UserID, Email: grant.Email, Source: "kilo-device-login"}
		a.FillFromTokens()
		stored, saveErr := s.Store.Upsert(a)
		login.Done = true
		login.Device.Secret = ""
		login.Cancel()
		if saveErr != nil {
			login.Error = "Kilo authorization succeeded but could not be saved; authorize again"
		} else {
			login.AccountID = stored.ID
		}
	} else if grant.Status != "pending" {
		login.Done = true
		login.Error = "Kilo authorization " + grant.Status
		login.Device.Secret = ""
		login.Cancel()
	}
	api.WriteJSON(w, 200, kiloLoginView(login))
}

func kiloError(w http.ResponseWriter, r *http.Request, err error) {
	if e, ok := err.(*kilo.Error); ok {
		writeError(w, r, e.Status, e.Code, "upstream_error", e.Error())
		return
	}
	writeError(w, r, 502, "kilo_request_failed", "server_error", "Kilo request failed")
}
