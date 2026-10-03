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
	// Surface is the page's own class on <main>, so the stylesheet can size one
	// surface differently from another.
	//
	// IT IS A CLASS AND NOT A WIDTH. An authentication surface is two fields and a
	// button and wants a narrow column; the settings surface is three cards of
	// configuration and wants the wider one. Carrying the page name lets the
	// stylesheet make that distinction in one place instead of each template
	// carrying inline geometry.
	Surface string
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
	// CertificateFingerprint is the pinned SHA-256 fingerprint, pre-filled for the
	// same reason: it is a hash of a certificate the firewall shows to anybody, so
	// it is configuration and not a secret.
	CertificateFingerprint string
	// The three states the settings surface reports. Each is a key.
	CredentialState   messageKey
	VerificationState messageKey
	LicenceKeyState   messageKey
	// Themes are the options the selector offers.
	Themes []themeOption

	// CollectionPath is where the collection surface lives, for the top bar's link
	// and for each card's form.
	//
	// THE TOP BAR CARRIES A LINK PER SURFACE A SIGNED-IN READER MAY REACH. A route
	// reachable only by typing its path is a route nobody finds, and the second
	// surface is what made the bar need links at all.
	CollectionPath string
	// CollectionCards are the collection surface's cards, one per source kind the
	// provider table holds, in the order internal/sizing registers them. A kind
	// with no provider row produces no card. Every figure in them is read from the
	// local database; drawing them contacts nothing.
	CollectionCards []collectionCard
}

// figureRow is one labelled figure on the collection surface: a measured or
// configured value, or the named state shown in its place.
type figureRow struct {
	// LabelKey names the figure.
	LabelKey messageKey
	// Value is the figure, formatted, when there is one to show.
	Value string
	// StateKey replaces Value when there is none. Exactly one of the two is set.
	StateKey messageKey
}

// gapRow is one gap reason found in the measured window.
type gapRow struct {
	// ReasonKey names the reason, from the schema's closed vocabulary.
	ReasonKey messageKey
	// Count is how many gaps of that reason the window holds.
	Count string
	// MissedSeconds is the time they cover.
	MissedSeconds string
}

// implementationRow is one implementation of a card's kind, with the operator's
// selection of it as the form shows it: stored, or typed into a refused form.
type implementationRow struct {
	// LabelKey names the implementation.
	LabelKey messageKey
	// Field is the form field carrying its selection.
	Field string
	// Options are the three selections, one of them selected.
	Options []selectionOption
}

// selectionOption is one of the three selections an implementation can have.
type selectionOption struct {
	// Value is auto, on or off, as config.Selection spells it.
	Value string
	// LabelKey is the state the selection puts the implementation in.
	LabelKey messageKey
	// Selected marks the one in force, or typed.
	Selected bool
}

// collectionCard is one source kind on the collection surface.
type collectionCard struct {
	// Kind is the registry kind, which names the card's fields and anchors.
	Kind string
	// LabelKey names the kind to the reader.
	LabelKey messageKey
	// PageIsAdjustable is false for a read whose page is not a setting; the card
	// then says so where the page field would be, which is the only place that
	// answers why the form offers one field.
	PageIsAdjustable bool

	// InForce, Measurement and Suggestion are the card's three groups, drawn under
	// three headings so that the pair in force and the pair suggested can never be
	// read as one another. Each row is a label and either a figure or a named state:
	// where there is no figure there is a sentence saying why, never a nought.
	InForce     []figureRow
	Measurement []figureRow
	Suggestion  []figureRow
	// Gaps are the gap reasons of the measured window, one row each.
	Gaps []gapRow
	// Implementations are the kind's implementations in the provider table, each
	// with the operator's selection, in registry order.
	Implementations []implementationRow
	// SuggestionStateKey is the outcome of the derivation, as a state: one of the
	// sizing outcomes, each its own sentence.
	SuggestionStateKey messageKey
	// SuggestionExists says whether there is a pair to fill the fields with; the
	// fill control is disabled without one, because a control that is offered
	// changes what the product does.
	SuggestionExists bool

	// NoticeKey is a save or a fill confirmed in this card; RefusalKey a value
	// refused in it, against InvalidField, which names the field that caused it.
	// They are drawn inside the card they belong to: a banner at the top of a page
	// four cards tall would name neither the kind saved nor the field refused.
	NoticeKey    messageKey
	RefusalKey   messageKey
	InvalidField string
	// PageSizeField and IntervalField are what the two fields show: the pair in
	// force, or what was typed when a submission is refused, or the suggestion
	// when the reader asked for it.
	PageSizeField string
	IntervalField string

	// The form's field names, carried rather than spelled in the template, so the
	// handler that reads them and the page that writes them cannot disagree. Each
	// card is its own form, so a value refused on one kind leaves the others
	// saveable.
	FieldKind     string
	FieldPageSize string
	FieldInterval string
	FieldFill     string
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
		CollectionPath: PathCollection,
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
	switch page {
	case pageSettings:
		if err := s.fillSettings(request, &built, msgVerifyNotAttempted); err != nil {
			s.failInternal(writer, request, err)
			return
		}
	case pageCollection:
		if err := s.fillCollection(request.Context(), &built, nil, nil); err != nil {
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
	case pageCollection:
		return msgTitleCollection
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
