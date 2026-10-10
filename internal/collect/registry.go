package collect

// How an implementation announces itself.
//
// THIS FILE IS THE SEAM'S TRANSPORT AND provider.go IS ITS CONTRACT, and the two are separate
// files because they change for different reasons. What a connector must SAY is next door and is
// meant to outlive this file; how it says it is here, and today it says it in-process, with an
// init() in its own file putting one value in one map. When the plugin engine becomes separate
// processes speaking a protocol, this is the file that becomes a handshake — and the contract
// beside it should not have to move for that.
//
// The registries are keyed by provider_key, which is the schema's own column, so a registered
// implementation with no registry row and a registry row with no implementation are both
// detectable and both tested.

// The registries. An implementation registers itself from its own file, so adding one
// touches that file and the schema's registry and nothing else.
var (
	firewallLogSources   = map[string]firewallLogSource{}
	securityEventSources = map[string]securityEventSource{}
	leaseSources         = map[string]leaseSource{}
	lookupSources        = map[string]lookupSource{}
	measurementSources   = map[string]measurementSource{}
	cacheSources         = map[string]resolverCacheSource{}
	localDataSources     = map[string]resolverLocalDataSource{}
)

// registerCacheSource adds one implementation of the resolver_cache kind.
func registerCacheSource(source resolverCacheSource) {
	cacheSources[source.providerKey()] = source
}

// registerLocalDataSource adds one implementation of the resolver_local_data kind.
func registerLocalDataSource(source resolverLocalDataSource) {
	localDataSources[source.providerKey()] = source
}

// registerFirewallLogSource adds one implementation of the firewall_log kind.
func registerFirewallLogSource(source firewallLogSource) {
	firewallLogSources[source.providerKey()] = source
}

// registerSecurityEventSource adds one implementation of the security_event kind.
func registerSecurityEventSource(source securityEventSource) {
	securityEventSources[source.providerKey()] = source
}

// registerLeaseSource adds one implementation of the dhcp_lease kind.
func registerLeaseSource(source leaseSource) {
	leaseSources[source.providerKey()] = source
}

// registerLookupSource adds one implementation of the dns_lookup kind.
func registerLookupSource(source lookupSource) {
	lookupSources[source.providerKey()] = source
}

// registerMeasurementSource adds one implementation of the measurement_sample kind.
func registerMeasurementSource(source measurementSource) {
	measurementSources[source.providerKey()] = source
}
