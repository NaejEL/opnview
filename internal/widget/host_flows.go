package widget

import (
	"github.com/dop251/goja"
)

// host.flows: the window's flows by their columns (flows.go) -- whichever source wrote them -- for
// the compositions by protocol, service port and blocking reason, their recent part, and the
// destination levels of a connection tree. A filter is {scope, interfaces, clients}: a traffic
// scope ("all", or absent, for every one) and id lists, null for no restriction.
//
//	host.flows.parts(filter)            the window's flows per direction, protocol, destination port
//	                                    and decision: [{traffic_direction, protocol, port, decision}
//	                                    and their summable figures (host.figures)], each connection
//	                                    once
//	host.flows.recent(seconds, filter)  the logged flows of the trailing seconds before now, in
//	                                    buckets: {from, to, bucket_seconds, complete_until, buckets,
//	                                    parts}; buckets is [{from, to, gap}], a bucket after
//	                                    complete_until (when the filter log's last pass ended healthy,
//	                                    null when none did), before the earliest flow kept or meeting
//	                                    a collection gap being a gap; parts is [{bucket_start,
//	                                    traffic_direction, protocol, port, decision, interface_id,
//	                                    client_id} and their summable figures]
//	host.flows.destinations()           the window's connections per inside end, direction, outside
//	                                    destination, site name, port and protocol: [{interface_id,
//	                                    client_id, owner_id, traffic_direction, peer_address,
//	                                    lookup_state, asn, site_name, port, protocol, connections,
//	                                    bytes, blocked, blocked_bytes, newest}]. Under aggregate_mode =
//	                                    no_domains no site name is read: every site_name is null
//	host.flows.serviceKey(protocol, port)  the key of a service, its port and protocol together:
//	                                    "port:53/udp", or "port:not_recorded" when no port was logged
//	host.flows.decisions                the decisions, by name: allowed, firewall_rule,
//	                                    firewall_no_rule, firewall_reason_not_recorded, unknown

// scriptFlowFilter is a filter as a script hands it.
type scriptFlowFilter struct {
	Scope      string  `json:"scope"`
	Interfaces []int64 `json:"interfaces"`
	Clients    []int64 `json:"clients"`
}

func init() {
	registerHostArea("flows", func(call *scriptCall, area *goja.Object) {
		r := call.request
		filterOf := func(value goja.Value) FlowFilter {
			if absent(value) {
				return FlowFilter{Scope: "all"}
			}
			var filter scriptFlowFilter
			call.decodeArgument(value, "the flow filter", &filter)
			return FlowFilter{Scope: filter.Scope, Interfaces: filter.Interfaces, Clients: filter.Clients}
		}
		call.function(area, "parts", func(arguments goja.FunctionCall) goja.Value {
			parts, err := r.FlowParts(filterOf(arguments.Argument(0)))
			if err != nil {
				return call.fail(err)
			}
			return call.value(parts)
		})
		call.function(area, "recent", func(arguments goja.FunctionCall) goja.Value {
			seconds := call.integerOf(arguments.Argument(0), "the seconds")
			recent, err := r.RecentFlows(seconds, filterOf(arguments.Argument(1)))
			if err != nil {
				return call.fail(err)
			}
			return call.value(recent)
		})
		call.function(area, "destinations", func(goja.FunctionCall) goja.Value {
			rows, err := r.DestinationRows()
			if err != nil {
				return call.fail(err)
			}
			return call.value(rows)
		})
		call.function(area, "serviceKey", func(arguments goja.FunctionCall) goja.Value {
			var protocol *string
			if !absent(arguments.Argument(0)) {
				text := call.textOf(arguments.Argument(0), "the protocol")
				protocol = &text
			}
			return call.vm.ToValue(ServiceKey(protocol, call.optionalIntegerOf(arguments.Argument(1), "the port")))
		})
		_ = area.Set("decisions", call.value(map[string]string{
			"allowed": DecisionAllowed, "firewall_rule": DecisionFirewallRule,
			"firewall_no_rule":             DecisionFirewallNoRule,
			"firewall_reason_not_recorded": DecisionFirewallReasonNotRecorded, "unknown": DecisionUnknown,
		}))
	})
}
