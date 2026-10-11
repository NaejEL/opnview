package web

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/config"
)

// The collection surface's settings cards.
//
// PROVENANCE OF EVERY FIXTURE HERE: SYNTHESISED. Every value typed is a boundary of the
// rule config.Load applies, chosen here; the providers are the registry the schema
// seeds. Nothing is read off a network.

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
	h := signedInHarness(t)
	page := pageSource(t, h, PathCollection)
	for kind, interval := range map[string][2]string{"resolver_cache": {"1", unitMinutes},
		"resolver_local_data": {"5", unitMinutes}} {
		card := cardOf(t, page, kind)
		// The interval in force is shown once, in its field, in its longest exact unit.
		if !durationFieldShows(t, card, fieldInterval+"_"+kind, interval[0], interval[1]) {
			t.Errorf("the %s card's interval field does not hold %s %s", kind, interval[0], interval[1])
		}
		if strings.Contains(card, `class="in-force"`) {
			t.Errorf("the %s card shows its interval in force twice", kind)
		}
		// AC13: no measurement, no suggestion, no fill control.
		for _, absent := range []string{catalogueText(t, "collection.group.measurement"),
			catalogueText(t, "collection.group.suggestion"), `name="` + fieldFill + `"`} {
			if strings.Contains(card, absent) {
				t.Errorf("the %s card carries %q", kind, absent)
			}
		}
		for _, row := range []string{"unbound", "dnsmasq"} {
			if !stateShown(t, card, providerLabelKey(kind, row), msgAvailabilityNotYetProbed) {
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
	if _, err := h.store.DB().Exec(`DELETE FROM source_availability WHERE provider_id IN
		(SELECT id FROM provider WHERE kind = 'resolver_cache')`); err != nil {
		t.Fatalf("deleting the availability rows: %v", err)
	}
	if _, err := h.store.DB().Exec("DELETE FROM provider WHERE kind = 'resolver_cache'"); err != nil {
		t.Fatalf("deleting the provider rows: %v", err)
	}
	page = pageSource(t, h, PathCollection)
	if strings.Contains(page, `id="card_resolver_cache"`) {
		t.Error("the resolver cache card is drawn with no provider row")
	}
	if !strings.Contains(page, `id="card_resolver_local_data"`) {
		t.Error("removing one kind's rows removed the other's card")
	}
	if status, _ := statusAndBody(t, h.post(PathCollectionSettings, url.Values{
		fieldCard: {"resolver_cache"}, cardFieldName("resolver_cache", fieldInterval): {"90"}})); status != http.StatusBadRequest {
		t.Errorf("saving a kind the registry does not hold answered %d", status)
	}
}

// TestASavedResolverIntervalReachesTheRunningServiceAndRefusalsStoreNothing is AC10.
func TestASavedResolverIntervalReachesTheRunningServiceAndRefusalsStoreNothing(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	generation := h.live.Generation()
	status, page := followed(t, h, h.post(PathCollectionSettings, url.Values{
		fieldCard: {"resolver_local_data"}, cardFieldName("resolver_local_data", fieldInterval): {"420"}}))
	if status != http.StatusSeeOther {
		t.Fatalf("saving an interval answered %d", status)
	}
	if got := h.setting(config.KeyResolverLocalDataInterval); got != "420" {
		t.Errorf("the stored interval is %q", got)
	}
	loaded, err := config.Load(context.Background(), h.store)
	if err != nil || loaded.ResolverLocalDataInterval != 420*time.Second {
		t.Errorf("config.Load returns %s (%v)", loaded.ResolverLocalDataInterval, err)
	}
	if h.live.Generation() != generation+1 || h.live.Get().ResolverLocalDataInterval != 420*time.Second {
		t.Error("the live holder was not handed the saved interval")
	}
	received := h.collector.received()
	if len(received) != 1 || received[0].ResolverLocalDataInterval != 420*time.Second {
		t.Error("the running collector was not configured with the saved interval")
	}
	card := cardOf(t, page, "resolver_local_data")
	if !strings.Contains(card, catalogueText(t, msgCollectionSaved)) ||
		!durationFieldShows(t, card, fieldInterval+"_resolver_local_data", "7", unitMinutes) {
		t.Error("the save is not confirmed in its card with the interval now in force")
	}

	// A figure in a unit is stored in seconds; a unit that is not one is refused.
	status, _ = statusAndBody(t, h.post(PathCollectionSettings, url.Values{
		fieldCard: {"resolver_local_data"}, cardFieldName("resolver_local_data", fieldInterval): {"2"},
		unitFieldName(cardFieldName("resolver_local_data", fieldInterval)): {unitHours}}))
	if status != http.StatusSeeOther || h.setting(config.KeyResolverLocalDataInterval) != "7200" {
		t.Errorf("2 hours answered %d and stored %q, want 7200", status,
			h.setting(config.KeyResolverLocalDataInterval))
	}
	status, page = statusAndBody(t, h.post(PathCollectionSettings, url.Values{
		fieldCard: {"resolver_local_data"}, cardFieldName("resolver_local_data", fieldInterval): {"2"},
		unitFieldName(cardFieldName("resolver_local_data", fieldInterval)): {"fortnights"}}))
	if status != http.StatusBadRequest || h.setting(config.KeyResolverLocalDataInterval) != "7200" ||
		!strings.Contains(cardOf(t, page, "resolver_local_data"), catalogueText(t, msgDurationUnitUnknown)) {
		t.Errorf("an unknown unit answered %d and was not refused against its field", status)
	}
	generation = h.live.Generation()
	receivedBefore := len(h.collector.received())

	for _, typed := range []string{"often", "0", "-5", "1.5",
		strconv.FormatInt(config.MaxIntervalSeconds+1, 10)} {
		status, page := statusAndBody(t, h.post(PathCollectionSettings, url.Values{
			fieldCard: {"resolver_cache"}, cardFieldName("resolver_cache", fieldInterval): {typed}}))
		if status != http.StatusBadRequest {
			t.Errorf("the interval %q answered %d", typed, status)
		}
		if _, present, err := h.store.Setting(context.Background(), config.KeyResolverCacheInterval); err != nil || present {
			t.Errorf("the refused interval %q was stored", typed)
		}
		card := cardOf(t, page, "resolver_cache")
		id := fieldInterval + "_resolver_cache"
		if fieldValue(card, id) != typed || !strings.Contains(card, `aria-describedby="`+id+`_refusal"`) ||
			!strings.Contains(card, `class="in-force"`) {
			t.Errorf("the refused interval %q is not kept and marked in its field, the interval in force beneath", typed)
		}
	}
	if len(h.collector.received()) != receivedBefore || h.live.Generation() != generation {
		t.Error("a refused interval reached the running collector")
	}
}

// TestASelectionSavedOnAResolverCardIsStoredAndApplied is AC12.
func TestASelectionSavedOnAResolverCardIsStoredAndApplied(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	status, page := followed(t, h, h.post(PathCollectionSettings, url.Values{
		fieldCard: {"resolver_cache"}, cardFieldName("resolver_cache", fieldInterval): {"60"},
		cardFieldName("resolver_cache", fieldSelectionPrefix+"dnsmasq"): {"off"}, cardFieldName("resolver_cache", fieldSelectionPrefix+"unbound"): {"on"}}))
	if status != http.StatusSeeOther {
		t.Fatalf("saving a selection answered %d", status)
	}
	if h.setting(config.KeySourceSelection("resolver_cache", "dnsmasq")) != "off" ||
		h.setting(config.KeySourceSelection("resolver_cache", "unbound")) != "on" {
		t.Error("the selections were not stored under their keys")
	}
	if got := h.collector.reselectedKinds(); len(got) != 1 || got[0] != "resolver_cache" {
		t.Errorf("the running collector applied %v, want [resolver_cache]", got)
	}
	if selectedIn(t, cardOf(t, page, "resolver_cache"), cardFieldName("resolver_cache", "selection_unbound")) != "on" {
		t.Error("the card does not show the selection now in force")
	}

	status, page = statusAndBody(t, h.post(PathCollectionSettings, url.Values{
		fieldCard: {"resolver_cache"}, cardFieldName("resolver_cache", fieldInterval): {"60"}, cardFieldName("resolver_cache", fieldSelectionPrefix+"unbound"): {"maybe"}}))
	if status != http.StatusBadRequest {
		t.Errorf("a selection that is not one of the three answered %d", status)
	}
	if !strings.Contains(cardOf(t, page, "resolver_cache"),
		`aria-describedby="resolver_cache.selection_unbound_refusal"`) {
		t.Error("the refused selection is not marked")
	}
	if h.setting(config.KeySourceSelection("resolver_cache", "unbound")) != "on" {
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
			h := signedInHarness(t)
			for _, value := range values {
				before, wasPresent, err := h.store.Setting(context.Background(), setting.key)
				if err != nil {
					t.Fatalf("reading %s: %v", setting.key, err)
				}
				var answer *http.Response
				switch setting.card {
				case "":
					answer = h.post(PathSettings, url.Values{fieldSettingsCard: {settingsCardPublicSuffix},
						setting.key: {value}})
				case "resolver_cache", "resolver_local_data":
					answer = h.post(PathCollectionSettings, url.Values{fieldCard: {setting.card},
						cardFieldName(setting.card, fieldInterval): {value}})
				default:
					form := url.Values{fieldCard: {setting.card}, setting.key: {value}}
					for _, spec := range settingCardSpecs() {
						if spec.ID != setting.card {
							continue
						}
						for _, other := range spec.Settings {
							if other.Key != setting.key {
								current, _, _ := h.store.Setting(context.Background(), other.Key)
								form.Set(other.Key, current)
							}
						}
					}
					if setting.confirm {
						form.Set(fieldConfirm, value)
					}
					answer = h.post(PathCollectionSettings, form)
				}
				status, _ := statusAndBody(t, answer)
				accepted := loadAccepts(setting.key, value)
				after, present, err := h.store.Setting(context.Background(), setting.key)
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
					if _, err := config.Load(context.Background(), h.store); err != nil {
						t.Errorf("config.Load refuses what the form stored: %v", err)
					}
				}
			}
		})
	}
}

// durationFieldShows says whether a card's duration field holds a figure in a unit.
func durationFieldShows(t *testing.T, card, id, figure, unit string) bool {
	t.Helper()
	return fieldValue(card, id) == figure && selectedIn(t, card, id+"_unit") == unit
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
	h := signedInHarness(t)
	defaults := config.Defaults()
	for _, spec := range settingCardSpecs() {
		for _, setting := range spec.Settings {
			if setting.Rule == ruleAggregateMode {
				continue
			}
			if err := h.store.DeleteSetting(context.Background(), setting.Key); err != nil {
				t.Fatalf("deleting %s: %v", setting.Key, err)
			}
		}
	}
	page := pageSource(t, h, PathCollection)
	for _, spec := range settingCardSpecs() {
		card := cardOf(t, page, spec.ID)
		for _, setting := range spec.Settings {
			switch {
			case setting.Rule == ruleAggregateMode:
				if selectedIn(t, card, setting.Key) != aggregateModeFull {
					t.Error("the site-name mode does not show its default")
				}
			case setting.Duration:
				figure, unit := fieldDuration(setting.inForce(defaults))
				if !durationFieldShows(t, card, setting.Key, figure, unit) {
					t.Errorf("%s does not show its default %s %s with no row", setting.Key, figure, unit)
				}
			default:
				want := strconv.FormatInt(setting.inForce(defaults), 10)
				if fieldValue(card, setting.Key) != want {
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
		if err := h.store.SetSetting(context.Background(), key, value, h.clock.Now().Unix()); err != nil {
			t.Fatalf("storing %s: %v", key, err)
		}
	}
	if err := h.store.SetSetting(context.Background(), config.KeyAggregateMode, "no_domains",
		h.clock.Now().Unix()); err != nil {
		t.Fatalf("storing the mode: %v", err)
	}
	// The surface shows the configuration in force, which is the live holder's.
	if err := h.server.applyConfiguration(context.Background()); err != nil {
		t.Fatalf("applying the configuration: %v", err)
	}
	page = pageSource(t, h, PathCollection)
	expected := map[string][2]string{
		config.KeyLegPairingWindow:    {"4", unitSeconds},
		config.KeyAttributionMaxDelay: {"9", unitSeconds},
		config.KeyCacheAnswerDelay:    {"30", unitMinutes},
		config.KeyRetentionSeconds:    {"0", unitSeconds},
	}
	for _, spec := range settingCardSpecs() {
		card := cardOf(t, page, spec.ID)
		for _, setting := range spec.Settings {
			switch {
			case setting.Rule == ruleAggregateMode:
				if selectedIn(t, card, setting.Key) != aggregateModeNoDomains {
					t.Error("the site-name mode does not show the stored mode")
				}
			case setting.Duration:
				want := expected[setting.Key]
				if !durationFieldShows(t, card, setting.Key, want[0], want[1]) {
					t.Errorf("%s does not show its stored value as %s %s", setting.Key, want[0], want[1])
				}
			default:
				if fieldValue(card, setting.Key) != stored[setting.Key] {
					t.Errorf("%s does not show its stored value %s", setting.Key, stored[setting.Key])
				}
			}
		}
	}
	// An unlimited retention is a named state, so it is drawn beneath its field.
	if line := inForceLineOf(cardOf(t, page, "storage"), config.KeyRetentionSeconds); !strings.Contains(line,
		`<span class="state">`+catalogueText(t, msgRetentionUnlimited)+"</span>") {
		t.Errorf("an unlimited retention is not its own state: %q", line)
	}
}

// TestAShorterRetentionIsConfirmedForTheValueItWarnedAbout: the next purge deletes
// what is older than the new horizon, so a shorter one stores nothing until it is
// confirmed; the confirmation states both horizons, offers no field to change them,
// is not drawn as a success, and confirms one horizon only.
func TestAShorterRetentionIsConfirmedForTheValueItWarnedAbout(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	form := url.Values{fieldCard: {"storage"}, config.KeyRetentionSeconds: {"1"},
		unitFieldName(config.KeyRetentionSeconds): {unitDays}, config.KeyAggregateMode: {"full"}}
	status, page := statusAndBody(t, h.post(PathCollectionSettings, form))
	if status != http.StatusOK {
		t.Fatalf("a shorter retention answered %d", status)
	}
	if got := h.setting(config.KeyRetentionSeconds); got != "7776000" {
		t.Errorf("a shorter retention was stored before its confirmation: %q", got)
	}
	card := cardOf(t, page, "storage")
	for _, expected := range []string{
		`<p class="notice bad">` + catalogueText(t, msgRetentionConfirm) + "</p>",
		"<dt>" + catalogueText(t, msgLabelRetentionNow) + "</dt>\n<dd>" + durationText(t, 7776000) + "</dd>",
		"<dt>" + catalogueText(t, msgLabelRetentionRequested) + "</dt>\n<dd>" + durationText(t, 86400) + "</dd>",
		`<input type="hidden" name="` + config.KeyRetentionSeconds + `" value="86400">`,
		`<input type="hidden" name="` + fieldConfirm + `" value="86400">`,
		`<button type="submit" name="` + fieldCard + `" value="storage" formaction="` + PathCollectionSettings +
			`#card_storage" class="destructive">`,
		`<button type="submit" name="` + fieldCancel + `" value="storage" formaction="` + PathCollectionSettings +
			`#card_storage" class="secondary">` + catalogueText(t, "action.cancel"),
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
	bound := url.Values{fieldCard: {"storage"}, config.KeyRetentionSeconds: {"7000000"},
		config.KeyAggregateMode: {"full"}, fieldConfirm: {"86400"}}
	status, page = statusAndBody(t, h.post(PathCollectionSettings, bound))
	if status != http.StatusOK || h.setting(config.KeyRetentionSeconds) != "7776000" {
		t.Errorf("a horizon other than the one confirmed answered %d and stored %q", status,
			h.setting(config.KeyRetentionSeconds))
	}
	if !strings.Contains(cardOf(t, page, "storage"), `name="`+fieldConfirm+`" value="7000000"`) {
		t.Error("a horizon other than the one confirmed is not asked about afresh")
	}

	// The confirmation as drawn stores the horizon it named.
	discard(t, h.post(PathCollectionSettings, url.Values{fieldCard: {"storage"},
		config.KeyRetentionSeconds: {"86400"}, config.KeyAggregateMode: {"full"}, fieldConfirm: {"86400"}}))
	if got := h.setting(config.KeyRetentionSeconds); got != "86400" {
		t.Errorf("a confirmed retention was not stored: %q", got)
	}
	// A longer one, or unlimited, purges nothing more and needs no confirmation.
	discard(t, h.post(PathCollectionSettings, url.Values{fieldCard: {"storage"},
		config.KeyRetentionSeconds: {"0"}, config.KeyAggregateMode: {"full"}}))
	if got := h.setting(config.KeyRetentionSeconds); got != "0" {
		t.Errorf("an unlimited retention waited for a confirmation: %q", got)
	}
}

// TestThePairingWindowRefusalStatesTheRangeConfigAccepts: the range is asked of
// config.Load, so a bound added there is stated without another edit.
func TestThePairingWindowRefusalStatesTheRangeConfigAccepts(t *testing.T) {
	t.Parallel()
	h := signedInHarness(t)
	status, page := statusAndBody(t, h.post(PathCollectionSettings, url.Values{
		fieldCard: {"pairing"}, config.KeyLegPairingWindow: {"-1"}}))
	if status != http.StatusBadRequest {
		t.Fatalf("a negative pairing window answered %d", status)
	}
	found := acceptedRangeOf(config.KeyLegPairingWindow)
	if found == nil {
		t.Fatal("config.Load accepts no pairing window at all")
	}
	// The range found is the one config.Load applies, at both ends.
	low := mustSeconds(t, found.Low.Parts)
	if !loadAccepts(config.KeyLegPairingWindow, strconv.FormatInt(low, 10)) ||
		loadAccepts(config.KeyLegPairingWindow, strconv.FormatInt(low-1, 10)) {
		t.Errorf("the range's low end %d is not config.Load's", low)
	}
	high := catalogueText(t, msgRangeUnbounded)
	if found.High.StateKey == "" {
		top := mustSeconds(t, found.High.Parts)
		high = durationText(t, top)
		if !loadAccepts(config.KeyLegPairingWindow, strconv.FormatInt(top, 10)) ||
			loadAccepts(config.KeyLegPairingWindow, strconv.FormatInt(top+1, 10)) {
			t.Errorf("the range's high end %d is not config.Load's", top)
		}
	}
	refusal := cardOf(t, page, "pairing")
	refusal = refusal[strings.Index(refusal, `<p class="field-refusal"`):]
	refusal = refusal[:strings.Index(refusal, "</p>")]
	for _, expected := range []string{catalogueText(t, msgValueOutOfRange),
		catalogueText(t, "range.accepted_from") + " " + durationText(t, low) + " " +
			catalogueText(t, "range.to"), high} {
		if !strings.Contains(refusal, expected) {
			t.Errorf("the refusal does not state %q: %s", expected, refusal)
		}
	}
}

// mustSeconds adds a spelled duration back up into seconds.
func mustSeconds(t *testing.T, parts []durationPart) int64 {
	t.Helper()
	var total int64
	for _, part := range parts {
		count, err := strconv.ParseInt(part.Figure, 10, 64)
		if err != nil {
			t.Fatalf("a duration figure %q is not a number", part.Figure)
		}
		matched := false
		for _, unit := range durationUnits() {
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
