package web

import (
	"fmt"
	"net/http"
	"os"

	"github.com/NaejEL/opnview/internal/web/messages"

	"github.com/NaejEL/opnview/internal/store/accounts"
)

// Base is the part of every page's data that is common to every page. A surface's page data
// is a struct that embeds it, and passes a pointer to that struct to Host.Render:
//
//	type view struct {
//		web.Base
//		// the surface's own fields
//	}
//
// IT CARRIES KEYS, NOT SENTENCES. Notice, Error and the tabs' titles are MessageKeys,
// resolved against the catalogue by the template. That is what makes "no handler emits a
// user-visible literal" a property rather than a habit: a handler that wanted to say
// something would have to add a catalogue entry to say it with.
//
// IT CARRIES NO SECRET. There is no field here for an API secret, a MaxMind key, a
// password, a salt, a parameter set or a key file, and a test asserts that no response body
// contains any of them.
type Base struct {
	// Language is what the document's lang attribute carries.
	Language string
	// ThemeAttribute is the data-theme value, or the empty string when the theme
	// follows the operating system — which is the first-launch state.
	ThemeAttribute string
	// TitleKey names the page. It is a catalogue key like every other string here.
	TitleKey MessageKey
	// StylesheetPath is the one asset a page loads, served by this binary.
	StylesheetPath string
	// Surface is the page's own class on <main>, so the stylesheet can size one
	// surface differently from another.
	//
	// IT IS A CLASS AND NOT A WIDTH. An authentication surface is two fields and a
	// button and wants a narrow column; the settings surface is cards of
	// configuration and wants the wider one. Carrying the page name lets the
	// stylesheet make that distinction in one place instead of each template
	// carrying inline geometry.
	Surface string
	// SignedIn says whether to draw the sign-out control.
	SignedIn bool
	// CSRFToken is the token every mutating form carries.
	CSRFToken string
	// Notice reports something that happened; Error reports something refused.
	Notice MessageKey
	Error  MessageKey
	// InlineNotices says the page draws its own notice and refusal inside the card they are
	// about, so the layout draws neither at the head of the page.
	InlineNotices bool

	// The paths the core knows, so a template never spells one itself.
	SetupPath    string
	SignInPath   string
	SignOutPath  string
	SettingsPath string
	RecoverPath  string

	// Nav is the top bar's tabs, one per surface that declares one, in order; the one of the
	// page being read is marked Current.
	Nav []NavItem
	// Indicators are the figures and states surfaces draw in the top bar of a signed-in page.
	Indicators []Indicator
}

// NavItem is one tab of the top bar as a template draws it.
type NavItem struct {
	Path     string
	TitleKey MessageKey
	Page     string
	// Current is true for the tab of the page being read.
	Current bool
}

// setSurface stamps the page's class and marks its tab. Render calls it.
func (b *Base) setSurface(page string) {
	b.Surface = "surface-" + page
	for index := range b.Nav {
		b.Nav[index].Current = b.Nav[index].Page == page
	}
}

// NewBase returns the common part of a page's data with everything filled in.
func (s *Server) NewBase(writer http.ResponseWriter, request *http.Request,
	session *accounts.Session, title MessageKey) (Base, error) {
	token, err := s.formToken(writer, request, session)
	if err != nil {
		return Base{}, err
	}
	built := Base{
		Language:       s.renderer.catalogue.Language,
		ThemeAttribute: s.ReadTheme(request.Context()).DocumentAttribute(),
		TitleKey:       title,
		StylesheetPath: PathStylesheet,
		SignedIn:       session != nil,
		CSRFToken:      token,
		SetupPath:      PathSetup,
		SignInPath:     PathSignIn,
		SignOutPath:    PathSignOut,
		SettingsPath:   PathSettings,
		RecoverPath:    PathRecover,
	}
	if session != nil {
		for _, tab := range s.tabs {
			built.Nav = append(built.Nav, NavItem{Path: tab.Path, TitleKey: tab.TitleKey, Page: tab.Page})
		}
		// The indicators are in the bar of every signed-in page, and only there: a reader with
		// no session is told nothing about the installation.
		//
		// A FAILED READ IS A STATE OF THE INDICATOR, NOT OF THE PAGE. The bar is on every
		// signed-in page, so an indicator that failed the page would take the whole interface
		// down with one unreadable table; it names its own state instead -- never nought, which
		// would claim nothing is wrong -- and the cause goes to the console.
		for _, source := range s.indicators {
			indicator, err := source.read(request.Context(), s)
			if err != nil {
				fmt.Fprintf(os.Stderr, "opnview: web: reading the %s surface's indicator: %v\n", source.surface, err)
				indicator = source.unknown
			}
			built.Indicators = append(built.Indicators, indicator)
		}
	}
	if carried := s.takeFlash(writer, request); carried != "" {
		if s.isNotice(carried) {
			built.Notice = carried
		} else {
			built.Error = carried
		}
	}
	return built, nil
}

// Render writes one page; see renderer.render. data must be a pointer to a struct that
// embeds Base.
func (s *Server) Render(writer http.ResponseWriter, status int, page string, data any) error {
	return s.renderer.render(writer, status, page, data)
}

// RedirectWithError sends the reader somewhere else, carrying a refusal.
func (s *Server) RedirectWithError(writer http.ResponseWriter, request *http.Request,
	path string, key MessageKey) {
	setFlash(writer, key)
	http.Redirect(writer, request, path, http.StatusSeeOther)
}

// RedirectWithNotice sends the reader somewhere else, carrying a notice.
func (s *Server) RedirectWithNotice(writer http.ResponseWriter, request *http.Request,
	path string, key MessageKey) {
	setFlash(writer, key)
	http.Redirect(writer, request, path, http.StatusSeeOther)
}

// redirectWithError is the spelling the core's own files use.
func (s *Server) redirectWithError(writer http.ResponseWriter, request *http.Request,
	path string, key MessageKey) {
	s.RedirectWithError(writer, request, path, key)
}

// RenderRefusal draws one page with a refusal on it, at the status the refusal
// deserves.
//
// A REFUSAL IS NOT AN HTTP 200 WITH A SAD SENTENCE ON IT. The status is what a
// test asserts against, and what a reverse proxy and a log will record.
func (s *Server) RenderRefusal(writer http.ResponseWriter, request *http.Request,
	status int, page string, key MessageKey) {
	definition, known := s.pages[page]
	if !known {
		s.FailInternal(writer, request, fmt.Errorf("web: a refusal on the unknown page %q", page))
		return
	}
	session := sessionFrom(request)
	built, err := s.NewBase(writer, request, session, definition.TitleKey)
	if err != nil {
		s.FailInternal(writer, request, err)
		return
	}
	built.Error = key
	var data any = &built
	if definition.Refusal != nil {
		data, err = definition.Refusal(s, request, built)
		if err != nil {
			s.FailInternal(writer, request, err)
			return
		}
	}
	if err := s.renderer.render(writer, status, page, data); err != nil {
		s.FailInternal(writer, request, err)
	}
}

// FailInternal is the last resort: something opnview could not do at all.
//
// IT WRITES A STATUS AND A CATALOGUE STRING, never the error. An error text is
// opnview's own diagnostic, it can name a path or a column, and it belongs on the
// console the operator reads rather than in a page anybody can reach. The console
// line is the one place the detail goes.
func (s *Server) FailInternal(writer http.ResponseWriter, request *http.Request, err error) {
	fmt.Fprintf(os.Stderr, "opnview: web: %s %s: %v\n", request.Method, request.URL.Path, err)
	text, lookupErr := s.renderer.catalogue.translate(string(messages.MsgInternal))
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

// failInternal is the spelling the core's own files use.
func (s *Server) failInternal(writer http.ResponseWriter, request *http.Request, err error) {
	s.FailInternal(writer, request, err)
}
