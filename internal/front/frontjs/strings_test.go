package frontjs_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	opnview "github.com/NaejEL/opnview"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/front"
	"github.com/NaejEL/opnview/internal/front/frontjs"
	"github.com/NaejEL/opnview/internal/store/flows"
	"github.com/NaejEL/opnview/internal/store/netflow"
	"github.com/NaejEL/opnview/internal/store/owners"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/widget/request"
)

// F7-AC5: GET /api/strings/en holds every key a front file uses, and every built-in view drawn in a
// pseudo-language that wraps each string in markers leaves no text node, aria-label, title,
// placeholder or option text that is neither marker-wrapped nor a formatted data value.

// keyCalls finds the four ways a front file asks for a string (front/core/strings.js).
var (
	literalCall  = regexp.MustCompile(`\bt\("([a-z0-9_.-]+)"`)
	pluralCall   = regexp.MustCompile(`\btp\("([a-z0-9_.-]+)"`)
	composedCall = regexp.MustCompile(`\btk\("([a-z0-9_.-]+)"`)
	comment      = regexp.MustCompile(`(?s:/\*.*?\*/)|(?m:^\s*//[^\n]*)`)
)

// frontFiles are the text of every script the shell loads, of every shape renderer and of every
// built-in widget's renderer.
func frontFiles(t *testing.T) map[string]string {
	t.Helper()
	scripts, err := frontjs.ShellScripts()
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, name := range scripts {
		raw, err := fs.ReadFile(opnview.BuiltinFront(), name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = comment.ReplaceAllString(string(raw), "")
	}
	renderers, err := frontjs.RendererSources()
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range renderers {
		files[file.Name] = comment.ReplaceAllString(file.Text, "")
	}
	return files
}

// schemaVocabulary reads the CHECK (<column> IN (...)) list of one table of the schema, its SQL
// comments removed.
func schemaVocabulary(t *testing.T, table, column string) []string {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(here), "..", "..", "store", "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	text := regexp.MustCompile(`--[^\n]*`).ReplaceAllString(string(raw), "")
	start := strings.Index(text, "CREATE TABLE IF NOT EXISTS "+table+" (")
	if start < 0 {
		t.Fatalf("the schema holds no table %s", table)
	}
	found := regexp.MustCompile(`(?s)CHECK \(` + column + ` IN \(([^)]*)\)\)`).FindStringSubmatch(text[start:])
	if found == nil {
		t.Fatalf("the table %s holds no CHECK on %s", table, column)
	}
	return regexp.MustCompile(`'([a-z_]+)'`).FindAllString(found[1], -1)
}

// composedValues are, for every prefix a front file composes a key with, the values the data can
// reach: each <prefix><value> must exist. A prefix missing here fails the test, so a new composed
// key is enumerated when it is written.
func composedValues(t *testing.T, page *frontjs.Runtime) map[string][]string {
	unquote := func(values []string) []string {
		out := make([]string, len(values))
		for index, value := range values {
			out[index] = strings.Trim(value, "'")
		}
		return out
	}
	var units []string
	if err := json.Unmarshal([]byte(page.Run(`JSON.stringify(opnview.format.symbolUnits())`).String()), &units); err != nil {
		t.Fatal(err)
	}
	var measures, conditions []string
	if err := json.Unmarshal([]byte(page.Run(`JSON.stringify(opnview.kit.create({}).MEASURES)`).String()), &measures); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(page.Run(`JSON.stringify(opnview.canvasView.PANEL_CONDITIONS)`).String()), &conditions); err != nil {
		t.Fatal(err)
	}
	scopeKinds := []string{"interface", "client", "owner"}
	periods := []string{"1h", "24h", "7d", "30d"}
	// The editing surfaces (front/views/dock.js, front/views/edit.js, front/views/dashboards.js): the
	// document module's refusals, the series vocabulary, the edit steps.
	list := func(script string) []string {
		var values []string
		if err := json.Unmarshal([]byte(page.Run("JSON.stringify("+script+")").String()), &values); err != nil {
			t.Fatalf("%s: %v", script, err)
		}
		return values
	}
	refusals := append(append(list("Object.keys(opnview.document.CONDITIONS).map(function (k) { return opnview.document.CONDITIONS[k]; })"),
		list("opnview.document.PARSE_CONDITIONS")...), "revision_conflict", "read_failed", "form_invalid",
		"unknown_dashboard", "internal_error")
	moves := list("opnview.editChrome.MOVES")
	reportConditions := []string{"unresolved_reference", "unknown_widget_type", "shape_not_served", "unknown_parameter_key",
		"invalid_parameter"}
	editing := map[string][]string{
		"document.refusal.":   refusals,
		"dock.notice.":        reportConditions,
		"report.condition.":   reportConditions,
		"dock.tab.":           list("opnview.dock.TABS"),
		"dock.syntax.":        list("opnview.editing.SYNTAXES"),
		"series.source.":      list("Object.keys(opnview.dock.SERIES_SOURCES)"),
		"series.axis.":        list("opnview.dock.SERIES_AXES"),
		"series.render.":      list("opnview.dock.SERIES_RENDERS"),
		"series.aggregation.": list("opnview.dock.SERIES_AGGREGATIONS"),
		"series.reference.":   list("opnview.dock.SERIES_REFERENCES"),
		"edit.move.":          moves[:4],
		"edit.size.":          moves[4:],
		"edit.face.":          moves,
		"shape.name.": {"figure", "ranked_bars", "time_series", "table", "feed", "cards", "map", "donut", "tree", "sankey",
			"graph", "matrix"},
		"notice.":            {"widget_removed", "canvas_removed", "dashboard_removed"},
		"dashboards.origin.": {"created", "imported", "default"},
	}
	// The form's labels: every key a built-in manifest declares and every value it offers (a range's
	// values are the period's own words), the core's volume_measure, and the series figures.
	parameterKeys := map[string]bool{"volume_measure": true}
	parameterValues := map[string]bool{"logged": true, "netflow": true, "best_available": true}
	for _, widget := range builtinWidgets(t) {
		for _, parameter := range widget.Parameters {
			parameterKeys[parameter.Key] = true
			if parameter.Key == "period" {
				continue
			}
			for _, value := range parameter.Values {
				parameterValues[value] = true
			}
		}
	}
	var seriesFigures [][]string
	if err := json.Unmarshal([]byte(page.Run("JSON.stringify(Object.keys(opnview.dock.SERIES_SOURCES).map(function (k) { return opnview.dock.SERIES_SOURCES[k].values; }))").String()), &seriesFigures); err != nil {
		t.Fatal(err)
	}
	for _, figures := range seriesFigures {
		for _, figure := range figures {
			parameterValues[figure] = true
		}
	}
	for key := range parameterKeys {
		editing["parameter."] = append(editing["parameter."], key)
	}
	for value := range parameterValues {
		editing["parameter.value."] = append(editing["parameter.value."], value)
	}
	composed := map[string][]string{
		"appearance.mode.":    config.AppearanceModes(),
		"blocklists.purpose.": owners.BlocklistPurposes,
		// The canvas (front/views/canvas.js, front/core/kit.js): a widget's condition, the provider
		// conditions a state badge names, the named states, the measures, the methods, the periods and
		// the scope kinds.
		"condition.":    append(conditions, "reachable"),
		"named_state.":  namedStates,
		"measure.":      measures,
		"method.":       {"resolver_cache_answer", "lookup_timing", "not_recorded_beyond_flow_horizon"},
		"period.":       periods,
		"period.short.": periods,
		// The renderers' shared words (front/core/kit.js, placeReason, direction, directionState): the
		// places traffic can be, the lookup states of an address, the directions, NetFlow's per-direction
		// states and the blocking engines.
		"column.scope.":           {"north_south", "east_west", "this_firewall"},
		"reason.":                 {"miss", "pending", "no_row", "no_coordinates"},
		"column.direction.":       {"in", "out", "inbound", "outbound"},
		"column.direction_state.": netflow.VolumeStates(),
		"column.engine.": {"firewall_rule", "security_engine", "dns_advertising_list", "dns_threat_list",
			"dns_unassigned_list", "dns_list_not_recorded"},
		"scope.all.":                 scopeKinds,
		"scope.kind.":                scopeKinds,
		"card.own_scope.":            scopeKinds,
		"card.not_scoped.":           scopeKinds,
		"collection.aggregate_mode.": {"full", "no_domains"},
		// The collection's setting cards (internal/web/surfaces/collection, settingCardSpecs).
		"collection.card.": {"pairing", "lease_backends", "attribution", "storage"},
		"collection.kind.": {"firewall_log", "security_event", "dhcp_lease", "dns_lookup", "flow_volume",
			"ip_geolocation", "measurement_sample", "registrable_domain", "resolver_cache", "resolver_local_data",
			"flow_record"},
		"collection.setting.": {config.KeyRetentionSeconds, config.KeyAggregateMode, config.KeyLegPairingWindow,
			config.KeyLeaseBackendFailurePassLimit, config.KeyAttributionMaxDelay, config.KeyCacheAnswerDelay},
		// Every condition a route the views call can answer (docs/widget-api.md).
		"error.": {"setup_token_refused", "login_required", "password_required", "password_too_short",
			"password_mismatch", "setup_closed", "sign_in_refused", "key_file_refused", "csrf_refused", "malformed_body",
			"internal_error", "no_session", "session_expired", "firewall_url_invalid", "fingerprint_invalid",
			"api_key_required", "firewall_url_required", "certificate_not_fetched", "collection_value_invalid",
			"collection_interval_too_large", "collection_kind_unknown", "value_not_non_negative",
			"aggregate_mode_unknown", "value_out_of_range", "unknown_setting", "invalid_setting_value",
			"setting_out_of_range", "no_key_file", "unknown_plugin", "read_failed", "interface_unknown",
			"interface_upstream", "interface_action_unknown", "link_local_invalid", "network_unknown",
			"network_malformed", "network_link_local", "network_ipv4_mapped", "network_host_bits", "user_label_empty",
			"unknown_interface", "display_name_empty", "display_name_taken", "unknown_client", "no_clients",
			"duplicate_client", "invalid_owner_assigned_at", "unknown_owner", "purpose_not_in_vocabulary",
			"unknown_blocklist", "palette_unknown", "mode_unknown", "language_unknown",
			"revision_conflict", "unknown_dashboard", "nothing_to_undo", "form_invalid"},
		"gap_reason.": unquote(schemaVocabulary(t, "collection_gap", "reason")),
		"opnsense_page.": {"ids_administration", "kea_dhcpv4", "dnsmasq_dhcp_ranges", "unbound_general",
			"unbound_reporting", "netflow"},
		"outcome.":                unquote(schemaVocabulary(t, "source_availability", "outcome")),
		"parts.origin.":           {front.OriginBuiltin, front.OriginDataDirectory},
		"parts.part.":             {front.PartTheme, front.PartLanguage, front.PartWidget, front.PartWidgetStrings},
		"parts.state.":            {"loaded", "refused"},
		"people.identity.":        unquote(schemaVocabulary(t, "client", "identity_kind")),
		"people.not_restored.":    {owners.NotRestoredAssignedElsewhere, owners.NotRestoredClientGone},
		"settings.live.":          config.LiveSettingKeys(),
		"settings.netflow.":       config.NetflowSettingKeys(),
		"sources.since.":          {request.SinceNotObserved, "state_unknown"},
		"state.availability.":     {"healthy", "not_collecting", "unavailable", "not_yet_probed", "unknown"},
		"state.counts.":           {"removed", "no_longer_detected"},
		"state.credential.":       {"absent", "ready", "undecryptable"},
		"state.origin.":           {flows.NetworkOriginDetected, flows.NetworkOriginOperator},
		"state.public_suffix.":    {"downloaded", "not_modified", "failed", "malformed", "not_yet_refreshed"},
		"state.sizing.selection_": {"auto", "on", "off"},
		"state.sizing.": {"no_ingested_clock", "rate_not_measured", "page_within_ceiling", "page_above_ceiling",
			"no_page_ceiling", "suggestion_available", "not_yet_measured", "rate_not_coverable", "source_not_collected"},
		"state.verification.": {"not_attempted", "verified", "credentials_rejected", "endpoint_not_found", "unreachable",
			"certificate_refused", "unexpected_answer", "no_url"},
		"unit.symbol.": units,
	}
	for prefix, values := range editing {
		composed[prefix] = values
	}
	return composed
}

// namedStates are the named states of the widget API (docs/widget-api.md, "Named states"), and the
// resolver window's, which a card names in its header.
var namedStates = []string{"matrix_rules_beyond_flow_horizon", "records_beyond_flow_horizon", "rules_beyond_flow_horizon",
	"tree_beyond_flow_horizon", "attribution_beyond_flow_horizon", "composition_beyond_flow_horizon",
	"client_left_beyond_flow_horizon", "joins_beyond_flow_horizon", "site_methods_not_recorded_beyond_flow_horizon",
	"series_read_from_hour_slots_beyond_flow_horizon", "registrable_domain_grouping_unavailable",
	"unnamed_destinations_beyond_flow_horizon", "netflow_not_configured", "capture_configuration_unknown",
	"netflow_not_covering_window", "netflow_not_served_by_type", "resolver_window_bounded"}

// usedKeys scans the front's files for every key they ask for: literal, plural and composed, each
// composed prefix expanded over the values the data can reach.
func usedKeys(t *testing.T) map[string]string {
	t.Helper()
	page := frontjs.New(t, frontjs.Options{})
	enumerated := composedValues(t, page)
	used := map[string]string{}
	prefixes := map[string]string{}
	for name, text := range frontFiles(t) {
		for _, match := range literalCall.FindAllStringSubmatch(text, -1) {
			used[match[1]] = name
		}
		for _, match := range pluralCall.FindAllStringSubmatch(text, -1) {
			// English's plural forms: one and other.
			used[match[1]+".one"] = name
			used[match[1]+".other"] = name
		}
		for _, match := range composedCall.FindAllStringSubmatch(text, -1) {
			prefixes[match[1]] = name
		}
	}
	english := englishCatalogue(t)
	for prefix, name := range prefixes {
		values, known := enumerated[prefix]
		if !known && strings.HasPrefix(prefix, "plugin.") {
			// A widget's own composed keys live in its plugin's catalogue, beside the renderer that
			// composes them: the prefix must name keys there. Its values are checked by drawing every
			// renderer on every recorded answer, which reports a key the catalogue lacks.
			held := false
			for key := range english {
				held = held || strings.HasPrefix(key, prefix)
			}
			if !held {
				t.Errorf("%s composes keys under %q, and the catalogue holds none", name, prefix)
			}
			continue
		}
		if !known {
			t.Errorf("%s composes keys under %q, and this test does not enumerate the values the data can reach",
				name, prefix)
			continue
		}
		for _, value := range values {
			used[prefix+value] = name
		}
	}
	return used
}

func TestEveryKeyAFrontFileUsesIsInTheEnglishCatalogue(t *testing.T) {
	t.Parallel()
	english := englishCatalogue(t)
	used := usedKeys(t)
	// The scan itself is held to finding what the views plainly use, so a regular expression that
	// stopped matching cannot pass this test by finding nothing.
	for _, key := range []string{"settings.title", "people.people_count.other", "error.display_name_taken",
		"outcome.no_message_received", "unit.symbol.megabyte"} {
		if _, found := used[key]; !found {
			t.Errorf("the scan of the front's files did not find %s", key)
		}
	}
	var missing []string
	for key, name := range used {
		if _, present := english[key]; !present {
			missing = append(missing, key+" ("+name+")")
		}
	}
	sort.Strings(missing)
	for _, key := range missing {
		t.Errorf("GET /api/strings/en lacks %s", key)
	}
}

// pseudoWalk lists every string of the document a reader sees that is neither wrapped in the
// pseudo-language's markers nor a value the data or a formatter produced.
const pseudoWalk = `(function (allowed) {
  var found = [];
  function visible(text, where) {
    var trimmed = text.trim();
    if (!trimmed) { return; }
    if (trimmed.indexOf("⟦") === 0 && trimmed.lastIndexOf("⟧") === trimmed.length - 1) { return; }
    if (allowed[trimmed]) { return; }
    // A mark between two values -- a separator, a slash, an arrow -- is typography, not a word.
    if (/^[\s·→←—–,;:\/()|+-]+$/.test(trimmed)) { return; }
    found.push(where + ": " + JSON.stringify(trimmed));
  }
  (function walk(node) {
    if (node.nodeType === 3) { visible(node.nodeValue, "text"); return; }
    ["aria-label", "title", "placeholder"].forEach(function (name) {
      var value = node.getAttribute(name);
      if (value !== null) { visible(value, name); }
    });
    node.childNodes.forEach(walk);
  })(document.body);
  return JSON.stringify(found);
})`

// leaves collects every string and number of a decoded answer.
func leaves(value any, into map[string]bool) {
	switch typed := value.(type) {
	case string:
		into[typed] = true
	case float64:
		raw, _ := json.Marshal(typed)
		into[string(raw)] = true
	case []any:
		for _, item := range typed {
			leaves(item, into)
		}
	case map[string]any:
		for _, item := range typed {
			leaves(item, into)
		}
	}
}

func TestEveryViewDrawnInAPseudoLanguageShowsNoLiteral(t *testing.T) {
	t.Parallel()
	type view struct {
		server   *fakeServer
		path     string
		signedIn bool
	}
	people := newFakeServer(t, true, true)
	people.on("GET /api/owners", func(frontjsRequest) frontjsAnswer {
		return ok(map[string]any{"owners": []any{map[string]any{"id": 1, "display_name": "Noé Example", "client_count": 1}}})
	})
	people.on("GET /api/clients", func(frontjsRequest) frontjsAnswer {
		return ok(map[string]any{"clients": []any{map[string]any{"id": 10, "identity_kind": "mac",
			"identity_key": "02:00:5e:10:00:01", "hostname": "laptop.example", "mac": "02:00:5e:10:00:01",
			"last_address": "192.0.2.10", "unstable_identity": true, "owner": map[string]any{"id": 1, "display_name": "Noé Example"},
			"owner_assigned_at": 1773478800, "interface": map[string]any{"id": 3, "identifier": "opt1",
				"description": "Example LAN", "user_label": "Workshop"}}}})
	})
	collection := newFakeServer(t, true, true)
	collection.on("GET /api/collection", func(frontjsRequest) frontjsAnswer { return ok(collectionAnswer(86400)) })
	interfaces := newFakeServer(t, true, true)
	interfaces.on("GET /api/interface-networks", func(frontjsRequest) frontjsAnswer {
		return ok(interfacesAnswer("Workshop", true, true))
	})
	blocklists := newFakeServer(t, true, true)
	blocklists.on("GET /api/blocklists", func(frontjsRequest) frontjsAnswer {
		return ok(map[string]any{"blocklists": []any{map[string]any{"id": 4, "name": "ads.example", "purpose": "tracking",
			"purpose_assigned_at": 1773478800, "first_seen_at": 1773400000, "last_seen_at": 1773478000}},
			"purposes": owners.BlocklistPurposes})
	})
	views := []view{
		{newFakeServer(t, false, false), "/setup", false},
		{newFakeServer(t, true, false), "/sign-in", false},
		{newFakeServer(t, true, false), "/recover", false},
		{newFakeServer(t, true, true), "/", true},
		{newFakeServer(t, true, true), "/no-such-view", true},
		{settingsServer(t), "/settings", true},
		{collection, "/collection", true},
		{interfaces, "/interfaces", true},
		{newFakeServer(t, true, true), "/sources", true},
		{people, "/people", true},
		{blocklists, "/blocklists", true},
	}
	for _, entry := range views {
		data := map[string]bool{}
		recording := func(request frontjsRequest) frontjsAnswer {
			answer := entry.server.handle(request)
			raw, _ := json.Marshal(answer.Body)
			var decoded any
			_ = json.Unmarshal(raw, &decoded)
			leaves(decoded, data)
			return answer
		}
		page := frontjs.New(t, frontjs.Options{Handler: recording})
		page.Run(`opnview.format.record(true); opnview.strings.setPseudo(true);`)
		page.Start(entry.path)
		if entry.signedIn && page.Count("nav.rail") != 1 {
			t.Fatalf("%s is not drawn in the layout:\n%s", entry.path, page.Outline())
		}
		var formatted []string
		if err := json.Unmarshal([]byte(page.Run(`JSON.stringify(opnview.format.recorded())`).String()), &formatted); err != nil {
			t.Fatal(err)
		}
		for _, text := range formatted {
			data[text] = true
		}
		allowed, _ := json.Marshal(data)
		var found []string
		if err := json.Unmarshal([]byte(page.Run(pseudoWalk+"("+string(allowed)+")").String()), &found); err != nil {
			t.Fatal(err)
		}
		for _, literal := range found {
			t.Errorf("%s shows a string that is neither the catalogue's nor data: %s", entry.path, literal)
		}
		if missing := page.Run(`JSON.stringify(opnview.strings.missing())`).String(); missing != "[]" {
			t.Errorf("%s asked for keys the catalogue does not hold: %s", entry.path, missing)
		}
	}
}

// TestEveryCatalogueEntryOfTheCoreTheSurfacesAndTheFrontIsReached: an entry no front file asks for,
// no Go code chooses and no registry row composes is a word nobody can read, left behind by the
// pages this step removed; it fails here until it is deleted or used.
func TestEveryCatalogueEntryOfTheCoreTheSurfacesAndTheFrontIsReached(t *testing.T) {
	t.Parallel()
	reached := map[string]bool{"language.name": true}
	for key := range usedKeys(t) {
		reached[key] = true
	}
	for _, key := range web.AllHandlerKeys() {
		reached[string(key)] = true
	}
	// Keys composed from a registry row, held to an entry by the surfaces' registry test.
	composedByRegistry := []string{"collection.kind.", "provider.", "gap_reason.", "dataset."}
	sources, err := web.CatalogueSources()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := fs.ReadFile(opnview.BuiltinFront(), "locale/en.json")
	if err != nil {
		t.Fatal(err)
	}
	sources["front/locale/en.json"] = string(raw)
	for file, text := range sources {
		var catalogue struct {
			Strings map[string]string `json:"strings"`
		}
		if err := json.Unmarshal([]byte(text), &catalogue); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		for key := range catalogue.Strings {
			composed := false
			for _, prefix := range composedByRegistry {
				composed = composed || strings.HasPrefix(key, prefix)
			}
			if !reached[key] && !composed {
				t.Errorf("%s holds %s, which nothing reaches", file, key)
			}
		}
	}
}
