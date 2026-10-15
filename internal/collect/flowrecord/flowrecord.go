package flowrecord

import (
	"context"

	"github.com/NaejEL/opnview/internal/collect/provider"
)

// Kind is the registry kind this package answers for.
const Kind = "flow_record"

// Record is one flow record as its exporter sent it, its fields the contract's.
type Record struct {
	// Identity is the core's: what makes the record one row, composed from the delivery it came in
	// (internal/plugin, pushkind.go).
	Identity          string
	ExporterAddress   string
	ObservationDomain int64
	// IngressInterface and EgressInterface are the exporter's raw interface indexes; nil when the
	// record carries none.
	IngressInterface *int64
	EgressInterface  *int64
	IPVersion        int64
	// Protocol is the IANA protocol number.
	Protocol   int64
	SrcAddress string
	DstAddress string
	// SrcPort and DstPort are nil for a record carrying no port.
	SrcPort *int64
	DstPort *int64
	// TCPFlags are the flags of every packet ORed together; nil when the record carries none.
	TCPFlags *int64
	Octets   int64
	Packets  int64
	// StartMillis and EndMillis are the first and last packets' instants, in UTC epoch milliseconds:
	// the precision the decoder carries, of which the exporter may fill only whole seconds.
	StartMillis int64
	EndMillis   int64
	// ReceivedAt is when opnview received it, in UTC epoch seconds.
	ReceivedAt int64
}

// Interface is one interface of a capture configuration.
type Interface struct {
	// Identifier is the interface's configuration identifier, as discovery names it.
	Identifier string
	// Ingress and Egress say which of its directions are accounted.
	Ingress bool
	Egress  bool
}

// Configuration is an exporter's capture configuration, as one read found it.
type Configuration struct {
	// Enabled says the export runs.
	Enabled bool
	// ExportVersion is the export protocol's version as the exporter names it; empty when unread.
	ExportVersion string
	// ActiveTimeoutSeconds and InactiveTimeoutSeconds are the exporter's flow timeouts; nil when the
	// configuration does not state them.
	ActiveTimeoutSeconds   *int64
	InactiveTimeoutSeconds *int64
	// Interfaces are the interfaces it captures on.
	Interfaces []Interface
}

// Captured returns the interfaces with at least one direction captured: the captured-interface
// count of PS18 is its length.
func (c Configuration) Captured() []Interface {
	var captured []Interface
	for _, captureInterface := range c.Interfaces {
		if captureInterface.Ingress || captureInterface.Egress {
			captured = append(captured, captureInterface)
		}
	}
	return captured
}

// Source is one implementation of the flow_record kind: what it reads by polling. Its records arrive
// through its push entry, never through the Source.
type Source interface {
	// Probe reports what the firewall says about this implementation.
	Probe(ctx context.Context, host provider.Session) (provider.Result, error)
	// Capture reads the exporter's capture configuration. A read that failed returns its error with
	// the result naming what the firewall answered; the coordinator then records the configuration as
	// unreadable, never as empty.
	Capture(ctx context.Context, host provider.Session) (Configuration, provider.Result, error)
}

// Condition is a fact the receiving decoder detected on an exporter's stream: today, that its
// sequence numbers interleave, which shows several counters behind one observation domain. The core
// holds it against the capture configuration in force (PS18's configuration contradicting the
// stream).
type Condition struct {
	// Reason is the decoder's name for it.
	Reason string
	// Scope is the stream: the exporter's address and the observation domain, "<address>/<domain>".
	Scope string
	// At is the instant the stream showed it, in UTC epoch seconds.
	At int64
}
