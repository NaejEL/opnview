package account

import (
	"errors"
	"net/http"
	"strings"

	"github.com/NaejEL/opnview/internal/auth"
	"github.com/NaejEL/opnview/internal/store/accounts"
	"github.com/NaejEL/opnview/internal/web"
)

// The account surface's JSON equivalents (docs/widget-api.md, "Settings API"): the session state
// and the form token, the first-account setup, sign-in, sign-out and the key-file reset. Each
// applies the page's own validation, access level and CSRF rule, and answers closed codes.

// The JSON paths.
const (
	// PathAPISession is the session state and the form token a JSON write carries.
	PathAPISession = "/api/session"
	// PathAPISetup creates the first account.
	PathAPISetup = "/api/account/setup"
	// PathAPISignIn issues a session.
	PathAPISignIn = "/api/session/sign-in"
	// PathAPISignOut ends the session.
	PathAPISignOut = "/api/session/sign-out"
	// PathAPIRecover resets a password with the key file.
	PathAPIRecover = "/api/account/recover"
)

// apiRoutes are the surface's JSON rows of the route table.
func (s *surface) apiRoutes() []web.Route {
	return []web.Route{
		// The session state discloses only whether an account exists and whether this reader is
		// signed in, which the pages disclose by where they send the reader; and the form token,
		// which every page carries.
		{Method: http.MethodGet, Pattern: PathAPISession, Access: web.AccessPublic, JSON: true,
			Handler: s.handleAPISession},
		{Method: http.MethodPost, Pattern: PathAPISetup, Access: web.AccessPublic, JSON: true,
			RequiresNoAccount: true, Handler: s.handleAPISetup},
		{Method: http.MethodPost, Pattern: PathAPISignIn, Access: web.AccessPublic, JSON: true,
			Handler: s.handleAPISignIn},
		web.JSONRoute(http.MethodPost, PathAPISignOut, s.handleAPISignOut),
		{Method: http.MethodPost, Pattern: PathAPIRecover, Access: web.AccessKeyFile, JSON: true,
			Handler: s.handleAPIRecover},
	}
}

// SessionState is GET /api/session's answer.
type SessionState struct {
	// AccountExists is whether the installation has an account: without one, the setup is open.
	AccountExists bool `json:"account_exists"`
	// SignedIn is whether this request carried a live session.
	SignedIn bool `json:"signed_in"`
	// CSRFToken is the token a JSON write of this reader carries in its X-CSRF-Token header.
	CSRFToken string `json:"csrf_token"`
}

// SignedInAnswer answers a setup or a sign-in: the session is in its cookie, and its form token
// here.
type SignedInAnswer struct {
	SignedIn  bool   `json:"signed_in"`
	CSRFToken string `json:"csrf_token"`
}

// SignedOutAnswer answers a sign-out and a reset: no session is held any more.
type SignedOutAnswer struct {
	SignedIn bool `json:"signed_in"`
}

func (s *surface) handleAPISession(writer http.ResponseWriter, request *http.Request) {
	exists, err := s.AccountExists(request.Context())
	if err != nil {
		web.FailJSON(writer, request, err)
		return
	}
	token, err := s.FormToken(writer, request)
	if err != nil {
		web.FailJSON(writer, request, err)
		return
	}
	web.WriteJSON(writer, http.StatusOK, SessionState{AccountExists: exists,
		SignedIn: web.SessionFrom(request) != nil, CSRFToken: token})
}

// readCredentials decodes a body carrying a login and a new password, or refuses it.
func readCredentials(writer http.ResponseWriter, request *http.Request) (map[string]any, bool) {
	body, ok := web.DecodeJSONObject(request.Body)
	if !ok {
		web.RefuseJSON(writer, http.StatusBadRequest, web.ConditionMalformedBody, "")
	}
	return body, ok
}

// text is one member of a body as a form field would carry it.
func text(body map[string]any, key string) string {
	value, _ := web.JSONText(body, key)
	return value
}

// handleAPISetup is handleSetupSubmit answered in JSON: the setup token first, the same
// credential rules, the account itself refusing a second one, the token spent last.
func (s *surface) handleAPISetup(writer http.ResponseWriter, request *http.Request) {
	body, ok := readCredentials(writer, request)
	if !ok {
		return
	}
	if err := s.setupToken.Check(text(body, "setup_token")); err != nil {
		web.RefuseJSON(writer, http.StatusForbidden, web.ConditionOf(msgSetupTokenRefused), "setup_token")
		return
	}
	login := strings.TrimSpace(text(body, "login"))
	password := text(body, "password")
	if key, ok := validateNewCredentials(login, password, text(body, "password_again")); !ok {
		web.RefuseJSON(writer, http.StatusBadRequest, web.ConditionOf(key), credentialField(key))
		return
	}
	hashed, err := auth.HashPassword(password, s.params)
	if err != nil {
		web.FailJSON(writer, request, err)
		return
	}
	accountID, err := s.store.CreateAccount(request.Context(), login, hashed, s.now().UTC().Unix())
	if errors.Is(err, accounts.ErrAccountExists) {
		web.RefuseJSON(writer, http.StatusForbidden, web.ConditionSetupClosed, "")
		return
	}
	if err != nil {
		web.FailJSON(writer, request, err)
		return
	}
	s.setupToken.Spend()
	issued, err := s.sessions.Issue(request.Context(), accountID, s.now())
	if err != nil {
		web.FailJSON(writer, request, err)
		return
	}
	web.SetSessionCookie(writer, issued.Token)
	web.WriteJSON(writer, http.StatusOK, SignedInAnswer{SignedIn: true, CSRFToken: issued.CSRFToken})
}

// handleAPISignIn is handleSignInSubmit answered in JSON: the same two indistinguishable
// failures, the decoy verified for an unknown login.
func (s *surface) handleAPISignIn(writer http.ResponseWriter, request *http.Request) {
	body, ok := readCredentials(writer, request)
	if !ok {
		return
	}
	login := strings.TrimSpace(text(body, "login"))
	password := text(body, "password")
	account, found, err := s.store.AccountByLogin(request.Context(), login)
	if err != nil {
		web.FailJSON(writer, request, err)
		return
	}
	stored := s.decoyPassword
	if found {
		stored = account.Password
	}
	verifyErr := auth.VerifyPassword(stored, password)
	if !found || verifyErr != nil {
		web.RefuseJSON(writer, http.StatusUnauthorized, web.ConditionOf(msgSignInRefused), "")
		return
	}
	if err := s.sessions.CollectExpired(request.Context(), s.now()); err != nil {
		web.FailJSON(writer, request, err)
		return
	}
	issued, err := s.sessions.Issue(request.Context(), account.ID, s.now())
	if err != nil {
		web.FailJSON(writer, request, err)
		return
	}
	web.SetSessionCookie(writer, issued.Token)
	web.WriteJSON(writer, http.StatusOK, SignedInAnswer{SignedIn: true, CSRFToken: issued.CSRFToken})
}

// handleAPISignOut is handleSignOut answered in JSON: the row is deleted, not merely the cookie.
func (s *surface) handleAPISignOut(writer http.ResponseWriter, request *http.Request) {
	if cookie, err := request.Cookie(web.SessionCookieName); err == nil {
		if err := s.sessions.Revoke(request.Context(), cookie.Value); err != nil {
			web.FailJSON(writer, request, err)
			return
		}
	}
	web.ClearSessionCookie(writer)
	web.WriteJSON(writer, http.StatusOK, SignedOutAnswer{SignedIn: false})
}

// handleAPIRecover is handleRecoverSubmit answered in JSON. The middleware has established
// possession of the key file from the body's key_file member.
func (s *surface) handleAPIRecover(writer http.ResponseWriter, request *http.Request) {
	body, ok := readCredentials(writer, request)
	if !ok {
		return
	}
	login := strings.TrimSpace(text(body, "login"))
	password := text(body, "password")
	if key, ok := validateNewCredentials(login, password, text(body, "password_again")); !ok {
		web.RefuseJSON(writer, http.StatusBadRequest, web.ConditionOf(key), credentialField(key))
		return
	}
	account, found, err := s.store.AccountByLogin(request.Context(), login)
	if err != nil {
		web.FailJSON(writer, request, err)
		return
	}
	if !found {
		// The page's own answer: the same refusal as a wrong key file.
		web.RefuseJSON(writer, http.StatusBadRequest, web.ConditionOf(msgKeyFileRefused), "")
		return
	}
	hashed, err := auth.HashPassword(password, s.params)
	if err != nil {
		web.FailJSON(writer, request, err)
		return
	}
	if err := s.store.SetAccountPassword(request.Context(), account.ID, hashed, s.now().UTC().Unix()); err != nil {
		web.FailJSON(writer, request, err)
		return
	}
	web.ClearSessionCookie(writer)
	web.WriteJSON(writer, http.StatusOK, SignedOutAnswer{SignedIn: false})
}

// credentialField is the body member a credential refusal is about.
func credentialField(key messageKey) string {
	switch key {
	case msgLoginRequired:
		return "login"
	case msgPasswordRequired, msgPasswordTooShort:
		return "password"
	case msgPasswordMismatch:
		return "password_again"
	}
	return ""
}
