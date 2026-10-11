package broker

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/NaejEL/opnview/internal/broker/schedule"
)

// The broker's settings, rows of the setting table (A12 of the broker amendment, docs/plugins.md,
// "The broker"). An absent key takes its default; a value out of range is refused at load, naming
// the key, and the default is used in its place.
const (
	KeyMaxInFlight       = "broker_max_in_flight"
	KeyMaxCallsPerSecond = "broker_max_calls_per_second"
	KeyRealTimeShare     = "broker_real_time_share"
	KeyRealTimeMaxAge    = "broker_real_time_max_age_seconds"
	KeyBackoffMax        = "broker_backoff_max_seconds"
	// KeyFloorPrefix begins an endpoint's floor override: broker_floor_seconds:<path>.
	KeyFloorPrefix = "broker_floor_seconds:"
)

// SettingKeys is every key of the broker's settings but the per-endpoint floors.
func SettingKeys() []string {
	return []string{KeyMaxInFlight, KeyMaxCallsPerSecond, KeyRealTimeShare, KeyRealTimeMaxAge, KeyBackoffMax}
}

// SettingReader reads one setting row.
type SettingReader func(ctx context.Context, key string) (string, bool, error)

// bound is one setting's range.
type bound struct {
	minimum, maximum float64
	apply            func(*schedule.Settings, float64)
}

var bounds = map[string]bound{
	KeyMaxInFlight:       {1, 32, func(s *schedule.Settings, v float64) { s.MaxInFlight = int(v) }},
	KeyMaxCallsPerSecond: {0.1, 1000, func(s *schedule.Settings, v float64) { s.MaxPerSecond = v }},
	KeyRealTimeShare:     {0.01, 0.99, func(s *schedule.Settings, v float64) { s.RealTimeShare = v }},
	KeyRealTimeMaxAge: {0.1, 3600, func(s *schedule.Settings, v float64) {
		s.RealTimeMaxAge = time.Duration(v * float64(time.Second))
	}},
	KeyBackoffMax: {1, 86400, func(s *schedule.Settings, v float64) {
		s.BackoffMax = time.Duration(v * float64(time.Second))
	}},
}

// LoadSettings reads the broker's settings. Each value out of range is returned as an error naming
// its key, and its default stands.
func LoadSettings(ctx context.Context, read SettingReader) (schedule.Settings, []error) {
	settings := schedule.DefaultSettings()
	var refused []error
	for _, key := range SettingKeys() {
		text, present, err := read(ctx, key)
		if err != nil {
			refused = append(refused, fmt.Errorf("broker: reading %s: %w", key, err))
			continue
		}
		if !present {
			continue
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
		limits := bounds[key]
		if err != nil || value < limits.minimum || value > limits.maximum {
			refused = append(refused, fmt.Errorf("broker: the setting %s is %q, outside %g to %g; its default "+
				"stands", key, text, limits.minimum, limits.maximum))
			continue
		}
		limits.apply(&settings, value)
	}
	return settings, refused
}

// floorOverride reads the operator's floor of one endpoint, zero for none.
func floorOverride(ctx context.Context, read SettingReader, path string) time.Duration {
	if read == nil {
		return 0
	}
	text, present, err := read(ctx, KeyFloorPrefix+path)
	if err != nil || !present {
		return 0
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil || seconds < 0 || seconds > 86400 {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}
