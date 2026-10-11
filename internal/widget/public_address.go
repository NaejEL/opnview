package widget

import (
	"net/netip"

	"github.com/NaejEL/opnview/internal/specialpurpose"
	"github.com/NaejEL/opnview/internal/store"
)

// public_address: per upstream interface -- an interface carrying a gateway, never one
// called something -- the addresses it holds, their history as opnview saw it, and
// whether each lies in a special-purpose range of the IANA registries
// (docs/widget-catalogue.md, "Public address"). Behind an upstream NAT the firewall does
// not see its public IPv4 address at all, and nothing opnview reads can supply it: the
// response says the installation is behind one, by arithmetic on the address, and never
// presents a special-purpose address as public. The change instant is absent until a
// second address has been seen. It describes the firewall, not traffic, so its figures
// carry no observation-point bound.

var publicAddress = Preset{
	Type: "public_address", Shape: "table",
	Params: []Param{
		{Key: "families", Kind: KindEnumSet, Values: []string{"ipv4", "ipv6"}, Default: []any{"ipv4", "ipv6"},
			Data: true},
		{Key: "gateways", Kind: KindStringList, Data: true},
		{Key: "show_history", Kind: KindBoolean, Default: true, Data: true},
		{Key: "history_limit", Kind: KindInteger, Min: 1, Default: float64(5), Data: true},
	},
	Serve: servePublicAddress,
}

// SpecialPurpose is the registry block an address lies in.
type SpecialPurpose struct {
	Block             string `json:"block"`
	Name              string `json:"name"`
	Reference         string `json:"reference"`
	GloballyReachable *bool  `json:"globally_reachable"`
}

// HeldAddress is one address an interface held.
type HeldAddress struct {
	Address      string `json:"address"`
	Family       string `json:"family"`
	PrefixLength *int64 `json:"prefix_length"`
	SourceField  string `json:"source_field"`
	// SpecialPurpose is null for an address in no registry block.
	SpecialPurpose *SpecialPurpose `json:"special_purpose"`
	// Public is false for every special-purpose address the registry does not mark
	// globally reachable.
	Public      bool  `json:"public"`
	FirstSeenAt int64 `json:"first_seen_at"`
	LastSeenAt  int64 `json:"last_seen_at"`
	// ChangedAt is when opnview first saw the current address, set only when an earlier
	// address of the same field was seen; ChangeState says which.
	ChangedAt   *int64 `json:"changed_at,omitempty"`
	ChangeState string `json:"change_state,omitempty"`
}

// UpstreamInterface is one interface carrying a gateway.
type UpstreamInterface struct {
	Interface InterfaceInfo `json:"interface"`
	Current   []HeldAddress `json:"current"`
	History   []HeldAddress `json:"history,omitempty"`
	// HistoryRemainder is the previous addresses past history_limit.
	HistoryRemainder *Remainder    `json:"history_remainder,omitempty"`
	Gateways         []HeldAddress `json:"gateways"`
	// BehindUpstreamNAT is true when the current IPv4 address is not public, false when it
	// is, null when the interface holds no IPv4 address.
	BehindUpstreamNAT *bool `json:"behind_upstream_nat"`
}

// PublicAddressData is the table.
type PublicAddressData struct {
	Upstreams []UpstreamInterface `json:"upstreams"`
	// Registries cites what special_purpose is read from.
	Registries []string `json:"registries"`
}

// The two registries, cited.
var specialPurposeRegistries = []string{"iana-ipv4-special-registry", "iana-ipv6-special-registry"}

func servePublicAddress(r *Request) (any, bool, error) {
	cache, err := r.install()
	if err != nil {
		return nil, false, err
	}
	addresses, err := r.db.ReadPublicAddresses(r.ctx)
	if err != nil {
		return nil, false, err
	}
	gateways := r.params.set("gateways")
	data := PublicAddressData{Upstreams: []UpstreamInterface{}, Registries: specialPurposeRegistries}
	for _, upstream := range addresses {
		info := cache.byID[upstream.InterfaceID]
		entry := UpstreamInterface{Interface: info, Current: []HeldAddress{}, Gateways: []HeldAddress{}}
		latest := map[string]int64{}
		seenPerField := map[string]int{}
		for _, held := range upstream.History {
			latest[held.SourceField] = max(latest[held.SourceField], held.LastSeenAt)
			seenPerField[held.SourceField]++
		}
		var previous []HeldAddress
		for _, held := range upstream.History {
			address := describe(held)
			if !r.params.has("families", address.Family) {
				continue
			}
			if held.SourceField == store.SourceFieldGateways {
				if len(gateways) > 0 && !contains(gateways, held.Address) {
					continue
				}
				entry.Gateways = append(entry.Gateways, address)
				continue
			}
			if held.LastSeenAt < latest[held.SourceField] {
				previous = append(previous, address)
				continue
			}
			if seenPerField[held.SourceField] > 1 {
				changed := held.FirstSeenAt
				address.ChangedAt, address.ChangeState = &changed, "changed"
			} else {
				address.ChangeState = "not_recorded_only_one_address_seen"
			}
			entry.Current = append(entry.Current, address)
			if address.Family == "ipv4" && (held.SourceField == store.SourceFieldAddr4) {
				behind := !address.Public
				entry.BehindUpstreamNAT = &behind
			}
		}
		if len(gateways) > 0 && len(entry.Gateways) == 0 {
			continue
		}
		if r.params.flag("show_history") {
			limit := int(r.params.integer("history_limit"))
			entry.History = []HeldAddress{}
			rest := Remainder{}
			for index, address := range previous {
				if index < limit {
					entry.History = append(entry.History, address)
					continue
				}
				rest.Members++
			}
			entry.HistoryRemainder = &rest
		}
		data.Upstreams = append(data.Upstreams, entry)
	}
	return data, len(data.Upstreams) > 0, nil
}

// describe classifies one held address against the registries.
func describe(held store.AddressHistory) HeldAddress {
	address := HeldAddress{Address: held.Address, Family: "ipv4", PrefixLength: held.PrefixLength,
		SourceField: held.SourceField, FirstSeenAt: held.FirstSeenAt, LastSeenAt: held.LastSeenAt}
	if held.AddressFamily == 6 {
		address.Family = "ipv6"
	}
	parsed, err := netip.ParseAddr(held.Address)
	if err != nil {
		return address
	}
	if block, special := specialpurpose.Lookup(parsed); special {
		address.SpecialPurpose = &SpecialPurpose{Block: block.Block.String(), Name: block.Name,
			Reference: block.Reference, GloballyReachable: block.GloballyReachable}
	}
	address.Public = specialpurpose.Public(parsed)
	return address
}
