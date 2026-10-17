package collection

import (
	"context"
	"strconv"
	"strings"

	"github.com/NaejEL/opnview/internal/web/messages"

	"github.com/NaejEL/opnview/internal/store/attribution"
	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/vocab"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/sizing"
)

// The collection surface: per source kind, the pair that governs its polling, the record rate
// opnview has measured, and the pair it would suggest (api.go answers them in JSON).
//
// IT READS THE LOCAL DATABASE AND NOTHING ELSE. Every figure comes from the rows the collectors
// already stored, through store.MeasureRecordRate; answering, measuring and suggesting contact
// nothing, and saving writes setting rows. Nothing here can reach the firewall.
//
// A SAVE TAKES EFFECT WITHOUT A RESTART. After the rows are written the whole configuration is
// loaded again, through the path start-up uses, and handed to the live holder the scheduler
// reads and to the running collector.

// kindNamed returns the registry entry of the kind a form names.
func kindNamed(name string) (sizing.Kind, bool) {
	for _, kind := range sizing.Kinds() {
		if kind.Kind == name {
			return kind, true
		}
	}
	return sizing.Kind{}, false
}

// typedPageSize reads a typed page size. A page is a positive count of records;
// anything else is refused, against the page_size member.
//
// A PAGE ABOVE THE CEILING IS NOT REFUSED HERE. The ceiling is the firewall's, the
// setting is the operator's, and a page above it is stored and reported as above
// the ceiling on the card, beside the ceiling it exceeds, rather than refused
// where nobody would see why.
//
// The returned key is empty when the figure is accepted, and names the refusal
// otherwise.
func typedPageSize(typed string) (int, messageKey) {
	pageSize, err := strconv.Atoi(strings.TrimSpace(typed))
	if err != nil || pageSize <= 0 {
		return 0, msgCollectionValueInvalid
	}
	return pageSize, ""
}

// measureKind measures one kind over the window the retention horizon allows,
// reports whether opnview collects it, and derives the suggestion from both. It
// reads the database only. A measurement taken before a later step failed is
// returned with the error, for a caller that can still use it.
func (s *surface) measureKind(ctx context.Context, kind sizing.Kind) (attribution.RecordRateMeasurement,
	sizing.Suggestion, kindState, error) {
	settings, err := s.Configuration(ctx)
	if err != nil {
		return attribution.RecordRateMeasurement{}, sizing.Suggestion{}, kindState{}, err
	}
	window := sizing.Window(s.now(), settings.RetentionSeconds)
	measured, err := s.store.MeasureRecordRate(ctx, kind.Spec, window, sizing.BucketSeconds)
	if err != nil {
		return measured, sizing.Suggestion{}, kindState{}, err
	}
	state, err := s.sourceState(ctx, kind.Kind)
	if err != nil {
		return measured, sizing.Suggestion{}, kindState{}, err
	}
	suggestion := sizing.Suggest(kind, measured, pairInForce(kind, settings), state.collected)
	return measured, suggestion, state, nil
}

// pairInForce is the pair one kind is polled at under a configuration.
func pairInForce(kind sizing.Kind, settings config.Config) sizing.Pair {
	switch kind.Kind {
	case "firewall_log":
		return sizing.Pair{PageSize: settings.FirewallLogPageSize,
			Interval: settings.FirewallLogInterval}
	case "security_event":
		return sizing.Pair{PageSize: settings.SecurityEventPageSize,
			Interval: settings.SecurityEventInterval}
	case "dhcp_lease":
		return sizing.Pair{PageSize: settings.DHCPLeasePageSize,
			Interval: settings.DHCPLeaseInterval}
	case "dns_lookup":
		return sizing.Pair{Interval: settings.DNSLookupInterval}
	}
	return sizing.Pair{}
}

// kindState is what the collection surface says about one kind's source, beside
// its figures.
type kindState struct {
	// availability is the firewall's report, as a state.
	availability messageKey
	// selection is the operator's decision, as a state.
	selection messageKey
	// collected says whether opnview reads the kind: some implementation is not
	// turned off, and some implementation is reachable. A kind not collected gets
	// no suggestion.
	collected bool
}

// sourceState summarises a kind's implementations for its card: what the firewall
// reports, what the operator decided, and whether opnview reads the kind at all.
//
// THE SELECTION IS NOT THE AVAILABILITY. They are two rows because they are two
// facts, and a surface that collapsed them would hide an operator's own decision
// behind a probe result.
//
// The kind's availability is kindAvailability's, in the Sources surface's values.
// Its selection is
// off when every implementation is turned off, on when any is turned on, and auto
// otherwise. A selection that cannot be read is an error, as everywhere.
func (s *surface) sourceState(ctx context.Context, kind string) (kindState, error) {
	providers, err := s.store.ProvidersOfKind(ctx, kind)
	if err != nil {
		return kindState{}, err
	}
	var reachable, anyOn, anyNotOff bool
	for _, provider := range providers {
		selection, err := config.LoadSourceSelection(ctx, s.store, kind, provider.ProviderKey)
		if err != nil {
			return kindState{}, err
		}
		if provider.State == vocab.StateReachable {
			reachable = true
		}
		if selection == config.SelectionOff {
			continue
		}
		anyNotOff = true
		anyOn = selection == config.SelectionOn || anyOn
	}

	// A kind with no implementation has none that is not turned off, and reads as
	// off: there is nothing to collect it from.
	state := kindState{selection: msgSelectionAuto}
	switch {
	case !anyNotOff:
		state.selection = msgSelectionOff
	case anyOn:
		state.selection = msgSelectionOn
	}
	// THE AVAILABILITY IS NAMED WITH THE SOURCES SURFACE'S OWN VALUES, so a kind reads
	// the same here as its providers read there: see kindAvailability.
	rows, err := s.store.SourceAvailability(ctx)
	if err != nil {
		return kindState{}, err
	}
	state.availability = kindAvailability(kind, rows)
	state.collected = anyNotOff && reachable
	return state, nil
}

// kindAvailability names a kind's condition from its providers' rows, with the
// values of the Source availability preset: healthy when any provider is reachable;
// failing that, installed and not collecting when any is; failing that, unavailable
// when any was checked and found so; failing that, not yet probed; and state unknown
// when no provider of the kind carries an availability row at all. Nothing but a
// reachable provider reads as healthy.
func kindAvailability(kind string, rows []reads.SourceRow) messageKey {
	found := map[messageKey]bool{}
	for _, row := range rows {
		if row.Kind != kind {
			continue
		}
		availability, _ := messages.AvailabilityOf(row)
		found[availability] = true
	}
	for _, availability := range []messageKey{msgAvailabilityHealthy, msgAvailabilityNotCollecting,
		msgAvailabilityUnavailable, msgAvailabilityNotYetProbed} {
		if found[availability] {
			return availability
		}
	}
	return msgAvailabilityUnknown
}

// pageStateKey says whether the page in force is within the ceiling.
func pageStateKey(kind sizing.Kind, pageSize int) messageKey {
	if pageSize > kind.PageCeiling {
		return msgPageAboveCeiling
	}
	return msgPageWithinCeiling
}

// suggestionStateKey is the state naming a derivation's outcome.
func suggestionStateKey(outcome sizing.Outcome) messageKey {
	switch outcome {
	case sizing.OutcomeSuggested:
		return msgSuggestionAvailable
	case sizing.OutcomeRateNotCoverable:
		return msgSuggestionNotCoverable
	case sizing.OutcomeSourceNotCollected:
		return msgSuggestionSourceNotCollected
	default:
		return msgSuggestionNotYetMeasured
	}
}
