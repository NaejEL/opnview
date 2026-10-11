package widget

import (
	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/store"
)

// host.addresses: the addresses this installation's upstream interfaces -- those carrying a
// gateway, never one called something -- have held, as opnview saw them (internal/store, read.go:
// ReadPublicAddresses). The address an interface holds is what the firewall sees, and only that:
// behind an upstream NAT the public address belongs to a device opnview does not read.
//
//	host.addresses.upstreams()     every upstream interface: [{interface_id, history}], history being
//	                               every address and gateway it held, newest first: [{source_field,
//	                               address, prefix_length, address_family, first_seen_at,
//	                               last_seen_at}]; prefix_length is null when none was reported
//	host.addresses.sourceFields    the fields an address is read from, by name: addr4, addr6, ipv4,
//	                               ipv6, gateways

// heldAddress is one address an upstream interface held, as a script reads it.
type heldAddress struct {
	SourceField   string `json:"source_field"`
	Address       string `json:"address"`
	PrefixLength  *int64 `json:"prefix_length"`
	AddressFamily int64  `json:"address_family"`
	FirstSeenAt   int64  `json:"first_seen_at"`
	LastSeenAt    int64  `json:"last_seen_at"`
}

// upstreamAddresses is one upstream interface's history, as a script reads it.
type upstreamAddresses struct {
	InterfaceID int64         `json:"interface_id"`
	History     []heldAddress `json:"history"`
}

func init() {
	registerHostArea("addresses", func(call *scriptCall, area *goja.Object) {
		r := call.request
		call.function(area, "upstreams", func(goja.FunctionCall) goja.Value {
			upstreams, err := r.Db.ReadPublicAddresses(r.Ctx)
			if err != nil {
				return call.fail(err)
			}
			read := make([]upstreamAddresses, 0, len(upstreams))
			for _, upstream := range upstreams {
				entry := upstreamAddresses{InterfaceID: upstream.InterfaceID,
					History: make([]heldAddress, 0, len(upstream.History))}
				for _, held := range upstream.History {
					entry.History = append(entry.History, heldAddress{SourceField: held.SourceField,
						Address: held.Address, PrefixLength: held.PrefixLength, AddressFamily: held.AddressFamily,
						FirstSeenAt: held.FirstSeenAt, LastSeenAt: held.LastSeenAt})
				}
				read = append(read, entry)
			}
			return call.value(read)
		})
		_ = area.Set("sourceFields", call.value(map[string]string{
			"addr4": store.SourceFieldAddr4, "addr6": store.SourceFieldAddr6, "ipv4": store.SourceFieldIPv4,
			"ipv6": store.SourceFieldIPv6, "gateways": store.SourceFieldGateways,
		}))
	})
}
