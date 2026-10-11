package config

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// The live-rate settings (C3 of the backend-completion cycle): how long a request for a live
// figure keeps fine sampling running, and how long the fine samples are kept.
//
// LIVE INTEREST is opnview's own term: OPNsense has no word for "somebody is looking at a live
// figure", because its own dashboard polls the firewall directly. Any authenticated request for a
// live figure renews a lease of KeyLiveInterestSeconds; while the lease holds, the plugins that
// declare a live pass run it at their live cadence, and when it lapses they stop. The live cadence
// itself is the declaring plugin's setting (the insight plugin's live_cadence_seconds), because
// it is a property of the endpoints that plugin reads.
const (
	// KeyLiveInterestSeconds is the lease a live-figure request renews.
	KeyLiveInterestSeconds = "live_interest_seconds"
	// KeyLiveSampleRetentionSeconds is how long a fine sample -- one taken by a live pass -- is
	// kept before the purge removes it. Baseline samples follow retention_seconds.
	KeyLiveSampleRetentionSeconds = "live_sample_retention_seconds"
)

// The defaults and bounds of the two settings.
const (
	// DefaultLiveInterestSeconds is 30 s, the maintainer's figure (C3, "Live interest"): it
	// survives a missed 2 s poll of the interface, and fine sampling stops within half a minute
	// of the last viewer.
	DefaultLiveInterestSeconds int64 = 30
	// MinLiveInterestSeconds is 5 s: a lease shorter than two polls of the interface at its
	// 2-second strip would lapse between two of them.
	MinLiveInterestSeconds int64 = 5
	// MaxLiveInterestSeconds is one hour: longer, and fine sampling would run for an hour after
	// a single glance at a live figure.
	MaxLiveInterestSeconds int64 = 3600

	// DefaultLiveSampleRetentionSeconds is one hour: "a short retention" (C3, "Persistence").
	// Fine samples serve the rolling strip and the current rates; the baseline samples, which
	// the normal retention keeps, are the history.
	DefaultLiveSampleRetentionSeconds int64 = 3600
	// MinLiveSampleRetentionSeconds is ten minutes: the rolling strip's longest window must
	// still find its samples.
	MinLiveSampleRetentionSeconds int64 = 600
	// MaxLiveSampleRetentionSeconds is seven days: beyond, fine samples are no longer short-lived
	// and would grow the table at the live cadence for a week.
	MaxLiveSampleRetentionSeconds int64 = 604800
)

// LiveSettingKeys are the two keys, in the order a listing shows them.
func LiveSettingKeys() []string {
	return []string{KeyLiveInterestSeconds, KeyLiveSampleRetentionSeconds}
}

// The closed codes a live setting value is refused with.
const (
	// LiveSettingUnknown is a key that is not a live setting.
	LiveSettingUnknown = "unknown_setting"
	// LiveSettingInvalid is a value that is not a whole number of seconds.
	LiveSettingInvalid = "invalid_setting_value"
	// LiveSettingOutOfRange is a whole number outside the key's bounds.
	LiveSettingOutOfRange = "setting_out_of_range"
)

// LiveSettingBounds returns a live setting's default and bounds.
func LiveSettingBounds(key string) (fallback, minimum, maximum int64, known bool) {
	switch key {
	case KeyLiveInterestSeconds:
		return DefaultLiveInterestSeconds, MinLiveInterestSeconds, MaxLiveInterestSeconds, true
	case KeyLiveSampleRetentionSeconds:
		return DefaultLiveSampleRetentionSeconds, MinLiveSampleRetentionSeconds, MaxLiveSampleRetentionSeconds, true
	}
	return 0, 0, 0, false
}

// LiveSettingRefusal is a live setting value refused, with its closed code and the key.
type LiveSettingRefusal struct {
	Code string
	Key  string
}

// Error names the key and the code.
func (r *LiveSettingRefusal) Error() string {
	return fmt.Sprintf("config: %s refused (%s)", r.Key, r.Code)
}

// ParseLiveSetting reads one live setting's stored text, refusing it with a closed code naming the
// key when it is not a whole number of seconds within the key's bounds.
func ParseLiveSetting(key, value string) (int64, error) {
	_, minimum, maximum, known := LiveSettingBounds(key)
	if !known {
		return 0, &LiveSettingRefusal{Code: LiveSettingUnknown, Key: key}
	}
	seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0, &LiveSettingRefusal{Code: LiveSettingInvalid, Key: key}
	}
	if seconds < minimum || seconds > maximum {
		return 0, &LiveSettingRefusal{Code: LiveSettingOutOfRange, Key: key}
	}
	return seconds, nil
}

// loadLive reads the two live settings into a configuration. An out-of-range row is refused at
// load, naming the key (C-AC11).
func loadLive(ctx context.Context, reader SettingReader, loaded *Config) error {
	for _, setting := range []struct {
		key    string
		target *int64
	}{
		{KeyLiveInterestSeconds, &loaded.LiveInterestSeconds},
		{KeyLiveSampleRetentionSeconds, &loaded.LiveSampleRetentionSeconds},
	} {
		value, present, err := reader.Setting(ctx, setting.key)
		if err != nil {
			return err
		}
		if !present {
			continue
		}
		seconds, err := ParseLiveSetting(setting.key, value)
		if err != nil {
			_, minimum, maximum, _ := LiveSettingBounds(setting.key)
			return fmt.Errorf("config: %s is %q, not a whole number of seconds from %d to %d: %w",
				setting.key, value, minimum, maximum, err)
		}
		*setting.target = seconds
	}
	return nil
}
