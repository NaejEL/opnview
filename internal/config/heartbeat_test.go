package config

import (
	"context"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/storetest"
)

// TestTheHeartbeatSettingsAreValidatedRowsWithTheirDefaults is D2 of
// SPEC-step-5b-widget-api.md: the heartbeat interval and the downtime threshold are setting
// rows the schema writes, 60 s and 300 s by default; each is a positive whole number of
// seconds; and a threshold at or below the interval is refused, naming both, because the
// service's own cadence would then read as downtime.
func TestTheHeartbeatSettingsAreValidatedRowsWithTheirDefaults(t *testing.T) {
	ctx := context.Background()
	loaded, err := Load(ctx, settingTable{rows: map[string]string{}})
	if err != nil || loaded.HeartbeatIntervalSeconds != 60 || loaded.DowntimeThresholdSeconds != 300 {
		t.Errorf("with no row the pair is %d, %d (%v), not 60, 300", loaded.HeartbeatIntervalSeconds,
			loaded.DowntimeThresholdSeconds, err)
	}
	database, err := store.Open(ctx, storetest.Database(t))
	if err != nil {
		t.Fatalf("opening a database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	for key, want := range map[string]string{KeyHeartbeatInterval: "60", KeyDowntimeThreshold: "300"} {
		if value, present, err := database.Setting(ctx, key); err != nil || !present || value != want {
			t.Errorf("the schema's %s row is %q (present %t, %v), not %s", key, value, present, err, want)
		}
	}

	loaded, err = Load(ctx, settingTable{rows: map[string]string{KeyHeartbeatInterval: "30",
		KeyDowntimeThreshold: "120"}})
	if err != nil || loaded.HeartbeatIntervalSeconds != 30 || loaded.DowntimeThresholdSeconds != 120 {
		t.Errorf("rows of 30 and 120 gave %d, %d (%v)", loaded.HeartbeatIntervalSeconds,
			loaded.DowntimeThresholdSeconds, err)
	}
	for _, key := range []string{KeyHeartbeatInterval, KeyDowntimeThreshold} {
		for _, value := range []string{"0", "-1", "1.5", "a minute", ""} {
			if _, err := Load(ctx, settingTable{rows: map[string]string{key: value}}); err == nil ||
				!strings.Contains(err.Error(), key) {
				t.Errorf("%s = %q was accepted, or refused without naming the key: %v", key, value, err)
			}
		}
	}
	for _, threshold := range []string{"60", "30"} {
		_, err := Load(ctx, settingTable{rows: map[string]string{KeyHeartbeatInterval: "60",
			KeyDowntimeThreshold: threshold}})
		if err == nil || !strings.Contains(err.Error(), KeyDowntimeThreshold) ||
			!strings.Contains(err.Error(), KeyHeartbeatInterval) {
			t.Errorf("a threshold of %s s beside a 60 s heartbeat gave %v", threshold, err)
		}
	}
}
