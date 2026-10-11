package widget

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// AC4 and AC5: parameters and references.

// TestUnknownKeysAreReportedAndInvalidValuesTakeTheirDefault is AC4 in the service: an
// unknown key is reported and ignored, never an error; an out-of-vocabulary value, a value
// of the wrong type and an out-of-range number are reported and the default applies, and
// a value of the right type is never clamped; a payload that is not a JSON object is
// malformed; an unknown type, client_activity_timeline among them, is unknown.
func TestUnknownKeysAreReportedAndInvalidValuesTakeTheirDefault(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureSpec{interfaces: 2, clients: 4, flows: 60, span: 3600})
	response := f.respond(t, "interface_traffic_matrix", map[string]any{
		"from_a_newer_build": true, "scope": "everywhere", "measure": 3, "period": "24h"})
	if _, reported := response.UnknownParameters["from_a_newer_build"]; !reported {
		t.Error("an unknown key was not reported")
	}
	if response.Condition != ConditionReachable {
		t.Errorf("an unknown key changed the condition to %q", response.Condition)
	}
	reasons := map[string]string{}
	for _, invalid := range response.InvalidParameters {
		reasons[invalid.Path] = invalid.Reason
	}
	if reasons["scope"] != reasonNotInVocabulary || reasons["measure"] != reasonWrongType {
		t.Errorf("the invalid values were reported as %v", reasons)
	}
	if response.Parameters["scope"] != scopeAll || response.Parameters["measure"] != "bytes" {
		t.Errorf("the defaults did not apply: %v", response.Parameters)
	}

	clamped := f.respond(t, "interface_volume_ranking", map[string]any{"limit": 0})
	if len(clamped.InvalidParameters) != 1 || clamped.InvalidParameters[0].Reason != reasonOutOfRange ||
		clamped.Parameters["limit"] != float64(10) {
		t.Errorf("an out-of-range limit was not reported with its default applied: %+v %v",
			clamped.InvalidParameters, clamped.Parameters["limit"])
	}
	kept := f.respond(t, "interface_volume_ranking", map[string]any{"limit": 100000})
	if kept.Parameters["limit"] != float64(100000) || len(kept.InvalidParameters) != 0 {
		t.Errorf("a large limit of the right type was clamped or reported: %v", kept.Parameters["limit"])
	}

	for _, malformed := range []string{"{", "[]", "null", "3", "{}{}"} {
		_, err := f.service.Respond(context.Background(), "interface_traffic_matrix", malformed)
		var refusal errMalformed
		if !errors.As(err, &refusal) {
			t.Errorf("%q was not refused as malformed: %v", malformed, err)
		}
	}
	for _, unknown := range []string{"client_activity_timeline", "security_alerts_over_time"} {
		if _, err := f.service.Respond(context.Background(), unknown, ""); !errors.Is(err, errUnknownType) {
			t.Errorf("%s was not an unknown type: %v", unknown, err)
		}
	}
	missing := f.respond(t, "client_traffic_detail", nil)
	if missing.Condition != ConditionMissingRequiredParameter || missing.Data != nil {
		t.Errorf("client_traffic_detail without a client answered %q with data %v", missing.Condition, missing.Data)
	}
}

// referenceCase is one reference to resolve.
type referenceCase struct {
	kind, by string
	value    any
}

// TestEveryReferenceKindResolvesByItsKeyAndNeverByItsLabel is AC5: for every kind and
// every accepted `by`, a match resolves; a non-match returns no data and the path, kind,
// by, value and label; a reference whose label matches a local row but whose value does not
// stays unresolved; and in a custom chart of two series an unresolved reference in one
// leaves the other's data intact.
func TestEveryReferenceKindResolvesByItsKeyAndNeverByItsLabel(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	interfaceRow := f.interfaces[0]
	matches := []referenceCase{
		{"interface", "identifier", "example_if_0"},
		{"interface", "device", interfaceRow.device},
		{"interface", "user_label", "example label"},
		{"client", "mac", "00:00:5e:00:53:00"},
		{"client", "hostname", "host0.example"},
		{"rule", "pf_label", "example-pass-label"},
		{"rule", "description", "example pass rule"},
		{"owner", "display_name", "Example Person A"},
		{"provider", "kind_and_key", "firewall_log/pf"},
		{"country", "iso_3166_1_alpha_2", "AA"},
		{"operator", "asn", 64496},
		{"site", "domain", f.sites[0]},
	}
	// A dhcp_client_id reference needs a client known by one.
	f.exec(t, `INSERT INTO client (identity_kind, identity_key, first_seen_at, last_seen_at)
		VALUES ('dhcp_client_id', 'example-client-id', ?, ?)`, f.now, f.now)
	matches = append(matches, referenceCase{"client", "dhcp_client_id", "example-client-id"})
	covered := map[string]bool{}
	for _, item := range matches {
		covered[item.kind+"/"+item.by] = true
		good := map[string]any{"kind": item.kind, "by": item.by, "value": item.value}
		bad := map[string]any{"kind": item.kind, "by": item.by, "value": "no-such-value-here",
			"label": "example label"}
		// The bad reference's label names a real interface: it must not be matched on.
		if item.kind == "operator" {
			bad["value"] = 1
		}
		if item.kind == "owner" {
			// No series scopes itself by a person: Per-person activity is the type that
			// takes owner references.
			okResponse := f.respond(t, "owner_activity", map[string]any{"owners": []any{good}})
			badResponse := f.respond(t, "owner_activity", map[string]any{"owners": []any{bad}})
			if len(okResponse.UnresolvedReferences) != 0 || okResponse.Data == nil {
				t.Errorf("owner by display_name did not resolve: %+v", okResponse.UnresolvedReferences)
			}
			if len(badResponse.UnresolvedReferences) != 1 || badResponse.UnresolvedReferences[0].Path != "owners[0]" ||
				badResponse.Data != nil || badResponse.Condition != ConditionUnresolvedReference {
				t.Errorf("an unmatched owner gave %+v, data %v", badResponse.UnresolvedReferences, badResponse.Data)
			}
			continue
		}
		okResponse := f.respond(t, "custom_chart", map[string]any{"series": []any{
			map[string]any{"source": "traffic", item.kind: good}}})
		if len(okResponse.UnresolvedReferences) != 0 {
			t.Errorf("%s by %s did not resolve: %+v", item.kind, item.by, okResponse.UnresolvedReferences)
		}
		badResponse := f.respond(t, "custom_chart", map[string]any{"series": []any{
			map[string]any{"source": "traffic", item.kind: bad},
			map[string]any{"source": "traffic"}}})
		if len(badResponse.UnresolvedReferences) != 1 {
			t.Fatalf("%s by %s: a non-match gave %+v", item.kind, item.by, badResponse.UnresolvedReferences)
		}
		unresolved := badResponse.UnresolvedReferences[0]
		if unresolved.Path != "series[0]."+item.kind || unresolved.Kind != item.kind || unresolved.By != item.by ||
			unresolved.Label != "example label" || unresolved.Value == nil {
			t.Errorf("%s by %s: the unresolved reference reads %+v", item.kind, item.by, unresolved)
		}
		chart := badResponse.Data.(CustomChartData)
		if chart.Series[0].Condition != ConditionUnresolvedReference || chart.Series[0].Buckets != nil {
			t.Errorf("%s by %s: the unresolved series has data", item.kind, item.by)
		}
		if chart.Series[1].Buckets == nil || chart.Series[1].Condition != ConditionReachable {
			t.Errorf("%s by %s: the other series lost its data (%s)", item.kind, item.by, chart.Series[1].Condition)
		}
	}
	for kind, accepted := range referenceByKind {
		for _, by := range accepted {
			if !covered[kind+"/"+by] {
				t.Errorf("no case resolves %s by %s", kind, by)
			}
		}
	}

	// A top-level reference: no data, never zeros.
	response := f.respond(t, "interface_traffic_matrix", map[string]any{"interfaces": []any{
		map[string]any{"kind": "interface", "by": "user_label", "value": "missing", "label": "example label"}}})
	if response.Condition != ConditionUnresolvedReference || response.Data != nil {
		t.Errorf("an unresolved top-level reference answered %q with data", response.Condition)
	}
	for _, outside := range []map[string]any{
		{"kind": "unheard_of_kind", "by": "identifier", "value": "x"},
		{"kind": "interface", "by": "name", "value": "x"},
		{"kind": "client", "by": "mac", "value": "x"},
	} {
		response := f.respond(t, "interface_traffic_matrix", map[string]any{"interfaces": []any{outside}})
		if response.Condition != ConditionUnresolvedReference || response.Data != nil ||
			!strings.HasPrefix(response.UnresolvedReferences[0].Path, "interfaces[0]") {
			t.Errorf("%v was not unresolved: %q %+v", outside, response.Condition, response.UnresolvedReferences)
		}
	}
}
