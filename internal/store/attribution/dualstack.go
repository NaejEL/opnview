package attribution

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/netip"

	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The dual-stack rule: group C of specs/SPEC-step-5b-widget-api.md.
//
// A host override with an A and an AAAA record names one machine, but its two addresses
// made every lookup logged under its name ambiguous, because the logged host name alone
// does not say which of the two asked. The resolver's query report does say which FAMILY
// asked: each row carries `family`, `ip4` or `ip6`, Unbound's family of the reply's
// address (opnsense/core 26.7.3, scripts/unbound-dnsbl/lib/__init__.py, Query, from
// comm_reply.family; Unbound's pythonmod/interface.i maps it from ss_family), and it is
// the querying address's family even where scripts/unbound/stats.py replaced that address
// by its reverse-resolved host name. The finding is in docs/opnsense-api-survey.md, "The
// querying family behind a logged host name, read from source for step 5B".
//
// So, when no lease names the host:
//   - one address in the local data: that address, local_data_hostname, as before;
//   - exactly one IPv4 and one IPv6 address under one name, and the lookup's family
//     reported: the address of that family, local_data_hostname;
//   - the same with no family reported: dual_stack_hostname, which names no machine;
//   - anything else with two or more addresses -- two of one family, three addresses,
//     two addresses under two different names: ambiguous_hostname.
//
// Nothing is guessed: without the family the lookup stays unattributed, and the family
// is the record's own field, stored on the lookup (dns_resolution.client_family) so the
// retry decides it again from the same evidence.

// localDataCandidate is one address the local data held for a host label at an instant,
// with the names it was held under.
type localDataCandidate struct {
	address string
	family  int64
	names   []string
}

// localDataCandidates reads, through local_data_addresses_for_hostname, at most three of
// the addresses the local data held for a host name's first label at an instant.
func (s *AttributionStore) localDataCandidates(ctx context.Context, hostname string, at int64) (
	[]localDataCandidate, error) {
	const name = "local_data_addresses_for_hostname"
	text, err := sqldb.Statement(name)
	if err != nil {
		return nil, err
	}
	rows, err := s.SQL.QueryContext(ctx, text, sql.Named("label", HostnameLabel(hostname)), sql.Named("at", at))
	if err != nil {
		return nil, fmt.Errorf("store: %s: %w", name, err)
	}
	defer func() { _ = rows.Close() }()
	var candidates []localDataCandidate
	for rows.Next() {
		var (
			candidate localDataCandidate
			names     string
		)
		if err := rows.Scan(&candidate.address, &names); err != nil {
			return nil, fmt.Errorf("store: %s: %w", name, err)
		}
		if err := json.Unmarshal([]byte(names), &candidate.names); err != nil {
			return nil, fmt.Errorf("store: %s: reading the names of %s: %w", name, candidate.address, err)
		}
		parsed, err := netip.ParseAddr(candidate.address)
		if err != nil {
			return nil, fmt.Errorf("store: %s: %q is not an address: %w", name, candidate.address, err)
		}
		candidate.family = 6
		if parsed.Is4() {
			candidate.family = 4
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %s: %w", name, err)
	}
	return candidates, nil
}

// dualStackPair reports whether two candidates are one IPv4 and one IPv6 address held
// under one name: the shape the dual-stack rule applies to.
func dualStackPair(candidates []localDataCandidate) bool {
	if len(candidates) != 2 || candidates[0].family == candidates[1].family {
		return false
	}
	for _, first := range candidates[0].names {
		for _, second := range candidates[1].names {
			if first == second {
				return true
			}
		}
	}
	return false
}

// resolveLocalData applies the local-data half of ResolveLoggedHostname: the address and
// its resolution, or the logged name verbatim with the resolution that names no machine.
func (s *AttributionStore) resolveLocalData(ctx context.Context, hostname string, family *int64, at int64) (
	string, string, error) {
	candidates, err := s.localDataCandidates(ctx, hostname, at)
	if err != nil {
		return "", "", err
	}
	switch {
	case len(candidates) == 0:
		return hostname, vocab.ClientResolutionUnknown, nil
	case len(candidates) == 1:
		return candidates[0].address, vocab.ClientResolutionLocalData, nil
	case dualStackPair(candidates):
		if family == nil {
			return hostname, vocab.ClientResolutionDualStack, nil
		}
		for _, candidate := range candidates {
			if candidate.family == *family {
				return candidate.address, vocab.ClientResolutionLocalData, nil
			}
		}
		// A family that is neither 4 nor 6 is refused by the column's CHECK, so this is
		// unreachable from stored data; it is still no evidence.
		return hostname, vocab.ClientResolutionDualStack, nil
	}
	return hostname, vocab.ClientResolutionAmbiguous, nil
}
