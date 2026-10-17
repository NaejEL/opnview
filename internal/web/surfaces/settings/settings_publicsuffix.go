package settings

import (
	"context"
	"time"

	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/web/settingfields"

	"github.com/NaejEL/opnview/internal/collect/registrabledomain"
	"github.com/NaejEL/opnview/internal/config"
)

// The Public Suffix List part of the settings answer: what the last refresh found, whether a
// copy is in use, and that copy's figures.
//
// THE OUTCOME COMES FROM THE AVAILABILITY ROW, THE FIGURES FROM THE PROCESS. The refresh
// records its probe in the public_suffix availability row, and that is what the outcome is read
// from. The copy's version, its rule count and the instant a refresh last confirmed it current
// come from the refresher in this process through Options.PublicSuffix -- never from the row's
// detail, which is a sentence for a person. A failed refresh over a copy still in use therefore
// shows both facts: the failure, and the copy that is still being used.
//
// A save writes the refresh interval only when it differs from the one in force, so saving
// leaves no row behind that merely repeats the default.
//
// READING IT CONTACTS NOTHING. The refresh is the third outbound call and runs in its own
// loop; nothing here asks it to run.

// publicSuffixStateKey names one refresh probe. A probe this function does not know reads as
// the failure state, never as success.
func publicSuffixStateKey(probe string) messageKey {
	switch probe {
	case registrabledomain.ProbeDownloaded:
		return msgPublicSuffixDownloaded
	case registrabledomain.ProbeNotModified:
		return msgPublicSuffixNotModified
	case registrabledomain.ProbeMalformed:
		return msgPublicSuffixMalformed
	case reads.ProbeNotYetProbed:
		return msgPublicSuffixNotYetRefreshed
	default:
		return msgPublicSuffixFailed
	}
}

// publicSuffixState reads the outcome of the last refresh from its availability row.
func (s *surface) publicSuffixState(ctx context.Context) (messageKey, error) {
	providerID, err := s.store.ProviderID(ctx, registrabledomain.Kind, registrabledomain.BuiltinKey)
	if err != nil {
		return "", err
	}
	_, probe, _, err := s.store.Availability(ctx, providerID)
	if err != nil {
		return "", err
	}
	return publicSuffixStateKey(probe), nil
}

// publicSuffixInterval reads a refresh interval in seconds: refused as every interval is, and
// then by config.Load. changed is false when it is the interval already in force.
func (s *surface) publicSuffixInterval(ctx context.Context, secondsText string) (seconds string, changed bool,
	refusal messageKey) {
	parsed, refusal := settingfields.TypedIntervalSeconds(secondsText)
	if refusal != "" {
		return "", false, refusal
	}
	if !settingfields.AcceptedByLoad(ctx, s.store,
		map[string]string{config.KeyPublicSuffixRefreshInterval: secondsText}) {
		return "", false, msgCollectionValueInvalid
	}
	settings, err := s.Configuration(ctx)
	if err == nil && time.Duration(parsed)*time.Second == settings.PublicSuffixRefreshInterval {
		return secondsText, false, ""
	}
	return secondsText, true, ""
}

// savePublicSuffixInterval stores a refresh interval and hands the configuration to the running
// loop, which asks the live holder before every wait.
func (s *surface) savePublicSuffixInterval(ctx context.Context, seconds string, now int64) error {
	if err := s.store.SetSetting(ctx, config.KeyPublicSuffixRefreshInterval, seconds, now); err != nil {
		return err
	}
	return s.ApplyConfiguration(ctx)
}
