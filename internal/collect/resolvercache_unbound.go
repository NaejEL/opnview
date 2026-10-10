package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// Unbound — one implementation of the resolver_cache kind, and the only one that can be
// read.
//
// Everything in this file is a fact about Unbound and about OPNsense's Unbound diagnostics
// controller, read at opnsense/core 26.7.3 (survey, "Resolver cache and local data, read
// from source for the resolver-cache cycle"):
//
//   - Unbound/Api/DiagnosticsController.php, dumpcacheAction, runs the configd action
//     `unbound dumpcache` (service/conf/actions.d/actions_unbound.conf), which is
//     scripts/unbound/wrapper.py -c over `unbound-control dump_cache`;
//   - the script keeps every line holding the class IN and not starting `msg`, and writes
//     {host, ttl, type, rrtype, value} for each (wrapper.py, lines 65-68): `ttl` the
//     seconds the record has left, as a string of digits, and `type` the CLASS;
//   - A ROW WHOSE TTL IS NULL IS NOT A RECORD. `unbound-control dump_cache` prints the
//     rrset cache, then the message cache: each `msg` line is followed by one line per
//     rrset the cached answer refers to, which dump_msg_ref (Unbound, daemon/cachedump.c)
//     prints as `name class type flags` -- no TTL and no record data. The `msg` line is
//     dropped, but the reference line holds IN, and the regular expression matches it
//     with its optional TTL group taking no part: re.split yields None for the group,
//     json.dumps writes it as null, and `value` is the flags, typically "0". So a null
//     ttl marks a message-cache reference, whose rrset the dump's rrset section already
//     holds; the kind counts it under its own wording and stores nothing from it
//     (resourcerecord.go);
//   - the controller answers {"status": "ok", "data": [...]}, or {"status": "failed"} with
//     no data when the script printed nothing, which is what it does when Unbound is not
//     running;
//   - the path is admitted by the ACL page "Services: Unbound" (page-services-unbound,
//     pattern api/unbound/*, models/OPNsense/Unbound/ACL/ACL.xml).
//
// What the kind does with the records is in resourcerecord.go and resolvercache.go.

func init() { registerCacheSource(unboundCache{}) }

// unboundCache reads /api/unbound/diagnostics/dumpcache.
type unboundCache struct{}

// providerKey is the registry row this implementation answers for.
func (unboundCache) providerKey() string { return ProviderUnbound }

// probe reads the service state and the configured state. The cache exists whenever the
// resolver runs; it needs neither query reporting nor any other setting, so the two are
// what makes it separable.
func (unboundCache) probe(ctx context.Context, host session) (probeResult, error) {
	return unboundResolverProbe(ctx, host)
}

// cache reads the dump.
func (unboundCache) cache(ctx context.Context, host session) (recordDump, probeResult, error) {
	return readUnboundRecords(ctx, host, opnsense.UnboundDumpCache, "host")
}

// unboundResolverProbe is the probe both of Unbound's record reads share: the service runs
// and unbound.general.enabled is set.
func unboundResolverProbe(ctx context.Context, host session) (probeResult, error) {
	result := probeResult{probe: opnsense.UnboundStatus}
	running, state, detail, err := host.serviceState(ctx, opnsense.UnboundStatus)
	if err != nil {
		return result, err
	}
	result.state, result.detail = state, detail
	if !running {
		return result, nil
	}
	settings, ok, err := host.readObject(ctx, opnsense.UnboundSettings)
	if err != nil {
		return result, err
	}
	if !ok || !decode.FlagOrFalse(decode.NestedFlag(settings, "unbound", "general", "enabled")) {
		result.state = store.StatePresentButDisabled
		result.detail = "the service runs and unbound.general.enabled is not set"
		return result, nil
	}
	result.separable = true
	return result, nil
}

// unboundPrivilege is the ACL page that admits /api/unbound/diagnostics/*, as ACL.xml
// names it.
const unboundPrivilege = `"Services: Unbound" (page-services-unbound)`

// readUnboundRecords reads one of the diagnostics controller's record lists: the cache
// dump or the local data. ownerField is the field the list names the owner under --
// `host` in the dump, `name` in the local data.
//
// Each absence is named on its own, and none stores anything: a failed authentication
// (401), which names no privilege; a refusal (403), naming the privilege; a 404; an answer that is not the {status, data} envelope; and a status that
// is not "ok".
func readUnboundRecords(ctx context.Context, host session, endpoint opnsense.Endpoint,
	ownerField string) (recordDump, probeResult, error) {
	result := probeResult{probe: endpoint, state: store.StateUnavailable}
	response, err := host.call(ctx, endpoint, opnsense.RequestOptions{})
	if err != nil && response.Outcome == "" {
		return recordDump{}, result, err
	}
	switch response.Outcome {
	case opnsense.OutcomeOK:
	case opnsense.OutcomeForbidden:
		if response.StatusCode == http.StatusUnauthorized {
			result.detail = fmt.Sprintf("%s for %s; nothing was stored", authenticationFailed, endpoint.Path)
			return recordDump{}, result, fmt.Errorf("collect: %s: authentication failed", endpoint.Path)
		}
		result.detail = fmt.Sprintf("denied (HTTP %d): the API key's user lacks the privilege %s, whose "+
			"pattern api/unbound/* admits %s; nothing was stored", response.StatusCode, unboundPrivilege,
			endpoint.Path)
		return recordDump{}, result, fmt.Errorf("collect: %s was refused", endpoint.Path)
	case opnsense.OutcomeNotFound:
		result.detail = fmt.Sprintf("not found (HTTP %d): this firewall does not answer %s; nothing "+
			"was stored", http.StatusNotFound, endpoint.Path)
		return recordDump{}, result, fmt.Errorf("collect: %s was not found", endpoint.Path)
	default:
		result.detail = fmt.Sprintf("%s answered %s: %s; nothing was stored", endpoint.Path,
			response.Outcome, response.Detail)
		return recordDump{}, result, fmt.Errorf("collect: %s answered %s", endpoint.Path, response.Outcome)
	}

	var envelope struct {
		Status *string           `json:"status"`
		Data   *[]map[string]any `json:"data"`
	}
	if err := json.Unmarshal(response.Body, &envelope); err != nil || envelope.Status == nil {
		result.detail = fmt.Sprintf("undecodable: the answer of %s is not the {status, data} envelope "+
			"the controller returns; nothing was stored", endpoint.Path)
		return recordDump{}, result, fmt.Errorf("collect: reading %s: the envelope is not {status, data}",
			endpoint.Path)
	}
	if *envelope.Status != "ok" {
		result.state = store.StateUnavailable
		result.detail = fmt.Sprintf("the resolver answered status %q: the controller answers \"failed\" "+
			"when the script printed nothing, which it does when Unbound is not running; nothing was "+
			"stored", *envelope.Status)
		return recordDump{}, result, fmt.Errorf("collect: %s answered status %q", endpoint.Path,
			*envelope.Status)
	}
	if envelope.Data == nil {
		result.detail = fmt.Sprintf("undecodable: %s answered status \"ok\" with no data list; nothing "+
			"was stored", endpoint.Path)
		return recordDump{}, result, fmt.Errorf("collect: %s answered no data list", endpoint.Path)
	}

	dump := recordDump{ResponseBytes: len(response.Body), Records: make([]rawRecord, 0, len(*envelope.Data))}
	for _, row := range *envelope.Data {
		// `type` is the record's class and is never read: the type is `rrtype`. The ttl is
		// read with its shape, because a null one is a message-cache reference.
		ttl, ttlShape := decode.TextField(row, "ttl")
		dump.Records = append(dump.Records, rawRecord{
			OwnerName: decode.RawString(row, ownerField),
			TTL:       ttl,
			TTLShape:  ttlShape,
			RRType:    decode.RawString(row, "rrtype"),
			Value:     decode.RawString(row, "value"),
		})
	}
	result.state = store.StateReachable
	result.separable = true
	return dump, result, nil
}
