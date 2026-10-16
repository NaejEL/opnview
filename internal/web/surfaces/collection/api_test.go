package collection_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/web/messages"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/web/settingfields"
	"github.com/NaejEL/opnview/internal/web/surfaces/collection"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"github.com/NaejEL/opnview/internal/web/webtest/collectiontest"
	"github.com/NaejEL/opnview/internal/web/webtest/settingstest"
)

// The collection surface's JSON equivalents (C-AC9): every figure of the page in the JSON answer,
// and every save of a card made, and refused, as the page makes and refuses it.

// settingRows renders every setting row.
func settingRows(t *testing.T, h *webtest.Harness) string {
	t.Helper()
	rows, err := h.Store.DB().Query("SELECT key, value FROM setting ORDER BY key")
	if err != nil {
		t.Fatalf("reading the settings: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var builder strings.Builder
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			t.Fatalf("reading the settings: %v", err)
		}
		builder.WriteString(key + "=" + value + "\n")
	}
	return builder.String()
}

// collectionInput is one input given to a page route and to its JSON route, on two installations.
type collectionInput struct {
	what     string
	pagePath string
	form     url.Values
	jsonPath string
	body     map[string]any
	refused  bool
}

// assertSameCollectionChange gives each input to the page on one installation and to its JSON
// route on another, and asserts both stored the same rows and told the running collector the
// same kinds, or both refused it and stored nothing.
func assertSameCollectionChange(t *testing.T, inputs []collectionInput) {
	t.Helper()
	for _, input := range inputs {
		t.Run(input.what, func(t *testing.T) {
			pageSide, jsonSide := collectiontest.SignedInHarness(t), collectiontest.SignedInHarness(t)
			pageBefore, jsonBefore := settingRows(t, pageSide), settingRows(t, jsonSide)
			answer := pageSide.Post(input.pagePath, input.form)
			webtest.Discard(t, answer)
			status, decoded := settingstest.SendJSON(jsonSide, input.jsonPath, input.body)
			if input.refused {
				if answer.StatusCode != http.StatusBadRequest {
					t.Errorf("the page answered %d rather than refusing", answer.StatusCode)
				}
				if status != http.StatusBadRequest || decoded["condition"] == nil {
					t.Errorf("the JSON route answered %d %v rather than refusing", status, decoded)
				}
				if settingRows(t, pageSide) != pageBefore || settingRows(t, jsonSide) != jsonBefore {
					t.Error("a refused input changed a row")
				}
				return
			}
			if answer.StatusCode != http.StatusSeeOther {
				t.Errorf("the page answered %d rather than saving", answer.StatusCode)
			}
			if status != http.StatusOK {
				t.Errorf("the JSON route answered %d %v rather than saving", status, decoded)
			}
			if page, json := settingRows(t, pageSide), settingRows(t, jsonSide); page != json {
				t.Errorf("the page and the JSON route stored different rows:\npage:\n%s\njson:\n%s", page, json)
			}
			if page, json := fmt.Sprint(pageSide.Collector.ReselectedKinds()),
				fmt.Sprint(jsonSide.Collector.ReselectedKinds()); page != json {
				t.Errorf("the page applied the selections of %s and the JSON route of %s", page, json)
			}
		})
	}
}

// TestTheCollectionJSONSavesMakeThePagesChangeAndRefuseItsInputs is C-AC9 for POST /collection and
// POST /collection/settings.
func TestTheCollectionJSONSavesMakeThePagesChangeAndRefuseItsInputs(t *testing.T) {
	t.Parallel()
	kindPath := strings.Replace(collection.PathAPICollectionKind, "{kind}", "firewall_log", 1)
	sized := func(what, pageSize, interval, selection string, refused bool) collectionInput {
		form := collectiontest.PairForm("firewall_log", pageSize, interval)
		body := map[string]any{"page_size": pageSize, "interval_seconds": interval}
		if number, err := strconv.Atoi(pageSize); err == nil {
			body["page_size"] = number
		}
		if number, err := strconv.Atoi(interval); err == nil {
			body["interval_seconds"] = number
		}
		if selection != "" {
			form.Set(webtest.CardFieldName("firewall_log", webtest.FieldSelectionPrefix+"pf"), selection)
			body["selections"] = map[string]any{"pf": selection}
		}
		return collectionInput{what: what, pagePath: webtest.PathCollection, form: form, jsonPath: kindPath,
			body: body, refused: refused}
	}
	polledPath := strings.Replace(collection.PathAPICollectionKind, "{kind}", "resolver_cache", 1)
	polled := func(what, interval, selection string, refused bool) collectionInput {
		form := url.Values{webtest.FieldCard: {"resolver_cache"},
			webtest.CardFieldName("resolver_cache", webtest.FieldInterval): {interval}}
		body := map[string]any{"interval_seconds": interval}
		if number, err := strconv.Atoi(interval); err == nil {
			body["interval_seconds"] = number
		}
		if selection != "" {
			form.Set(webtest.CardFieldName("resolver_cache", webtest.FieldSelectionPrefix+"unbound"), selection)
			body["selections"] = map[string]any{"unbound": selection}
		}
		return collectionInput{what: what, pagePath: webtest.PathCollectionSettings, form: form,
			jsonPath: polledPath, body: body, refused: refused}
	}
	cardPath := func(card string) string {
		return strings.Replace(collection.PathAPICollectionSettings, "{card}", card, 1)
	}
	setting := func(what, card string, values map[string]string, refused bool) collectionInput {
		form := url.Values{webtest.FieldCard: {card}}
		body := map[string]any{}
		for key, value := range values {
			form.Set(key, value)
			if number, err := strconv.ParseInt(value, 10, 64); err == nil {
				body[key] = number
			} else {
				body[key] = value
			}
		}
		return collectionInput{what: what, pagePath: webtest.PathCollectionSettings, form: form,
			jsonPath: cardPath(card), body: body, refused: refused}
	}
	assertSameCollectionChange(t, []collectionInput{
		sized("a sized kind's pair", "250", "30", "", false),
		sized("a sized kind's pair and a selection", "250", "30", "off", false),
		sized("an interval of nought", "250", "0", "", true),
		sized("an interval that is no number", "250", "soon", "", true),
		sized("a page of nought", "0", "30", "", true),
		sized("a selection that is no selection", "250", "30", "maybe", true),
		polled("a polled kind's interval and selection", "90", "on", false),
		polled("a polled kind's interval of nought", "0", "", true),
		setting("the attribution delays", "attribution", map[string]string{
			config.KeyAttributionMaxDelay: "40", config.KeyCacheAnswerDelay: "600"}, false),
		setting("a lease failure limit of nought", "lease_backends",
			map[string]string{config.KeyLeaseBackendFailurePassLimit: "0"}, true),
		setting("an unknown site-name mode", "storage", map[string]string{
			config.KeyRetentionSeconds: "0", config.KeyAggregateMode: "partial"}, true),
		setting("the site-name mode", "storage", map[string]string{
			config.KeyRetentionSeconds: "0", config.KeyAggregateMode: "no_domains"}, false),
	})

	t.Run("an unknown kind or card is refused by both", func(t *testing.T) {
		h := collectiontest.SignedInHarness(t)
		before := settingRows(t, h)
		status, answer := settingstest.SendJSON(h, strings.Replace(collection.PathAPICollectionKind, "{kind}", "no_such_kind", 1),
			map[string]any{"interval_seconds": 30})
		if status != http.StatusBadRequest || answer["condition"] != "collection_kind_unknown" {
			t.Errorf("an unknown kind answered %d %v", status, answer)
		}
		status, answer = settingstest.SendJSON(h, cardPath("no_such_card"), map[string]any{})
		if status != http.StatusBadRequest || answer["condition"] != "collection_kind_unknown" {
			t.Errorf("an unknown card answered %d %v", status, answer)
		}
		if settingRows(t, h) != before {
			t.Error("a refused input changed a row")
		}
	})
}

// TestAShorterRetentionIsConfirmedThroughTheJSONRouteAsOnThePage is C-AC9 for the retention
// confirmation: a shorter horizon stores nothing until the save confirms that very horizon.
func TestAShorterRetentionIsConfirmedThroughTheJSONRouteAsOnThePage(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	path := strings.Replace(collection.PathAPICollectionSettings, "{card}", "storage", 1)
	before := settingRows(t, h)
	status, answer := settingstest.SendJSON(h, path, map[string]any{config.KeyRetentionSeconds: 86400,
		config.KeyAggregateMode: "no_domains"})
	if status != http.StatusConflict || answer["condition"] != collection.ConditionRetentionConfirmationRequired ||
		answer["requested_retention_seconds"] != float64(86400) {
		t.Fatalf("a shorter horizon answered %d %v rather than asking for its confirmation", status, answer)
	}
	changes, _ := answer["changes"].([]any)
	if len(changes) != 1 || settingstest.Path(changes[0], "key") != config.KeyAggregateMode {
		t.Errorf("the confirmation lists %v, not the site-name mode the same save changes", changes)
	}
	if settingRows(t, h) != before {
		t.Fatal("a horizon waiting for its confirmation was stored")
	}
	status, answer = settingstest.SendJSON(h, path, map[string]any{config.KeyRetentionSeconds: 86400,
		config.KeyAggregateMode: "full", "confirm": 172800})
	if status != http.StatusConflict {
		t.Errorf("a confirmation of another horizon answered %d %v rather than asking again", status, answer)
	}
	status, answer = settingstest.SendJSON(h, path, map[string]any{config.KeyRetentionSeconds: 86400,
		config.KeyAggregateMode: "full", "confirm": 86400})
	if status != http.StatusOK || collectiontest.Setting(h, config.KeyRetentionSeconds) != "86400" {
		t.Errorf("the confirmed horizon answered %d %v and stored %q", status, answer,
			collectiontest.Setting(h, config.KeyRetentionSeconds))
	}
}

// TestTheCollectionJSONCarriesEveryFigureOfThePage is C-AC9's figures: every figure the page draws
// is in the JSON answer, compared value by value on a measured installation.
func TestTheCollectionJSONCarriesEveryFigureOfThePage(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	collectiontest.SetAvailability(h, "firewall_log", "pf", vocab.StateReachable)
	collectiontest.SetSelection(h, "dhcp_lease", "kea", config.SelectionOff)
	collectiontest.SetSelection(h, "resolver_cache", "unbound", config.SelectionOn)
	now := h.Clock.Now().Unix()
	var instants []int64
	for minute := int64(60); minute > 0; minute-- {
		instants = append(instants, now-minute*60, now-minute*60+7)
	}
	collectiontest.StoreFlows(h, instants...)
	collectiontest.RecordGap(h, "firewall_log", "pf", vocab.GapDigestOutsideWindow, now-1800, now-1740)

	figures, err := collection.PageFigures(t.Context(), h.Server)
	if err != nil {
		t.Fatalf("reading the page's figures: %v", err)
	}
	if len(figures) == 0 {
		t.Fatal("the page draws no figure, so this assertion is vacuous")
	}
	answer := settingstest.GetJSON(h, collection.PathAPICollection)
	expected := map[collection.PageFigure]bool{}
	add := func(card, label, text string) {
		expected[collection.PageFigure{Card: card, Label: label, Text: text}] = true
	}
	spelled := func(value any) string {
		seconds, _ := value.(float64)
		return fmt.Sprint(settingfields.SpelledDuration(int64(seconds)))
	}
	whole := func(value any) string {
		number, _ := value.(float64)
		return strconv.FormatInt(int64(number), 10)
	}
	instant := func(value any, absent string) string {
		number, ok := value.(float64)
		if !ok {
			return absent
		}
		return settingfields.Instant(int64(number))
	}
	field := func(value any) string {
		number, _ := value.(float64)
		figure, unit := settingfields.FieldDuration(int64(number))
		return figure + " " + unit
	}
	rate := func(value any) string {
		if state, ok := settingstest.Path(value, "state").(string); ok {
			return "state:state.sizing." + state
		}
		perSecond, _ := settingstest.Path(value, "per_second").(float64)
		return strconv.FormatFloat(perSecond, 'f', 3, 64)
	}
	for _, raw := range settingstest.Path(answer, "kinds").([]any) {
		kind, _ := settingstest.Path(raw, "kind").(string)
		label := func(name string) string { return "collection.label." + name }
		add(kind, label("source_state"), "state:state.availability."+settingstest.Path(raw, "source_state", "availability").(string))
		add(kind, label("selection"), "state:state.sizing.selection_"+settingstest.Path(raw, "source_state", "selection").(string))
		if settingstest.Path(raw, "page_size_adjustable") == true {
			add(kind, label("page_size_in_force"), whole(settingstest.Path(raw, "in_force", "page_size")))
			if ceiling := settingstest.Path(raw, "in_force", "page_ceiling"); ceiling != nil {
				add(kind, label("page_ceiling"), whole(ceiling))
				add(kind, label("page_state"), "state:state.sizing."+settingstest.Path(raw, "in_force", "page_state").(string))
			} else {
				add(kind, label("page_ceiling"), "state:state.sizing.no_page_ceiling")
			}
		}
		add(kind, label("interval_in_force"), spelled(settingstest.Path(raw, "in_force", "interval_seconds")))
		measurement := settingstest.Path(raw, "measurement")
		add(kind, label("window_start"), instant(settingstest.Path(measurement, "window_start_at"), ""))
		add(kind, label("window_end"), instant(settingstest.Path(measurement, "window_end_at"), ""))
		if retention := settingstest.Path(measurement, "retention_seconds"); retention != nil {
			add(kind, label("retention_horizon"), spelled(retention))
		} else {
			add(kind, label("retention_horizon"), "state:state.sizing.retention_unlimited")
		}
		add(kind, label("covered_from"), instant(settingstest.Path(measurement, "covered_from_at"), "state:state.sizing.no_records_in_window"))
		add(kind, label("covered_to"), instant(settingstest.Path(measurement, "covered_to_at"), "state:state.sizing.no_records_in_window"))
		add(kind, label("record_count"), whole(settingstest.Path(measurement, "record_count")))
		add(kind, label("mean_rate_source"), rate(settingstest.Path(measurement, "mean_rate")))
		add(kind, label("mean_rate_ingested"), rate(settingstest.Path(measurement, "ingested_mean_rate")))
		add(kind, label("peak_rate"), rate(settingstest.Path(measurement, "peak_rate")))
		add(kind, label("gap_count"), whole(settingstest.Path(measurement, "gap_count")))
		add(kind, label("missed_seconds"), spelled(settingstest.Path(measurement, "missed_seconds")))
		for _, gap := range settingstest.Path(measurement, "gaps").([]any) {
			add(kind, "gap_reason."+settingstest.Path(gap, "reason").(string),
				whole(settingstest.Path(gap, "count"))+" "+spelled(settingstest.Path(gap, "missed_seconds")))
		}
		if pageSize := settingstest.Path(raw, "suggestion", "page_size"); pageSize != nil {
			add(kind, label("suggested_page_size"), whole(pageSize))
		}
		if interval := settingstest.Path(raw, "suggestion", "interval_seconds"); interval != nil {
			add(kind, label("suggested_interval"), spelled(interval))
		}
		add(kind, label("suggestion_state"), "state:state.sizing."+settingstest.Path(raw, "suggestion", "state").(string))
		for _, implementation := range settingstest.Path(raw, "implementations").([]any) {
			add(kind, string(messages.ProviderLabelKey(kind, settingstest.Path(implementation, "provider_key").(string))),
				settingstest.Path(implementation, "selection").(string))
		}
	}
	for _, raw := range settingstest.Path(answer, "polled_kinds").([]any) {
		kind, _ := settingstest.Path(raw, "kind").(string)
		for _, implementation := range settingstest.Path(raw, "implementations").([]any) {
			provider := string(messages.ProviderLabelKey(kind, settingstest.Path(implementation, "provider_key").(string)))
			add(kind, "availability "+provider, "state:state.availability."+settingstest.Path(implementation, "availability").(string))
			add(kind, provider, settingstest.Path(implementation, "selection").(string))
		}
		add(kind, webtest.CardFieldName(kind, webtest.FieldInterval), field(settingstest.Path(raw, "interval_seconds")))
	}
	durations := map[string]bool{}
	for _, spec := range collection.SettingCardSpecs() {
		for _, setting := range spec.Settings {
			durations[setting.Key] = setting.Duration
		}
	}
	for _, raw := range settingstest.Path(answer, "settings").([]any) {
		card, _ := settingstest.Path(raw, "card").(string)
		for _, setting := range settingstest.Path(raw, "settings").([]any) {
			key, _ := settingstest.Path(setting, "key").(string)
			value := settingstest.Path(setting, "value")
			switch {
			case durations[key]:
				add(card, key, field(value))
			case key == config.KeyAggregateMode:
				add(card, key, value.(string))
			default:
				add(card, key, whole(value))
			}
		}
	}
	for _, figure := range figures {
		if !expected[figure] {
			t.Errorf("the page draws %+v, which the JSON answer does not carry", figure)
		}
	}
}
