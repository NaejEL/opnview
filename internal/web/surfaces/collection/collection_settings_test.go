package collection_test

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/surfaces/collection"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"github.com/NaejEL/opnview/internal/web/webtest/collectiontest"
	"github.com/NaejEL/opnview/internal/web/webtest/settingstest"
)

// mapReader is a configuration holding exactly the rows given.
type mapReader map[string]string

// Setting reads one row.
func (m mapReader) Setting(_ context.Context, key string) (string, bool, error) {
	value, present := m[key]
	return value, present, nil
}

// loadAccepts says whether config.Load accepts one row on its own.
func loadAccepts(key, value string) bool {
	_, err := config.Load(context.Background(), mapReader{key: value})
	return err == nil
}

// polledOf is one polled kind of the collection answer, nil when absent.
func polledOf(answer map[string]any, kind string) any {
	for _, raw := range settingstest.Path(answer, "polled_kinds").([]any) {
		if settingstest.Path(raw, "kind") == kind {
			return raw
		}
	}
	return nil
}

// TestTheResolverKindsAreAnsweredWithTheirIntervalAndImplementations is AC9 and AC13: each polled
// kind the registry holds is answered with its interval in force and the availability of each
// implementation, and with no measurement and no suggestion.
func TestTheResolverKindsAreAnsweredWithTheirIntervalAndImplementations(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	answer := settingstest.GetJSON(h, collection.PathAPICollection)
	defaults := config.Defaults()
	for kind, interval := range map[string]time.Duration{"resolver_cache": defaults.ResolverCacheInterval,
		"resolver_local_data": defaults.ResolverLocalDataInterval} {
		polled := polledOf(answer, kind)
		if polled == nil {
			t.Fatalf("the polled kind %s is not answered", kind)
		}
		if got := settingstest.Path(polled, "interval_seconds"); got != float64(interval/time.Second) {
			t.Errorf("the %s interval is answered %v, want %d", kind, got, int64(interval/time.Second))
		}
		for _, absent := range []string{"measurement", "suggestion"} {
			if settingstest.Path(polled, absent) != nil {
				t.Errorf("the %s kind carries a %s", kind, absent)
			}
		}
		availability := map[string]any{}
		for _, implementation := range settingstest.Path(polled, "implementations").([]any) {
			availability[settingstest.Path(implementation, "provider_key").(string)] =
				settingstest.Path(implementation, "availability")
		}
		for _, row := range []string{"unbound", "dnsmasq"} {
			if availability[row] != web.ConditionOf(msgAvailabilityNotYetProbed) {
				t.Errorf("the %s kind answers the availability of %s as %v", kind, row, availability[row])
			}
		}
	}
}

// TestASavedResolverIntervalReachesTheRunningServiceAndRefusalsStoreNothing is AC10.
func TestASavedResolverIntervalReachesTheRunningServiceAndRefusalsStoreNothing(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	generation := h.Live.Generation()
	status, answer := settingstest.SendJSON(h, kindPath("resolver_local_data"), map[string]any{"interval_seconds": 420})
	if status != http.StatusOK {
		t.Fatalf("saving an interval answered %d %v", status, answer)
	}
	if got := collectiontest.Setting(h, config.KeyResolverLocalDataInterval); got != "420" {
		t.Errorf("the stored interval is %q", got)
	}
	loaded, err := config.Load(context.Background(), h.Store)
	if err != nil || loaded.ResolverLocalDataInterval != 420*time.Second {
		t.Errorf("config.Load returns %s (%v)", loaded.ResolverLocalDataInterval, err)
	}
	if h.Live.Generation() != generation+1 || h.Live.Get().ResolverLocalDataInterval != 420*time.Second {
		t.Error("the live holder was not handed the saved interval")
	}
	received := h.Collector.Received()
	if len(received) != 1 || received[0].ResolverLocalDataInterval != 420*time.Second {
		t.Error("the running collector was not configured with the saved interval")
	}
	if got := settingstest.Path(polledOf(answer, "resolver_local_data"), "interval_seconds"); got != float64(420) {
		t.Errorf("the save's answer reports the interval in force as %v", got)
	}

	generation = h.Live.Generation()
	receivedBefore := len(h.Collector.Received())
	for _, typed := range []any{"often", 0, -5, 1.5, config.MaxIntervalSeconds + 1} {
		status, answer := settingstest.SendJSON(h, kindPath("resolver_cache"), map[string]any{"interval_seconds": typed})
		if status != http.StatusBadRequest || answer["field"] != "interval_seconds" {
			t.Errorf("the interval %v answered %d %v", typed, status, answer)
		}
		if _, present, err := h.Store.Setting(context.Background(), config.KeyResolverCacheInterval); err != nil || present {
			t.Errorf("the refused interval %v was stored", typed)
		}
	}
	if len(h.Collector.Received()) != receivedBefore || h.Live.Generation() != generation {
		t.Error("a refused interval reached the running collector")
	}
}

// TestASelectionSavedOnAResolverKindIsStoredAndApplied is AC12.
func TestASelectionSavedOnAResolverKindIsStoredAndApplied(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	status, answer := settingstest.SendJSON(h, kindPath("resolver_cache"), map[string]any{"interval_seconds": 60,
		"selections": map[string]any{"dnsmasq": "off", "unbound": "on"}})
	if status != http.StatusOK {
		t.Fatalf("saving a selection answered %d %v", status, answer)
	}
	if collectiontest.Setting(h, config.KeySourceSelection("resolver_cache", "dnsmasq")) != "off" ||
		collectiontest.Setting(h, config.KeySourceSelection("resolver_cache", "unbound")) != "on" {
		t.Error("the selections were not stored under their keys")
	}
	if got := h.Collector.ReselectedKinds(); len(got) != 1 || got[0] != "resolver_cache" {
		t.Errorf("the running collector applied %v, want [resolver_cache]", got)
	}
	for _, implementation := range settingstest.Path(polledOf(answer, "resolver_cache"), "implementations").([]any) {
		if settingstest.Path(implementation, "provider_key") == "unbound" && settingstest.Path(implementation, "selection") != "on" {
			t.Errorf("the selection of unbound is answered %v", implementation)
		}
	}
	status, answer = settingstest.SendJSON(h, kindPath("resolver_cache"), map[string]any{"interval_seconds": 60,
		"selections": map[string]any{"unbound": "maybe"}})
	if status != http.StatusBadRequest || answer["field"] != "selections.unbound" {
		t.Errorf("a selection that is not one of the three answered %d %v", status, answer)
	}
	if collectiontest.Setting(h, config.KeySourceSelection("resolver_cache", "unbound")) != "on" {
		t.Error("a refused selection was stored")
	}
}

// settingCase is one setting, the route it is saved through, and whether a shorter value is
// confirmed with it.
type settingCase struct {
	key     string
	card    string
	confirm bool
}

// TestEverySettingAcceptsExactlyWhatConfigLoadAccepts is AC11: whatever route saves a setting
// stores exactly what config.Load accepts, and refuses, storing nothing, exactly what it refuses.
func TestEverySettingAcceptsExactlyWhatConfigLoadAccepts(t *testing.T) {
	t.Parallel()
	maxInterval := strconv.FormatInt(config.MaxIntervalSeconds, 10)
	overInterval := strconv.FormatInt(config.MaxIntervalSeconds+1, 10)
	cases := map[settingCase][]string{
		{key: config.KeyLegPairingWindow, card: "pairing"}:                    {"-1", "0", "1", "60", "61", "x", ""},
		{key: config.KeyLeaseBackendFailurePassLimit, card: "lease_backends"}: {"-1", "0", "1", "2", "x"},
		{key: config.KeyAttributionMaxDelay, card: "attribution"}:             {"-1", "0", "1", "5", "x"},
		{key: config.KeyCacheAnswerDelay, card: "attribution"}:                {"-1", "0", "1", "3600", "x"},
		{key: config.KeyRetentionSeconds, card: "storage", confirm: true}:     {"-1", "0", "1", "86400", "x"},
		{key: config.KeyAggregateMode, card: "storage"}:                       {"full", "no_domains", "domains", ""},
		{key: config.KeyResolverCacheInterval, card: "resolver_cache"}: {"-1", "0", "1", maxInterval,
			overInterval},
		{key: config.KeyResolverLocalDataInterval, card: "resolver_local_data"}: {"0", "1", maxInterval,
			overInterval},
		{key: config.KeyPublicSuffixRefreshInterval, card: ""}: {"-1", "0", "1", maxInterval, overInterval},
	}
	for setting, values := range cases {
		t.Run(setting.key, func(t *testing.T) {
			t.Parallel()
			h := collectiontest.SignedInHarness(t)
			for _, value := range values {
				before, wasPresent, err := h.Store.Setting(context.Background(), setting.key)
				if err != nil {
					t.Fatalf("reading %s: %v", setting.key, err)
				}
				var path string
				body := map[string]any{}
				switch setting.card {
				case "":
					path, body["refresh_interval_seconds"] = "/api/settings/public-suffix", value
				case "resolver_cache", "resolver_local_data":
					path, body["interval_seconds"] = kindPath(setting.card), value
				default:
					path, body[setting.key] = cardPath(setting.card), value
					for _, spec := range collection.SettingCardSpecs() {
						if spec.ID != setting.card {
							continue
						}
						for _, other := range spec.Settings {
							if other.Key != setting.key {
								current, _, _ := h.Store.Setting(context.Background(), other.Key)
								body[other.Key] = current
							}
						}
					}
					if setting.confirm {
						body["confirm"] = value
					}
				}
				status, _ := settingstest.SendJSON(h, path, body)
				accepted := loadAccepts(setting.key, value)
				after, present, err := h.Store.Setting(context.Background(), setting.key)
				if err != nil {
					t.Fatalf("reading %s: %v", setting.key, err)
				}
				switch {
				case accepted && (status != http.StatusOK || !present || after != value):
					t.Errorf("%q is accepted by config.Load and the save answered %d storing %q",
						value, status, after)
				case !accepted && (status != http.StatusBadRequest || present != wasPresent || after != before):
					t.Errorf("%q is refused by config.Load and the save answered %d storing %q",
						value, status, after)
				}
				if accepted {
					if _, err := config.Load(context.Background(), h.Store); err != nil {
						t.Errorf("config.Load refuses what the save stored: %v", err)
					}
				}
			}
		})
	}
}

// TestEverySettingAnswersItsValueInForce is AC11's first half: the default when no row exists,
// the stored value otherwise.
func TestEverySettingAnswersItsValueInForce(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	defaults := config.Defaults()
	for _, spec := range collection.SettingCardSpecs() {
		for _, setting := range spec.Settings {
			if err := h.Store.DeleteSetting(context.Background(), setting.Key); err != nil {
				t.Fatalf("deleting %s: %v", setting.Key, err)
			}
		}
	}
	if err := h.Server.ApplyConfiguration(context.Background()); err != nil {
		t.Fatalf("applying the configuration: %v", err)
	}
	values := func() map[string]any {
		found := map[string]any{}
		for _, card := range settingstest.Path(settingstest.GetJSON(h, collection.PathAPICollection), "settings").([]any) {
			for _, setting := range settingstest.Path(card, "settings").([]any) {
				found[settingstest.Path(setting, "key").(string)] = settingstest.Path(setting, "value")
			}
		}
		return found
	}
	answered := values()
	for _, spec := range collection.SettingCardSpecs() {
		for _, setting := range spec.Settings {
			var want any = float64(0)
			if setting.Rule == collection.RuleAggregateMode {
				want = webtest.AggregateModeFull
			} else {
				want = float64(collection.SettingInForce(setting, defaults))
			}
			if answered[setting.Key] != want {
				t.Errorf("%s is answered %v with no row, want its default %v", setting.Key, answered[setting.Key], want)
			}
		}
	}

	stored := map[string]string{
		config.KeyLegPairingWindow: "4", config.KeyLeaseBackendFailurePassLimit: "6",
		config.KeyAttributionMaxDelay: "9", config.KeyCacheAnswerDelay: "1800",
		config.KeyRetentionSeconds: "0", config.KeyAggregateMode: webtest.AggregateModeNoDomains,
	}
	for key, value := range stored {
		if err := h.Store.SetSetting(context.Background(), key, value, h.Clock.Now().Unix()); err != nil {
			t.Fatalf("storing %s: %v", key, err)
		}
	}
	// The answer is the configuration in force, which is the live holder's.
	if err := h.Server.ApplyConfiguration(context.Background()); err != nil {
		t.Fatalf("applying the configuration: %v", err)
	}
	answered = values()
	for key, value := range stored {
		var want any = value
		if number, err := strconv.ParseInt(value, 10, 64); err == nil {
			want = float64(number)
		}
		if answered[key] != want {
			t.Errorf("%s is answered %v, want the stored %v", key, answered[key], want)
		}
	}
}
