package settings

import (
	"context"
	"github.com/NaejEL/opnview/internal/web"
	"net/http"
	"strconv"
	"time"

	"github.com/NaejEL/opnview/internal/collect/registrabledomain"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/store"
)

// The Public Suffix List card of the settings surface, beside Geolocation and in
// the same form as the MaxMind state: what the last refresh found, whether a copy is
// in use, and that copy's figures.
//
// THE OUTCOME COMES FROM THE AVAILABILITY ROW, THE FIGURES FROM THE PROCESS. The
// refresh records its probe in the public_suffix availability row, and that is what
// the outcome is read from. The copy's version, its rule count and the instant a
// refresh last confirmed it current come from the refresher in this process through
// Options.PublicSuffix -- never from the row's detail, which is a sentence for a
// person. A failed refresh over a copy still in use therefore shows both facts: the
// failure, and the copy that is still being used.
//
// ITS REFRESH INTERVAL IS A DURATION FIELD like the collection surface's: a figure
// beside its unit, the value in force drawn again under it only when the field shows
// something else. A save writes the setting only when the interval typed differs
// from the one in force, so saving the card leaves no row behind that merely repeats
// the default.
//
// DRAWING THE CARD CONTACTS NOTHING. The refresh is the third outbound call and runs
// in its own loop; nothing here asks it to run.

// fieldPublicSuffixInterval is the refresh interval's field: the setting key itself.
const fieldPublicSuffixInterval = config.KeyPublicSuffixRefreshInterval

// publicSuffixCard is what the card shows.
type publicSuffixCard struct {
	// StateKey is the last refresh's outcome.
	StateKey messageKey
	// InUseKey says whether a copy is in use.
	InUseKey messageKey
	// Confirmed is when a refresh last confirmed the copy current, or
	// ConfirmedState when none has.
	Confirmed      string
	ConfirmedState messageKey
	// Version is the copy's own VERSION line, as data, or VersionState when it
	// states none; Rules is its rule count. Both are drawn only with a copy in use.
	InUse        bool
	Version      string
	VersionState messageKey
	Rules        string
	// IntervalField is the refresh interval's field.
	IntervalField web.SettingField
}

// publicSuffixStateKey names one refresh probe. A probe this function does not know
// reads as the failure state, never as success.
func publicSuffixStateKey(probe string) messageKey {
	switch probe {
	case registrabledomain.ProbeDownloaded:
		return msgPublicSuffixDownloaded
	case registrabledomain.ProbeNotModified:
		return msgPublicSuffixNotModified
	case registrabledomain.ProbeMalformed:
		return msgPublicSuffixMalformed
	case store.ProbeNotYetProbed:
		return msgPublicSuffixNotYetRefreshed
	default:
		return msgPublicSuffixFailed
	}
}

// fillPublicSuffix fills the card from the availability row and the process.
func (s *surface) fillPublicSuffix(ctx context.Context, built *view) error {
	providerID, err := s.store.ProviderID(ctx, registrabledomain.Kind, registrabledomain.BuiltinKey)
	if err != nil {
		return err
	}
	_, probe, _, err := s.store.Availability(ctx, providerID)
	if err != nil {
		return err
	}
	settings, err := s.Configuration(ctx)
	if err != nil {
		return err
	}
	card := publicSuffixCard{
		StateKey: publicSuffixStateKey(probe),
		InUseKey: msgNo,
		IntervalField: web.DurationField(fieldPublicSuffixInterval, "public_suffix_interval",
			msgFieldPublicSuffixInterval, int64(settings.PublicSuffixRefreshInterval/time.Second), nil),
	}
	var confirmedAt int64
	if s.publicSuffix != nil {
		version, rules, confirmed, held := s.publicSuffix.Held()
		confirmedAt = confirmed
		if held {
			card.InUse, card.InUseKey = true, msgYes
			card.Rules = strconv.Itoa(rules)
			card.Version = version
			if version == "" {
				card.VersionState = msgVersionNotStated
			}
		}
	}
	if confirmedAt > 0 {
		card.Confirmed = web.Instant(confirmedAt)
	} else {
		card.ConfirmedState = msgNeverConfirmed
	}
	built.PublicSuffix = card
	return nil
}

// typedPublicSuffixInterval reads the refresh interval a settings submission
// carries, figure and unit, as every interval is read, and then by config.Load.
// changed is false when the form carried no such field, or carried the interval
// already in force: either way the stored rows are left as they are.
func (s *surface) typedPublicSuffixInterval(request *http.Request) (seconds string, changed bool,
	refusal messageKey) {
	values, sent := request.PostForm[fieldPublicSuffixInterval]
	if !sent || len(values) == 0 {
		return "", false, ""
	}
	secondsText, refusal := web.TypedDuration(values[0],
		request.PostFormValue(web.UnitFieldName(fieldPublicSuffixInterval)))
	if refusal != "" {
		return "", false, refusal
	}
	parsed, refusal := web.TypedIntervalSeconds(secondsText)
	if refusal != "" {
		return "", false, refusal
	}
	if !web.AcceptedByLoad(request.Context(), s.store,
		map[string]string{fieldPublicSuffixInterval: secondsText}) {
		return "", false, msgCollectionValueInvalid
	}
	settings, err := s.Configuration(request.Context())
	if err == nil && time.Duration(parsed)*time.Second == settings.PublicSuffixRefreshInterval {
		return secondsText, false, ""
	}
	return secondsText, true, ""
}
