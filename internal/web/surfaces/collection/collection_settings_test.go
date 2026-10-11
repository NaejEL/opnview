package collection_test

import (
	"context"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/surfaces/collection"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
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

// TestTheResolverCardsAreDrawnExactlyWhenTheRegistryHoldsTheirKind is AC9 and AC13.
func TestTheResolverCardsAreDrawnExactlyWhenTheRegistryHoldsTheirKind(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	page := webtest.PageSource(t, h, webtest.PathCollection)
	for kind, interval := range map[string][2]string{"resolver_cache": {"1", web.UnitMinutes},
		"resolver_local_data": {"5", web.UnitMinutes}} {
		card := webtest.CardOf(t, page, kind)
		// The interval in force is shown once, in its field, in its longest exact unit.
		if !webtest.DurationFieldShows(t, card, webtest.FieldInterval+"_"+kind, interval[0], interval[1]) {
			t.Errorf("the %s card's interval field does not hold %s %s", kind, interval[0], interval[1])
		}
		if strings.Contains(card, `class="in-force"`) {
			t.Errorf("the %s card shows its interval in force twice", kind)
		}
		// AC13: no measurement, no suggestion, no fill control.
		for _, absent := range []string{webtest.CatalogueText(t, "collection.group.measurement"),
			webtest.CatalogueText(t, "collection.group.suggestion"), `name="` + webtest.FieldFill + `"`} {
			if strings.Contains(card, absent) {
				t.Errorf("the %s card carries %q", kind, absent)
			}
		}
		for _, row := range []string{"unbound", "dnsmasq"} {
			if !webtest.StateShown(t, card, web.ProviderLabelKey(kind, row), msgAvailabilityNotYetProbed) {
				t.Errorf("the %s card does not show the availability of %s", kind, row)
			}
		}
	}
	// The cards follow the resolver lookups' card, and the attribution card follows them.
	order := []string{`id="card_dns_lookup"`, `id="card_resolver_cache"`, `id="card_resolver_local_data"`,
		`id="card_attribution"`}
	for index := 1; index < len(order); index++ {
		if strings.Index(page, order[index-1]) > strings.Index(page, order[index]) {
			t.Errorf("%s is drawn before %s", order[index], order[index-1])
		}
	}

	// Without the registry rows, no card.
	if _, err := h.Store.DB().Exec(`DELETE FROM source_availability WHERE provider_id IN
		(SELECT id FROM provider WHERE kind = 'resolver_cache')`); err != nil {
		t.Fatalf("deleting the availability rows: %v", err)
	}
	if _, err := h.Store.DB().Exec("DELETE FROM provider WHERE kind = 'resolver_cache'"); err != nil {
		t.Fatalf("deleting the provider rows: %v", err)
	}
	page = webtest.PageSource(t, h, webtest.PathCollection)
	if strings.Contains(page, `id="card_resolver_cache"`) {
		t.Error("the resolver cache card is drawn with no provider row")
	}
	if !strings.Contains(page, `id="card_resolver_local_data"`) {
		t.Error("removing one kind's rows removed the other's card")
	}
	if status, _ := webtest.StatusAndBody(t, h.Post(webtest.PathCollectionSettings, url.Values{
		webtest.FieldCard: {"resolver_cache"}, webtest.CardFieldName("resolver_cache", webtest.FieldInterval): {"90"}})); status != http.StatusBadRequest {
		t.Errorf("saving a kind the registry does not hold answered %d", status)
	}
}

// TestASavedResolverIntervalReachesTheRunningServiceAndRefusalsStoreNothing is AC10.
func TestASavedResolverIntervalReachesTheRunningServiceAndRefusalsStoreNothing(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	generation := h.Live.Generation()
	status, page := webtest.Followed(t, h, h.Post(webtest.PathCollectionSettings, url.Values{
		webtest.FieldCard: {"resolver_local_data"}, webtest.CardFieldName("resolver_local_data", webtest.FieldInterval): {"420"}}))
	if status != http.StatusSeeOther {
		t.Fatalf("saving an interval answered %d", status)
	}
	if got := h.Setting(config.KeyResolverLocalDataInterval); got != "420" {
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
	card := webtest.CardOf(t, page, "resolver_local_data")
	if !strings.Contains(card, webtest.CatalogueText(t, msgCollectionSaved)) ||
		!webtest.DurationFieldShows(t, card, webtest.FieldInterval+"_resolver_local_data", "7", web.UnitMinutes) {
		t.Error("the save is not confirmed in its card with the interval now in force")
	}

	// A figure in a unit is stored in seconds; a unit that is not one is refused.
	status, _ = webtest.StatusAndBody(t, h.Post(webtest.PathCollectionSettings, url.Values{
		webtest.FieldCard: {"resolver_local_data"}, webtest.CardFieldName("resolver_local_data", webtest.FieldInterval): {"2"},
		web.UnitFieldName(webtest.CardFieldName("resolver_local_data", webtest.FieldInterval)): {web.UnitHours}}))
	if status != http.StatusSeeOther || h.Setting(config.KeyResolverLocalDataInterval) != "7200" {
		t.Errorf("2 hours answered %d and stored %q, want 7200", status,
			h.Setting(config.KeyResolverLocalDataInterval))
	}
	status, page = webtest.StatusAndBody(t, h.Post(webtest.PathCollectionSettings, url.Values{
		webtest.FieldCard: {"resolver_local_data"}, webtest.CardFieldName("resolver_local_data", webtest.FieldInterval): {"2"},
		web.UnitFieldName(webtest.CardFieldName("resolver_local_data", webtest.FieldInterval)): {"fortnights"}}))
	if status != http.StatusBadRequest || h.Setting(config.KeyResolverLocalDataInterval) != "7200" ||
		!strings.Contains(webtest.CardOf(t, page, "resolver_local_data"), webtest.CatalogueText(t, msgDurationUnitUnknown)) {
		t.Errorf("an unknown unit answered %d and was not refused against its field", status)
	}
	generation = h.Live.Generation()
	receivedBefore := len(h.Collector.Received())

	for _, typed := range []string{"often", "0", "-5", "1.5",
		strconv.FormatInt(config.MaxIntervalSeconds+1, 10)} {
		status, page := webtest.StatusAndBody(t, h.Post(webtest.PathCollectionSettings, url.Values{
			webtest.FieldCard: {"resolver_cache"}, webtest.CardFieldName("resolver_cache", webtest.FieldInterval): {typed}}))
		if status != http.StatusBadRequest {
			t.Errorf("the interval %q answered %d", typed, status)
		}
		if _, present, err := h.Store.Setting(context.Background(), config.KeyResolverCacheInterval); err != nil || present {
			t.Errorf("the refused interval %q was stored", typed)
		}
		card := webtest.CardOf(t, page, "resolver_cache")
		id := webtest.FieldInterval + "_resolver_cache"
		if webtest.FieldValue(card, id) != typed || !strings.Contains(card, `aria-describedby="`+id+`_refusal"`) ||
			!strings.Contains(card, `class="in-force"`) {
			t.Errorf("the refused interval %q is not kept and marked in its field, the interval in force beneath", typed)
		}
	}
	if len(h.Collector.Received()) != receivedBefore || h.Live.Generation() != generation {
		t.Error("a refused interval reached the running collector")
	}
}

// TestASelectionSavedOnAResolverCardIsStoredAndApplied is AC12.
func TestASelectionSavedOnAResolverCardIsStoredAndApplied(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	status, page := webtest.Followed(t, h, h.Post(webtest.PathCollectionSettings, url.Values{
		webtest.FieldCard: {"resolver_cache"}, webtest.CardFieldName("resolver_cache", webtest.FieldInterval): {"60"},
		webtest.CardFieldName("resolver_cache", webtest.FieldSelectionPrefix+"dnsmasq"): {"off"}, webtest.CardFieldName("resolver_cache", webtest.FieldSelectionPrefix+"unbound"): {"on"}}))
	if status != http.StatusSeeOther {
		t.Fatalf("saving a selection answered %d", status)
	}
	if h.Setting(config.KeySourceSelection("resolver_cache", "dnsmasq")) != "off" ||
		h.Setting(config.KeySourceSelection("resolver_cache", "unbound")) != "on" {
		t.Error("the selections were not stored under their keys")
	}
	if got := h.Collector.ReselectedKinds(); len(got) != 1 || got[0] != "resolver_cache" {
		t.Errorf("the running collector applied %v, want [resolver_cache]", got)
	}
	if webtest.SelectedIn(t, webtest.CardOf(t, page, "resolver_cache"), webtest.CardFieldName("resolver_cache", "selection_unbound")) != "on" {
		t.Error("the card does not show the selection now in force")
	}

	status, page = webtest.StatusAndBody(t, h.Post(webtest.PathCollectionSettings, url.Values{
		webtest.FieldCard: {"resolver_cache"}, webtest.CardFieldName("resolver_cache", webtest.FieldInterval): {"60"}, webtest.CardFieldName("resolver_cache", webtest.FieldSelectionPrefix+"unbound"): {"maybe"}}))
	if status != http.StatusBadRequest {
		t.Errorf("a selection that is not one of the three answered %d", status)
	}
	if !strings.Contains(webtest.CardOf(t, page, "resolver_cache"),
		`aria-describedby="resolver_cache.selection_unbound_refusal"`) {
		t.Error("the refused selection is not marked")
	}
	if h.Setting(config.KeySourceSelection("resolver_cache", "unbound")) != "on" {
		t.Error("a refused selection was stored")
	}
}

// settingCase is one setting of scope 2, the card it is saved on, and the fields its
// card saves beside it.
type settingCase struct {
	key  string
	card string
	// confirm is sent with every save, so a shorter retention is not held for its
	// confirmation.
	confirm bool
}

// TestEverySettingAcceptsExactlyWhatConfigLoadAccepts is AC11.
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
			h := webtest.SignedInHarness(t)
			for _, value := range values {
				before, wasPresent, err := h.Store.Setting(context.Background(), setting.key)
				if err != nil {
					t.Fatalf("reading %s: %v", setting.key, err)
				}
				var answer *http.Response
				switch setting.card {
				case "":
					answer = h.Post(web.PathSettings, url.Values{webtest.FieldSettingsCard: {webtest.SettingsCardPublicSuffix},
						setting.key: {value}})
				case "resolver_cache", "resolver_local_data":
					answer = h.Post(webtest.PathCollectionSettings, url.Values{webtest.FieldCard: {setting.card},
						webtest.CardFieldName(setting.card, webtest.FieldInterval): {value}})
				default:
					form := url.Values{webtest.FieldCard: {setting.card}, setting.key: {value}}
					for _, spec := range collection.SettingCardSpecs() {
						if spec.ID != setting.card {
							continue
						}
						for _, other := range spec.Settings {
							if other.Key != setting.key {
								current, _, _ := h.Store.Setting(context.Background(), other.Key)
								form.Set(other.Key, current)
							}
						}
					}
					if setting.confirm {
						form.Set(webtest.FieldConfirm, value)
					}
					answer = h.Post(webtest.PathCollectionSettings, form)
				}
				status, _ := webtest.StatusAndBody(t, answer)
				accepted := loadAccepts(setting.key, value)
				after, present, err := h.Store.Setting(context.Background(), setting.key)
				if err != nil {
					t.Fatalf("reading %s: %v", setting.key, err)
				}
				switch {
				case accepted && (status != http.StatusSeeOther || !present || after != value):
					t.Errorf("%q is accepted by config.Load and the form answered %d storing %q",
						value, status, after)
				case !accepted && (status != http.StatusBadRequest || present != wasPresent || after != before):
					t.Errorf("%q is refused by config.Load and the form answered %d storing %q",
						value, status, after)
				}
				if accepted {
					if _, err := config.Load(context.Background(), h.Store); err != nil {
						t.Errorf("config.Load refuses what the form stored: %v", err)
					}
				}
			}
		})
	}
}

// inForceLineOf is the value-in-force line beneath one field, or the empty string:
// what follows the field's input up to the next field or the card's actions.
func inForceLineOf(card, id string) string {
	start := strings.Index(card, `id="`+id+`"`)
	if start < 0 {
		return ""
	}
	block := card[start:]
	for _, next := range []string{`<div class="field`, `<div class="actions">`} {
		if end := strings.Index(block, next); end > 0 {
			block = block[:end]
		}
	}
	at := strings.Index(block, `<p class="in-force">`)
	if at < 0 {
		return ""
	}
	return block[at : at+strings.Index(block[at:], "</p>")]
}

// TestEverySettingShowsItsValueInForce is AC11's first half: the default when no row
// exists, the stored value otherwise -- shown once, in its field, a duration in its
// longest exact unit; and drawn again beneath the field only when it is a named state.
func TestEverySettingShowsItsValueInForce(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	defaults := config.Defaults()
	for _, spec := range collection.SettingCardSpecs() {
		for _, setting := range spec.Settings {
			if setting.Rule == collection.RuleAggregateMode {
				continue
			}
			if err := h.Store.DeleteSetting(context.Background(), setting.Key); err != nil {
				t.Fatalf("deleting %s: %v", setting.Key, err)
			}
		}
	}
	page := webtest.PageSource(t, h, webtest.PathCollection)
	for _, spec := range collection.SettingCardSpecs() {
		card := webtest.CardOf(t, page, spec.ID)
		for _, setting := range spec.Settings {
			switch {
			case setting.Rule == collection.RuleAggregateMode:
				if webtest.SelectedIn(t, card, setting.Key) != webtest.AggregateModeFull {
					t.Error("the site-name mode does not show its default")
				}
			case setting.Duration:
				figure, unit := web.FieldDuration(collection.SettingInForce(setting, defaults))
				if !webtest.DurationFieldShows(t, card, setting.Key, figure, unit) {
					t.Errorf("%s does not show its default %s %s with no row", setting.Key, figure, unit)
				}
			default:
				want := strconv.FormatInt(collection.SettingInForce(setting, defaults), 10)
				if webtest.FieldValue(card, setting.Key) != want {
					t.Errorf("%s does not show its default %s with no row", setting.Key, want)
				}
			}
			if line := inForceLineOf(card, setting.Key); line != "" {
				t.Errorf("%s shows its value in force twice: %s", setting.Key, line)
			}
		}
	}

	stored := map[string]string{
		config.KeyLegPairingWindow: "4", config.KeyLeaseBackendFailurePassLimit: "6",
		config.KeyAttributionMaxDelay: "9", config.KeyCacheAnswerDelay: "1800",
		config.KeyRetentionSeconds: "0",
	}
	for key, value := range stored {
		if err := h.Store.SetSetting(context.Background(), key, value, h.Clock.Now().Unix()); err != nil {
			t.Fatalf("storing %s: %v", key, err)
		}
	}
	if err := h.Store.SetSetting(context.Background(), config.KeyAggregateMode, "no_domains",
		h.Clock.Now().Unix()); err != nil {
		t.Fatalf("storing the mode: %v", err)
	}
	// The surface shows the configuration in force, which is the live holder's.
	if err := h.Server.ApplyConfiguration(context.Background()); err != nil {
		t.Fatalf("applying the configuration: %v", err)
	}
	page = webtest.PageSource(t, h, webtest.PathCollection)
	expected := map[string][2]string{
		config.KeyLegPairingWindow:    {"4", web.UnitSeconds},
		config.KeyAttributionMaxDelay: {"9", web.UnitSeconds},
		config.KeyCacheAnswerDelay:    {"30", web.UnitMinutes},
		config.KeyRetentionSeconds:    {"0", web.UnitSeconds},
	}
	for _, spec := range collection.SettingCardSpecs() {
		card := webtest.CardOf(t, page, spec.ID)
		for _, setting := range spec.Settings {
			switch {
			case setting.Rule == collection.RuleAggregateMode:
				if webtest.SelectedIn(t, card, setting.Key) != webtest.AggregateModeNoDomains {
					t.Error("the site-name mode does not show the stored mode")
				}
			case setting.Duration:
				want := expected[setting.Key]
				if !webtest.DurationFieldShows(t, card, setting.Key, want[0], want[1]) {
					t.Errorf("%s does not show its stored value as %s %s", setting.Key, want[0], want[1])
				}
			default:
				if webtest.FieldValue(card, setting.Key) != stored[setting.Key] {
					t.Errorf("%s does not show its stored value %s", setting.Key, stored[setting.Key])
				}
			}
		}
	}
	// An unlimited retention is a named state, so it is drawn beneath its field.
	if line := inForceLineOf(webtest.CardOf(t, page, "storage"), config.KeyRetentionSeconds); !strings.Contains(line,
		`<span class="state">`+webtest.CatalogueText(t, msgRetentionUnlimited)+"</span>") {
		t.Errorf("an unlimited retention is not its own state: %q", line)
	}
}

// TestAShorterRetentionIsConfirmedForTheValueItWarnedAbout: the next purge deletes
// what is older than the new horizon, so a shorter one stores nothing until it is
// confirmed; the confirmation states both horizons, offers no field to change them,
// is not drawn as a success, and confirms one horizon only.
func TestAShorterRetentionIsConfirmedForTheValueItWarnedAbout(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	form := url.Values{webtest.FieldCard: {"storage"}, config.KeyRetentionSeconds: {"1"},
		web.UnitFieldName(config.KeyRetentionSeconds): {web.UnitDays}, config.KeyAggregateMode: {"full"}}
	status, page := webtest.StatusAndBody(t, h.Post(webtest.PathCollectionSettings, form))
	if status != http.StatusOK {
		t.Fatalf("a shorter retention answered %d", status)
	}
	if got := h.Setting(config.KeyRetentionSeconds); got != "7776000" {
		t.Errorf("a shorter retention was stored before its confirmation: %q", got)
	}
	card := webtest.CardOf(t, page, "storage")
	for _, expected := range []string{
		`<p class="notice bad">` + webtest.CatalogueText(t, msgRetentionConfirm) + "</p>",
		"<dt>" + webtest.CatalogueText(t, msgLabelRetentionNow) + "</dt>\n<dd>" + webtest.DurationText(t, 7776000) + "</dd>",
		"<dt>" + webtest.CatalogueText(t, msgLabelRetentionRequested) + "</dt>\n<dd>" + webtest.DurationText(t, 86400) + "</dd>",
		`<input type="hidden" name="` + config.KeyRetentionSeconds + `" value="86400">`,
		`<input type="hidden" name="` + webtest.FieldConfirm + `" value="86400">`,
		`<button type="submit" name="` + webtest.FieldCard + `" value="storage" formaction="` + webtest.PathCollectionSettings +
			`#card_storage" class="destructive">`,
		`<button type="submit" name="` + webtest.FieldCancel + `" value="storage" formaction="` + webtest.PathCollectionSettings +
			`#card_storage" class="secondary">` + webtest.CatalogueText(t, "action.cancel"),
	} {
		if !strings.Contains(card, expected) {
			t.Errorf("the confirmation does not carry %q", expected)
		}
	}
	for _, absent := range []string{`<p class="notice ok">`, `id="` + config.KeyRetentionSeconds + `"`,
		`<select`} {
		if strings.Contains(card, absent) {
			t.Errorf("the confirmation carries %q", absent)
		}
	}

	// THE CONFIRMATION IS FOR THE HORIZON IT SHOWED. Another shorter horizon sent with
	// it is asked about again, and nothing is stored.
	bound := url.Values{webtest.FieldCard: {"storage"}, config.KeyRetentionSeconds: {"7000000"},
		config.KeyAggregateMode: {"full"}, webtest.FieldConfirm: {"86400"}}
	status, page = webtest.StatusAndBody(t, h.Post(webtest.PathCollectionSettings, bound))
	if status != http.StatusOK || h.Setting(config.KeyRetentionSeconds) != "7776000" {
		t.Errorf("a horizon other than the one confirmed answered %d and stored %q", status,
			h.Setting(config.KeyRetentionSeconds))
	}
	if !strings.Contains(webtest.CardOf(t, page, "storage"), `name="`+webtest.FieldConfirm+`" value="7000000"`) {
		t.Error("a horizon other than the one confirmed is not asked about afresh")
	}

	// The confirmation as drawn stores the horizon it named.
	webtest.Discard(t, h.Post(webtest.PathCollectionSettings, url.Values{webtest.FieldCard: {"storage"},
		config.KeyRetentionSeconds: {"86400"}, config.KeyAggregateMode: {"full"}, webtest.FieldConfirm: {"86400"}}))
	if got := h.Setting(config.KeyRetentionSeconds); got != "86400" {
		t.Errorf("a confirmed retention was not stored: %q", got)
	}
	// A longer one, or unlimited, purges nothing more and needs no confirmation.
	webtest.Discard(t, h.Post(webtest.PathCollectionSettings, url.Values{webtest.FieldCard: {"storage"},
		config.KeyRetentionSeconds: {"0"}, config.KeyAggregateMode: {"full"}}))
	if got := h.Setting(config.KeyRetentionSeconds); got != "0" {
		t.Errorf("an unlimited retention waited for a confirmation: %q", got)
	}
}

// TestThePairingWindowRefusalStatesTheRangeConfigAccepts: the range is asked of
// config.Load, so a bound added there is stated without another edit.
func TestThePairingWindowRefusalStatesTheRangeConfigAccepts(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	status, page := webtest.StatusAndBody(t, h.Post(webtest.PathCollectionSettings, url.Values{
		webtest.FieldCard: {"pairing"}, config.KeyLegPairingWindow: {"-1"}}))
	if status != http.StatusBadRequest {
		t.Fatalf("a negative pairing window answered %d", status)
	}
	found := collection.AcceptedRangeOf(config.KeyLegPairingWindow)
	if found == nil {
		t.Fatal("config.Load accepts no pairing window at all")
	}
	// The range found is the one config.Load applies, at both ends.
	low := mustSeconds(t, found.Low.Parts)
	if !loadAccepts(config.KeyLegPairingWindow, strconv.FormatInt(low, 10)) ||
		loadAccepts(config.KeyLegPairingWindow, strconv.FormatInt(low-1, 10)) {
		t.Errorf("the range's low end %d is not config.Load's", low)
	}
	top := mustSeconds(t, found.High.Parts)
	high := webtest.DurationText(t, top)
	if !loadAccepts(config.KeyLegPairingWindow, strconv.FormatInt(top, 10)) ||
		loadAccepts(config.KeyLegPairingWindow, strconv.FormatInt(top+1, 10)) {
		t.Errorf("the range's high end %d is not config.Load's", top)
	}
	refusal := webtest.CardOf(t, page, "pairing")
	refusal = refusal[strings.Index(refusal, `<p class="field-refusal"`):]
	refusal = refusal[:strings.Index(refusal, "</p>")]
	for _, expected := range []string{webtest.CatalogueText(t, msgValueOutOfRange),
		webtest.CatalogueText(t, "range.accepted_from") + " " + webtest.DurationText(t, low) + " " +
			webtest.CatalogueText(t, "range.to"), high} {
		if !strings.Contains(refusal, expected) {
			t.Errorf("the refusal does not state %q: %s", expected, refusal)
		}
	}
}

// mustSeconds adds a spelled duration back up into seconds.
func mustSeconds(t *testing.T, parts []web.DurationPart) int64 {
	t.Helper()
	var total int64
	for _, part := range parts {
		count, err := strconv.ParseInt(part.Figure, 10, 64)
		if err != nil {
			t.Fatalf("a duration figure %q is not a number", part.Figure)
		}
		matched := false
		for _, unit := range web.DurationUnits() {
			if part.UnitKey == unit.One || part.UnitKey == unit.Other {
				total += count * unit.Seconds
				matched = true
			}
		}
		if !matched {
			t.Fatalf("a duration unit %q is not one of the four", part.UnitKey)
		}
	}
	return total
}
