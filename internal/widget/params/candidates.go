package params

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// Reference candidates: what each reference kind can name on this installation
// (specs/SPEC-step-7-frontend.md, A8). The repair surface and the no-code form offer them as
// dropdowns, so a reference is always chosen from what exists and never typed.
//
// EVERY CANDIDATE CARRIES EVERY ACCEPTED `by` KEY of its kind (ReferenceByKind), each the value a
// reference by that key would have to carry to resolve to this candidate through ResolveReference,
// or null when no reference by that key can name it: a client identified by its DHCP client
// identity is not named by a `mac` reference even when its MAC is known, because the resolver
// matches `mac` against the identity of a client identified by its MAC. The label is data -- a
// name, a description, an address -- for the dropdown to show, and is never matched on.
//
// THE STATEMENTS NEVER SCAN A GROWING TABLE (candidates.statements.sql): the bounded tables are
// read whole, and the growing ones a page at a time by index searches. The q filter and the
// remainder count are computed here over those pages, so the remainder of a kind with many
// distinct values costs one index search per value.

// CandidateLimit is the number of candidates a listing answers at most.
const CandidateLimit = 200

// candidatePage is the number of keys one page of a growing source holds.
const candidatePage = 500

// The reason a listed candidate cannot be referenced.
const (
	// NotReferenceableAddressInInterface is a client identified by its address within its
	// interface: that identity is this installation's own and carries no key a reference names.
	NotReferenceableAddressInInterface = "address_in_interface"
)

// ErrUnknownReferenceKind is a listing asked for a kind outside the vocabulary.
var ErrUnknownReferenceKind = errors.New("params: unknown reference kind")

// Candidate is one thing a reference of a kind can name.
type Candidate struct {
	// ID is the row's id for the kinds that name a row (interface, client, rule, owner,
	// provider); absent for a country, an operator and a site, which name a value.
	ID *int64 `json:"id,omitempty"`
	// Keys holds every accepted `by` key of the kind: the value a reference by that key carries
	// to name this candidate, or null when none can.
	Keys map[string]any `json:"keys"`
	// Label is what the dropdown shows. It is data and never matched on.
	Label string `json:"label"`
	// Referenceable is false for a candidate no reference can name; Reason then says why.
	Referenceable bool   `json:"referenceable"`
	Reason        string `json:"reason,omitempty"`
	// UnstableIdentity marks a client whose MAC is randomised: a `mac` reference to it does not
	// travel and may stop resolving here.
	UnstableIdentity bool `json:"unstable_identity,omitempty"`
}

// CandidateList is one kind's candidates.
type CandidateList struct {
	// Candidates are the first CandidateLimit candidates the filter keeps, in the source's order.
	Candidates []Candidate
	// Remainder is how many more the filter keeps past them.
	Remainder int
}

// collector keeps the first CandidateLimit candidates a filter keeps and counts the rest.
type collector struct {
	query string
	list  CandidateList
}

func newCollector(q string) *collector {
	return &collector{query: strings.ToLower(strings.TrimSpace(q)), list: CandidateList{Candidates: []Candidate{}}}
}

// offer keeps or counts one candidate the filter keeps.
func (c *collector) offer(candidate Candidate) {
	if !c.matches(candidate) {
		return
	}
	if len(c.list.Candidates) < CandidateLimit {
		c.list.Candidates = append(c.list.Candidates, candidate)
		return
	}
	c.list.Remainder++
}

// matches is the q filter: a case-insensitive substring of the label or of any key's value.
func (c *collector) matches(candidate Candidate) bool {
	if c.query == "" {
		return true
	}
	if strings.Contains(strings.ToLower(candidate.Label), c.query) {
		return true
	}
	for _, value := range candidate.Keys {
		var text string
		switch typed := value.(type) {
		case string:
			text = typed
		case int64:
			text = strconv.FormatInt(typed, 10)
		default:
			continue
		}
		if strings.Contains(strings.ToLower(text), c.query) {
			return true
		}
	}
	return false
}

// nullable is a nullable text as a key value: the text, or nil.
func nullable(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}

// firstOf is the first non-empty of a list of nullable texts.
func firstOf(values ...sql.NullString) string {
	for _, value := range values {
		if value.Valid && value.String != "" {
			return value.String
		}
	}
	return ""
}

// Candidates lists what a reference of one kind can name, filtered by q. A site under the
// aggregate mode no_domains is never listed: the caller answers the kind withheld.
func Candidates(ctx context.Context, database *store.Store, kind, q string) (CandidateList, error) {
	if _, known := ReferenceByKind[kind]; !known {
		return CandidateList{}, ErrUnknownReferenceKind
	}
	found := newCollector(q)
	rows := func(name string, parameters map[string]any, scan func(reads.RowScanner) error) error {
		return database.WidgetRows(ctx, name, vocab.Period{}, parameters, scan)
	}
	var err error
	switch kind {
	case "interface":
		err = rows("widget_candidate_interfaces", nil, func(row reads.RowScanner) error {
			var id int64
			var identifier, device, label, description sql.NullString
			if err := row.Scan(&id, &identifier, &device, &label, &description); err != nil {
				return err
			}
			found.offer(Candidate{ID: &id, Referenceable: true, Label: firstOf(label, description, identifier),
				Keys: map[string]any{"identifier": nullable(identifier), "device": nullable(device),
					"user_label": nullable(label)}})
			return nil
		})
	case "rule":
		err = rows("widget_candidate_rules", nil, func(row reads.RowScanner) error {
			var id int64
			var label, description sql.NullString
			if err := row.Scan(&id, &label, &description); err != nil {
				return err
			}
			found.offer(Candidate{ID: &id, Referenceable: true, Label: firstOf(description, label),
				Keys: map[string]any{"pf_label": nullable(label), "description": nullable(description)}})
			return nil
		})
	case "owner":
		err = rows("widget_candidate_owners", nil, func(row reads.RowScanner) error {
			var id int64
			var name sql.NullString
			if err := row.Scan(&id, &name); err != nil {
				return err
			}
			found.offer(Candidate{ID: &id, Referenceable: true, Label: firstOf(name),
				Keys: map[string]any{"display_name": nullable(name)}})
			return nil
		})
	case "provider":
		err = rows("widget_candidate_providers", nil, func(row reads.RowScanner) error {
			var id int64
			var providerKind, key, name sql.NullString
			if err := row.Scan(&id, &providerKind, &key, &name); err != nil {
				return err
			}
			found.offer(Candidate{ID: &id, Referenceable: true, Label: firstOf(name, key),
				Keys: map[string]any{"kind_and_key": providerKind.String + "/" + key.String}})
			return nil
		})
	case "client":
		err = clientCandidates(rows, found)
	case "country":
		err = pagedCandidates(rows, "widget_candidate_countries_page", "", func(row reads.RowScanner) (any, error) {
			var code, name sql.NullString
			if err := row.Scan(&code, &name); err != nil {
				return nil, err
			}
			found.offer(Candidate{Referenceable: true, Label: firstOf(name, code),
				Keys: map[string]any{"iso_3166_1_alpha_2": code.String}})
			return code.String, nil
		})
	case "operator":
		err = pagedCandidates(rows, "widget_candidate_operators_page", int64(-1), func(row reads.RowScanner) (any, error) {
			var asn int64
			var operator sql.NullString
			if err := row.Scan(&asn, &operator); err != nil {
				return nil, err
			}
			found.offer(Candidate{Referenceable: true, Label: firstOf(operator),
				Keys: map[string]any{"asn": asn}})
			return asn, nil
		})
	case "site":
		err = pagedCandidates(rows, "widget_candidate_sites_page", "", func(row reads.RowScanner) (any, error) {
			var name string
			if err := row.Scan(&name); err != nil {
				return nil, err
			}
			found.offer(Candidate{Referenceable: true, Label: name, Keys: map[string]any{"domain": name}})
			return name, nil
		})
	}
	if err != nil {
		return CandidateList{}, err
	}
	return found.list, nil
}

// rowsFunc runs one widget statement over its rows.
type rowsFunc func(name string, parameters map[string]any, scan func(reads.RowScanner) error) error

// clientCandidates reads the clients a page at a time, keyset on their id.
func clientCandidates(rows rowsFunc, found *collector) error {
	after := int64(0)
	for {
		read := 0
		err := rows("widget_candidate_clients_page", map[string]any{"after": after, "page": int64(candidatePage)},
			func(row reads.RowScanner) error {
				var id, unstable int64
				var identityKind, identityKey, hostname, address sql.NullString
				if err := row.Scan(&id, &identityKind, &identityKey, &hostname, &address, &unstable); err != nil {
					return err
				}
				read++
				after = id
				candidate := Candidate{ID: &id, Referenceable: true, UnstableIdentity: unstable == 1,
					Label: firstOf(hostname, address, identityKey),
					Keys:  map[string]any{"mac": nil, "dhcp_client_id": nil, "hostname": nullable(hostname)}}
				switch identityKind.String {
				case "mac", "dhcp_client_id":
					candidate.Keys[identityKind.String] = identityKey.String
				default:
					// The address within the interface is no key a reference names, and the format
					// lists such a client as not referenceable.
					candidate.Referenceable = false
					candidate.Reason = NotReferenceableAddressInInterface
				}
				found.offer(candidate)
				return nil
			})
		if err != nil || read < candidatePage {
			return err
		}
	}
}

// pagedCandidates walks the distinct values of a growing source a page at a time: each page
// starts after the last value the previous one returned. read offers one row and returns its key.
func pagedCandidates(rows rowsFunc, name string, first any, read func(reads.RowScanner) (any, error)) error {
	after := first
	for {
		count := 0
		err := rows(name, map[string]any{"after": after, "page": int64(candidatePage)}, func(row reads.RowScanner) error {
			key, err := read(row)
			if err != nil {
				return err
			}
			count++
			after = key
			return nil
		})
		if err != nil || count < candidatePage {
			return err
		}
	}
}
