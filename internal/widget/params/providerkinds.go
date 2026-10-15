package params

// The provider kinds are the vocabulary a manifest binds (`providers`, `primary`) and the
// source_availability widget lists. A provider with no availability row reads state_unknown,
// never healthy; availability and activeness are two separate fields.

// ProviderKinds are the provider kinds of the registry (internal/store/schema.sql,
// provider.kind).
var ProviderKinds = []string{"firewall_log", "security_event", "flow_volume", "dhcp_lease", "dns_lookup",
	"ip_geolocation", "measurement_sample", "reconciled_state", "registrable_domain", "resolver_cache",
	"resolver_local_data"}
