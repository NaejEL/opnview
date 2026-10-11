package widget

// source_availability: one row per registered provider -- kind, key, display name, its
// availability and the condition it maps to, the probe, the detail, when it was checked,
// and whether it is the provider opnview reads for its kind (docs/widget-catalogue.md,
// "Source availability"). It has no empty state: the schema seeds an availability row for
// every registry row. A provider with no availability row reads state_unknown, never
// healthy. Availability and activeness are two separate fields.

// providerKinds are the provider kinds of the registry (internal/store/schema.sql,
// provider.kind).
var providerKinds = []string{"firewall_log", "security_event", "flow_volume", "dhcp_lease", "dns_lookup",
	"geo_asn", "measurement_sample", "reconciled_state", "public_suffix", "resolver_cache",
	"resolver_local_data"}

var sourceAvailability = Preset{
	Type: "source_availability", Shape: "table",
	Providers: providerKinds,
	Params: []Param{
		{Key: "kinds", Kind: KindEnumSet, Values: providerKinds, Default: stringsAsAny(providerKinds), Data: true},
		{Key: "show_inactive", Kind: KindBoolean, Default: true, Data: true},
		{Key: "compact", Kind: KindBoolean, Default: false},
	},
	Serve: serveSourceAvailability,
}

// SourceAvailabilityData is the table.
type SourceAvailabilityData struct {
	Providers []ProviderAvailability `json:"providers"`
}

func serveSourceAvailability(r *Request) (any, bool, error) {
	data := SourceAvailabilityData{Providers: []ProviderAvailability{}}
	for _, provider := range r.response.Availability {
		if !r.params.has("kinds", provider.Kind) || (!r.params.flag("show_inactive") && !provider.Active) {
			continue
		}
		data.Providers = append(data.Providers, provider)
	}
	return data, len(data.Providers) > 0, nil
}
