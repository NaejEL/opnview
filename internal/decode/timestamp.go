package decode

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Timestamp normalisation. The collector layer converts everything to a UTC
// epoch in seconds at the boundary, because the schema stores nothing else.
//
// docs/data-model.md names three input shapes and there is no fourth:
//
//	epoch seconds              resolver `time`, lease `expire`, aggregate `last_seen`
//	ISO 8601 string            Suricata `timestamp`, and the filter log's
//	                           `__timestamp__` when syslog is configured that way
//	year-less syslog string    the filter log's `__timestamp__` otherwise
//
// The year-less form is the one that needs a decision, and the decision is
// recorded rather than hidden: survey gap 7 says to normalise it against the
// firewall's current time, read in the same API session. That is what Reference
// is. The residual assumption is the firewall's UTC offset, which the year-less
// form does not carry either; OffsetSeconds is where it goes. It is zero until
// FirewallOffsetSeconds has measured it from the firewall's own clock, which
// discovery does on every refresh. Read with the offset at zero, a live OPNsense
// on CEST had every filter-log line stored 7 200 s ahead of UTC. Nothing here
// ever reads the HOST's zone, which is why the tests pass with TZ set to a
// non-UTC zone.

// isoLayouts are the ISO shapes the two sources produce. The survey records that
// the filter log's ISO form comes back with the UTC offset stripped
// ("2026-09-26T23:51:06", measured), while Suricata writes a full RFC 3339
// timestamp, so both are accepted.
var isoLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05.999999999-0700",
	"2006-01-02T15:04:05-0700",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
}

// syslogLayout is the year-less, zone-less form: "Mon dd HH:MM:SS", where dd is
// space-padded on single-digit days. Go's `_2` matches both paddings.
const syslogLayout = "Jan _2 15:04:05"

// Normaliser turns a source timestamp into a UTC epoch in seconds.
type Normaliser struct {
	// Reference is the firewall's current time, read in the same API session.
	// It is what supplies the year the syslog form omits.
	Reference time.Time
	// OffsetSeconds is the firewall's assumed offset from UTC, for the one shape
	// that carries no zone. It is the recorded assumption of survey gap 7.
	OffsetSeconds int
}

// NormaliseEpoch reads a value the source already expresses as epoch seconds.
// It accepts a number or a string of digits, because a grid search returns
// whichever the body encoding produced.
func NormaliseEpoch(value any) (int64, error) {
	switch typed := value.(type) {
	case float64:
		return int64(typed), nil
	case int64:
		return typed, nil
	case int:
		return int64(typed), nil
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 0, fmt.Errorf("decode: an empty string is not an epoch")
		}
		// A fractional epoch is legitimate and is truncated to the second the
		// schema stores.
		if seconds, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return int64(seconds), nil
		}
		return 0, fmt.Errorf("decode: %q is not an epoch", typed)
	default:
		return 0, fmt.Errorf("decode: %T is not an epoch", value)
	}
}

// NormaliseISO reads one of the ISO shapes. A timestamp with no zone is read as
// the firewall's local time and shifted by OffsetSeconds; one that carries a
// zone is read as it stands, because then there is nothing to assume.
func (n Normaliser) NormaliseISO(value string) (int64, error) {
	trimmed := strings.TrimSpace(value)
	for _, layout := range isoLayouts {
		parsed, err := time.Parse(layout, trimmed)
		if err != nil {
			continue
		}
		if zoneless(layout) {
			return parsed.UTC().Unix() - int64(n.OffsetSeconds), nil
		}
		return parsed.UTC().Unix(), nil
	}
	return 0, fmt.Errorf("decode: %q is not one of the ISO shapes the sources produce", value)
}

// zoneless reports whether a layout carries no zone, so a parse of it yields a
// time in UTC by default rather than in the zone the string named.
func zoneless(layout string) bool {
	return !strings.ContainsAny(layout, "Z-+") || layout == "2006-01-02T15:04:05.999999999" ||
		layout == "2006-01-02T15:04:05" || layout == "2006-01-02 15:04:05"
}

// NormaliseSyslog reads the year-less, zone-less form and infers the year from
// Reference.
//
// The inference is the December-to-January case and nothing else: a line written
// on 31 December read on 1 January must not be stamped eleven months in the
// future. The rule is therefore "the candidate year that puts the timestamp
// closest to the reference", tried as the reference year, the one before and the
// one after — the third because a firewall a little ahead of opnview's clock
// writes a line dated 1 January while opnview still believes it is 31 December.
func (n Normaliser) NormaliseSyslog(value string) (int64, error) {
	parsed, err := time.Parse(syslogLayout, strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("decode: %q is not the year-less syslog shape: %w", value, err)
	}
	if n.Reference.IsZero() {
		return 0, fmt.Errorf("decode: %q carries no year and no reference time was given", value)
	}

	reference := n.Reference.UTC()
	referenceYear := reference.Year()
	best := int64(0)
	bestDistance := int64(-1)

	for _, year := range []int{referenceYear - 1, referenceYear, referenceYear + 1} {
		candidate := time.Date(year, parsed.Month(), parsed.Day(),
			parsed.Hour(), parsed.Minute(), parsed.Second(), 0, time.UTC)
		epoch := candidate.Unix() - int64(n.OffsetSeconds)
		distance := epoch - reference.Unix()
		if distance < 0 {
			distance = -distance
		}
		if bestDistance < 0 || distance < bestDistance {
			best, bestDistance = epoch, distance
		}
	}
	return best, nil
}

// NormaliseFilterLogTimestamp reads the filter log's `__timestamp__`, which is
// one shape or the other depending on the syslog format in use. Both remain
// possible per the survey, and only the ISO one was exercised on a live
// firewall, so both are handled and neither is assumed.
func (n Normaliser) NormaliseFilterLogTimestamp(value string) (int64, error) {
	if epoch, err := n.NormaliseISO(value); err == nil {
		return epoch, nil
	}
	epoch, err := n.NormaliseSyslog(value)
	if err != nil {
		return 0, fmt.Errorf("decode: %q is neither the ISO nor the year-less filter-log shape", value)
	}
	return epoch, nil
}

// systemTimeLayout is the shape of /api/diagnostics/system/systemTime's `datetime`,
// measured on a live OPNsense 26.7 on 3 October 2026: "Sat Oct 3 21:25:37 CEST 2026".
// The zone is an abbreviation, which names no offset a parser can trust, so it is
// read and ignored: what the value gives is the firewall's WALL CLOCK.
const systemTimeLayout = "Mon Jan _2 15:04:05 MST 2006"

// offsetGranularity is the step every UTC offset in use is a multiple of. A wall
// clock read a moment after the reference instant is rounded to it, which absorbs
// the request's latency and any small clock drift.
const offsetGranularity = 15 * 60

// maxOffsetSeconds bounds a plausible offset: no zone in use is further from UTC
// than fourteen hours.
const maxOffsetSeconds = 14 * 3600

// FirewallOffsetSeconds measures the firewall's offset from UTC, survey gap 7, from
// its wall clock as systemTime reports it and the UTC instant it was read at: the
// difference, rounded to the quarter hour. Read on every discovery, it follows a
// change of the firewall's zone and the change to and from summer time.
func FirewallOffsetSeconds(datetime string, readAt time.Time) (int, error) {
	parsed, err := time.Parse(systemTimeLayout, strings.Join(strings.Fields(datetime), " "))
	if err != nil {
		return 0, fmt.Errorf("decode: %q is not the systemTime datetime shape: %w", datetime, err)
	}
	wall := time.Date(parsed.Year(), parsed.Month(), parsed.Day(),
		parsed.Hour(), parsed.Minute(), parsed.Second(), 0, time.UTC)
	difference := wall.Sub(readAt.UTC()).Seconds()
	rounded := int(math.Round(difference/offsetGranularity)) * offsetGranularity
	if rounded > maxOffsetSeconds || rounded < -maxOffsetSeconds {
		return 0, fmt.Errorf("decode: the firewall's clock is %ds from UTC, which no zone is; "+
			"its clock is wrong rather than its zone", rounded)
	}
	return rounded, nil
}
