package store

// Which provider kinds admit several concurrently active providers.
//
// THE RULE, WHICH IS WRITTEN OUT IN FULL BESIDE THE INDEX THAT ENFORCES IT IN
// schema.sql:
//
//	A KIND ADMITS SEVERAL CONCURRENTLY ACTIVE PROVIDERS EXACTLY WHEN THE
//	IDENTITY OF ITS DESTINATION ROWS INCLUDES THE PROVIDER.
//
// Where the identity includes the provider, two active providers can neither
// collide nor double-count, because every row says who reported it. Where it does
// not, the second provider's rows are indistinguishable from the first's by
// origin, so it would either collide or silently double a figure nobody could
// decompose. That is why running several detection engines together is normal —
// security_event rows are keyed by (provider_id, provider_event_key) — and why
// two DHCP servers on two VLANs is normal too: a lease is keyed by
// (address, generation_key, provider_id), and the client identity cascade keys on
// the DHCP client identifier and then the MAC, neither of them scoped to an
// interface, so one machine leased by both servers is ONE client holding TWO
// leases. What the rule still refuses is a kind whose rows cannot say who
// reported them.
//
// THIS SET MIRRORS THE SCHEMA AND IS NOT A SECOND TRUTH. The database is the
// authority: uq_provider_active_per_exclusive_kind rejects a second active
// provider of an exclusive kind whatever this file says. The set is here so the
// probe round can activate every qualifying provider of a concurrent kind instead
// of discovering the constraint by failing, and a test reads the index's own
// definition out of sqlite_master and asserts the two agree.
var concurrentKinds = map[string]struct{}{
	"security_event":     {},
	"dhcp_lease":         {},
	"measurement_sample": {},
	"reconciled_state":   {},
	// resource_record_observation is keyed by (provider_id, owner_name, rrtype,
	// value, first_seen_at): every record says which provider's poll saw it.
	"resolver_cache":      {},
	"resolver_local_data": {},
}

// KindAdmitsSeveralActiveProviders reports whether more than one provider of a
// kind may be active at once.
func KindAdmitsSeveralActiveProviders(kind string) bool {
	_, concurrent := concurrentKinds[kind]
	return concurrent
}

// ConcurrentKinds returns the kinds that admit several active providers, for a
// test and for a diagnostic. The order is not meaningful.
func ConcurrentKinds() []string {
	kinds := make([]string, 0, len(concurrentKinds))
	for kind := range concurrentKinds {
		kinds = append(kinds, kind)
	}
	return kinds
}
