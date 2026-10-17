package widgets

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/figures"
	"github.com/NaejEL/opnview/internal/widget/params"
	"github.com/NaejEL/opnview/internal/widget/request"

	"github.com/NaejEL/opnview/internal/config"
)

// AC13, AC14 and AC15: site names and their methods, the no_domains mode, and what a volume
// figure is.

// TestEverySiteNameCarriesItsMethodAndTheRatesAgreeWithTheStore is AC13.
func TestEverySiteNameCarriesItsMethodAndTheRatesAgreeWithTheStore(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	ctx := context.Background()
	// One attribution by the cache's answer: an observation of the cache holding the
	// destination, and the attribution pointing at it.
	provider, err := f.db.ProviderID(ctx, "resolver_cache", "unbound")
	check(t, err)
	f.exec(t, `INSERT INTO resource_record_observation (provider_id, held_in, owner_name, rrtype, value, address,
		first_seen_at, last_seen_at, covered_from_at, covered_until_at)
		VALUES ($1, 'cache', 'www.example.com', 'A', '198.51.100.10', '198.51.100.10', $2, $3, $4, $5)`,
		provider, f.now-86400, f.now, f.now-86400, f.now)
	f.exec(t, `UPDATE domain_attribution SET method = 'resolver_cache_answer',
		address_observation_id = (SELECT max(id) FROM resource_record_observation)
		WHERE flow_id = (SELECT min(flow_id) FROM domain_attribution)`)

	detail := decodeData[clientDetailData](t, f.respond(t, "client_traffic_detail", map[string]any{"period": "30d",
		"client": map[string]any{"kind": "client", "by": "mac", "value": "00:00:5e:00:53:00"}}))
	named := 0
	for _, record := range detail.Records {
		if record.SiteName != nil {
			named++
			if record.Method == nil || record.CorrelationDelaySeconds == nil {
				t.Errorf("record %d carries a site name without its method or delay", record.FlowID)
			}
		}
	}
	if named == 0 {
		t.Fatal("no record carries a site name, so this assertion is vacuous")
	}
	sites := decodeData[topSitesData](t, f.respond(t, "top_sites", map[string]any{"period": "30d"}))
	cacheNamed := int64(0)
	for _, row := range sites.Rows {
		if row.Kind == figures.DestinationKindAddress {
			// An address row is a destination no name was inferred for: it carries no method.
			continue
		}
		if row.Methods.ResolverCacheAnswer+row.Methods.LookupTiming+row.Methods.NotRecorded != row.Flows.Value {
			t.Errorf("%s: the methods do not sum to the flows", row.Name)
		}
		cacheNamed += row.Methods.ResolverCacheAnswer
	}
	if cacheNamed != 1 {
		t.Errorf("top sites counts %d flows named by the cache's answer, not 1", cacheNamed)
	}

	response := f.respond(t, "attribution_rate_per_client", map[string]any{"period": "30d", "limit": 1000})
	rows := decodeData[attributionData](t, response).Rows
	diagnostic := diagnosticRows(t, f, response.Window.Requested)
	for _, row := range rows {
		id := row.Client.ID
		store, err := f.db.ReadAttributionRate(ctx, response.Window.Requested.From, response.Window.Requested.To, &id)
		check(t, err)
		if row.EligibleFlows.Value != store.EligibleFlows || row.NamedFlows.Value != store.NamedFlows ||
			row.Methods.ResolverCacheAnswer != store.NamedByCacheAnswer || row.Methods.LookupTiming != store.NamedByLookupTiming {
			t.Errorf("client %d: the API reads %+v, ReadAttributionRate %+v", id, row, store)
		}
		expected := diagnostic[id]
		if [4]int64{row.EligibleFlows.Value, row.NamedFlows.Value, row.Methods.ResolverCacheAnswer,
			row.Methods.LookupTiming} != expected {
			t.Errorf("client %d: the API disagrees with the diagnostic %v", id, expected)
		}
		if (row.Rate == nil) != (store.Rate == nil) {
			t.Errorf("client %d: the API's rate is defined %v, the store's %v", id, row.Rate != nil, store.Rate != nil)
		}
	}

	// With no resolver coverage the rate is undefined, never 0.
	f.exec(t, "DELETE FROM dns_resolution")
	f.setAvailability(t, "dns_lookup", "unbound", "unavailable", "transport_failure", "example probe")
	bare := decodeData[attributionData](t, f.respond(t, "attribution_rate_per_client", map[string]any{"period": "30d"}))
	for _, row := range bare.Rows {
		if row.Rate != nil || row.RateState != figures.RateUndefinedNoResolver {
			t.Errorf("client %d: with no resolver coverage the rate reads %v (%s)", row.Client.ID, row.Rate, row.RateState)
		}
	}
	if len(bare.Rows) == 0 {
		t.Error("no client row was returned, so the undefined rate is unasserted")
	}
}

// diagnosticRows runs the diagnostic "Attribution rate per client" over a window.
func diagnosticRows(t *testing.T, f *fixture, window envelope.Interval) map[int64][4]int64 {
	t.Helper()
	text, err := os.ReadFile("../../../sql/queries/diagnostics.sql")
	check(t, err)
	body := string(text)
	start := strings.Index(body, "-- diagnostic: Attribution rate per client")
	end := strings.Index(body[start+1:], "-- diagnostic:")
	statement := body[start : start+1+end]
	statement = regexp.MustCompile(`(?m)^--.*$`).ReplaceAllString(statement, "")
	statement = strings.ReplaceAll(statement, ":window_start", "$1")
	statement = strings.ReplaceAll(statement, ":window_end", "$2")
	statement = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(statement), ";"))
	rows, err := f.db.DB().Query(statement, window.From, window.To)
	check(t, err)
	defer func() { _ = rows.Close() }()
	result := map[int64][4]int64{}
	for rows.Next() {
		var (
			id, flows, attributed, cache, timing int64
			rate, mean                           any
			maximum                              any
		)
		check(t, rows.Scan(&id, &flows, &attributed, &cache, &timing, &rate, &mean, &maximum))
		result[id] = [4]int64{flows, attributed, cache, timing}
	}
	return result
}

// TestNoDomainsWithholdsEveryDomainNameAndKeepsEveryCount is AC14: under aggregate_mode =
// no_domains no response of any served type, for any period, carries a site name, a lookup's
// domain or a registrable domain the fixture holds; every withheld field is listed; and
// every number of every response equals its value under full.
func TestNoDomainsWithholdsEveryDomainNameAndKeepsEveryCount(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	ctx := context.Background()
	full := map[string]string{}
	for _, widgetType := range f.service.Types() {
		for _, period := range []string{"1h", "24h", "7d", "30d"} {
			full[widgetType+period] = f.serialised(t, widgetType, period)
		}
	}
	check(t, f.db.SetSetting(ctx, config.KeyAggregateMode, request.AggregateModeNoDomains, f.now))
	names := append([]string{}, f.sites...)
	names = append(names, "example.com", "example.net", "example.org", "blocked0.example", "blocked1.example",
		"blocked2.example")
	for _, widgetType := range f.service.Types() {
		for _, period := range []string{"1h", "24h", "7d", "30d"} {
			serialised := f.serialised(t, widgetType, period)
			for _, name := range names {
				if strings.Contains(serialised, name) {
					t.Errorf("%s %s carries %q under no_domains", widgetType, period, name)
				}
			}
			response := f.respond(t, widgetType, withPeriod(f, widgetType, period))
			if strings.Contains(full[widgetType+period], "site_name\":\"") && len(response.Withheld) == 0 {
				t.Errorf("%s %s withheld names without listing them", widgetType, period)
			}
			preset, _ := f.service.Preset(widgetType)
			if preset.WithheldWholeUnderNoDomains {
				if response.Condition != envelope.ConditionWithheldByAggregateMode || response.Data != nil {
					t.Errorf("%s is not withheld whole", widgetType)
				}
				continue
			}
			if widgetType == "connection_tree" {
				// The site level is withheld whole, figures with it, because each figure there
				// belongs to a name; the levels above it, and the total, are unchanged.
				var before, after treeData
				check(t, json.Unmarshal([]byte(full[widgetType+period]), &before))
				check(t, json.Unmarshal([]byte(serialised), &after))
				if before.Total.Bytes.Value != after.Total.Bytes.Value || len(before.Outside) != len(after.Outside) {
					t.Errorf("connection_tree %s: the totals changed under no_domains", period)
				}
				continue
			}
			before := full[widgetType+period]
			if widgetType == "client_traffic_detail" {
				// A record's correlation delay says that, and how, a name was inferred: under
				// no_domains every destination is in address form, so it is withheld with the name
				// (C2 of the backend-completion cycle). It is a delay, not a count or a volume.
				before = withoutKey(t, before, "correlation_delay_seconds")
			}
			if before, after := numbers(t, before), numbers(t, serialised); !equalNumbers(before, after) {
				t.Errorf("%s %s: a count or a volume changed under no_domains", widgetType, period)
			}
		}
	}
}

// withPeriod is a type's default parameters with a period, where it takes one.
func withPeriod(f *fixture, widgetType, period string) map[string]any {
	parameters := defaultParameters(f, widgetType)
	if parameters == nil {
		parameters = map[string]any{}
	}
	if params.Contains(paramKeys(f.service, widgetType), "period") {
		parameters["period"] = period
	}
	return parameters
}

// serialised is a type's data, as JSON.
func (f *fixture) serialised(t *testing.T, widgetType, period string) string {
	t.Helper()
	response := f.respond(t, widgetType, withPeriod(f, widgetType, period))
	encoded, err := json.Marshal(response.Data)
	check(t, err)
	return string(encoded)
}

// numbers collects every number of a JSON document, sorted.
// withoutKey is a JSON document with every member named key removed, at any depth.
func withoutKey(t *testing.T, document, key string) string {
	t.Helper()
	var decoded any
	check(t, json.Unmarshal([]byte(document), &decoded))
	var walk func(value any)
	walk = func(value any) {
		switch typed := value.(type) {
		case []any:
			for _, item := range typed {
				walk(item)
			}
		case map[string]any:
			delete(typed, key)
			for _, item := range typed {
				walk(item)
			}
		}
	}
	walk(decoded)
	encoded, err := json.Marshal(decoded)
	check(t, err)
	return string(encoded)
}

func numbers(t *testing.T, document string) []float64 {
	t.Helper()
	var decoded any
	check(t, json.Unmarshal([]byte(document), &decoded))
	var result []float64
	var walk func(value any)
	walk = func(value any) {
		switch typed := value.(type) {
		case float64:
			result = append(result, typed)
		case []any:
			for _, item := range typed {
				walk(item)
			}
		case map[string]any:
			for _, item := range typed {
				walk(item)
			}
		}
	}
	walk(decoded)
	sort.Float64s(result)
	return result
}

func equalNumbers(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}

// TestEveryByteFigureNamesItsMeasureAndItsBound is AC15: every figure of bytes in every
// response sits beside the measure it is and its lower bound, and the measures are the
// documented ones, so no figure can be a sum of logged and sampled bytes.
func TestEveryByteFigureNamesItsMeasureAndItsBound(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	allowed := map[string]bool{envelope.MeasureLogged: true, envelope.MeasureSampled: true, envelope.MeasureInterfaceCounter: true,
		"refusals": true, "dns_lookups": true, "security_events": true, "clients": true, "addresses": true,
		"sampled_gauge": true, "dns": true, "security_event": true}
	for _, widgetType := range f.service.Types() {
		var decoded any
		check(t, json.Unmarshal([]byte(f.serialised(t, widgetType, "24h")), &decoded))
		var walk func(path string, value any)
		walk = func(path string, value any) {
			switch typed := value.(type) {
			case []any:
				for _, item := range typed {
					walk(path+"[]", item)
				}
			case map[string]any:
				if measure, present := typed["measure"].(string); present {
					if _, figure := typed["lower_bound"]; figure && !allowed[measure] {
						t.Errorf("%s %s: the measure %q is not documented", widgetType, path, measure)
					}
				}
				for key, item := range typed {
					if _, number := item.(float64); number && strings.HasSuffix(key, "bytes") {
						if _, named := typed["measure"]; !named {
							t.Errorf("%s %s.%s is a byte figure with no measure beside it", widgetType, path, key)
						}
						if bound, _ := typed["lower_bound"].(bool); !bound {
							t.Errorf("%s %s.%s is a byte figure not marked a lower bound", widgetType, path, key)
						}
					}
					walk(path+"."+key, item)
				}
			}
		}
		walk(widgetType, decoded)
	}
}
