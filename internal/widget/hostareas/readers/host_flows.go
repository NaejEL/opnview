package readers

import (
	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/widget/script"
	"github.com/NaejEL/opnview/internal/widget/traffic"
)

// host.flows: the window's flows by their columns (internal/widget/traffic/flows.go) -- whichever
// source wrote them -- for the compositions by protocol, service port and blocking reason, their
// recent part, and the destination levels of a connection tree. A filter is {scope, interfaces,
// clients}: a traffic scope ("all", or absent, for every one) and id lists, null for no
// restriction.
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
	script.RegisterHostArea("flows", func(call *script.ScriptCall, area *goja.Object) {
		r := call.Request
		filterOf := func(value goja.Value) traffic.FlowFilter {
			if script.Absent(value) {
				return traffic.FlowFilter{Scope: "all"}
			}
			var filter scriptFlowFilter
			call.DecodeArgument(value, "the flow filter", &filter)
			return traffic.FlowFilter{Scope: filter.Scope, Interfaces: filter.Interfaces, Clients: filter.Clients}
		}
		call.Function(area, "parts", func(arguments goja.FunctionCall) goja.Value {
			parts, err := traffic.FlowParts(r, filterOf(arguments.Argument(0)))
			if err != nil {
				return call.Fail(err)
			}
			return call.Value(parts)
		})
		call.Function(area, "recent", func(arguments goja.FunctionCall) goja.Value {
			seconds := call.IntegerOf(arguments.Argument(0), "the seconds")
			recent, err := traffic.RecentFlows(r, seconds, filterOf(arguments.Argument(1)))
			if err != nil {
				return call.Fail(err)
			}
			return call.Value(recent)
		})
		call.Function(area, "destinations", func(goja.FunctionCall) goja.Value {
			rows, err := traffic.DestinationRows(r)
			if err != nil {
				return call.Fail(err)
			}
			return call.Value(rows)
		})
		call.Function(area, "serviceKey", func(arguments goja.FunctionCall) goja.Value {
			var protocol *string
			if !script.Absent(arguments.Argument(0)) {
				text := call.TextOf(arguments.Argument(0), "the protocol")
				protocol = &text
			}
			return call.VM.ToValue(traffic.ServiceKey(protocol, call.OptionalIntegerOf(arguments.Argument(1), "the port")))
		})
		_ = area.Set("decisions", call.Value(map[string]string{
			"allowed": traffic.DecisionAllowed, "firewall_rule": traffic.DecisionFirewallRule,
			"firewall_no_rule":             traffic.DecisionFirewallNoRule,
			"firewall_reason_not_recorded": traffic.DecisionFirewallReasonNotRecorded, "unknown": traffic.DecisionUnknown,
		}))
	})
}
