package firewalllog

import (
	"context"

	"github.com/NaejEL/opnview/internal/collect/provider"
)

// Kind is the registry kind this package answers for.
const Kind = "firewall_log"

// Record is one filter-log record, decoded and normalised by its implementation and not yet
// joined to anything.
type Record struct {
	Digest      string
	ObservedAt  int64
	Device      string
	SrcAddress  string
	DstAddress  string
	SrcPort     *int64
	DstPort     *int64
	Protocol    string
	IPVersion   int64
	Action      string
	Direction   string
	LogReason   *string
	PacketBytes int64
	Rid         *string
	// IPID and TCPSeq are the two fields verified to cross the firewall unchanged,
	// which pair the two records of one connection; nil where the record has none.
	IPID   *int64
	TCPSeq *int64
}

// Source is one implementation of the firewall_log kind.
type Source interface {
	// Probe reports what the firewall says about this implementation.
	Probe(ctx context.Context, host provider.Session) (provider.Result, error)
	// Records returns the most recent page, newest first, with the result of the call so
	// the kind can record it.
	Records(ctx context.Context, host provider.Session, pageSize int) ([]Record, provider.Result, error)
}
