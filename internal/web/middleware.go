package web

import (
	"context"
	"crypto/subtle"
	"errors"
	"mime"
	"net/http"
	"net/url"

	"github.com/NaejEL/opnview/internal/auth"
	"github.com/NaejEL/opnview/internal/store/accounts"
	"github.com/NaejEL/opnview/internal/widget"
)

// The middleware every route passes through.
//
// THE ORDER IS LOAD-BEARING, and it is the order below:
//
//  1. the setup-closing guard, which is a COUNT AGAINST THE DATABASE and runs on
//     every method of the setup routes;
//  2. the session, resolved for every route — required where the route is
//     authenticated, read opportunistically otherwise so that a form token is compared
//     against the session's own;
//  3. the CSRF proof, on every mutating request;
//  4. the key file, on the one route that is authorised by possession of it.
//
// A handler therefore never decides whether it may run. That is the point: a
// decision written into a handler is a decision the next handler can forget.
//
// EVERY REFUSAL IS ANSWERED IN JSON. No surface draws a page any more: the interface is the
// front's shell, a public document carrying no data, and every route a reader's data or a
// write passes through answers JSON. A refusal is never a redirect.

// sessionContextKey is how a resolved session reaches a handler.
type sessionContextKey struct{}

// sessionFrom returns the session a request was resolved to, if any.
func sessionFrom(request *http.Request) *accounts.Session {
	session, present := request.Context().Value(sessionContextKey{}).(*accounts.Session)
	if !present {
		return nil
	}
	return session
}

// guard wraps one route's handler in the middleware its access level requires.
func (s *Server) guard(route Route) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		ctx := request.Context()

		// 1. The setup closes permanently once an account exists.
		//
		// DERIVED FROM THE DATABASE, ON EVERY METHOD, ON EVERY REQUEST. Not a flag
		// set at start-up, which a restart would reset; not a check in one handler,
		// which would leave another open. A test asserts the closure with a valid
		// token, with a spent one, and again after a restart against the same data
		// directory.
		if route.RequiresNoAccount {
			exists, err := s.AccountExists(ctx)
			if err != nil {
				FailJSON(writer, request, err)
				return
			}
			if exists {
				RefuseJSON(writer, http.StatusForbidden, ConditionSetupClosed, "")
				return
			}
		}

		// 2. The session.
		session, err := s.resolveSession(request)
		if route.Access == AccessAuthenticated && session == nil {
			if errors.Is(err, auth.ErrSessionExpired) {
				clearSessionCookie(writer)
			}
			refuseUnauthenticatedJSON(writer, err)
			return
		}
		if session != nil {
			request = request.WithContext(
				context.WithValue(request.Context(), sessionContextKey{}, session))
		}

		// 3. The form token, on every mutating request, carried as a header beside a
		// same-origin JSON body (jsonProofValid).
		if route.Method == http.MethodPost && !s.jsonProofValid(request, session) {
			widget.WriteJSON(writer, http.StatusForbidden, widget.Failure{Condition: ConditionCSRFRefused})
			return
		}

		// 4. The key file, on the one route authorised by possession of it, presented as
		// the body's "key_file" member; the handler still reads the whole body.
		if route.Access == AccessKeyFile {
			var presented string
			presented, request = keyFileFromJSON(request)
			if !s.keyFileMatches(presented) {
				RefuseJSON(writer, http.StatusForbidden, ConditionKeyFileRefused, "")
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
func (s *Server) resolveSession(request *http.Request) (*accounts.Session, error) {
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

// HeaderCSRFToken is the header a JSON write carries its session's form token in.
const HeaderCSRFToken = "X-CSRF-Token"

// ConditionCSRFRefused is the condition a JSON write is refused with when its proof is missing,
// wrong, another session's, or sent from another origin.
const ConditionCSRFRefused = "csrf_refused"

// jsonProofValid is the form token rule for a write. With a session the expected token is the
// session's own; it travels in a header, which a page of another origin cannot set without the
// browser asking this server first; the body is JSON, which no plain HTML form can send; and an
// Origin header, when the browser sends one, names this server's own host.
//
// WITHOUT A SESSION -- the setup, sign-in and reset, the only writes a reader with no session
// reaches -- the expected token is the one in this browser's own cookie. The application reads
// it from GET /api/session, which sets that cookie when there is none.
//
// WHICH TOKEN IS THE EXPECTED ONE DEPENDS ON WHETHER THERE IS A SESSION, and that is what makes
// "a token belonging to another session is refused" hold. The comparison is constant-time.
func (s *Server) jsonProofValid(request *http.Request, session *accounts.Session) bool {
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

// formToken returns the token a write of this reader must carry, setting the cookie the
// writes made with no session are compared against.
//
// A signed-in reader gets the session's own token and no cookie is written: the
// row already holds it.
func (s *Server) formToken(writer http.ResponseWriter, request *http.Request,
	session *accounts.Session) (string, error) {
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

// SessionFrom returns the session a request was resolved to, if any. The middleware has
// resolved it before a surface's handler runs.
func SessionFrom(request *http.Request) *accounts.Session { return sessionFrom(request) }

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

// refuseUnauthenticatedJSON turns away a request with no live session: 401, and the condition
// saying whether a session expired or there never was one. It serves no data.
func refuseUnauthenticatedJSON(writer http.ResponseWriter, reason error) {
	condition := conditionNoSession
	if errors.Is(reason, auth.ErrSessionExpired) {
		condition = conditionSessionExpired
	}
	widget.WriteJSON(writer, http.StatusUnauthorized, widget.Failure{Condition: condition})
}
