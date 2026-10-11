package sources

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/web"
)

// The Sources surface: one row per registered provider, with its availability, and
// every collection gap opnview detected in the last day.
//
// IT IS THE SERVER-RENDERED COUNTERPART OF THE SOURCE AVAILABILITY PRESET
// (docs/widget-catalogue.md, Health and honesty, Source availability), and it uses
// that preset's binding and its condition rules: the conditions are rendered as
// VALUES -- not yet probed, installed and not collecting, healthy, unavailable --
// and a provider with no availability row at all reads "state unknown", never
// healthy. The keys and the read are the ones the step-7 widget reuses.
//
// IT READS THE LOCAL DATABASE AND NOTHING ELSE. Drawing it contacts no host.
//
// THE DETAIL COLUMN IS DATA. source_availability.detail and collection_gap.detail
// are free text a collector wrote, in English, for a person; they are rendered
// verbatim, escaped, marked as data and declared untranslated (translate="no"),
// which is the one declared exception to "every string comes from the catalogue".
// The at-a-glance reading is the availability value beside it, from the catalogue.

// recentGapWindow is how far back the Sources surface lists collection gaps: the
// maintainer's decision 5 of specs/SPEC-deferred-interface-bundle.md.
const recentGapWindow = 24 * time.Hour

// sourceRow is one provider on the Sources surface.
type sourceRow struct {
	// KindKey and ProviderKey name the registry row from the catalogue.
	KindKey     messageKey
	ProviderKey messageKey
	// Availability is the condition, as the preset names it, and Mark the class that
	// colours its square mark.
	Availability messageKey
	Mark         string
	// ActiveKey says whether opnview reads this provider; SelectionKey is the
	// operator's selection, as the state it puts the provider in.
	ActiveKey    messageKey
	SelectionKey messageKey
	// InOutage is whether the row counts towards an outage the indicator counts, and
	// OutageKey says so in a word.
	InOutage  bool
	OutageKey messageKey
	// Checked is the instant the row was determined, or CheckedState when it never
	// was.
	Checked      string
	CheckedState messageKey
	// ProbeParts and Detail are data, rendered verbatim; DetailState names a NULL
	// detail. The probe is cut after each slash, every character kept, so the template
	// can offer a line break between a path's components and never inside one.
	ProbeParts  []string
	Detail      string
	DetailState messageKey
	// Pages are, for a provider installed and not collecting, the OPNsense pages that
	// turn it on, on the configured firewall (opnsense_pages.go).
	Pages []opnsenseLink
}

// pathParts cuts a probe after each slash, keeping every character: joined, the
// parts are the probe exactly.
func pathParts(probe string) []string {
	var parts []string
	for probe != "" {
		cut := strings.IndexByte(probe, '/')
		if cut < 0 || cut == len(probe)-1 {
			parts = append(parts, probe)
			break
		}
		parts = append(parts, probe[:cut+1])
		probe = probe[cut+1:]
	}
	return parts
}

// outageRow is one current outage on the Sources surface: one line per unit of the
// indicator's figure. ProviderKey is empty for a kind's outage.
type outageRow struct {
	KindKey     messageKey
	ProviderKey messageKey
	StateKey    messageKey
}

// recentGapRow is one collection gap on the Sources surface.
type recentGapRow struct {
	KindKey     messageKey
	ProviderKey messageKey
	ReasonKey   messageKey
	Start       string
	End         string
	// Missed is the time the gap covers, in days, hours, minutes and seconds.
	Missed      []web.DurationPart
	Detail      string
	DetailState messageKey
}

// sourceStatus is one provider's availability beside the operator's selection of
// it: the two facts the outage rule reads.
type sourceStatus struct {
	row       store.SourceRow
	selection config.Selection
}

// readSourceStatuses reads every provider's availability and selection.
func (s *surface) readSourceStatuses(ctx context.Context) ([]sourceStatus, error) {
	rows, err := s.store.SourceAvailability(ctx)
	if err != nil {
		return nil, err
	}
	statuses := make([]sourceStatus, 0, len(rows))
	for _, row := range rows {
		selection, err := config.LoadSourceSelection(ctx, s.store, row.Kind, row.ProviderKey)
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, sourceStatus{row: row, selection: selection})
	}
	return statuses, nil
}

// isReachable is the one condition that reads as healthy.
func isReachable(row store.SourceRow) bool {
	return row.HasAvailability && row.State == store.StateReachable
}

// outage is one current outage, as the indicator counts it: a kind none of whose
// considered providers is reachable, or one provider the operator turned on that is
// not reachable. rows are the indexes, into the statuses read, of the providers it is
// about, so the Sources surface can mark exactly those rows.
type outage struct {
	kind string
	// provider is the provider turned on and failing, or empty for a kind's outage.
	provider string
	rows     []int
}

// findOutages applies the outage rule of specs/SPEC-deferred-interface-bundle.md,
// scope 4, decision 4:
//
//   - a KIND is in outage when at least one of its providers has a selection other
//     than off and none of those is reachable;
//   - and, beyond that, a PROVIDER is in outage when the operator turned it on and
//     it is not reachable;
//   - a provider still not yet probed is left out of both: nothing has been checked,
//     so nothing can be said to have failed.
//
// A provider turned on in a kind that is already in outage is not counted a second
// time: the kind's outage already covers it, and the figure is a count of things
// the operator has to look at. A provider with no availability row is not
// reachable, so it can be in outage; that row reads "state unknown", never healthy.
//
// A KIND'S OUTAGE NAMES EVERY PROVIDER IT WAS DECIDED ON -- each considered, none
// reachable -- and a provider's outage names that provider. Those are the rows the
// Sources surface marks, so the page the indicator opens shows which rows its figure
// counts; a row that is unavailable but counts towards nothing -- declined, or beside
// a reachable implementation of its kind -- stays unmarked.
func findOutages(statuses []sourceStatus) []outage {
	var kinds []string
	byKind := map[string][]int{}
	for index, status := range statuses {
		if _, seen := byKind[status.row.Kind]; !seen {
			kinds = append(kinds, status.row.Kind)
		}
		byKind[status.row.Kind] = append(byKind[status.row.Kind], index)
	}
	var outages []outage
	for _, kind := range kinds {
		var considered []int
		reachable := false
		for _, index := range byKind[kind] {
			status := statuses[index]
			if status.selection == config.SelectionOff || status.row.NotYetProbed() {
				continue
			}
			considered = append(considered, index)
			reachable = reachable || isReachable(status.row)
		}
		if len(considered) > 0 && !reachable {
			outages = append(outages, outage{kind: kind, rows: considered})
			continue
		}
		for _, index := range byKind[kind] {
			status := statuses[index]
			if status.selection == config.SelectionOn && !status.row.NotYetProbed() &&
				!isReachable(status.row) {
				outages = append(outages, outage{kind: kind, provider: status.row.ProviderKey,
					rows: []int{index}})
			}
		}
	}
	return outages
}

// countOutages is the number of current outages: the indicator's figure.
func countOutages(statuses []sourceStatus) int { return len(findOutages(statuses)) }

// indicator is the top bar's outage indicator: the count of current outages, or the named
// state at zero. It reads the database and contacts nothing. It is drawn on every signed-in
// page, which is why this surface contributes it to the bar rather than drawing it.
func (s *surface) indicator(ctx context.Context) (web.Indicator, error) {
	statuses, err := s.readSourceStatuses(ctx)
	if err != nil {
		return indicatorUnknown(), err
	}
	if count := countOutages(statuses); count > 0 {
		return web.Indicator{Path: PathSources, Class: "has-outages", TextKey: msgIndicatorOutages,
			Figure: strconv.Itoa(count)}, nil
	}
	return web.Indicator{Path: PathSources, TextKey: msgIndicatorNoOutage}, nil
}

// indicatorUnknown is the indicator whose read failed: a named state, never nought, which
// would claim nothing is wrong.
func indicatorUnknown() web.Indicator {
	return web.Indicator{Path: PathSources, Class: "outage-unknown", TextKey: msgIndicatorUnknown}
}

// handleSourcesForm draws the Sources surface.
func (s *surface) handleSourcesForm(writer http.ResponseWriter, request *http.Request) {
	built, err := s.newView(writer, request, web.SessionFrom(request), msgTitleSources)
	if err != nil {
		s.FailInternal(writer, request, err)
		return
	}
	if err := s.fillSources(request.Context(), &built); err != nil {
		s.FailInternal(writer, request, err)
		return
	}
	if err := s.Render(writer, http.StatusOK, pageSources, &built); err != nil {
		s.FailInternal(writer, request, err)
	}
}

// fillSources puts the provider rows and the recent gaps into a view.
func (s *surface) fillSources(ctx context.Context, built *view) error {
	statuses, err := s.readSourceStatuses(ctx)
	if err != nil {
		return err
	}
	// The firewall the OPNsense pages are on: the URL the operator entered, read here and
	// never contacted.
	baseURL, _, err := s.store.Setting(ctx, config.KeyOPNsenseBaseURL)
	if err != nil {
		return err
	}
	// The outages first: the list the indicator counts, and the rows each is about.
	counted := map[int]bool{}
	for _, found := range findOutages(statuses) {
		drawn := outageRow{KindKey: web.KindLabelKey(found.kind), StateKey: msgOutageKind}
		if found.provider != "" {
			drawn.ProviderKey = web.ProviderLabelKey(found.kind, found.provider)
			drawn.StateKey = msgOutageProvider
		}
		built.Outages = append(built.Outages, drawn)
		for _, index := range found.rows {
			counted[index] = true
		}
	}
	for index, status := range statuses {
		row := status.row
		availability, mark := web.AvailabilityOf(row)
		drawn := sourceRow{
			KindKey:      web.KindLabelKey(row.Kind),
			ProviderKey:  web.ProviderLabelKey(row.Kind, row.ProviderKey),
			Availability: availability,
			Mark:         mark,
			ActiveKey:    yesNo(row.IsActive),
			SelectionKey: selectionStateKey(status.selection),
			InOutage:     counted[index],
			OutageKey:    yesNo(counted[index]),
			ProbeParts:   pathParts(row.Probe),
		}
		// A row never checked carries the schema's checked_at of 0, which as an
		// instant would read as the first second of 1970.
		if row.HasAvailability && !row.NotYetProbed() {
			drawn.Checked = web.Instant(row.CheckedAt)
		} else {
			drawn.CheckedState = msgNeverChecked
		}
		if availability == msgAvailabilityNotCollecting {
			drawn.Pages = opnsenseLinks(baseURL, row.Kind, row.ProviderKey)
		}
		if row.HasDetail {
			drawn.Detail = row.Detail
		} else {
			drawn.DetailState = msgNoDetail
		}
		built.Sources = append(built.Sources, drawn)
	}

	gaps, err := s.store.RecentCollectionGaps(ctx, s.now().Add(-recentGapWindow).UTC().Unix())
	if err != nil {
		return err
	}
	for _, gap := range gaps {
		drawn := recentGapRow{
			KindKey:     web.KindLabelKey(gap.Kind),
			ProviderKey: web.ProviderLabelKey(gap.Kind, gap.ProviderKey),
			ReasonKey:   web.GapReasonKey(string(gap.Reason)),
			Start:       web.Instant(gap.IntervalStartAt),
			End:         web.Instant(gap.IntervalEndAt),
			Missed:      web.SpelledDuration(gap.MissedSeconds()),
		}
		if gap.HasDetail {
			drawn.Detail = gap.Detail
		} else {
			drawn.DetailState = msgNoDetail
		}
		built.RecentGaps = append(built.RecentGaps, drawn)
	}
	return nil
}

// selectionStateKey is the state a selection puts a provider in.
func selectionStateKey(selection config.Selection) messageKey {
	switch selection {
	case config.SelectionOn:
		return msgSelectionOn
	case config.SelectionOff:
		return msgSelectionOff
	default:
		return msgSelectionAuto
	}
}

// yesNo names a stored boolean.
func yesNo(value bool) messageKey {
	if value {
		return msgYes
	}
	return msgNo
}
