package decode

import (
	"encoding/json"
	"testing"
)

// The field-reader tests.
//
// EVERY CASE BELOW COMES FROM A MEASUREMENT, not from a guess about what JSON might look
// like. docs/opnsense-api-survey.md was corrected against a live OPNsense 26.7.3_11 on
// 2026-09-27, and its section "Verified against a live firewall" is the authority these tables
// cite: where that section contradicts the inferred part of the document, it is right, because
// it was measured. Each case carries the finding it comes from in its `why`, so a case that
// stops being true can be traced back to the sentence that justified it.
//
// The rule under test is one rule: A FIELD THAT IS ABSENT, OR PRESENT IN A SHAPE THAT CANNOT BE
// READ, IS REPORTED AS ABSENT. Never as a zero, never as an empty string that looks like an
// answer. Every table therefore has as many negative rows as positive ones, because the
// negative half is the half that protects the product from publishing a figure nobody measured.

// TestStringReadsTheShapesOPNsenseActuallyReturns covers the uncast-request problem, which is
// the reason this layer is tolerant at all: OPNsense's Mvc\Request reads $_REQUEST uncast, so
// the same logical field arrives as a JSON number from one endpoint and as a string from
// another depending on how the backend assembled the response.
func TestStringReadsTheShapesOPNsenseActuallyReturns(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		object  Object
		key     string
		want    string
		present bool
		why     string
	}{
		{
			name:    "a plain string",
			object:  Object{"hostname": "an-example-host"},
			key:     "hostname",
			want:    "an-example-host",
			present: true,
			why:     "the Dnsmasq lease search returns hostname as a string",
		},
		{
			name:    "a number where a string is expected",
			object:  Object{"total": float64(1000)},
			key:     "total",
			want:    "1000",
			present: true,
			why: "measured: the resolver's total is 1000 whatever window is asked for, and it " +
				"arrives as a number",
		},
		{
			name:    "an explicit null",
			object:  Object{"uuid": nil},
			key:     "uuid",
			present: false,
			why: "measured: uuid is null on EVERY row the resolver returns, which is why the " +
				"de-duplication key is composed from the row's content instead",
		},
		{
			name:    "an absent key",
			object:  Object{},
			key:     "uuid",
			present: false,
			why:     "an absent field is the same state as a null one: not reported",
		},
		{
			name:    "an empty string",
			object:  Object{"hostname": ""},
			key:     "hostname",
			present: false,
			why: "a lease with an empty hostname named nothing; reporting it as present would " +
				"put an empty label on a screen",
		},
		{
			name:    "whitespace only",
			object:  Object{"hostname": "   "},
			key:     "hostname",
			present: false,
			why:     "the same, with the padding an uncast request leaves behind",
		},
		{
			name:    "a shape that cannot be read",
			object:  Object{"details": []any{"a", "b"}},
			key:     "details",
			present: false,
			why:     "traffic/top nests a details[] array; read as a string it is ABSENT, not \"[]\"",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, present := String(testCase.object, testCase.key)
			if present != testCase.present {
				t.Fatalf("presence is %v, want %v — %s", present, testCase.present, testCase.why)
			}
			if present && got != testCase.want {
				t.Fatalf("read %q, want %q — %s", got, testCase.want, testCase.why)
			}
			pointer := StringPointer(testCase.object, testCase.key)
			if testCase.present != (pointer != nil) {
				t.Fatalf("the nullable form disagrees with the two-value form about presence")
			}
		})
	}
}

// TestTheNullResolverIdentifierIsAbsentRatherThanEmpty is the single measurement that changed
// the data model, given a test of its own because it is the one a future reader will doubt.
//
// The survey assumed the resolver's `uuid` could serve as a de-duplication key. Measured on the
// live firewall, it is null on every row. Had this layer reported null as the empty string, the
// key would have been a constant, every lookup would have collided with every other, and the
// table would have held one row for ever — silently, with no error anywhere.
func TestTheNullResolverIdentifierIsAbsentRatherThanEmpty(t *testing.T) {
	row := Object{"uuid": nil, "client": "an-example-address", "domain": "an.example"}

	if _, present := String(row, "uuid"); present {
		t.Error("a null identifier was reported as present, so a composed key would never be used")
	}
	if pointer := StringPointer(row, "uuid"); pointer != nil {
		t.Errorf("a null identifier became %q rather than a nil column value", *pointer)
	}
	if got := RawString(row, "uuid"); got != "" {
		t.Errorf("the raw reader returned %q for a null field", got)
	}
	// And a key composed from content is not empty, which is what makes the substitute work.
	if RawString(row, "client") == "" || RawString(row, "domain") == "" {
		t.Error("the fields the composed key is built from did not read back")
	}
}

// TestIntReadsBothEncodingsAndRefusesWhatItCannotRead covers the same uncast-request problem on
// the numeric side, where reporting an unreadable field as zero would be worst: a zero packet
// count, a zero byte count and a missing measurement are three different statements.
func TestIntReadsBothEncodingsAndRefusesWhatItCannotRead(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		value   any
		want    int64
		present bool
		why     string
	}{
		{"a JSON number", float64(1000), 1000, true, "measured: total comes back as 1000"},
		{"a numeric string", "1000", 1000, true, "the same field from an endpoint that stringified it"},
		{"a padded numeric string", " 443 ", 443, true, "an uncast request leaves the padding on"},
		{"a fractional string", "1759100000.25", 1759100000, true,
			"a fractional epoch is legitimate and truncates to the second the schema stores"},
		{"a boolean true", true, 1, true, "OPNsense writes flags as booleans on some endpoints"},
		{"a boolean false", false, 0, true,
			"false is a READING of zero, which is different from being unable to read"},
		{"null", nil, 0, false, "measured on uuid: a null field is not reported"},
		{"an empty string", "", 0, false, "an empty string is not a number"},
		{"text", "an-example-name", 0, false,
			"an unreadable value must not become a zero count on a screen"},
		{"an object", Object{"nested": 1}, 0, false, "nor must a nested shape"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			object := Object{"field": testCase.value}
			got, present := Int(object, "field")
			if present != testCase.present {
				t.Fatalf("presence is %v, want %v — %s", present, testCase.present, testCase.why)
			}
			if present && got != testCase.want {
				t.Fatalf("read %d, want %d — %s", got, testCase.want, testCase.why)
			}
			if pointer := IntPointer(object, "field"); testCase.present != (pointer != nil) {
				t.Fatal("the nullable form disagrees with the two-value form about presence")
			}
		})
	}
}

// TestAFlagIsThreeStatedBecauseTheFirewallWritesItThreeWays pins the shape Suricata's own
// status uses.
//
// Measured on the live firewall: the IDS reports `enabled: 1`. Other endpoints write the same
// logical flag as "0"/"1" and as a JSON boolean. All three mean the same thing; a field that is
// absent means something else entirely, and conflating "switched off" with "we could not ask"
// is exactly what source_availability exists to prevent.
func TestAFlagIsThreeStatedBecauseTheFirewallWritesItThreeWays(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		value any
		want  *bool
		why   string
	}{
		{"one as a number", float64(1), boolean(true), "measured: the IDS reports enabled: 1"},
		{"zero as a number", float64(0), boolean(false), "the same endpoint when it is off"},
		{"one as a string", "1", boolean(true), "the encoding an uncast request produces"},
		{"zero as a string", "0", boolean(false), "the same"},
		{"a true boolean", true, boolean(true), "the encoding a model response produces"},
		{"a false boolean", false, boolean(false), "the same"},
		{"null", nil, nil, "not reported is a third state, not false"},
		{"absent", nil, nil, "and so is an absent key"},
		{"text", "an-example-value", nil,
			"a flag that cannot be read is not reported; treating it as false would switch a " +
				"source off on the strength of a shape nobody recorded"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			object := Object{}
			if testCase.name != "absent" {
				object["enabled"] = testCase.value
			}
			got := Flag(object, "enabled")
			switch {
			case testCase.want == nil && got != nil:
				t.Fatalf("read %v, want not-reported — %s", *got, testCase.why)
			case testCase.want != nil && got == nil:
				t.Fatalf("read not-reported, want %v — %s", *testCase.want, testCase.why)
			case testCase.want != nil && *got != *testCase.want:
				t.Fatalf("read %v, want %v — %s", *got, *testCase.want, testCase.why)
			}
			// The two-state reduction a two-state column needs: not-reported is not true.
			if FlagOrFalse(got) && (testCase.want == nil || !*testCase.want) {
				t.Fatal("a not-reported or false flag was reduced to true")
			}
		})
	}
}

// TestAnEmptyCollectionIsAReadingAndNotAFailure is the trap the verified section names outright.
//
// Measured: /api/diagnostics/traffic/top/ given a DEVICE name rather than an interface name
// returns `[]` with HTTP 200 — "a wrong argument looks like an absence of traffic". And
// Suricata's query_alerts returned `[]` for every window up to seven days on a correctly working
// firewall with five narrow abuse.ch feeds enabled. So an empty collection must decode as zero
// rows WITHOUT error: the difference between "nothing to report" and "we could not ask" is
// recorded by the caller against the response, and cannot be recovered here.
func TestAnEmptyCollectionIsAReadingAndNotAFailure(t *testing.T) {
	for _, testCase := range []struct {
		name string
		body string
		why  string
	}{
		{"a bare empty array", `[]`,
			"measured: traffic/top with a device name, and query_alerts with nothing to report"},
		{"an empty grid-search envelope", `{"rows": [], "total": 0}`,
			"the shape the search endpoints use when they match nothing"},
		{"an empty map", `{}`, "the shape a map-keyed collection uses when it is empty"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			rows, err := Rows([]byte(testCase.body))
			if err != nil {
				t.Fatalf("an empty collection was an error: %v — %s", err, testCase.why)
			}
			if len(rows) != 0 {
				t.Fatalf("read %d rows from an empty collection", len(rows))
			}
		})
	}

	// And a body that genuinely cannot be read IS an error, so the two are distinguishable.
	if _, err := Rows([]byte(`not json at all`)); err == nil {
		t.Error("an unreadable body decoded as an empty collection, which would make a broken " +
			"endpoint indistinguishable from a quiet one")
	}
}

// TestRowsDecodesTheThreeCollectionShapesTheEndpointsUse covers the shapes the survey records
// across the endpoints this layer reads: a grid-search envelope, a bare array, and a map keyed
// by something — an interface identifier or a device name.
func TestRowsDecodesTheThreeCollectionShapesTheEndpointsUse(t *testing.T) {
	for _, testCase := range []struct {
		name string
		body string
		want int
		why  string
	}{
		{
			name: "a grid-search envelope",
			body: `{"total": 2, "rows": [{"address": "a"}, {"address": "b"}]}`,
			want: 2,
			why:  "the shape searchLease, search and searchQueries use",
		},
		{
			name: "a bare array",
			body: `[{"mac": "a"}, {"mac": "b"}, {"mac": "c"}]`,
			want: 3,
			why:  "measured: get_arp and get_ndp return one row per neighbour",
		},
		{
			name: "a map keyed by something",
			body: `{"lan": {"records": []}, "opt1": {"records": []}}`,
			want: 2,
			why: "traffic/top answers per interface; the key is not lost, a caller that needs " +
				"it reads the map itself",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			rows, err := Rows([]byte(testCase.body))
			if err != nil {
				t.Fatalf("decoding: %v — %s", err, testCase.why)
			}
			if len(rows) != testCase.want {
				t.Fatalf("read %d rows, want %d — %s", len(rows), testCase.want, testCase.why)
			}
		})
	}
}

// TestALoneObjectIsNotReadAsARowOfItsOwn records an ambiguity that cannot be resolved, and a
// comment that used to claim it had been.
//
// The doc comment on Objects asserted that "a single object where a list was expected is
// returned as a list of one". It never did that, and it could not: a map whose VALUES are the
// rows and a single row that happens to be a map are the same JSON. Accepting both readings
// would make the meaning depend on which field types a given row happened to carry. The keyed
// reading is the one the API requires — measured: traffic/top answers per interface, under the
// interface's own name — so it is the only one, and the comment now says so.
//
// The consequence is deliberate and is what this test pins: a lone row arriving where a
// collection was expected decodes as ZERO rows, which the caller records as having read nothing.
// That is the honest outcome. The alternative was a row assembled by guessing which of two
// incompatible shapes the endpoint meant.
func TestALoneObjectIsNotReadAsARowOfItsOwn(t *testing.T) {
	rows, err := Rows([]byte(`{"rows": {"address": "198.51.100.10", "hostname": "an-example-host"}}`))
	if err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("a lone row decoded as %d rows; the keyed reading is the only one, so this must "+
			"read as nothing rather than as a row nobody can be sure was one", len(rows))
	}

	// The keyed reading, on the same syntax, does yield its rows — which is the whole reason
	// the ambiguity resolves this way rather than the other.
	rows, err = Rows([]byte(`{"lan": {"records": []}, "opt1": {"records": []}}`))
	if err != nil {
		t.Fatalf("decoding the keyed shape: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("the keyed shape read %d rows, want 2", len(rows))
	}
}

// TestTheDnsmasqLeaseRowReadsBackFieldForField is the verified section's own list.
//
// Measured: /api/dnsmasq/leases/search returns rows carrying hwaddr, address, hostname,
// client_id, expire, is_reserved, the vendor as mac_info, and the interface under all three of
// its names at once. The survey's earlier claim that Dnsmasq needed a free-text parser for
// leases is wrong, and this test is what keeps that correction from being lost: if these field
// names stop reading back, the lease table silently empties.
func TestTheDnsmasqLeaseRowReadsBackFieldForField(t *testing.T) {
	// Example values, not a capture: nothing here is any installation's addressing.
	row := Object{
		"hwaddr":      "AA:BB:CC:DD:EE:FF",
		"address":     "198.51.100.10",
		"hostname":    "an-example-host",
		"client_id":   "an-example-client-id",
		"expire":      float64(1759100000),
		"is_reserved": "0",
		"mac_info":    "An Example Vendor",
		"if":          "an-example-device",
		"if_descr":    "An example description",
		"interface":   "opt9",
	}

	for _, field := range []string{"hwaddr", "address", "hostname", "client_id", "mac_info",
		"if", "if_descr", "interface"} {
		if _, present := String(row, field); !present {
			t.Errorf("the lease field %q did not read back, and the survey measured it present",
				field)
		}
	}
	expire, present := Epoch(row, "expire")
	if !present || expire != 1759100000 {
		t.Errorf("expire read back as (%d, %v); it is epoch seconds on this backend",
			expire, present)
	}
	if reserved := Flag(row, "is_reserved"); reserved == nil || *reserved {
		t.Error("is_reserved did not read back as a reported false")
	}
	// The interface arrives under three names at once, and all three are distinct values. The
	// kind decides which to join on; this layer must not lose any of them.
	device, descr, identifier := RawString(row, "if"), RawString(row, "if_descr"),
		RawString(row, "interface")
	if device == descr || device == identifier || descr == identifier {
		t.Error("the three interface names collapsed into fewer than three readings")
	}
}

// TestFirstFloatNamesTheKeyThatAnswered is how an UNVERIFIED assumption is kept honest.
//
// The verified section establishes that the telemetry endpoints ANSWER — systemResources,
// systemTemperature, systemTime, systemDisk, traffic/interface — and does not
// establish their field names at all. So the candidate list is an assumption, and returning the
// key that answered is what lets a later pass record which one a real firewall uses instead of
// guessing a second time. A candidate list where none matches must report absence, because a
// gauge nobody could read is not a gauge reading zero.
func TestFirstFloatNamesTheKeyThatAnswered(t *testing.T) {
	object := Object{"cumulative_bytes_in": float64(4096), "rate_bits_in": "1024"}

	value, key, present := FirstFloat(object, "bytes_in", "cumulative_bytes_in", "rate_bits_in")
	if !present || value != 4096 || key != "cumulative_bytes_in" {
		t.Fatalf("read (%v, %q, %v); want the first candidate that answered", value, key, present)
	}

	// A stringified figure answers too, because an uncast request produces one.
	value, key, present = FirstFloat(object, "rate_bits_in")
	if !present || value != 1024 || key != "rate_bits_in" {
		t.Fatalf("a stringified figure read back as (%v, %q, %v)", value, key, present)
	}

	// A percentage rendered for display, which one telemetry endpoint is known to do.
	if value, _, present = FirstFloat(Object{"used": "43.5%"}, "used"); !present || value != 43.5 {
		t.Fatalf("a rendered percentage read back as (%v, %v)", value, present)
	}

	// And nothing matching is an absence that names no key.
	if value, key, present = FirstFloat(object, "a_key_nobody_measured"); present {
		t.Fatalf("an unmeasured key answered with (%v, %q); a gauge nobody could read must be "+
			"recorded as missing, never written as a zero", value, key)
	}
}

// TestNestedWalksTheModelResponsesTheWayOPNsenseNestsThem covers the model endpoints, whose
// answers nest — ids.general.enabled, dhcpv4.general.enabled, unbound.general.enabled — and
// which is where a wrong path would read a missing flag as a disabled service.
func TestNestedWalksTheModelResponsesTheWayOPNsenseNestsThem(t *testing.T) {
	body := Object{
		"ids": Object{
			"general": Object{"enabled": "1", "detect": Object{"profile": "medium"}},
		},
	}

	if flag := NestedFlag(body, "ids", "general", "enabled"); flag == nil || !*flag {
		t.Error("the IDS enabled flag did not read back; measured on the live firewall as 1")
	}
	if got, present := NestedString(body, "ids", "general", "detect", "profile"); !present ||
		got != "medium" {
		t.Errorf("a deeper leaf read back as (%q, %v)", got, present)
	}
	if _, present := Nested(body, "ids", "a_branch_nobody_measured"); present {
		t.Error("a path that does not exist reported present")
	}
	if flag := NestedFlag(body, "ids", "a_branch_nobody_measured", "enabled"); flag != nil {
		t.Error("a flag under a missing branch read as reported; a service opnview could not " +
			"ask about must not look like a service that is switched off")
	}
	// A leaf that is not an object where the walk expects one stops the walk rather than
	// pretending.
	if _, present := Nested(Object{"ids": "not-an-object"}, "ids", "general"); present {
		t.Error("the walk descended into a non-object")
	}
}

// TestPortRefusesWhatTheSchemaWouldRefuse keeps an out-of-range value out of a column the
// database constrains, so the failure is a nil rather than a rejected write.
func TestPortRefusesWhatTheSchemaWouldRefuse(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		value any
		want  *int64
	}{
		{"a low port", float64(53), number(53)},
		{"a high port", float64(65535), number(65535)},
		{"zero", float64(0), number(0)},
		{"a stringified port", "443", number(443)},
		{"above the range", float64(65536), nil},
		{"negative", float64(-1), nil},
		{"text", "an-example-value", nil},
		{"null", nil, nil},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := Port(Object{"dstport": testCase.value}, "dstport")
			switch {
			case testCase.want == nil && got != nil:
				t.Fatalf("read %d, want nil: the schema constrains this column", *got)
			case testCase.want != nil && got == nil:
				t.Fatalf("read nil, want %d", *testCase.want)
			case testCase.want != nil && *got != *testCase.want:
				t.Fatalf("read %d, want %d", *got, *testCase.want)
			}
		})
	}
}

// TestEpochRefusesAnInstantItCannotRead protects the columns every screen orders by. A zero
// epoch is 1970, which sorts first for ever and would put an unreadable row at the top of every
// chronological view.
func TestEpochRefusesAnInstantItCannotRead(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		value   any
		present bool
	}{
		{"epoch seconds as a number", float64(1759100000), true},
		{"epoch seconds as a string", "1759100000", true},
		{"a fractional epoch", "1759100000.75", true},
		{"null", nil, false},
		{"an empty string", "", false},
		{"an ISO timestamp", "2026-09-26T23:51:06", false},
		{"text", "an-example-value", false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			object := Object{"time": testCase.value}
			if _, present := Epoch(object, "time"); present != testCase.present {
				t.Fatalf("presence is %v, want %v", present, testCase.present)
			}
			if pointer := EpochPointer(object, "time"); testCase.present != (pointer != nil) {
				t.Fatal("the nullable form disagrees with the two-value form about presence")
			}
		})
	}

	// Zero and negative are refused by the nullable form specifically: they are the shapes a
	// backend uses for "no expiry", and storing 1970 would make a lease look long expired.
	for _, value := range []any{float64(0), float64(-1)} {
		if pointer := EpochPointer(Object{"expire": value}, "expire"); pointer != nil {
			t.Errorf("%v became the instant %d rather than a nil column value", value, *pointer)
		}
	}
}

// TestNonEmptyIsOnlyEverAskedWhetherSomethingIsThere pins the one use this helper has:
// dnsmasq.dhcp_ranges, where only emptiness is read. The ranges themselves are the firewall's
// addressing configuration, which opnview neither stores nor interprets — so this tests that
// the helper answers the presence question over every shape a configuration value takes, and
// nothing about what is in it.
func TestNonEmptyIsOnlyEverAskedWhetherSomethingIsThere(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		value any
		want  bool
	}{
		{"a populated list", []any{Object{}}, true},
		{"an empty list", []any{}, false},
		{"a populated map", Object{"a": 1}, true},
		{"an empty map", Object{}, false},
		{"a non-empty string", "something", true},
		{"an empty string", "", false},
		{"null", nil, false},
		{"zero", float64(0), false},
		{"a number", float64(1), true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := NonEmpty(testCase.value); got != testCase.want {
				t.Fatalf("read %v, want %v", got, testCase.want)
			}
		})
	}
}

// TestTheCaseHelpersTouchOnlyASCIIAndNeverAName guards the rule that no classification is made
// from the text of a name.
//
// LowerASCII exists to compare values whose vocabulary the survey establishes — measured: Kea
// reports "disabled" and Dnsmasq "running" — and UpperASCII exists for exactly one thing: to
// reproduce an endpoint's own documented fallback for a missing interface description, which is
// the upper-cased identifier. Neither may alter anything else, because a helper that
// transliterated would be one a future branch could use to read a name's text.
func TestTheCaseHelpersTouchOnlyASCIIAndNeverAName(t *testing.T) {
	if got := LowerASCII("Running"); got != "running" {
		t.Errorf("the status vocabulary lower-cased to %q", got)
	}
	if got := LowerASCII("DISABLED"); got != "disabled" {
		t.Errorf("the status vocabulary lower-cased to %q", got)
	}
	if got := UpperASCII("opt9"); got != "OPT9" {
		t.Errorf("the endpoint's own fallback for a missing description rendered as %q", got)
	}
	// Everything that is not an ASCII letter is left exactly as it stands, digits, separators
	// and non-ASCII alike.
	for _, value := range []string{"vlan01", "1.2.3.4", "a-b_c.d", "Ré-Séau", "éàü"} {
		if LowerASCII(value) != expectASCIIFolded(value, false) {
			t.Errorf("lower-casing %q altered something that is not an ASCII letter", value)
		}
		if UpperASCII(value) != expectASCIIFolded(value, true) {
			t.Errorf("upper-casing %q altered something that is not an ASCII letter", value)
		}
	}
}

// expectASCIIFolded folds only the ASCII letters of s, computed independently of the helpers
// under test so the assertion is not the implementation restated.
func expectASCIIFolded(s string, upper bool) string {
	out := []byte(s)
	for index := range out {
		switch {
		case upper && out[index] >= 'a' && out[index] <= 'z':
			out[index] -= 32
		case !upper && out[index] >= 'A' && out[index] <= 'Z':
			out[index] += 32
		}
	}
	return string(out)
}

// boolean returns an addressable flag, for the tables above.
func boolean(value bool) *bool { return &value }

// number returns an addressable integer, for the tables above.
func number(value int64) *int64 { return &value }

// TestTextFieldKeepsNullApartFromTheEmptyStringAndRawStringIsUnchanged is the decoding half
// of specs/SPEC-resolver-cache-follow-ups.md, scope 5: TextField tells a JSON null, an
// absent key, a string and any other shape apart, while RawString still reads null and
// absent as the empty string and renders a number, as its existing callers rely on.
func TestTextFieldKeepsNullApartFromTheEmptyStringAndRawStringIsUnchanged(t *testing.T) {
	t.Parallel()
	var row Object
	if err := json.Unmarshal([]byte(`{"null": null, "empty": "", "digits": " 300 ", "number": 300, "flag": true}`),
		&row); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		key       string
		wantText  string
		wantShape Shape
		wantRaw   string
	}{
		{"null", "", ShapeNull, ""},
		{"absent", "", ShapeAbsent, ""},
		{"empty", "", ShapeString, ""},
		{"digits", " 300 ", ShapeString, "300"},
		{"number", "", ShapeOther, "300"},
		{"flag", "", ShapeOther, "true"},
	} {
		text, shape := TextField(row, testCase.key)
		if text != testCase.wantText || shape != testCase.wantShape {
			t.Errorf("TextField(%q) = %q, %d; want %q, %d", testCase.key, text, shape, testCase.wantText,
				testCase.wantShape)
		}
		if raw := RawString(row, testCase.key); raw != testCase.wantRaw {
			t.Errorf("RawString(%q) = %q; want %q", testCase.key, raw, testCase.wantRaw)
		}
	}
}
