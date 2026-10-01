package web

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"

	"github.com/NaejEL/opnview/internal/auth"
	"github.com/NaejEL/opnview/internal/store"
)

// The middleware every route passes through.
//
// THE ORDER IS LOAD-BEARING, and it is the order below:
//
//  1. the setup-closing guard, which is a COUNT AGAINST THE DATABASE and runs on
//     every method of the setup routes;
//  2. the session, resolved for every route — required where the route is
//     authenticated, read opportunistically otherwise so that a signed-in reader
//     sees the sign-out control and so that a form token is compared against the
//     session's own;
//  3. the CSRF token, on every mutating request;
//  4. the key file, on the one route that is authorised by possession of it.
//
// A handler therefore never decides whether it may run. That is the point: a
// decision written into a handler is a decision the next handler can forget.

// sessionContextKey is how a resolved session reaches a handler.
type sessionContextKey struct{}

// sessionFrom returns the session a request was resolved to, if any.
func sessionFrom(request *http.Request) *store.Session {
	session, present := request.Context().Value(sessionContextKey{}).(*store.Session)
	if !present {
		return nil
	}
	return session
}

// guard wraps one route's handler in the middleware its access level requires.
func (s *Server) guard(route Route) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		ctx := request.Context()

		// 1. The setup surface closes permanently once an account exists.
		//
		// DERIVED FROM THE DATABASE, ON EVERY METHOD, ON EVERY REQUEST. Not a flag
		// set at start-up, which a restart would reset; not a check in the GET
		// handler, which would leave the POST open. Both of those are the hole this
		// cycle's stated top risk names, and a test asserts the closure with a valid
		// token, with a spent one, and again after a restart against the same data
		// directory.
		if route.RequiresNoAccount {
			exists, err := s.AccountExists(ctx)
			if err != nil {
				s.failInternal(writer, request, err)
				return
			}
			if exists {
				s.refuseSetupClosed(writer, request)
				return
			}
		}

		// 2. The session.
		session, err := s.resolveSession(request)
		if route.Access == AccessAuthenticated && session == nil {
			s.refuseUnauthenticated(writer, request, err)
			return
		}
		if session != nil {
			request = request.WithContext(
				context.WithValue(request.Context(), sessionContextKey{}, session))
		}

		// 3. The form token, on every mutating request.
		if route.Method == http.MethodPost {
			if err := request.ParseForm(); err != nil {
				s.refuseFormToken(writer, request)
				return
			}
			if !s.formTokenValid(request, session) {
				s.refuseFormToken(writer, request)
				return
			}
		}

		// 4. The key file, on the one route authorised by possession of it.
		if route.Access == AccessKeyFile && !s.keyFilePresented(request) {
			s.refuseKeyFile(writer, request)
			return
		}

		route.handler(writer, request)
	}
}

// resolveSession validates the session cookie, if there is one.
//
// It returns the session and, separately, why there is none, so that a reader
// whose session ended is told that rather than being told they were never signed
// in.
func (s *Server) resolveSession(request *http.Request) (*store.Session, error) {
	cookie, err := request.Cookie(cookieSession)
	if err != nil || cookie.Value == "" {
		return nil, auth.ErrNoSession
	}
	session, err := s.sessions.Validate(request.Context(), cookie.Value, s.now())
	if err != nil {
		return nil, err
	}
	return &session, nil
}

// formTokenValid compares the submitted form token against the one that belongs
// to this request.
//
// WHICH TOKEN IS THE EXPECTED ONE DEPENDS ON WHETHER THERE IS A SESSION, and that
// is what makes "a token belonging to another session is refused" hold:
//
//   - with a session, the expected token is the SESSION'S OWN, stored in its row.
//     Another session's token is a different value and fails.
//   - without one — setup, sign-in, the password reset — the expected token is the
//     value in this browser's own cookie, which the form was rendered with. A token
//     lifted from somewhere else does not match the cookie the request carries.
//
// The comparison is constant-time.
func (s *Server) formTokenValid(request *http.Request, session *store.Session) bool {
	submitted := request.PostFormValue("csrf_token")
	if submitted == "" {
		return false
	}
	expected := ""
	if session != nil {
		expected = session.CSRFToken
	} else if cookie, err := request.Cookie(cookieCSRF); err == nil {
		expected = cookie.Value
	}
	if expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(submitted), []byte(expected)) == 1
}

// keyFilePresented reports whether the request carried the contents of the key
// file beside the database.
//
// A MISSING KEY FILE REFUSES RATHER THAN ACCEPTING ANYTHING. If this installation
// has no key file, there is nothing to possess and so nothing authorises a reset:
// the box is nil and every submission fails.
func (s *Server) keyFilePresented(request *http.Request) bool {
	if s.box == nil {
		return false
	}
	presented := request.PostFormValue("key_file")
	if presented == "" {
		return false
	}
	return s.box.Matches([]byte(presented))
}

// formToken returns the token a form about to be rendered must carry, setting the
// cookie the unauthenticated surfaces are compared against.
//
// A signed-in reader gets the session's own token and no cookie is written: the
// row already holds it.
func (s *Server) formToken(writer http.ResponseWriter, request *http.Request,
	session *store.Session) (string, error) {
	if session != nil {
		return session.CSRFToken, nil
	}
	if cookie, err := request.Cookie(cookieCSRF); err == nil && cookie.Value != "" {
		return cookie.Value, nil
	}
	token, err := auth.RandomToken()
	if err != nil {
		return "", err
	}
	http.SetCookie(writer, &http.Cookie{
		Name:     cookieCSRF,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	return token, nil
}

// setSessionCookie puts a freshly issued session token in the cookie.
//
// NO Secure FLAG, SameSite=Lax. Decision 6 of the specification, and the reason is
// in README.md's limitations section rather than here: Secure makes sign-in
// impossible over plain HTTP and there is no TLS story before step 8. HttpOnly, and
// no Max-Age, so the cookie is a session cookie and the two server-side bounds are
// the only thing that decides how long it is worth anything.
func setSessionCookie(writer http.ResponseWriter, token string) {
	http.SetCookie(writer, &http.Cookie{
		Name:     cookieSession,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// clearSessionCookie removes the session cookie. Signing out deletes the row as
// well; this is what stops the browser presenting a token that no longer resolves.
func clearSessionCookie(writer http.ResponseWriter) {
	http.SetCookie(writer, &http.Cookie{
		Name:     cookieSession,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// setFlash carries one catalogue KEY across a redirect.
//
// IT CARRIES A KEY AND NEVER A STRING, so nothing a reader sees can be put there
// by whoever sends them a link: the value is compared against a closed list on the
// way out and dropped if it is not on it. It is a cookie rather than a query
// parameter so the address bar carries nothing.
func setFlash(writer http.ResponseWriter, key messageKey) {
	http.SetCookie(writer, &http.Cookie{
		Name:     cookieNotice,
		Value:    string(key),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// takeFlash reads and clears a carried key, dropping anything that is not on the
// closed list.
func takeFlash(writer http.ResponseWriter, request *http.Request) messageKey {
	cookie, err := request.Cookie(cookieNotice)
	if err != nil || cookie.Value == "" {
		return ""
	}
	http.SetCookie(writer, &http.Cookie{
		Name:     cookieNotice,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	for _, permitted := range flashKeys() {
		if string(permitted) == cookie.Value {
			return permitted
		}
	}
	return ""
}

// flashKeys is the closed list of keys a redirect may carry.
func flashKeys() []messageKey {
	return append(noticeKeys(),
		msgSetupClosed, msgSessionExpired, msgSignInRefused, msgKeyFileRefused)
}

// noticeKeys is the subset of flashKeys that reports something the reader asked
// for having happened. A key outside it renders as a refusal rather than a notice,
// which is how one cookie carries both without a second one.
func noticeKeys() []messageKey {
	return []messageKey{msgSetupDone, msgSettingsSaved, msgPasswordReset, msgSignedOut}
}

// isNotice reports whether a carried key is a notice rather than a refusal.
func isNotice(key messageKey) bool {
	for _, candidate := range noticeKeys() {
		if candidate == key {
			return true
		}
	}
	return false
}

// refuseUnauthenticated turns away a request with no live session.
//
// IT SERVES NO CONTENT. A reader with no session is sent to the surface that
// applies: setup when the installation has no account yet, sign-in otherwise. A
// session that EXPIRED is told so, which is a different sentence from never having
// had one.
func (s *Server) refuseUnauthenticated(writer http.ResponseWriter, request *http.Request,
	reason error) {
	clearSessionCookie(writer)
	if errors.Is(reason, auth.ErrSessionExpired) {
		s.redirectWithError(writer, request, PathSignIn, msgSessionExpired)
		return
	}
	exists, err := s.AccountExists(request.Context())
	if err != nil {
		s.failInternal(writer, request, err)
		return
	}
	if !exists {
		http.Redirect(writer, request, PathSetup, http.StatusSeeOther)
		return
	}
	http.Redirect(writer, request, PathSignIn, http.StatusSeeOther)
}

// refuseSetupClosed turns away a request to the setup surface after an account
// exists. A GET is redirected; a POST is refused outright with 403, so nothing
// about it can read as having half-succeeded.
func (s *Server) refuseSetupClosed(writer http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodGet {
		s.redirectWithError(writer, request, PathSignIn, msgSetupClosed)
		return
	}
	s.renderRefusal(writer, request, http.StatusForbidden, pageSignIn, msgSetupClosed)
}

// refuseFormToken turns away a mutating request whose form token is missing,
// wrong, or another session's.
func (s *Server) refuseFormToken(writer http.ResponseWriter, request *http.Request) {
	s.renderRefusal(writer, request, http.StatusForbidden, s.pageFor(request), msgFormTokenRefused)
}

// refuseKeyFile turns away a reset that did not present the key file.
func (s *Server) refuseKeyFile(writer http.ResponseWriter, request *http.Request) {
	s.renderRefusal(writer, request, http.StatusForbidden, pageRecover, msgKeyFileRefused)
}

// pageFor is the page a refusal on this path should be rendered as, so the reader
// lands on the form they were filling in rather than somewhere else.
func (s *Server) pageFor(request *http.Request) string {
	switch request.URL.Path {
	case PathSetup:
		return pageSetup
	case PathSettings:
		return pageSettings
	case PathRecover:
		return pageRecover
	default:
		return pageSignIn
	}
}
