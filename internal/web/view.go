package web

import (
	"fmt"
	"net/http"
	"os"

	"github.com/NaejEL/opnview/internal/store"
)

// view is everything a template may read.
//
// IT CARRIES KEYS, NOT SENTENCES. Notice, Error and every *State field is a
// messageKey, resolved against the catalogue by the template. That is what makes
// "no handler emits a user-visible literal" a property rather than a habit: a
// handler that wanted to say something would have to add a catalogue entry to say
// it with.
//
// IT CARRIES NO SECRET. There is no field here for an API secret, a MaxMind key, a
// password, a salt, a parameter set or a key file, and a test asserts that no
// response body contains any of them.
type view struct {
	// Language is what the document's lang attribute carries.
	Language string
	// ThemeAttribute is the data-theme value, or the empty string when the theme
	// follows the operating system — which is the first-launch state.
	ThemeAttribute string
	// TitleKey names the page. It is a catalogue key like every other string here.
	TitleKey messageKey
	// StylesheetPath is the one asset a page loads, served by this binary.
	StylesheetPath string
	// SignedIn says whether to draw the sign-out control.
	SignedIn bool
	// CSRFToken is the token every mutating form carries.
	CSRFToken string
	// Notice reports something that happened; Error reports something refused.
	Notice messageKey
	Error  messageKey

	// The paths, so a template never spells one itself.
	SetupPath    string
	SignInPath   string
	SignOutPath  string
	SettingsPath string
	RecoverPath  string

	// Login is what the reader typed, put back so a refusal does not make them
	// type it again. A password is never put back.
	Login string

	// FirewallURL and APIKey are configuration rather than secrets, so they are
	// pre-filled: a reader correcting a typo has to be able to see the typo.
	FirewallURL string
	APIKey      string
	// The three states the settings surface reports. Each is a key.
	CredentialState   messageKey
	VerificationState messageKey
	LicenceKeyState   messageKey
	// Themes are the options the selector offers.
	Themes []themeOption
}

// themeOption is one row of the theme selector.
type themeOption struct {
	// Value is what the form submits and what the setting row holds.
	Value Theme
	// Label is the catalogue key naming it.
	Label messageKey
	// Selected marks the one in force.
	Selected bool
}

// newView returns a view with everything common to every page filled in.
func (s *Server) newView(writer http.ResponseWriter, request *http.Request,
	session *store.Session, titleKey messageKey) (view, error) {
	token, err := s.formToken(writer, request, session)
	if err != nil {
		return view{}, err
	}
	built := view{
		Language:       s.renderer.catalogue.Language,
		ThemeAttribute: s.readTheme(request.Context()).DocumentAttribute(),
		TitleKey:       titleKey,
		StylesheetPath: PathStylesheet,
		SignedIn:       session != nil,
		CSRFToken:      token,
		SetupPath:      PathSetup,
		SignInPath:     PathSignIn,
		SignOutPath:    PathSignOut,
		SettingsPath:   PathSettings,
		RecoverPath:    PathRecover,
	}
	if carried := takeFlash(writer, request); carried != "" {
		if isNotice(carried) {
			built.Notice = carried
		} else {
			built.Error = carried
		}
	}
	return built, nil
}

// redirectWithError sends the reader somewhere else, carrying a refusal.
func (s *Server) redirectWithError(writer http.ResponseWriter, request *http.Request,
	path string, key messageKey) {
	setFlash(writer, key)
	http.Redirect(writer, request, path, http.StatusSeeOther)
}

// redirectWithNotice sends the reader somewhere else, carrying a notice.
func (s *Server) redirectWithNotice(writer http.ResponseWriter, request *http.Request,
	path string, key messageKey) {
	setFlash(writer, key)
	http.Redirect(writer, request, path, http.StatusSeeOther)
}

// renderRefusal draws one page with a refusal on it, at the status the refusal
// deserves.
//
// A REFUSAL IS NOT AN HTTP 200 WITH A SAD SENTENCE ON IT. The status is what a
// test asserts against, and what a reverse proxy and a log will record.
func (s *Server) renderRefusal(writer http.ResponseWriter, request *http.Request,
	status int, page string, key messageKey) {
	session := sessionFrom(request)
	built, err := s.newView(writer, request, session, titleKeyFor(page))
	if err != nil {
		s.failInternal(writer, request, err)
		return
	}
	built.Error = key
	if page == pageSettings {
		if err := s.fillSettings(request, &built, msgVerifyNotAttempted); err != nil {
			s.failInternal(writer, request, err)
			return
		}
	}
	if err := s.renderer.render(writer, status, page, built); err != nil {
		s.failInternal(writer, request, err)
	}
}

// titleKeyFor is the catalogue key naming one page.
func titleKeyFor(page string) messageKey {
	switch page {
	case pageSetup:
		return msgTitleSetup
	case pageSettings:
		return msgTitleSettings
	case pageRecover:
		return msgTitleRecover
	default:
		return msgTitleSignIn
	}
}

// failInternal is the last resort: something opnview could not do at all.
//
// IT WRITES A STATUS AND A CATALOGUE STRING, never the error. An error text is
// opnview's own diagnostic, it can name a path or a column, and it belongs on the
// console the operator reads rather than in a page anybody can reach. The console
// line is the one place the detail goes.
func (s *Server) failInternal(writer http.ResponseWriter, request *http.Request, err error) {
	fmt.Fprintf(os.Stderr, "opnview: web: %s %s: %v\n", request.Method, request.URL.Path, err)
	text, lookupErr := s.renderer.catalogue.translate(string(msgInternal))
	if lookupErr != nil {
		// The catalogue itself is unusable. There is nothing left to say in the
		// reader's language, so say nothing but the status.
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusInternalServerError)
	_, _ = writer.Write([]byte(text))
}
