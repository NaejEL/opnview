// Package sizing turns a measured record rate into a suggested pair — a page
// size and a poll interval — for each paged read.
//
// A PAGE AND AN INTERVAL ARE ONE DECISION. A poll loses records when its source
// writes more between two polls than one page holds, and the API offers no way
// to recover them. So the question this package answers is not "how often" or
// "how much" but the two together: given the busiest minute this installation
// has actually produced, which pair keeps a page ahead of it with room to spare.
//
// The input is a store.RecordRateMeasurement, taken on this installation's own
// rows; nothing here is a figure from somebody else's network. The output is a
// suggestion the operator may apply or ignore on the collection surface: the
// package writes nothing and decides nothing.
//
// Rebuilt after the loss of 2 October 2026 from the compiled package of that
// evening: the declarations, the registry and the derivation are the compiled
// ones; the comments are rewritten.
package sizing

import (
	"math"
	"time"

	"github.com/NaejEL/opnview/internal/store"
)

// HeadroomFactor is how many times the measured peak a page must hold. A page
// sized to the peak exactly would overflow on the first minute busier than any
// measured so far.
//
// Four is opnview's own figure, chosen here and stated as such, not measured.
// It leaves room for a burst several times the busiest minute measured without
// asking the firewall for pages far bigger than the source ever needs, and the
// collection surface prints the requirement it produces, so the figure can be
// judged on an installation's own numbers rather than taken on trust here. It
// applies to every kind alike.
const HeadroomFactor = 4

// BucketSeconds is the width of the buckets the peak is counted in. A minute is
// short enough to catch a burst a mean would smooth away, and long enough to
// hold more than one record on a source worth sizing; opnview's own figure too.
//
// The diagnostic file measures with the same width, so the peak shown can be
// reproduced by hand.
const BucketSeconds int64 = 60

// ShortestSuggestibleInterval is the shortest interval a suggestion may carry:
// the default filter-log cadence, the fastest the survey justifies for any
// read. A source that needs polling faster than this to keep a page ahead of
// it is a source no page the firewall serves keeps up with, and that is
// reported as such rather than hidden behind an interval of a second. It bounds
// suggestions only; an interval the operator types is Load's to judge.
const ShortestSuggestibleInterval = 10 * time.Second

// MinimumRecordsForASuggestion is how many records a window must hold before a
// pair is derived from it. One record has no span, and a pair derived from a
// span of nothing would be a figure with no measurement behind it. It is a
// floor of the arithmetic, not a confidence threshold: two records already
// give a span, and the span must also be at least one bucket long before a
// suggestion is made.
const MinimumRecordsForASuggestion int64 = 2

// The page ceilings: the largest page the firewall serves for each paged read.
// A ceiling is a property of the API, not a preference. Each kind in Kinds
// names the survey section recording its own, and a test holds that citation
// to docs/opnsense-api-survey.md.
const (
	// PageCeilingUnbounded means the read has no page limit the survey
	// records. The lease searches are grid searches, whose `rowCount` is the
	// page size and `-1` means all rows: "Authentication and conventions",
	// under "Pagination envelope". Nothing on the firewall's side caps it.
	PageCeilingUnbounded = 0

	// PageCeilingFirewallLog is the filter log's `limit`. Survey, "Data source 1
	// — Filter logs": the default is 1000, and `limit=0` is silently coerced back
	// to 1000. A request for more is not refused; it is answered with 1000
	// lines. A page size above the ceiling is therefore accepted by the setting
	// and reported on the collection surface, where the operator can see that
	// the firewall will not honour it, and never suggested here.
	//
	// The same holds for the two ceilings below.
	PageCeilingFirewallLog = 1000

	// PageCeilingSecurityEvent is the alert query's `rowCount` default. Survey,
	// "Data source 2 — Suricata `eve.json`": `rowCount` defaults to 9999.
	// A page this size is far beyond what an alert feed produces per poll.
	PageCeilingSecurityEvent = 9999

	// PageCeilingDNSLookup is the resolver's ring buffer. Survey, "Data source
	// 5 — Resolver DNS lookups": the endpoint holds the last 1000 lookups and
	// nothing older, whatever is asked. The page is not a setting for this read
	// at all; only the interval keeps the buffer from wrapping between two
	// polls, which is what the suggestion for this kind is about.
	// A suggestion for this kind therefore carries an interval and no page.
	PageCeilingDNSLookup = 1000
)

// Kind is one paged read as this package sizes it.
type Kind struct {
	// Kind is the registry kind, as in provider.kind.
	Kind string
	// Spec says where the kind's records are measured.
	Spec store.RecordRateSpec
	// PageSizeKey is the setting key of the page size, empty when the read
	// takes no page size.
	PageSizeKey string
	// IntervalKey is the setting key of the poll interval.
	IntervalKey string
	// PageCeiling is the largest page the firewall serves, or
	// PageCeilingUnbounded.
	PageCeiling int
	// SurveyHeading is the heading of the survey section recording the
	// ceiling, verbatim, so a test can hold the citation to the document.
	SurveyHeading string
	// LabelKey is the catalogue key of the kind's name on the collection
	// surface.
	LabelKey string
}

// PageIsAdjustable reports whether the read takes a page size at all.
func (k Kind) PageIsAdjustable() bool { return k.PageSizeKey != "" }

// HasPageCeiling reports whether the firewall caps the read's page.
func (k Kind) HasPageCeiling() bool { return k.PageCeiling != PageCeilingUnbounded }

// Kinds is the registry of the paged reads this package sizes, in the order the
// collection surface shows them.
//
// The tables and columns named here are spliced into the measurement SQL, which
// is why they live in code and never come from input. A test holds every kind
// to the provider registry of the schema, and every ceiling to a heading of the
// survey.
//
// The per-pair sampler is not here: it reads a snapshot with no paging and no
// record of its own to measure. A new slice is returned on every call, so a
// caller may reorder or filter it without changing what the next caller sees.
func Kinds() []Kind {
	return []Kind{
		{
			Kind: "firewall_log",
			Spec: store.RecordRateSpec{
				Kind: "firewall_log", Table: "flow",
				SourceColumn: "observed_at", IngestedColumn: "ingested_at",
			},
			PageSizeKey:   "page_size_firewall_log",
			IntervalKey:   "poll_interval_firewall_log_seconds",
			PageCeiling:   PageCeilingFirewallLog,
			SurveyHeading: "Data source 1 — Filter logs",
			LabelKey:      "collection.kind.firewall_log",
		},
		{
			Kind: "security_event",
			Spec: store.RecordRateSpec{
				Kind: "security_event", Table: "security_event",
				SourceColumn: "occurred_at", IngestedColumn: "ingested_at",
			},
			PageSizeKey:   "page_size_security_event",
			IntervalKey:   "poll_interval_security_event_seconds",
			PageCeiling:   PageCeilingSecurityEvent,
			SurveyHeading: "Data source 2 — Suricata `eve.json`",
			LabelKey:      "collection.kind.security_event",
		},
		{
			// A lease row records when opnview saw it and no other instant of its
			// own, so the lease table is measured on one clock.
			Kind: "dhcp_lease",
			Spec: store.RecordRateSpec{
				Kind: "dhcp_lease", Table: "dhcp_lease",
				SourceColumn: "observed_at", IngestedColumn: "",
			},
			PageSizeKey:   "page_size_dhcp_lease",
			IntervalKey:   "poll_interval_dhcp_lease_seconds",
			PageCeiling:   PageCeilingUnbounded,
			SurveyHeading: "Authentication and conventions",
			LabelKey:      "collection.kind.dhcp_lease",
		},
		{
			// The resolver's page is its ring buffer, not a setting, so this
			// kind has no PageSizeKey and only its interval is suggested.
			Kind: "dns_lookup",
			Spec: store.RecordRateSpec{
				Kind: "dns_lookup", Table: "dns_resolution",
				SourceColumn: "looked_up_at", IngestedColumn: "ingested_at",
			},
			IntervalKey:   "poll_interval_dns_lookup_seconds",
			PageCeiling:   PageCeilingDNSLookup,
			SurveyHeading: "Data source 5 — Resolver DNS lookups",
			LabelKey:      "collection.kind.dns_lookup",
		},
	}
}

// Window is the span a suggestion is measured over: NominalWindowSeconds up to
// now, or the retention horizon if that is shorter, since the purge has removed
// whatever lies beyond it. A retention of 0 is unlimited and leaves the nominal
// window as it is.
//
// A day is long enough to hold a working day's peak and short enough that an
// installation sees a suggestion on its first evening rather than its first
// week. The window ends now and excludes it, as every window in the store
// does, so a record written during the measurement is left for the next one.
// The clock is the caller's, so a test can place the window anywhere.
func Window(now time.Time, retentionSeconds int64) store.RecordRateWindow {
	end := now.UTC().Unix()
	length := NominalWindowSeconds
	if retentionSeconds > 0 && retentionSeconds < length {
		length = retentionSeconds
	}
	return store.RecordRateWindow{StartAt: end - length, EndAt: end}
}

// NominalWindowSeconds is the length of the measurement window, 24 hours.
const NominalWindowSeconds int64 = 86400

// Pair is what a suggestion suggests: a page size and an interval, the two
// settings of one paged read.
type Pair struct {
	PageSize int
	Interval time.Duration
}

// Outcome is what came of an attempt to suggest a pair. Each one is a state the
// collection surface shows as such; none of them is rendered as a pair of
// zeros, which would read as a suggestion to poll for nothing, never, and is
// the defect this type exists to prevent.
type Outcome string

// The outcomes. Their values are stable: the collection surface keys its
// catalogue strings on them, so renaming one is a change to the catalogue as
// well, and a test there holds every outcome to a string that exists. Only
// OutcomeSuggested carries a pair.
const (
	// OutcomeSuggested is a pair derived from the measurement and within
	// every rule of the derivation.
	OutcomeSuggested Outcome = "suggested"
	// OutcomeNotYetMeasured is a window that does not hold enough to derive
	// a pair from: no record, too few, or a span shorter than one bucket. It
	// is what a fresh installation shows on its first day.
	OutcomeNotYetMeasured Outcome = "not_yet_measured"
	// OutcomeRateNotCoverable is a measured rate no pair can keep up with: a
	// page at the ceiling would still need an interval below the floor. It is
	// the one outcome that says the source writes faster than opnview reads.
	OutcomeRateNotCoverable Outcome = "rate_not_coverable"
	// OutcomeSourceNotCollected is a kind opnview does not read, which has no
	// rate worth sizing a page on: no row of it is being written, so a
	// measurement of it would be a measurement of the past.
	OutcomeSourceNotCollected Outcome = "source_not_collected"
)

// Suggestion is the result of Suggest.
type Suggestion struct {
	// Outcome says whether Pair means anything.
	Outcome Outcome
	// Pair is the suggested pair. It is meaningful only when Outcome is
	// OutcomeSuggested; otherwise it is the zero pair and must not be shown.
	Pair Pair
	// IntervalOnly is true for a read whose page is not a setting: only the
	// interval of the pair is a suggestion, and the page is the ceiling.
	IntervalOnly bool
	// RequiredRecordsPerPage is how many records a page must hold at the
	// suggested interval, headroom included. It is shown beside the pair so
	// the operator can see what the suggestion is made of.
	RequiredRecordsPerPage int
}

// Suggest derives a pair for one kind from a measurement of it, given the pair
// in force and whether opnview collects the kind at all.
//
// The order is the point. A kind not collected gets no suggestion. A
// measurement that does not hold enough gets none either, and says so. Then the
// page moves before the interval: at the interval in force — never below
// ShortestSuggestibleInterval — the page the peak requires is computed, and
// within the ceiling, that page at that interval is the suggestion. Beyond the
// ceiling, the page is set to the ceiling and the interval shortened until a
// page of that size keeps up, down to the floor; a rate that still overflows is
// reported as not coverable.
//
// For a read whose page is not a setting, the ceiling is the page and only the
// interval moves.
//
// Every suggestion passes through validated before it is returned, so no path
// through this function can hand out a pair that breaks a rule above. The
// function reads nothing and writes nothing: the measurement and the pair in
// force are taken by the caller, which is what lets a test drive it over every
// measured peak, and what keeps the collection surface's figures and its
// suggestion from coming from two different readings.
func Suggest(kind Kind, measured store.RecordRateMeasurement, inForce Pair, collected bool) Suggestion {
	// The read's page is either a setting, or fixed at the ceiling.
	intervalOnly := !kind.PageIsAdjustable()
	if !collected {
		return Suggestion{Outcome: OutcomeSourceNotCollected, IntervalOnly: intervalOnly}
	}

	// A pair needs a peak, a bucket to read it in, at least two records, and a
	// span at least one bucket long: a peak counted in a minute the window does
	// not even fill is not a peak of anything.
	if !measured.PeakRate.Known ||
		measured.PeakBucketRecords <= 0 ||
		measured.BucketSeconds <= 0 ||
		measured.RecordCount < MinimumRecordsForASuggestion ||
		measured.BucketSeconds > measured.CoveredSpanSeconds() {
		return Suggestion{Outcome: OutcomeNotYetMeasured, IntervalOnly: intervalOnly}
	}

	// Start from the interval in force: the operator chose it, or the survey
	// did, and a suggestion that moved both halves of the pair at once would be
	// impossible to read. An interval below the floor is raised to it, since no
	// suggestion may carry one.
	//
	// The requirement at that interval decides the rest. It may be unknown —
	// a peak so high the requirement does not fit an int — and that case is
	// handled like a requirement above the ceiling: the interval has to move,
	// since the page cannot grow past what a request can ask for.
	interval := max(inForce.Interval, ShortestSuggestibleInterval)

	// It never moves above the interval in force: a suggestion only ever asks
	// for a bigger page or a shorter interval, never for polling less often.
	required, ok := requiredRecordsPerPage(measured.PeakBucketRecords, measured.BucketSeconds, interval)

	// A read whose page is not a setting: the page is the ceiling, so the
	// interval in force stands if the ceiling covers it, and otherwise the
	// longest interval the ceiling does cover is the suggestion. The page of
	// the pair stays zero, since there is no page to suggest.
	if !kind.PageIsAdjustable() {
		if ok && required <= kind.PageCeiling {
			return validated(Suggestion{
				Outcome: OutcomeSuggested, IntervalOnly: true,
				Pair:                   Pair{Interval: interval},
				RequiredRecordsPerPage: required,
			}, kind)
		}
		interval, ok := longestIntervalThatFits(kind.PageCeiling, measured)
		if !ok {
			return Suggestion{Outcome: OutcomeRateNotCoverable, IntervalOnly: true}
		}
		required, ok := requiredRecordsPerPage(measured.PeakBucketRecords,
			measured.BucketSeconds, interval)
		if !ok {
			return Suggestion{Outcome: OutcomeRateNotCoverable, IntervalOnly: true}
		}
		return validated(Suggestion{
			Outcome: OutcomeSuggested, IntervalOnly: true,
			Pair:                   Pair{Interval: interval},
			RequiredRecordsPerPage: required,
		}, kind)
	}

	if ok && (!kind.HasPageCeiling() || required <= kind.PageCeiling) {
		// The page moves first: the interval in force stands, and the page is
		// sized to it.
		return validated(Suggestion{
			Outcome:                OutcomeSuggested,
			Pair:                   Pair{PageSize: required, Interval: interval},
			RequiredRecordsPerPage: required,
		}, kind)
	}
	longest, ok := longestIntervalThatFits(kind.PageCeiling, measured)
	if !ok {
		return Suggestion{Outcome: OutcomeRateNotCoverable}
	}
	required, ok = requiredRecordsPerPage(measured.PeakBucketRecords,
		measured.BucketSeconds, longest)
	if !ok {
		return Suggestion{Outcome: OutcomeRateNotCoverable}
	}
	return validated(Suggestion{
		Outcome:                OutcomeSuggested,
		Pair:                   Pair{PageSize: kind.PageCeiling, Interval: longest},
		RequiredRecordsPerPage: required,
	}, kind)
}

// validated is the last check every suggestion passes. It states once more, on
// the result, the rules the derivation above is written to respect — no
// interval below ShortestSuggestibleInterval, a positive requirement within the
// ceiling, no page for a read whose page is not a setting, and a positive page
// within the ceiling for a read whose page is — so that a mistake in the
// derivation produces a refusal rather than a pair that would lose records.
//
// A suggestion that breaks one of them becomes OutcomeRateNotCoverable.
// Outcomes other than OutcomeSuggested pass through untouched: they carry no
// pair to check.
func validated(suggestion Suggestion, kind Kind) Suggestion {
	if suggestion.Outcome != OutcomeSuggested {
		return suggestion
	}
	refused := Suggestion{
		Outcome:      OutcomeRateNotCoverable,
		IntervalOnly: !kind.PageIsAdjustable(),
	}
	switch {
	case suggestion.Pair.Interval < ShortestSuggestibleInterval:
		return refused
	case suggestion.RequiredRecordsPerPage <= 0:
		return refused
	case kind.HasPageCeiling() && suggestion.RequiredRecordsPerPage > kind.PageCeiling:
		return refused
	case !kind.PageIsAdjustable() && suggestion.Pair.PageSize != 0:
		return refused
	case kind.PageIsAdjustable() && suggestion.Pair.PageSize <= 0:
		return refused
	case kind.PageIsAdjustable() && kind.HasPageCeiling() &&
		suggestion.Pair.PageSize > kind.PageCeiling:
		return refused
	}
	return suggestion
}

// requiredRecordsPerPage is how many records a page must hold to keep up with a
// peak of peakBucketRecords per bucketSeconds when polled every interval, with
// HeadroomFactor of room: the peak rate, times the seconds between two polls,
// times the headroom, rounded up. A requirement is at least one record.
//
// It reports false when an input is not positive, and when the requirement
// does not fit a 32-bit int: a page of two thousand million records is not a
// page anybody can ask a firewall for, and a requirement that large is a rate
// no pair covers. The bound is the 32-bit one on every platform, so that the
// same measurement gives the same answer wherever the product runs.
//
// The arithmetic is done in floating point because the rate is a fraction;
// every input is an integer count or a whole number of seconds, so the only
// rounding that matters is the final one, which is upwards: a page one record
// short is a page that loses that record.
//
// It is the one place the requirement is computed: Suggest, the search for the
// longest interval and the tests all call it, so they cannot disagree.
func requiredRecordsPerPage(peakBucketRecords, bucketSeconds int64, interval time.Duration) (int, bool) {
	// Every input divides or multiplies; none of them can be nought.
	if peakBucketRecords <= 0 || bucketSeconds <= 0 || interval <= 0 {
		return 0, false
	}
	// Peak per second, times the seconds between two polls, times the room.
	needed := float64(peakBucketRecords) * interval.Seconds() * HeadroomFactor / float64(bucketSeconds)
	if math.IsNaN(needed) || needed > math.MaxInt32 {
		return 0, false
	}
	records := int(math.Ceil(needed))
	if records < 1 {
		// A peak so small the requirement rounds below one record still needs
		// a page that holds one.
		return 1, true
	}
	return records, true
}

// longestIntervalThatFits is the longest whole number of seconds, no shorter
// than ShortestSuggestibleInterval, at which a page of pageSize records keeps
// up with the measured peak, headroom included. It reports false when no such
// interval exists: the peak needs a page larger than pageSize even at the floor.
//
// It starts from the closed form — the page over the peak rate and the
// headroom, rounded down — and steps down a second at a time while the
// requirement, rounded as requiredRecordsPerPage rounds it, does not fit.
func longestIntervalThatFits(pageSize int, measured store.RecordRateMeasurement) (time.Duration, bool) {
	// Nothing fits a page of nothing, and nothing needs to fit a peak of none.
	if measured.PeakBucketRecords <= 0 || pageSize <= 0 {
		return 0, false
	}
	seconds := int64(math.Floor(float64(pageSize) * float64(measured.BucketSeconds) /
		(float64(measured.PeakBucketRecords) * HeadroomFactor)))
	// The floor of the derivation, in whole seconds.
	for seconds >= int64(ShortestSuggestibleInterval/time.Second) {
		interval := time.Duration(seconds) * time.Second
		required, ok := requiredRecordsPerPage(measured.PeakBucketRecords,
			measured.BucketSeconds, interval)
		if ok && required <= pageSize {
			return interval, true
		}
		seconds--
	}
	return 0, false
}
