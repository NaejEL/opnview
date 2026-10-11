package config

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/NaejEL/opnview/internal/store"
)

// The NetFlow settings (PS17-PS19 of the NetFlow join amendment to
// specs/SPEC-backend-completion-plugins.md): how often a flow_record provider's capture
// configuration is read, how far apart a leg and a flow record may be and still join, and how long
// flow records are kept.
const (
	// KeyFlowRecordInterval overrides how often the capture configuration is read.
	KeyFlowRecordInterval = "poll_interval_flow_record_seconds"
	// KeyNetflowJoinTolerance is how far after a leg was logged, in seconds, a flow record may start
	// and still join it (PS19 rule 3). The schema writes its default row.
	KeyNetflowJoinTolerance = store.NetflowJoinToleranceKey
	// KeyFlowRecordRetention is how long a flow record is kept, in seconds; 0 follows
	// retention_seconds (PS17). The schema writes its default row.
	KeyFlowRecordRetention = store.FlowRecordRetentionKey
)

// DefaultFlowRecordInterval is 60 s. Not a survey cadence: the read is one model's settings, which
// the firewall answers from its configuration, so its cost is a request a minute. The interval is the
// longest a changed capture configuration can go unseen, and so the longest a
// capture_configuration_unknown interval lasts after a change (Risk 60 of the amendment).
const DefaultFlowRecordInterval = 60 * time.Second

// DefaultNetflowJoinToleranceSeconds is 3 s, opnview's own, the sum of the three one-second
// resolutions the join compares across:
//   - the filter log's timestamp is whole seconds (survey, data source 1);
//   - OPNsense's NetFlow start instant is whole seconds -- the header's unix_secs and the uptime
//     fields are each truncated to a second (survey, Push sources, S10) -- so the instant computed
//     from them is off by up to a second;
//   - the normaliser reads a zone-less filter-log timestamp with the firewall's measured offset,
//     which is measured to the second.
//
// LV-NJ measures the distribution of record start minus leg instant and confirms or replaces it.
const DefaultNetflowJoinToleranceSeconds = store.DefaultNetflowJoinToleranceSeconds

// MaxNetflowJoinToleranceSeconds is 60 s: a tolerance larger than a minute would let a record join a
// leg logged a minute before it began, which no resolution explains.
const MaxNetflowJoinToleranceSeconds int64 = 60

// NetflowSettingKeys are the NetFlow settings a settings API lists, in order.
func NetflowSettingKeys() []string {
	return []string{KeyNetflowJoinTolerance, KeyFlowRecordRetention}
}

// ParseNetflowSetting reads one NetFlow setting's text, refusing it with the live settings' closed
// codes (liverate.go) when it is not a whole number within the key's bounds: the tolerance from 1
// to MaxNetflowJoinToleranceSeconds, the retention 0 or more.
func ParseNetflowSetting(key, value string) (int64, error) {
	seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	switch key {
	case KeyNetflowJoinTolerance:
		if err != nil {
			return 0, &LiveSettingRefusal{Code: LiveSettingInvalid, Key: key}
		}
		if seconds < 1 || seconds > MaxNetflowJoinToleranceSeconds {
			return 0, &LiveSettingRefusal{Code: LiveSettingOutOfRange, Key: key}
		}
	case KeyFlowRecordRetention:
		if err != nil {
			return 0, &LiveSettingRefusal{Code: LiveSettingInvalid, Key: key}
		}
		if seconds < 0 || seconds > MaxIntervalSeconds {
			return 0, &LiveSettingRefusal{Code: LiveSettingOutOfRange, Key: key}
		}
	default:
		return 0, &LiveSettingRefusal{Code: LiveSettingUnknown, Key: key}
	}
	return seconds, nil
}

// loadNetflow reads the NetFlow settings into a configuration. A row out of bounds is refused at
// load, naming the key.
func loadNetflow(ctx context.Context, reader SettingReader, loaded *Config) error {
	if value, present, err := reader.Setting(ctx, KeyFlowRecordInterval); err != nil {
		return err
	} else if present {
		seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || seconds <= 0 || seconds > MaxIntervalSeconds {
			return fmt.Errorf("config: %s is %q, not a positive whole number of seconds no larger than %d",
				KeyFlowRecordInterval, value, MaxIntervalSeconds)
		}
		loaded.FlowRecordInterval = time.Duration(seconds) * time.Second
	}
	for _, setting := range []struct {
		key    string
		target *int64
	}{
		{KeyNetflowJoinTolerance, &loaded.NetflowJoinToleranceSeconds},
		{KeyFlowRecordRetention, &loaded.FlowRecordRetentionSeconds},
	} {
		value, present, err := reader.Setting(ctx, setting.key)
		if err != nil {
			return err
		}
		if !present {
			continue
		}
		seconds, err := ParseNetflowSetting(setting.key, value)
		if err != nil {
			return fmt.Errorf("config: %s is %q, out of its bounds: %w", setting.key, value, err)
		}
		*setting.target = seconds
	}
	return nil
}

// NetflowSettingBounds returns a NetFlow setting's default and bounds; known is false for a key that is
// not one. The retention's default, 0, follows retention_seconds.
func NetflowSettingBounds(key string) (fallback, minimum, maximum int64, known bool) {
	switch key {
	case KeyNetflowJoinTolerance:
		return DefaultNetflowJoinToleranceSeconds, 1, MaxNetflowJoinToleranceSeconds, true
	case KeyFlowRecordRetention:
		return 0, 0, MaxIntervalSeconds, true
	}
	return 0, 0, 0, false
}
