package web

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/NaejEL/opnview/internal/auth"
	"github.com/NaejEL/opnview/internal/store"
)

// The setup, sign-in, sign-out and password-reset handlers.

// minimumPasswordRunes is the shortest password this product accepts.
//
// TWELVE, AND THE REASON IS WRITTEN DOWN RATHER THAN ASSUMED. There is no lockout
// and no delay after repeated failures — the requirement asks for neither, and what
// it does ask for is indistinguishable failures — so the only thing standing
// between a local network and an offline guess is the password itself and the
// Argon2id cost beside it. It is counted in RUNES and not bytes, so a passphrase in
// any script is measured by what somebody typed.
const minimumPasswordRunes = 12

// handleRoot sends a signed-in reader to the settings surface.
//
// There is nothing else to send them to yet: canvases are step 7. The middleware
// has already turned away a reader with no session, so reaching here means there is
// one.
func (s *Server) handleRoot(writer http.ResponseWriter, request *http.Request) {
	http.Redirect(writer, request, PathSettings, http.StatusSeeOther)
}

// handleSetupForm draws the first-account surface. The middleware has already
// established that no account exists.
func (s *Server) handleSetupForm(writer http.ResponseWriter, request *http.Request) {
	built, err := s.newView(writer, request, sessionFrom(request), titleKeyFor(pageSetup))
	if err != nil {
		s.failInternal(writer, request, err)
		return
	}
	if err := s.renderer.render(writer, http.StatusOK, pageSetup, built); err != nil {
		s.failInternal(writer, request, err)
	}
}

// handleSetupSubmit creates the first account.
//
// THE TOKEN IS CHECKED BEFORE ANYTHING IS READ FROM THE FORM and spent only after
// the account exists. Checking first means a request without it creates nothing and
// costs nothing; spending last means a valid token met by a mistyped password is
// not burnt, which would otherwise force the operator to restart the service to get
// a new one.
//
// THE ACCOUNT ITSELF IS WHAT REFUSES A SECOND ONE, not the count the middleware ran
// a moment earlier. The middleware's count closes the surface; between that count
// and this insert there is a window, and store.CreateAccount closes it by carrying
// the guard inside its own statement. Losing that race reads here as
// store.ErrAccountExists and is answered with the same permanent closure the
// middleware answers with, so two concurrent requests with two different logins
// cannot both create an account.
func (s *Server) handleSetupSubmit(writer http.ResponseWriter, request *http.Request) {
	if err := s.setupToken.Check(request.PostFormValue("setup_token")); err != nil {
		s.renderRefusal(writer, request, http.StatusForbidden, pageSetup, msgSetupTokenRefused)
		return
	}

	login := strings.TrimSpace(request.PostFormValue("login"))
	password := request.PostFormValue("password")
	if key, ok := validateNewCredentials(login, password,
		request.PostFormValue("password_again")); !ok {
		s.renderSetupRefusal(writer, request, login, key)
		return
	}

	hashed, err := auth.HashPassword(password, s.params)
	if err != nil {
		s.failInternal(writer, request, err)
		return
	}
	accountID, err := s.store.CreateAccount(request.Context(), login, hashed, s.now().UTC().Unix())
	if errors.Is(err, store.ErrAccountExists) {
		s.refuseSetupClosed(writer, request)
		return
	}
	if err != nil {
		s.failInternal(writer, request, err)
		return
	}
	s.setupToken.Spend()

	// The account exists, so this reader is signed in rather than sent round to the
	// sign-in surface to type what they have just typed.
	issued, err := s.sessions.Issue(request.Context(), accountID, s.now())
	if err != nil {
		s.failInternal(writer, request, err)
		return
	}
	setSessionCookie(writer, issued.Token)
	s.redirectWithNotice(writer, request, PathSettings, msgSetupDone)
}

// renderSetupRefusal redraws the setup form with a refusal and the login kept.
func (s *Server) renderSetupRefusal(writer http.ResponseWriter, request *http.Request,
	login string, key messageKey) {
	built, err := s.newView(writer, request, nil, titleKeyFor(pageSetup))
	if err != nil {
		s.failInternal(writer, request, err)
		return
	}
	built.Login = login
	built.Error = key
	if err := s.renderer.render(writer, http.StatusBadRequest, pageSetup, built); err != nil {
		s.failInternal(writer, request, err)
	}
}

// handleSignInForm draws the sign-in surface.
func (s *Server) handleSignInForm(writer http.ResponseWriter, request *http.Request) {
	built, err := s.newView(writer, request, sessionFrom(request), titleKeyFor(pageSignIn))
	if err != nil {
		s.failInternal(writer, request, err)
		return
	}
	if err := s.renderer.render(writer, http.StatusOK, pageSignIn, built); err != nil {
		s.failInternal(writer, request, err)
	}
}

// handleSignInSubmit issues a session, or refuses without saying why.
//
// THE TWO FAILURES ARE INDISTINGUISHABLE, and that costs one deliberate piece of
// work: a login that does not exist is verified against a decoy record anyway, so
// both paths pay the same Argon2id cost, answer the same status, and render the same
// message. Without the decoy, an unknown login would answer in microseconds and a
// known one in tens of milliseconds, which is an enumeration oracle.
func (s *Server) handleSignInSubmit(writer http.ResponseWriter, request *http.Request) {
	login := strings.TrimSpace(request.PostFormValue("login"))
	password := request.PostFormValue("password")

	account, found, err := s.store.AccountByLogin(request.Context(), login)
	if err != nil {
		s.failInternal(writer, request, err)
		return
	}
	stored := s.decoyPassword
	if found {
		stored = account.Password
	}
	verifyErr := auth.VerifyPassword(stored, password)
	if !found || verifyErr != nil {
		s.renderSignInRefusal(writer, request, login)
		return
	}

	// A sign-in is a good moment to collect what has expired: it is a write path
	// already, it is not on any read path, and it keeps the table bounded without a
	// loop of its own.
	if err := s.sessions.CollectExpired(request.Context(), s.now()); err != nil {
		s.failInternal(writer, request, err)
		return
	}
	issued, err := s.sessions.Issue(request.Context(), account.ID, s.now())
	if err != nil {
		s.failInternal(writer, request, err)
		return
	}
	setSessionCookie(writer, issued.Token)
	http.Redirect(writer, request, PathSettings, http.StatusSeeOther)
}

// renderSignInRefusal redraws the sign-in form with the one refusal it has.
func (s *Server) renderSignInRefusal(writer http.ResponseWriter, request *http.Request,
	login string) {
	built, err := s.newView(writer, request, nil, titleKeyFor(pageSignIn))
	if err != nil {
		s.failInternal(writer, request, err)
		return
	}
	built.Login = login
	built.Error = msgSignInRefused
	if err := s.renderer.render(writer, http.StatusUnauthorized, pageSignIn, built); err != nil {
		s.failInternal(writer, request, err)
	}
}

// handleSignOut ends the session.
//
// THE ROW IS DELETED, not merely the cookie. A replay of the token afterwards
// resolves to nothing and is refused, which is what makes signing out mean
// something on a machine somebody else also uses.
//
// COLLECTION DOES NOT STOP. The collectors read their credentials from the live
// holder and know nothing about sessions; signing out changes nothing about the
// next pass, which is what a monitoring tool has to do.
func (s *Server) handleSignOut(writer http.ResponseWriter, request *http.Request) {
	if cookie, err := request.Cookie(cookieSession); err == nil {
		if err := s.sessions.Revoke(request.Context(), cookie.Value); err != nil {
			s.failInternal(writer, request, err)
			return
		}
	}
	clearSessionCookie(writer)
	s.redirectWithNotice(writer, request, PathSignIn, msgSignedOut)
}

// handleRecoverForm draws the password reset surface.
//
// It renders unauthenticated and discloses nothing: it names no login, lists
// nothing, and says the same thing to somebody who has an account and somebody who
// is guessing.
func (s *Server) handleRecoverForm(writer http.ResponseWriter, request *http.Request) {
	built, err := s.newView(writer, request, nil, titleKeyFor(pageRecover))
	if err != nil {
		s.failInternal(writer, request, err)
		return
	}
	if err := s.renderer.render(writer, http.StatusOK, pageRecover, built); err != nil {
		s.failInternal(writer, request, err)
	}
}

// handleRecoverSubmit resets the password of the account whose login was given.
//
// THE MIDDLEWARE HAS ALREADY ESTABLISHED POSSESSION OF THE KEY FILE. Reaching here
// without it is impossible: the guard refuses the request before this function
// exists, which is what makes "and by nothing else" structural rather than
// remembered.
//
// The reset drops every session the account had, because a password change whose
// old sessions survive has changed nothing for whoever was holding one.
func (s *Server) handleRecoverSubmit(writer http.ResponseWriter, request *http.Request) {
	login := strings.TrimSpace(request.PostFormValue("login"))
	password := request.PostFormValue("password")
	if key, ok := validateNewCredentials(login, password,
		request.PostFormValue("password_again")); !ok {
		s.renderRecoverRefusal(writer, request, login, key)
		return
	}

	account, found, err := s.store.AccountByLogin(request.Context(), login)
	if err != nil {
		s.failInternal(writer, request, err)
		return
	}
	if !found {
		// The key file was right and the login was not. It is the same refusal as a
		// wrong key file: somebody holding the key file already holds the secrets, so
		// there is nothing to protect by telling them which half they got wrong, and
		// one message is one fewer thing to get inconsistent.
		s.renderRecoverRefusal(writer, request, login, msgKeyFileRefused)
		return
	}

	hashed, err := auth.HashPassword(password, s.params)
	if err != nil {
		s.failInternal(writer, request, err)
		return
	}
	if err := s.store.SetAccountPassword(request.Context(), account.ID, hashed,
		s.now().UTC().Unix()); err != nil {
		s.failInternal(writer, request, err)
		return
	}
	clearSessionCookie(writer)
	s.redirectWithNotice(writer, request, PathSignIn, msgPasswordReset)
}

// renderRecoverRefusal redraws the reset form with a refusal and the login kept.
func (s *Server) renderRecoverRefusal(writer http.ResponseWriter, request *http.Request,
	login string, key messageKey) {
	built, err := s.newView(writer, request, nil, titleKeyFor(pageRecover))
	if err != nil {
		s.failInternal(writer, request, err)
		return
	}
	built.Login = login
	built.Error = key
	if err := s.renderer.render(writer, http.StatusBadRequest, pageRecover, built); err != nil {
		s.failInternal(writer, request, err)
	}
}

// validateNewCredentials checks a login and a new password, returning the
// catalogue key of the first thing wrong with them.
//
// It is one function because setup and the reset ask for the same three things, and
// two copies of a password rule drift.
func validateNewCredentials(login, password, again string) (messageKey, bool) {
	switch {
	case login == "":
		return msgLoginRequired, false
	case password == "":
		return msgPasswordRequired, false
	case utf8.RuneCountInString(password) < minimumPasswordRunes:
		return msgPasswordTooShort, false
	case password != again:
		return msgPasswordMismatch, false
	}
	return "", true
}
