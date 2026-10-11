package widget

import (
	"github.com/dop251/goja"
)

// host.installation: what a response names its members by -- this installation's interfaces,
// clients, owners and rules, read once per request and only when asked. Every label is the user's
// own where one is set and the firewall's discovered description otherwise; nothing is classified
// by what it is called.
//
//	host.installation.interfaces()   every interface: [{id, identifier, device, description, user_label,
//	                                 label, labelled, link_kind, vlan_tag, upstream}]
//	host.installation.interface(id)  one interface, or null
//	host.installation.client(id)     one client: {id, identity_kind, identity_key, interface_id, mac,
//	                                 hostname, last_address, owner_id, owner_assigned_at,
//	                                 unstable_identity, last_seen_at}, or null
//	host.installation.owners()       every owner: [{id, display_name}]
//	host.installation.rule(id)       one rule: {id, pf_label, description, action, automatic}, or null

func init() {
	registerHostArea("installation", func(call *scriptCall, area *goja.Object) {
		r := call.request
		call.function(area, "interfaces", func(goja.FunctionCall) goja.Value {
			cache, err := r.Install()
			if err != nil {
				return call.fail(err)
			}
			interfaces := cache.Interfaces
			if interfaces == nil {
				interfaces = []InterfaceInfo{}
			}
			return call.value(interfaces)
		})
		call.function(area, "interface", func(arguments goja.FunctionCall) goja.Value {
			id := call.integerOf(arguments.Argument(0), "the interface id")
			cache, err := r.Install()
			if err != nil {
				return call.fail(err)
			}
			info, found := cache.ByID[id]
			if !found {
				return goja.Null()
			}
			return call.value(info)
		})
		call.function(area, "client", func(arguments goja.FunctionCall) goja.Value {
			info, err := r.Client(call.integerOf(arguments.Argument(0), "the client id"))
			if err != nil {
				return call.fail(err)
			}
			if info == nil {
				return goja.Null()
			}
			return call.value(info)
		})
		call.function(area, "owners", func(goja.FunctionCall) goja.Value {
			owners, err := r.Owners()
			if err != nil {
				return call.fail(err)
			}
			if owners == nil {
				owners = []OwnerInfo{}
			}
			return call.value(owners)
		})
		call.function(area, "rule", func(arguments goja.FunctionCall) goja.Value {
			info, err := r.Rule(call.integerOf(arguments.Argument(0), "the rule id"))
			if err != nil {
				return call.fail(err)
			}
			if info == nil {
				return goja.Null()
			}
			return call.value(info)
		})
	})
}
