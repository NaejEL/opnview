package config

import (
	"context"
	"strings"
	"testing"
)

// C-AC11: the live settings are setting rows, and an out-of-range value is refused at load,
// naming the key.

func TestLiveCadenceAndInterestSettingsHaveTheirDefaultsWithNoRow(t *testing.T) {
	loaded, err := Load(context.Background(), settingTable{rows: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.LiveInterestSeconds != 30 || loaded.LiveSampleRetentionSeconds != 3600 {
		t.Errorf("with no row the live settings are %d and %d, want 30 and 3600", loaded.LiveInterestSeconds,
			loaded.LiveSampleRetentionSeconds)
	}
}

func TestLiveCadenceAndInterestAnOutOfRangeSettingIsRefusedAtLoadNamingTheKey(t *testing.T) {
	for _, row := range []struct{ key, value string }{
		{KeyLiveInterestSeconds, "4"},
		{KeyLiveInterestSeconds, "3601"},
		{KeyLiveInterestSeconds, "half a minute"},
		{KeyLiveSampleRetentionSeconds, "599"},
		{KeyLiveSampleRetentionSeconds, "604801"},
	} {
		_, err := Load(context.Background(), settingTable{rows: map[string]string{row.key: row.value}})
		if err == nil || !strings.Contains(err.Error(), row.key) {
			t.Errorf("%s = %q loaded with %v, want a refusal naming the key", row.key, row.value, err)
		}
	}
	loaded, err := Load(context.Background(), settingTable{rows: map[string]string{
		KeyLiveInterestSeconds: "5", KeyLiveSampleRetentionSeconds: "604800"}})
	if err != nil || loaded.LiveInterestSeconds != 5 || loaded.LiveSampleRetentionSeconds != 604800 {
		t.Errorf("the bounds themselves loaded as %d, %d, %v", loaded.LiveInterestSeconds,
			loaded.LiveSampleRetentionSeconds, err)
	}
}

func TestLiveCadenceAndInterestTheLiveKeysAreTheCores(t *testing.T) {
	for _, key := range LiveSettingKeys() {
		if !IsCoreKey(key) {
			t.Errorf("%s is not a core key, so a plugin could shadow it", key)
		}
	}
}
