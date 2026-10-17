package sources

import (
	"context"
	"time"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The Sources surface: one row per registered provider, with its availability, and every
// collection gap opnview detected in the last day, in JSON (api.go).
//
// IT USES THE SOURCE AVAILABILITY PRESET'S BINDING AND ITS CONDITION RULES
// (docs/widget-catalogue.md, Health and honesty, Source availability): the conditions are
// VALUES -- not yet probed, installed and not collecting, healthy, unavailable -- and a provider
// with no availability row at all reads "state unknown", never healthy.
//
// IT READS THE LOCAL DATABASE AND NOTHING ELSE. Answering contacts no host.
//
// THE DETAIL IS DATA. source_availability.detail and collection_gap.detail are free text a
// collector wrote, in English, for a person; they are answered verbatim and nothing reads them.

// recentGapWindow is how far back the Sources surface lists collection gaps: the
// maintainer's decision 5 of specs/SPEC-deferred-interface-bundle.md.
const recentGapWindow = 24 * time.Hour

// sourceStatus is one provider's availability beside the operator's selection of
// it: the two facts the outage rule reads.
type sourceStatus struct {
	row       reads.SourceRow
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
func isReachable(row reads.SourceRow) bool {
	return row.HasAvailability && row.State == vocab.StateReachable
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
// reachable -- and a provider's outage names that provider. Those are the providers the
// Sources answer marks in_outage, so the view the indicator opens shows which rows its figure
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

// verbatimDetail is a provider's availability detail as data, null when there is none: the JSON
// answer carries it verbatim, as the page draws it, and nothing reads it.
func verbatimDetail(row reads.SourceRow) *string {
	if !row.HasDetail {
		return nil
	}
	detail := row.Detail
	return &detail
}

// verbatimGapDetail is a collection gap's detail as data, null when there is none.
func verbatimGapDetail(gap reads.RecentGap) *string {
	if !gap.HasDetail {
		return nil
	}
	detail := gap.Detail
	return &detail
}
