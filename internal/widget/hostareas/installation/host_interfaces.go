package installation

import (
	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/widget/script"
)

// host.interfaces: what the store counts per interface beyond its volume -- the clients a window
// showed on each interface out of those it is known to have, and the rules that do not log, per
// interface (internal/store/reads/read.go: ReadInterfaceClients and ReadRuleLogging). Both are counts of
// what opnview stored; no interface is classified by what it is called.
//
//	host.interfaces.clients(from, to)  per interface: [{interface_id, seen, known}]: seen is the
//	                                   distinct inside clients of the flows of [from, to) on it, known
//	                                   the client rows of the interface seen at or after from
//	host.interfaces.ruleLogging()      {not_logging_by_interface, floating_not_logging,
//	                                   unresolved_not_logging, not_reported}: the first maps an
//	                                   interface id, as a string, to the rules naming it whose logging
//	                                   is off; the others count the rules naming no interface, the
//	                                   rules whose interfaces did not resolve, and the rules whose
//	                                   logging flag was not reported at all

// interfaceClients is one interface's client counts as a script reads them.
type interfaceClients struct {
	InterfaceID int64 `json:"interface_id"`
	Seen        int64 `json:"seen"`
	Known       int64 `json:"known"`
}

// ruleLogging is the rules that do not log, as a script reads them.
type ruleLogging struct {
	NotLoggingByInterface map[int64]int64 `json:"not_logging_by_interface"`
	FloatingNotLogging    int64           `json:"floating_not_logging"`
	UnresolvedNotLogging  int64           `json:"unresolved_not_logging"`
	NotReported           int64           `json:"not_reported"`
}

func init() {
	script.RegisterHostArea("interfaces", func(call *script.ScriptCall, area *goja.Object) {
		r := call.Request
		call.Function(area, "clients", func(arguments goja.FunctionCall) goja.Value {
			from := call.IntegerOf(arguments.Argument(0), "from")
			to := call.IntegerOf(arguments.Argument(1), "to")
			counts, err := r.Db.ReadInterfaceClients(r.Ctx, from, to)
			if err != nil {
				return call.Fail(err)
			}
			read := make([]interfaceClients, 0, len(counts))
			for _, count := range counts {
				read = append(read, interfaceClients{InterfaceID: count.InterfaceID, Seen: count.Seen, Known: count.Known})
			}
			return call.Value(read)
		})
		call.Function(area, "ruleLogging", func(goja.FunctionCall) goja.Value {
			logging, err := r.Db.ReadRuleLogging(r.Ctx)
			if err != nil {
				return call.Fail(err)
			}
			byInterface := logging.NotLoggingByInterface
			if byInterface == nil {
				byInterface = map[int64]int64{}
			}
			return call.Value(ruleLogging{NotLoggingByInterface: byInterface,
				FloatingNotLogging: logging.FloatingNotLogging, UnresolvedNotLogging: logging.UnresolvedNotLogging,
				NotReported: logging.NotReported})
		})
	})
}
