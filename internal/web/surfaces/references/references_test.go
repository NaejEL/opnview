package references_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/web/surfaces/references"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"github.com/NaejEL/opnview/internal/widget"
	"github.com/NaejEL/opnview/internal/widget/params"
)

// PROVENANCE OF THE FIXTURE. Every value below is synthesised for these tests: addresses from the
// documentation ranges (RFC 5737, RFC 3849), MAC addresses from the documentation block of
// RFC 7042 (00:00:5e:00:53:xx), names under the reserved domains of RFC 2606, AS numbers from the
// documentation range of RFC 5398, and the user-assigned country codes XA and XB of ISO 3166-1. The
// number of interfaces is a parameter of the seed. No value comes from a real network.

// The documentation ranges of RFC 5737, TEST-NET-1 to TEST-NET-3: an address is built from its
// range and its host number, so no dotted quad is written in a source of internal/web
// (internal/web/surfaces/repository_test.go).
func testNet1(host byte) string { return netip.AddrFrom4([4]byte{192, 0, 2, host}).String() }
func testNet2(host byte) string { return netip.AddrFrom4([4]byte{198, 51, 100, host}).String() }
func testNet3(host byte) string { return netip.AddrFrom4([4]byte{203, 0, 113, host}).String() }

// fixture is what seed wrote.
type fixture struct {
	interfaces int
	providerID int64
}

// exec runs one statement of the seed.
func exec(t *testing.T, harness *webtest.Harness, statement string, arguments ...any) {
	t.Helper()
	if _, err := harness.Store.DB().ExecContext(context.Background(), statement, arguments...); err != nil {
		t.Fatalf("seeding: %v\n%s", err, statement)
	}
}

// seed writes interfaceCount interfaces and one row, or two where an ambiguity is wanted, of every
// other kind a reference names.
func seed(t *testing.T, harness *webtest.Harness, interfaceCount int) fixture {
	t.Helper()
	at := harness.Clock.Now().Unix() - 60
	for index := 1; index <= interfaceCount; index++ {
		var label any
		if index%2 == 0 {
			label = fmt.Sprintf("Example label %d", index)
		}
		exec(t, harness, `INSERT INTO interface (id, identifier, device, description, user_label, link_type, link_kind,
			first_seen_at, last_seen_at) VALUES (?, ?, ?, ?, ?, 'example-link', 'vlan', ?, ?)`,
			index, fmt.Sprintf("example_if_%d", index), fmt.Sprintf("exdev%d", index),
			fmt.Sprintf("Example description %d", index), label, at, at)
	}
	exec(t, harness, `INSERT INTO client (id, identity_kind, identity_key, interface_id, mac, hostname, last_address,
		first_seen_at, last_seen_at) VALUES
		(1, 'mac', '00:00:5e:00:53:01', 1, '00:00:5e:00:53:01', 'laptop.example', ?, ?, ?),
		(2, 'dhcp_client_id', '01:00:00:5e:00:53:02', 1, '00:00:5e:00:53:02', 'shared.example', ?, ?, ?),
		(3, 'mac', '02:00:5e:00:53:03', 1, '02:00:5e:00:53:03', 'shared.example', ?, ?, ?),
		(4, 'address_in_interface', ?, 1, NULL, NULL, ?, ?, ?)`,
		testNet1(11), at, at, testNet1(12), at, at, testNet1(13), at, at,
		"example_if_1/"+testNet1(14)+"@0", testNet1(14), at, at)
	exec(t, harness, `INSERT INTO rule (id, pf_label, description, action, discovered_at) VALUES
		(1, 'example-label-1', 'Example rule', 'pass', ?),
		(2, 'example-label-2', 'Example duplicate', 'block', ?),
		(3, 'example-label-3', 'Example duplicate', 'block', ?)`, at, at, at)
	exec(t, harness, `INSERT INTO owner (id, display_name, created_at, updated_at) VALUES
		(1, 'Example Person', ?, ?)`, at, at)
	exec(t, harness, `INSERT INTO provider (kind, provider_key, display_name, registered_at)
		VALUES ('dns_lookup', 'example_resolver', 'Example resolver', ?)`, at)
	var providerID int64
	if err := harness.Store.DB().QueryRowContext(context.Background(),
		`SELECT id FROM provider WHERE kind = 'dns_lookup' AND provider_key = 'example_resolver'`).Scan(&providerID); err != nil {
		t.Fatal(err)
	}
	exec(t, harness, `INSERT INTO geo_asn (address, provider_id, lookup_state, country_code, country_name, asn, operator,
		dataset_build_at, looked_up_at) VALUES
		(?, ?, 'resolved', 'XA', 'Example Land A', 64496, 'Example Operator One', ?, ?),
		(?, ?, 'resolved', 'XA', 'Example Land A', 64497, 'Example Operator Two', ?, ?),
		(?, ?, 'resolved', 'XB', 'Example Land B', 64496, 'Example Operator One', ?, ?)`,
		testNet2(1), providerID, at, at, testNet2(2), providerID, at, at, testNet3(3), providerID, at, at)
	for index, domain := range []string{"www.example.com", "WWW.Example.com.", "mail.example.net"} {
		exec(t, harness, `INSERT INTO dns_resolution (id, lookup_key, client_address, domain, resolver, action,
			looked_up_at, ingested_at) VALUES (?, ?, ?, ?, 'example_resolver', 'pass', ?, ?)`,
			index+1, fmt.Sprintf("example-lookup-%d", index), testNet1(11), domain, at, at)
	}
	return fixture{interfaces: interfaceCount, providerID: providerID}
}

// getJSON reads a JSON answer into a value, failing on any other status than want.
func getJSON[T any](t *testing.T, harness *webtest.Harness, path string, want int) T {
	t.Helper()
	status, body := webtest.StatusAndBody(t, harness.Get(path))
	if status != want {
		t.Fatalf("GET %s answered %d, want %d: %.400s", path, status, want, body)
	}
	var value T
	if err := json.Unmarshal([]byte(body), &value); err != nil {
		t.Fatalf("GET %s is not JSON: %v: %.400s", path, err, body)
	}
	return value
}

// signedIn is a signed-in harness over the seed.
func signedIn(t *testing.T, interfaces int) (*webtest.Harness, fixture) {
	t.Helper()
	harness := webtest.New(t)
	harness.CompleteSetup()
	return harness, seed(t, harness, interfaces)
}

// listing is one kind's listing.
func listing(t *testing.T, harness *webtest.Harness, kind, q string) references.CandidatesAnswer {
	t.Helper()
	path := "/api/references/" + kind
	if q != "" {
		path += "?" + url.Values{references.QueryFilter: {q}}.Encode()
	}
	return getJSON[references.CandidatesAnswer](t, harness, path, http.StatusOK)
}

// TestEveryKindListsEveryCandidateWithEveryAcceptedKey is F7-AC12's listing half.
func TestEveryKindListsEveryCandidateWithEveryAcceptedKey(t *testing.T) {
	t.Parallel()
	harness, seeded := signedIn(t, 3)

	// What each kind must list, by a key that names the candidate, for this seed.
	want := map[string]map[string]string{
		"interface": {"example_if_1": "identifier", "example_if_2": "identifier", "example_if_3": "identifier"},
		"client":    {"00:00:5e:00:53:01": "mac", "01:00:00:5e:00:53:02": "dhcp_client_id", "02:00:5e:00:53:03": "mac"},
		"rule":      {"example-label-1": "pf_label", "example-label-2": "pf_label", "example-label-3": "pf_label"},
		"owner":     {"Example Person": "display_name"},
		"provider":  {"dns_lookup/example_resolver": "kind_and_key"},
		"country":   {"XA": "iso_3166_1_alpha_2", "XB": "iso_3166_1_alpha_2"},
		"operator":  {"64496": "asn", "64497": "asn"},
		"site":      {"www.example.com": "domain", "mail.example.net": "domain"},
	}
	for kind, accepted := range params.ReferenceByKind {
		answer := listing(t, harness, kind, "")
		if answer.Kind != kind || strings.Join(answer.By, ",") != strings.Join(accepted, ",") ||
			answer.Limit != params.CandidateLimit || answer.Remainder != 0 || answer.Condition != "" {
			t.Errorf("%s: the listing's frame is %+v", kind, answer)
		}
		seen := map[string]bool{}
		for _, candidate := range answer.Candidates {
			for _, by := range accepted {
				if _, present := candidate.Keys[by]; !present {
					t.Errorf("%s: the candidate %+v does not carry the key %q", kind, candidate, by)
				}
			}
			if len(candidate.Keys) != len(accepted) {
				t.Errorf("%s: the candidate %+v carries keys beyond %v", kind, candidate, accepted)
			}
			if candidate.Label == "" {
				t.Errorf("%s: the candidate %+v has no label", kind, candidate)
			}
			for _, by := range accepted {
				switch value := candidate.Keys[by].(type) {
				case string:
					seen[value] = true
				case float64:
					seen[fmt.Sprint(int64(value))] = true
				}
			}
		}
		for value := range want[kind] {
			if !seen[value] {
				t.Errorf("%s: %q is not listed: %+v", kind, value, answer.Candidates)
			}
		}
		switch kind {
		case "interface":
			if len(answer.Candidates) != seeded.interfaces {
				t.Errorf("interface: %d candidates for %d interfaces", len(answer.Candidates), seeded.interfaces)
			}
		case "site":
			// A name looked up twice, in two spellings, is one site, compared as the resolver does.
			if len(answer.Candidates) != 2 {
				t.Errorf("site: %+v", answer.Candidates)
			}
		}
	}
}

// TestAClientWithNoPortableKeyIsListedNotReferenceable: the address within an interface is no key a
// reference names.
func TestAClientWithNoPortableKeyIsListedNotReferenceable(t *testing.T) {
	t.Parallel()
	harness, _ := signedIn(t, 1)
	answer := listing(t, harness, "client", "")
	if len(answer.Candidates) != 4 {
		t.Fatalf("four clients were seeded and %d are listed", len(answer.Candidates))
	}
	for _, candidate := range answer.Candidates {
		addressOnly := candidate.ID != nil && *candidate.ID == 4
		switch {
		case addressOnly && (candidate.Referenceable || candidate.Reason != params.NotReferenceableAddressInInterface):
			t.Errorf("the address_in_interface client is %+v", candidate)
		case !addressOnly && (!candidate.Referenceable || candidate.Reason != ""):
			t.Errorf("a client with a portable key is %+v", candidate)
		}
		// A client identified by its DHCP client identity is not named by a mac reference.
		if candidate.ID != nil && *candidate.ID == 2 && candidate.Keys["mac"] != nil {
			t.Errorf("a DHCP-identified client carries a mac key: %+v", candidate)
		}
		if candidate.ID != nil && *candidate.ID == 3 && !candidate.UnstableIdentity {
			t.Errorf("a randomised MAC is not marked unstable: %+v", candidate)
		}
	}
}

// TestTheFilterAndTheLimit: q keeps the candidates whose key or label holds it, without regard to
// case, and a listing answers at most the limit with the rest counted.
func TestTheFilterAndTheLimit(t *testing.T) {
	t.Parallel()
	interfaces := params.CandidateLimit + 7
	harness, _ := signedIn(t, interfaces)
	if answer := listing(t, harness, "interface", "EXDEV12"); len(answer.Candidates) != 1+10 ||
		answer.Remainder != 0 {
		// exdev12 and exdev120 to exdev129.
		t.Errorf("q matched %d (remainder %d)", len(answer.Candidates), answer.Remainder)
	}
	if answer := listing(t, harness, "operator", "two"); len(answer.Candidates) != 1 ||
		answer.Candidates[0].Keys["asn"] != float64(64497) {
		t.Errorf("q on an operator's label: %+v", answer.Candidates)
	}
	answer := listing(t, harness, "interface", "")
	if len(answer.Candidates) != params.CandidateLimit || answer.Remainder != 7 {
		t.Errorf("%d interfaces answered %d candidates and a remainder of %d", interfaces,
			len(answer.Candidates), answer.Remainder)
	}
}

// TestASiteUnderNoDomainsIsWithheld: no domain name is listed while names are withheld.
func TestASiteUnderNoDomainsIsWithheld(t *testing.T) {
	t.Parallel()
	harness, _ := signedIn(t, 1)
	if err := harness.Store.SetSetting(context.Background(), config.KeyAggregateMode, "no_domains",
		harness.Clock.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	answer := listing(t, harness, "site", "")
	if answer.Condition != params.UnresolvedWithheld || len(answer.Candidates) != 0 || answer.Remainder != 0 {
		t.Errorf("site under no_domains answered %+v", answer)
	}
	if other := listing(t, harness, "country", ""); other.Condition != "" || len(other.Candidates) != 2 {
		t.Errorf("a country listing under no_domains answered %+v", other)
	}
	resolved := resolve(t, harness, []any{reference("site", "domain", "www.example.com")})
	if resolved.References[0].State != params.UnresolvedWithheld || resolved.References[0].Value != nil {
		t.Errorf("a site reference under no_domains resolved as %+v", resolved.References[0])
	}
}

// TestUnknownKindAndMalformedResolutionAreRefused with closed codes.
func TestUnknownKindAndMalformedResolutionAreRefused(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	harness.CompleteSetup()
	if refusal := getJSON[references.Refusal](t, harness, "/api/references/planet", http.StatusNotFound); refusal.Condition != references.ConditionUnknownKind {
		t.Errorf("an unknown kind answered %+v", refusal)
	}
	for raw, reason := range map[string]string{`[`: "not_json", `{}`: "not_an_array", `[] []`: "trailing_data"} {
		path := references.PathResolve + "?" + url.Values{references.QueryReferences: {raw}}.Encode()
		refusal := getJSON[references.Refusal](t, harness, path, http.StatusBadRequest)
		if refusal.Condition != references.ConditionMalformedParameters || refusal.Reason != reason {
			t.Errorf("%s answered %+v", raw, refusal)
		}
	}
	anonymous := webtest.FreshClient(t, harness)
	for _, path := range []string{"/api/references/interface", references.PathResolve} {
		if status, _ := webtest.StatusAndBody(t, anonymous.Get(path)); status != http.StatusUnauthorized {
			t.Errorf("%s answered %d without a session", path, status)
		}
	}
}

// reference is a reference object.
func reference(kind, by string, value any) map[string]any {
	return map[string]any{"kind": kind, "by": by, "value": value, "label": "as exported"}
}

// resolve asks the resolution of a list.
func resolve(t *testing.T, harness *webtest.Harness, list []any) references.ResolveAnswer {
	t.Helper()
	encoded, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	path := references.PathResolve + "?" + url.Values{references.QueryReferences: {string(encoded)}}.Encode()
	return getJSON[references.ResolveAnswer](t, harness, path, http.StatusOK)
}

// envelopeState is the state the widget envelope gives a reference: resolved when its path is not in
// unresolved_references, else the reason there.
func envelopeState(t *testing.T, harness *webtest.Harness, widgetType string, parameters map[string]any, path string) string {
	t.Helper()
	encoded, err := json.Marshal(parameters)
	if err != nil {
		t.Fatal(err)
	}
	answer := getJSON[struct {
		Unresolved []params.UnresolvedReference `json:"unresolved_references"`
	}](t, harness, widget.PathPrefix+widgetType+"?"+url.Values{widget.ParametersQueryKey: {string(encoded)}}.Encode(),
		http.StatusOK)
	for _, unresolved := range answer.Unresolved {
		if unresolved.Path == path {
			return unresolved.Reason
		}
	}
	return params.ResolvedState
}

// TestResolutionGivesTheWidgetEnvelopesState is F7-AC12's resolution half: for every state, the
// resolution answers what the widget envelope answers for the same reference.
func TestResolutionGivesTheWidgetEnvelopesState(t *testing.T) {
	t.Parallel()
	harness, _ := signedIn(t, 2)
	cases := []struct {
		reference map[string]any
		state     string
	}{
		{reference("interface", "identifier", "example_if_1"), params.ResolvedState},
		{reference("interface", "user_label", "Example label 2"), params.ResolvedState},
		{reference("interface", "device", "exdev9"), "no_match"},
		{reference("interface", "colour", "blue"), "unknown_by"},
		{reference("planet", "identifier", "example_if_1"), "unknown_kind"},
		{reference("client", "mac", "00:00:5e:00:53:01"), params.ResolvedState},
		{reference("client", "hostname", "shared.example"), "ambiguous"},
		{reference("rule", "description", "Example duplicate"), "ambiguous"},
		{reference("rule", "pf_label", "example-label-1"), params.ResolvedState},
		{reference("provider", "kind_and_key", "dns_lookup/example_resolver"), params.ResolvedState},
		{reference("country", "iso_3166_1_alpha_2", "XB"), params.ResolvedState},
		{reference("operator", "asn", 64497), params.ResolvedState},
		{reference("operator", "asn", 64511), "no_match"},
		{reference("site", "domain", "WWW.example.com"), params.ResolvedState},
		{reference("owner", "display_name", "Example Person"), params.ResolvedState},
		{reference("owner", "display_name", "Nobody Here"), "no_match"},
	}
	list := make([]any, 0, len(cases))
	for _, item := range cases {
		list = append(list, item.reference)
	}
	answered := resolve(t, harness, append(list, "not a reference"))
	if len(answered.References) != len(cases)+1 {
		t.Fatalf("%d references answered %d", len(cases)+1, len(answered.References))
	}
	if last := answered.References[len(cases)]; last.State != params.NotAReferenceObject || last.Kind != "" {
		t.Errorf("a string answered %+v", last)
	}
	for index, item := range cases {
		got := answered.References[index]
		if got.Index != index || got.State != item.state || got.Kind != item.reference["kind"] {
			t.Errorf("%v answered %+v, want %s", item.reference, got, item.state)
		}
		// The same reference through a widget: a series entry of custom_chart scopes itself with a
		// reference filed under the key of its kind, and owner_activity takes a list of owners.
		kind, _ := item.reference["kind"].(string)
		var envelope string
		if kind == "owner" {
			envelope = envelopeState(t, harness, "owner_activity",
				map[string]any{"owners": []any{item.reference}}, "owners[0]")
		} else {
			key := kind
			if _, accepted := params.ReferenceByKind[kind]; !accepted {
				key = "interface"
			}
			envelope = envelopeState(t, harness, "custom_chart", map[string]any{"series": []any{
				map[string]any{"source": "traffic", "measure": "bytes", key: item.reference}}}, "series[0]."+key)
		}
		if envelope != got.State {
			t.Errorf("%v: the resolution answered %s and the widget envelope %s", item.reference, got.State, envelope)
		}
	}
	// The states this test covered, so a case list that lost one fails.
	covered := map[string]bool{}
	for _, item := range cases {
		covered[item.state] = true
	}
	var names []string
	for name := range covered {
		names = append(names, name)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "ambiguous,no_match,resolved,unknown_by,unknown_kind" {
		t.Errorf("the cases cover %v", names)
	}
}
