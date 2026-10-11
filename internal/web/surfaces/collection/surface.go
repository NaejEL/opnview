// Package collection is the collection surface: per source kind, the pair that governs its
// polling, the record rate opnview has measured, and the pair it would suggest, and the
// settings cards that govern what collection does with what it reads. It reads the local
// database and writes setting rows only; nothing on it can reach the firewall.
package collection

import (
	"embed"
	"net/http"
	"time"

	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/web"
)

// The surface's files: its page template, its catalogue file and its marker.
//
//go:embed template locale surface.json
var files embed.FS

type messageKey = web.MessageKey

// PathCollection is where the operator sees, per source kind, the pair that governs its
// polling, the record rate opnview has measured, and the pair it would suggest, and saves
// a new pair.
//
// It is a route of its own rather than a card on the settings surface: the settings surface
// is where credentials enter, and this one reads the database and writes configuration
// rows only.
const PathCollection = "/collection"

// PathCollectionSettings is where a settings card of the collection surface is saved: a
// polled kind's interval and selections, or a group of settings. It is its own route so the
// sized kinds' handler stays the one it was.
const PathCollectionSettings = "/collection/settings"

// querySaved names, in the redirect after a saved edit, the card the edit was made in.
const querySaved = "saved"

// pageCollection is the surface's one page.
const pageCollection = "collection"

func init() {
	web.RegisterSurface(web.Surface{
		Name:  "collection",
		Files: files,
		Pages: []web.Page{{Name: pageCollection, TitleKey: msgTitleCollection}},
		Routes: func(host web.Host) []web.Route {
			s := newSurface(host)
			return []web.Route{
				// The collection surface reads the local database and writes setting rows only.
				{Method: http.MethodGet, Pattern: PathCollection, Access: web.AccessAuthenticated,
					Handler: s.handleCollectionForm},
				{Method: http.MethodPost, Pattern: PathCollection, Access: web.AccessAuthenticated,
					Handler: s.handleCollectionSubmit},
				{Method: http.MethodPost, Pattern: PathCollectionSettings, Access: web.AccessAuthenticated,
					Handler: s.handleCollectionSettingsSubmit},
			}
		},
		Tab:  &web.Tab{Page: pageCollection, Path: PathCollection, TitleKey: msgTitleCollection, Order: 20},
		Keys: func() []web.MessageKey { return keys() },
	})
}

// surface is the surface's handlers over what the host grants.
type surface struct {
	web.Host
	store     *store.Store
	now       func() time.Time
	collector web.Reconfigurable
}

// newSurface binds the handlers to a host.
func newSurface(host web.Host) *surface {
	return &surface{Host: host, store: host.Store(), now: host.Now, collector: host.Collector()}
}

// view is everything the surface's template may read.
type view struct {
	web.Base

	// CollectionPath is where the collection surface lives, for each card's form.
	CollectionPath string
	// CollectionCards are the surface's sized kinds' cards, one per source kind the provider
	// table holds, in the order internal/sizing registers them. A kind with no provider row
	// produces no card. Every figure in them is read from the local database; drawing them
	// contacts nothing.
	CollectionCards []collectionCard
	// CollectionEntries are every card of the surface in page order: the sized kinds' cards
	// above, with the settings cards placed after the card each belongs beside
	// (collection_settings.go). The template draws these.
	CollectionEntries []collectionEntry
	// CollectionSettingsPath is where a settings card's form is saved.
	CollectionSettingsPath string
	// CollectionIndex heads the surface: one link per card, in page order.
	CollectionIndex []indexLink
}

// hasUnsavedEdits says whether any card of the page is drawn with an edit that is not
// saved: one a redirect would discard.
func (built view) hasUnsavedEdits() bool {
	for _, entry := range built.CollectionEntries {
		if (entry.Sizing != nil && entry.Sizing.Edited) || (entry.Settings != nil && entry.Settings.Edited) {
			return true
		}
	}
	return false
}

// newView returns a view with everything common to every page filled in.
func (s *surface) newView(writer http.ResponseWriter, request *http.Request,
	session *store.Session, title messageKey) (view, error) {
	base, err := s.NewBase(writer, request, session, title)
	if err != nil {
		return view{}, err
	}
	return view{Base: base, CollectionPath: PathCollection, CollectionSettingsPath: PathCollectionSettings}, nil
}

// figureRow is one labelled figure on the collection surface: a measured or
// configured value, or the named state shown in its place.
type figureRow struct {
	// LabelKey names the figure.
	LabelKey messageKey
	// Value is the figure, formatted, when there is one to show.
	Value string
	// Duration replaces Value for a figure that is a length of time, spelled in days,
	// hours, minutes and seconds.
	Duration []web.DurationPart
	// StateKey replaces Value when there is none. Exactly one of the three is set.
	StateKey messageKey
}

// gapRow is one gap reason found in the measured window.
type gapRow struct {
	// ReasonKey names the reason, from the schema's closed vocabulary.
	ReasonKey messageKey
	// Count is how many gaps of that reason the window holds.
	Count string
	// Missed is the time they cover, spelled in days, hours, minutes and seconds.
	Missed []web.DurationPart
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
	// RefusalKey refuses what was selected, against the select.
	RefusalKey messageKey
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

	// NoticeKey is a save or a fill confirmed in this card. A refused value is marked on
	// its own field. Both are drawn inside the card they belong to: a banner at the top
	// of a page four cards tall would name neither the kind saved nor the field refused.
	NoticeKey messageKey
	// Edited says the card is drawn with values a submission carried that are not
	// stored: another card was saved, and this one was not.
	Edited bool
	// PageSize and Interval are the two fields: the pair in force, or what was typed
	// and not stored, or the suggestion when the reader asked for it. The interval is a
	// figure beside its unit, as every duration on the page is.
	PageSize web.SettingField
	Interval web.SettingField

	// The names of the two buttons a card carries, carried rather than spelled in the
	// template, so the handler that reads them and the page that writes them cannot
	// disagree. The whole page is one form; each button sends the card it belongs to.
	FieldKind string
	FieldFill string
}
