package declared_test

import (
	"context"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/collect/provider"
)

// TestADeclaredCadenceIsOverriddenNeverBelowItsBound is A3's scheduling rule for a provider
// stating its own cadence: it runs at its manifest cadence, overridden by its namespaced setting,
// and never below its declared lower bound; an override that cannot be read leaves the declared
// cadence in force. The override reaches a running loop, because the interval is asked before
// every wait.
func TestADeclaredCadenceIsOverriddenNeverBelowItsBound(t *testing.T) {
	database := collecttest.NewTestStore(t)
	collector := collect.NewWithSources(nil, database, core.SystemClock{}, nil)
	key := provider.CadenceSettingKey("example_inventory", "example_inventory", "example_inventory")
	interval := collector.OverriddenCadence(key, 10*time.Minute, 2*time.Minute)
	ctx := context.Background()
	for _, step := range []struct {
		stored string
		want   time.Duration
	}{
		{"", 10 * time.Minute},
		{"300", 5 * time.Minute},
		{"30", 2 * time.Minute},
		{"not a number", 10 * time.Minute},
		{"-5", 10 * time.Minute},
	} {
		if step.stored != "" {
			if err := database.SetSetting(ctx, key, step.stored, 1790000000); err != nil {
				t.Fatal(err)
			}
		}
		if got := interval(); got != step.want {
			t.Errorf("with %q stored the interval is %s, want %s", step.stored, got, step.want)
		}
	}
	if key != "example_inventory_cadence__example_inventory__example_inventory" {
		t.Errorf("the override is stored under %q", key)
	}
}
