package provider

import (
	"errors"

	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// ErrUnsupportedRead is returned by an implementation that is registered and probed but
// whose material opnview cannot read.
//
// It is a first-class outcome rather than a missing code path, and the cases it covers are
// all measured. The end-of-life ISC plugin's lease endpoint answered 404 on the surveyed
// firmware and its contract is marked UNVERIFIED in the survey. The Dnsmasq resolver offers no
// structured query API at all: its per-client lookups exist only in the free-text line of the
// generic log endpoint, under a grammar that is dnsmasq's own rather than an OPNsense contract
// and that nobody has recorded. Writing either reader would mean guessing at a contract, so
// both are registered, probed and reported -- and the reason travels with the refusal.
var ErrUnsupportedRead = errors.New("collect: this implementation is registered and its material cannot be read")

// AuthenticationFailed is the wording of an HTTP 401, whichever endpoint answered it.
// ApiControllerBase's beforeExecuteRoute (opnsense/core 26.7.3,
// mvc/app/controllers/OPNsense/Base/ApiControllerBase.php) answers 401 "Authentication
// Failed" only when the key and secret do not authenticate, and 403 "Forbidden" when they
// do but the ACL refuses the page. A 401 therefore names no privilege: granting one would
// not cure it.
const AuthenticationFailed = "authentication failed (HTTP 401): the firewall did not accept the API " +
	"key and secret"

// Result is what one implementation's probe or read concluded.
type Result struct {
	// State is the availability state the survey names for what the firewall answered.
	State vocab.AvailabilityState
	// Outcome names what the probe or read turned into, from the closed set the schema
	// holds (store.AvailabilityOutcomes). It is written beside the detail, so no reader
	// ever has to parse the detail to tell one absence from another.
	Outcome vocab.AvailabilityOutcome
	// Probe is the endpoint that determined it, recorded so a screen can say how the
	// conclusion was reached.
	Probe opnsense.Endpoint
	// Detail is the human-readable account, which is where the difference between
	// "switched off" and "unreachable" actually becomes legible.
	Detail string
	// Separable says whether the FIREWALL'S OWN CONFIGURATION marks this implementation as
	// the one serving clients. It is the only thing activation is allowed to read: when
	// two implementations of a kind are both separable the firewall has not chosen between
	// them, and opnview then reads neither rather than choosing for the user.
	Separable bool
}

// Discovery is the snapshot of the firewall's own configuration the collectors
// join against: the two first-class join keys, and the interface identifiers the
// per-pair sampler needs. The coordinator of a kind hands it to an implementation only where
// the kind's contract says so (the measurement sampler), per call.
type Discovery struct {
	// InterfaceIDByIdentifier maps a configuration key to its stored row.
	InterfaceIDByIdentifier map[string]int64
	// InterfaceIDByDevice maps a network device name to its stored row. A device
	// absent from this map is the modelled `not_found` state, never a dropped row.
	InterfaceIDByDevice map[string]int64
	// Identifiers are the configuration keys, which is what
	// /api/diagnostics/traffic/top takes.
	Identifiers []opnsense.InterfaceName
	// Devices are the network device names, which is what that endpoint must
	// NEVER be given: a device name returns an empty array with HTTP 200, which
	// is indistinguishable from an absence of traffic.
	Devices map[string]struct{}
	// RuleIDByPfLabel maps a filter-log `rid` to its stored rule. A rid absent
	// from this map is normal: the rule was removed.
	RuleIDByPfLabel map[string]int64
	// Upstream holds the interfaces the firewall reports a gateway behind. Evidence
	// read on one of them -- a neighbour, a lease -- does not make an address a member
	// of it: an address reached through an upstream interface is outside.
	Upstream map[int64]struct{}
	// RefreshedAt is when this snapshot was read.
	RefreshedAt int64
}
