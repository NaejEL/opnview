package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// pathsOf walks a JSON text.
func pathsOf(t *testing.T, text string) pathSet {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		t.Fatal(err)
	}
	set := newPathSet()
	set.walk(value, "")
	return set
}

// recordedOf is the record of one type from its answers at the defaults.
func recordedOf(t *testing.T, texts ...string) recorded {
	t.Helper()
	entry := recorded{all: map[string]bool{}, defaults: map[string]bool{}}
	for _, text := range texts {
		set := pathsOf(t, text)
		for path := range set.paths {
			entry.all[path] = true
			if set.always(path) {
				entry.defaults[path] = true
			}
		}
	}
	return entry
}

// TestAMemberAddedOrLostIsDriftAndMissingDataIsNot: a member the seed answers that no recorded
// answer carries, and a member every recorded object carries that the seed's objects lack, are
// drift; a member only some recorded elements carry, a member below an array the seed left empty and
// one below a null are not.
func TestAMemberAddedOrLostIsDriftAndMissingDataIsNot(t *testing.T) {
	t.Parallel()
	known := recordedOf(t, `{"condition": "reachable", "data": {"rows": [
		{"kind": "site", "name": "a.example", "flows": {"value": 1}},
		{"kind": "address", "address": "192.0.2.1", "flows": {"value": 2}}], "other": {"value": 3}, "gaps": []}}`)
	for _, test := range []struct {
		name, seed     string
		added, removed string
	}{
		{"the same members", `{"condition": "x", "data": {"rows": [{"kind": "site", "name": "b.example", "flows": {"value": 9}}],
			"other": {"value": 1}, "gaps": []}}`, "", ""},
		{"a member added", `{"condition": "x", "state_details": {}, "data": {"rows": [], "other": {"value": 1}, "gaps": []}}`,
			".state_details", ""},
		{"a member lost", `{"condition": "x", "data": {"rows": [{"kind": "site", "name": "b.example"}], "other": {"value": 1},
			"gaps": []}}`, "", ".data.rows[].flows"},
		{"data the seed does not have", `{"condition": "x", "data": {"rows": [], "other": null, "gaps": []}}`, "", ""},
	} {
		added, removed := compare(pathsOf(t, test.seed), known)
		if strings.Join(added, " ") != test.added || strings.Join(removed, " ") != test.removed {
			t.Errorf("%s: added %v and removed %v, want %q and %q", test.name, added, removed, test.added, test.removed)
		}
	}
}
