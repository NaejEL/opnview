package widget

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/NaejEL/opnview/internal/store"
)

// Reference resolution (A4.4), against the tables of docs/dashboard-format.md, "The
// reference object". A reference resolves by EXACT match on its declared `by` key and on
// nothing else: its `label` is carried from the exporting installation for the repair
// surface and is never matched on, here or anywhere. A `kind` or `by` outside the
// vocabulary is unresolved, never an error; and an unresolved reference makes the widget --
// or, inside `series`, its own series -- return no data, never zeros.

// Reference is the four-key reference object.
type Reference struct {
	Kind  string `json:"kind"`
	By    string `json:"by"`
	Value any    `json:"value"`
	Label any    `json:"label,omitempty"`
}

// referenceByKind is the closed vocabulary: each kind and the `by` keys it accepts.
var referenceByKind = map[string][]string{
	"interface": {"identifier", "device", "user_label"},
	"client":    {"mac", "dhcp_client_id", "hostname"},
	"rule":      {"pf_label", "description"},
	"owner":     {"display_name"},
	"provider":  {"kind_and_key"},
	"country":   {"iso_3166_1_alpha_2"},
	"operator":  {"asn"},
	"site":      {"domain"},
}

// UnresolvedReference is a reference that matched nothing on this installation, with
// everything the repair surface needs to name it.
type UnresolvedReference struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	By     string `json:"by"`
	Value  any    `json:"value"`
	Label  any    `json:"label"`
	Reason string `json:"reason"`
}

// The reasons a reference is unresolved.
const (
	unresolvedUnknownKind     = "unknown_kind"
	unresolvedUnknownBy       = "unknown_by"
	unresolvedKindNotAccepted = "kind_not_accepted_here"
	unresolvedNoMatch         = "no_match"
	unresolvedAmbiguous       = "ambiguous"
	// unresolvedWithheld is a site reference under aggregate_mode = no_domains (A10 and
	// decision 6). A domain name may not select, scope or probe anything while names are
	// withheld, so the reference is not looked up at all: the answer is the same whether
	// or not the name was ever seen, and nothing can be learnt from it.
	unresolvedWithheld = "withheld_by_aggregate_mode"
)

// resolved is a reference that matched: an id for the kinds that name a row, a text for a
// country or a site, an AS number for an operator.
type resolved struct {
	id   int64
	text string
}

// decodeReference reads a reference object. A value that is not an object carrying
// `kind`, `by` and `value` is not a reference at all, which is an invalid value rather than
// an unresolved reference.
func decodeReference(raw json.RawMessage) (Reference, bool) {
	var fields map[string]any
	if json.Unmarshal(raw, &fields) != nil {
		return Reference{}, false
	}
	kind, kindOK := fields["kind"].(string)
	by, byOK := fields["by"].(string)
	value, valueOK := fields["value"]
	if !kindOK || !byOK || !valueOK || value == nil {
		return Reference{}, false
	}
	return Reference{Kind: kind, By: by, Value: value, Label: fields["label"]}, true
}

// referenceValue is how a reference is echoed in the effective parameters: as given.
func referenceValue(reference Reference) map[string]any {
	echoed := map[string]any{"kind": reference.Kind, "by": reference.By, "value": reference.Value}
	if reference.Label != nil {
		echoed["label"] = reference.Label
	}
	return echoed
}

// resolveReference resolves one reference expected to be of kind expected.
func resolveReference(ctx context.Context, database *store.Store, at referenceAt, noDomains bool) (
	resolved, string, error) {
	reference := at.reference
	accepted, known := referenceByKind[reference.Kind]
	switch {
	case !known:
		return resolved{}, unresolvedUnknownKind, nil
	case !contains(accepted, reference.By):
		return resolved{}, unresolvedUnknownBy, nil
	case at.expected != "" && reference.Kind != at.expected:
		return resolved{}, unresolvedKindNotAccepted, nil
	}
	// After the vocabulary, which depends on nothing stored, and before any lookup: a site
	// reference under no_domains is withheld whatever its value.
	if noDomains && reference.Kind == "site" {
		return resolved{}, unresolvedWithheld, nil
	}
	text, isText := reference.Value.(string)
	ids := func(name string, parameters map[string]any) (resolved, string, error) {
		var matches []int64
		err := database.WidgetRows(ctx, name, store.Period{}, parameters, func(row store.RowScanner) error {
			var id int64
			if err := row.Scan(&id); err != nil {
				return err
			}
			matches = append(matches, id)
			return nil
		})
		switch {
		case err != nil:
			return resolved{}, "", err
		case len(matches) == 0:
			return resolved{}, unresolvedNoMatch, nil
		case len(matches) > 1:
			return resolved{}, unresolvedAmbiguous, nil
		}
		return resolved{id: matches[0]}, "", nil
	}
	exists := func(name string, parameters map[string]any, value resolved) (resolved, string, error) {
		var one int64
		found, err := database.WidgetRow(ctx, name, parameters, &one)
		switch {
		case err != nil:
			return resolved{}, "", err
		case !found:
			return resolved{}, unresolvedNoMatch, nil
		}
		return value, "", nil
	}

	switch reference.Kind {
	case "interface", "client", "rule", "owner", "provider", "country", "site":
		if !isText || text == "" {
			return resolved{}, unresolvedNoMatch, nil
		}
	}
	switch reference.Kind + "/" + reference.By {
	case "interface/identifier":
		return ids("widget_ref_interface_identifier", map[string]any{"value": text})
	case "interface/device":
		return ids("widget_ref_interface_device", map[string]any{"value": text})
	case "interface/user_label":
		return ids("widget_ref_interface_user_label", map[string]any{"value": text})
	case "client/mac":
		return ids("widget_ref_client_identity", map[string]any{"identity_kind": "mac", "value": text})
	case "client/dhcp_client_id":
		return ids("widget_ref_client_identity",
			map[string]any{"identity_kind": "dhcp_client_id", "value": text})
	case "client/hostname":
		return ids("widget_ref_client_hostname", map[string]any{"value": text})
	case "rule/pf_label":
		return ids("widget_ref_rule_pf_label", map[string]any{"value": text})
	case "rule/description":
		return ids("widget_ref_rule_description", map[string]any{"value": text})
	case "owner/display_name":
		return ids("widget_ref_owner_display_name", map[string]any{"value": text})
	case "provider/kind_and_key":
		kind, key, found := strings.Cut(text, "/")
		if !found {
			return resolved{}, unresolvedNoMatch, nil
		}
		value, reason, err := ids("widget_ref_provider", map[string]any{"kind": kind, "value": key})
		value.text = key
		return value, reason, err
	case "country/iso_3166_1_alpha_2":
		return exists("widget_ref_country", map[string]any{"value": text}, resolved{text: text})
	case "operator/asn":
		asn, ok := asNumber(reference.Value)
		if !ok {
			return resolved{}, unresolvedNoMatch, nil
		}
		return exists("widget_ref_operator", map[string]any{"asn": asn}, resolved{id: asn})
	case "site/domain":
		name := strings.TrimSuffix(strings.ToLower(text), ".")
		return exists("widget_ref_site", map[string]any{"value": text}, resolved{text: name})
	}
	return resolved{}, unresolvedUnknownBy, nil
}

// asNumber reads a whole number from a decoded JSON value, or from a string holding only
// its digits, because a JSON file may carry an AS number either way. Nothing else is read
// as one: "AS64496" is not the number 64496 to an exact match.
func asNumber(value any) (int64, bool) {
	switch typed := value.(type) {
	case float64:
		if typed == float64(int64(typed)) {
			return int64(typed), true
		}
	case json.Number:
		number, err := typed.Int64()
		return number, err == nil
	case string:
		parsed, err := json.Number(typed).Int64()
		return parsed, err == nil
	}
	return 0, false
}
