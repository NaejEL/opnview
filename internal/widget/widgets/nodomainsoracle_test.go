package widgets

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/params"
	"github.com/NaejEL/opnview/internal/widget/request"

	"github.com/NaejEL/opnview/internal/config"
)

// A10 and decision 6, against probing: under aggregate_mode = no_domains no reference,
// parameter or filter may let a domain name select, scope or probe data. The test sends,
// to every served type, a site reference at every place a reference can stand -- every
// reference and reference-list key, and every series entry of every source -- once naming
// a site the fixture visited and once naming one it never saw, under every `by` the site
// kind accepts and one it does not. The two whole responses must be the same once the two
// names are set aside, and neither may carry the name it was sent.

// neverVisited is a site name no fixture row holds.
const neverVisited = "never-visited.example"

// siteProbe is one request carrying a site reference, built for a name.
type siteProbe struct {
	widgetType string
	place      string
	build      func(name string, by string) map[string]any
}

// siteProbes is every place of every served type a site reference can be sent to.
func siteProbes(f *fixture) []siteProbe {
	reference := func(name, by string) map[string]any {
		return map[string]any{"kind": "site", "by": by, "value": name, "label": name}
	}
	var probes []siteProbe
	for _, widgetType := range f.service.Types() {
		preset, _ := f.service.Preset(widgetType)
		for _, param := range preset.Params {
			param := param
			widgetType := widgetType
			base := func() map[string]any {
				parameters := defaultParameters(f, widgetType)
				if parameters == nil {
					parameters = map[string]any{}
				}
				if params.Contains(paramKeys(f.service, widgetType), "period") {
					parameters["period"] = "30d"
				}
				return parameters
			}
			switch param.Kind {
			case params.KindReference:
				probes = append(probes, siteProbe{widgetType: widgetType, place: param.Key,
					build: func(name, by string) map[string]any {
						parameters := base()
						parameters[param.Key] = reference(name, by)
						return parameters
					}})
			case params.KindReferenceList:
				probes = append(probes, siteProbe{widgetType: widgetType, place: param.Key + "[0]",
					build: func(name, by string) map[string]any {
						parameters := base()
						parameters[param.Key] = []any{reference(name, by)}
						return parameters
					}})
			case params.KindSeries:
				for _, source := range params.SeriesSources {
					source := source
					probes = append(probes, siteProbe{widgetType: widgetType,
						place: param.Key + "[1].site (" + source + ")",
						build: func(name, by string) map[string]any {
							parameters := base()
							// An unscoped series beside the probed one, so the probe also shows
							// that withholding one series leaves the other intact.
							parameters[param.Key] = []any{
								map[string]any{"source": params.SourceTraffic, "measure": "bytes"},
								map[string]any{"source": source, "site": reference(name, by)},
							}
							return parameters
						}})
				}
			}
		}
	}
	return probes
}

// TestNoDomainsLetsNoSiteReferenceSelectOrProbeData is the parameterised test: every
// served type, every place a site reference can stand, every `by`.
func TestNoDomainsLetsNoSiteReferenceSelectOrProbeData(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	check(t, f.db.SetSetting(context.Background(), config.KeyAggregateMode, request.AggregateModeNoDomains, f.now))
	visited := f.sites[0]
	probes := siteProbes(f)
	if len(probes) == 0 {
		t.Fatal("no served type accepts a reference, so this test probes nothing")
	}
	seriesProbed := false
	for _, probe := range probes {
		for _, by := range append(append([]string{}, params.ReferenceByKind["site"]...), "label") {
			seenRaw, seen := f.serialisedWhole(t, probe.widgetType, probe.build(visited, by))
			unseenRaw, unseen := f.serialisedWhole(t, probe.widgetType, probe.build(neverVisited, by))
			if strings.Contains(seenRaw, visited) {
				t.Errorf("%s %s by %s: the response carries the site name it was sent", probe.widgetType,
					probe.place, by)
			}
			if strings.Contains(unseenRaw, neverVisited) {
				t.Errorf("%s %s by %s: the response carries the site name it was sent", probe.widgetType,
					probe.place, by)
			}
			if seen != unseen {
				t.Errorf("%s %s by %s: a visited site and a never-seen one get different answers, which "+
					"tells which names were visited:\n%s\n%s", probe.widgetType, probe.place, by, seen, unseen)
			}
		}
		if strings.HasPrefix(probe.place, "series[1]") {
			seriesProbed = true
			response := f.respond(t, probe.widgetType, probe.build(visited, "domain"))
			chart := decodeData[customChartData](t, response)
			if len(chart.Series) != 2 {
				t.Fatalf("%s %s: %d series came back, not 2", probe.widgetType, probe.place, len(chart.Series))
			}
			if probed := chart.Series[1]; probed.Condition != envelope.ConditionWithheldByAggregateMode ||
				probed.Buckets != nil || probed.Total != nil {
				t.Errorf("%s %s: the site-scoped series is %s with data, not withheld", probe.widgetType,
					probe.place, probed.Condition)
			}
			if other := chart.Series[0]; other.Condition != envelope.ConditionReachable || other.Total == nil {
				t.Errorf("%s %s: the unscoped series beside it lost its data (%s)", probe.widgetType, probe.place,
					other.Condition)
			}
			if !params.Contains(response.Withheld, "series[1].site.value") {
				t.Errorf("%s %s: the withheld value is not listed: %v", probe.widgetType, probe.place,
					response.Withheld)
			}
		}
	}
	if !seriesProbed {
		t.Error("no series entry was probed, so the custom chart's site scope went untested")
	}
}

// TestUnderFullASiteReferenceStillScopesTheChart is the control: the same probe under
// aggregate_mode = full does tell a visited site from a never-seen one, so the test above
// fails if no_domains stops withholding -- the probe is not blind.
func TestUnderFullASiteReferenceStillScopesTheChart(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	visited := f.sites[0]
	build := func(name string) map[string]any {
		return map[string]any{"period": "30d", "series": []any{map[string]any{"source": params.SourceTraffic,
			"site": map[string]any{"kind": "site", "by": "domain", "value": name}}}}
	}
	seen := decodeData[customChartData](t, f.respond(t, "custom_chart", build(visited))).Series[0]
	unseen := decodeData[customChartData](t, f.respond(t, "custom_chart", build(neverVisited))).Series[0]
	if seen.Condition != envelope.ConditionReachable || seen.Total == nil || *seen.Total == 0 {
		t.Errorf("under full, a visited site's series is %s with total %v", seen.Condition, seen.Total)
	}
	if unseen.Condition != envelope.ConditionUnresolvedReference {
		t.Errorf("under full, a never-seen site's series is %s, not unresolved", unseen.Condition)
	}
}

// serialisedWhole is a whole response -- envelope and data -- as JSON, as it is and with
// the two probe names replaced by one token, so two responses differing only in the name
// they were sent compare equal and any other difference shows.
func (f *fixture) serialisedWhole(t *testing.T, widgetType string, parameters map[string]any) (
	string, string) {
	t.Helper()
	response := f.respond(t, widgetType, parameters)
	encoded, err := json.Marshal(response)
	check(t, err)
	raw := string(encoded)
	text := raw
	for _, name := range []string{f.sites[0], neverVisited} {
		text = strings.ReplaceAll(text, name, "<probed-name>")
	}
	return raw, text
}
