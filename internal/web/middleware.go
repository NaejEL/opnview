package web

import (
	"context"
	"crypto/subtle"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"sort"

	"github.com/NaejEL/opnview/internal/auth"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/widget"
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
				if route.JSON {
					RefuseJSON(writer, http.StatusForbidden, ConditionSetupClosed, "")
					return
				}
				s.refuseSetupClosed(writer, request)
				return
			}
		}

		// 2. The session.
		session, err := s.resolveSession(request)
		if route.Access == AccessAuthenticated && session == nil {
			if route.JSON {
				refuseUnauthenticatedJSON(writer, err)
				return
			}
			s.refuseUnauthenticated(writer, request, err)
			return
		}
		if session != nil {
			request = request.WithContext(
				context.WithValue(request.Context(), sessionContextKey{}, session))
		}

		// 3. The form token, on every mutating request: a page's form carries it as a field, a JSON
		// write as a header beside a same-origin JSON body (jsonProofValid).
		if route.Method == http.MethodPost && route.JSON {
			if !s.jsonProofValid(request, session) {
				widget.WriteJSON(writer, http.StatusForbidden, widget.Failure{Condition: ConditionCSRFRefused})
				return
			}
		} else if route.Method == http.MethodPost {
			if err := request.ParseForm(); err != nil {
				s.refuseFormToken(writer, request, route)
				return
			}
			if !s.formTokenValid(request, session) {
				s.refuseFormToken(writer, request, route)
				return
			}
		}

		// 4. The key file, on the one route authorised by possession of it.
		if route.Access == AccessKeyFile {
			// A page presents the key file as a form field, a JSON write as its body's
			// "key_file" member; the handler still reads the whole body.
			presented := ""
			if route.JSON {
				presented, request = keyFileFromJSON(request)
			} else {
				presented = request.PostFormValue("key_file")
			}
			if !s.keyFileMatches(presented) {
				if route.JSON {
					RefuseJSON(writer, http.StatusForbidden, ConditionKeyFileRefused, "")
					return
				}
				s.refuseKeyFile(writer, request, route)
				return
			}
		}

		route.Handler(writer, request)
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

// HeaderCSRFToken is the header a JSON write carries its session's form token in.
const HeaderCSRFToken = "X-CSRF-Token"

// ConditionCSRFRefused is the condition a JSON write is refused with when its proof is missing,
// wrong, another session's, or sent from another origin.
const ConditionCSRFRefused = "csrf_refused"

// jsonProofValid is the form token rule for a JSON write. With a session the expected token is the
// session's own; it travels in a header, which
// a page of another origin cannot set without the browser asking this server first; the body is
// JSON, which no plain HTML form can send; and an Origin header, when the browser sends one, names
// this server's own host.
//
// WITHOUT A SESSION -- the JSON setup, sign-in and reset, the only JSON writes a reader with no
// session reaches -- the expected token is the one in this browser's own cookie, exactly as a
// page's form is compared with it (formTokenValid); the application reads it from
// GET /api/session, which sets that cookie as a page draw does.
func (s *Server) jsonProofValid(request *http.Request, session *store.Session) bool {
	expected := ""
	if session != nil {
		expected = session.CSRFToken
	} else if cookie, err := request.Cookie(cookieCSRF); err == nil {
		expected = cookie.Value
	}
	if expected == "" {
		return false
	}
	if mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type")); err != nil ||
		mediaType != "application/json" {
		return false
	}
	if origin := request.Header.Get("Origin"); origin != "" {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Host != request.Host {
			return false
		}
	}
	if request.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	submitted := request.Header.Get(HeaderCSRFToken)
	return submitted != "" && subtle.ConstantTimeCompare([]byte(submitted), []byte(expected)) == 1
}

// keyFileMatches reports whether what the request presented is the contents of the key
// file beside the database.
//
// A MISSING KEY FILE REFUSES RATHER THAN ACCEPTING ANYTHING. If this installation
// has no key file, there is nothing to possess and so nothing authorises a reset:
// the box is nil and every submission fails.
func (s *Server) keyFileMatches(presented string) bool {
	if s.box == nil {
		return false
	}
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
//
// The list is closed over the keys the core and the mounted surfaces declared as
// carriable (Surface.Notices, Surface.Refusals), so a value a reader was sent in a link
// can only ever be one of those.
func (s *Server) takeFlash(writer http.ResponseWriter, request *http.Request) MessageKey {
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
	carried := MessageKey(cookie.Value)
	if _, permitted := s.notices[carried]; permitted {
		return carried
	}
	if _, permitted := s.refusals[carried]; permitted {
		return carried
	}
	return ""
}

// FlashKeys returns the closed list of keys a redirect may carry, sorted, for a test.
func (s *Server) FlashKeys() []MessageKey {
	var keys []MessageKey
	for key := range s.notices {
		keys = append(keys, key)
	}
	for key := range s.refusals {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

// isNotice reports whether a carried key is a notice rather than a refusal. A key outside
// the notices renders as a refusal, which is how one cookie carries both without a second
// one.
func (s *Server) isNotice(key MessageKey) bool {
	_, notice := s.notices[key]
	return notice
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
	s.RenderRefusal(writer, request, http.StatusForbidden, PageSignIn, msgSetupClosed)
}

// RefuseSetupClosed is refuseSetupClosed for a surface: the account surface answers a lost
// race for the first account with the same permanent closure the middleware does.
func (s *Server) RefuseSetupClosed(writer http.ResponseWriter, request *http.Request) {
	s.refuseSetupClosed(writer, request)
}

// refuseFormToken turns away a mutating request whose form token is missing,
// wrong, or another session's. The refusal is drawn on the page the route names, so the
// reader lands on the form they were filling in.
func (s *Server) refuseFormToken(writer http.ResponseWriter, request *http.Request, route Route) {
	s.RenderRefusal(writer, request, http.StatusForbidden, pageOf(route), msgFormTokenRefused)
}

// refuseKeyFile turns away a reset that did not present the key file.
func (s *Server) refuseKeyFile(writer http.ResponseWriter, request *http.Request, route Route) {
	s.RenderRefusal(writer, request, http.StatusForbidden, pageOf(route), msgKeyFileRefused)
}

// pageOf is the page a refusal on this route is drawn on: the one the route names, or the
// sign-in page.
func pageOf(route Route) string {
	if route.Page != "" {
		return route.Page
	}
	return PageSignIn
}

// SessionFrom returns the session a request was resolved to, if any. The middleware has
// resolved it before a surface's handler runs.
func SessionFrom(request *http.Request) *store.Session { return sessionFrom(request) }

// SetSessionCookie puts a freshly issued session token in the cookie.
func SetSessionCookie(writer http.ResponseWriter, token string) { setSessionCookie(writer, token) }

// ClearSessionCookie removes the session cookie.
func ClearSessionCookie(writer http.ResponseWriter) { clearSessionCookie(writer) }

// The conditions an unauthenticated API request is refused with (decision 4 of
// specs/SPEC-step-5b-widget-api.md).
const (
	conditionNoSession      = "no_session"
	conditionSessionExpired = "session_expired"
)

// refuseUnauthenticatedJSON turns away an API request with no live session: 401, and the
// condition saying whether a session expired or there never was one. It serves no data.
func refuseUnauthenticatedJSON(writer http.ResponseWriter, reason error) {
	condition := conditionNoSession
	if errors.Is(reason, auth.ErrSessionExpired) {
		condition = conditionSessionExpired
	}
	widget.WriteJSON(writer, http.StatusUnauthorized, widget.Failure{Condition: condition})
}
