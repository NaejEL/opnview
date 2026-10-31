package flows

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// The interface networks that count, decision D1: every network the operator set, and every detected
// one the operator did not remove that discovery still reports. The membership read that placed an
// address with them is the clients plugin's function interface_of since ADR 0021
// (plugins/clients/clients.relations.sql); what stays here is what the network writer (network.go)
// reads to say which stored networks count.

// PrefixEvidence is one interface network that counts.
type PrefixEvidence struct {
	interfaceID int64
	prefix      netip.Prefix
	operator    bool
}

// LoadPrefixes reads the interface networks that count, decision D1.
func LoadPrefixes(ctx context.Context, q sqldb.Querier) ([]PrefixEvidence, error) {
	text, err := sqldb.Statement("membership_networks")
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("store: membership_networks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var evidence []PrefixEvidence
	for rows.Next() {
		var (
			interfaceID int64
			address     string
			bits        int64
			origin      string
		)
		if err := rows.Scan(&interfaceID, &address, &bits, &origin); err != nil {
			return nil, fmt.Errorf("store: membership_networks: %w", err)
		}
		parsed, err := netip.ParseAddr(address)
		if err != nil {
			continue
		}
		prefix, err := parsed.Unmap().Prefix(int(bits))
		if err != nil {
			continue
		}
		evidence = append(evidence, PrefixEvidence{interfaceID: interfaceID, prefix: prefix,
			operator: origin == NetworkOriginOperator})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: membership_networks: %w", err)
	}
	return evidence, nil
}
