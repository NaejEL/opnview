package values

import (
	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/widget/figures"
	"github.com/NaejEL/opnview/internal/widget/script"
)

// host.services: what the Public Suffix List in use answers, the registrable-domain grouping. A widget
// never downloads, looks up or refreshes anything: it reads what the core holds. The IP geolocation of
// the dataset in use is the figures library's services area since ADR 0021 (plugins/figures), over
// the widget's own statement.
//
//	host.services.registrable(names)   {available, groups}: groups maps each name to its registrable
//	                                   domain (a name that is itself a public suffix stands for itself);
//	                                   available is false, and groups null, while no list is held

func init() {
	script.RegisterHostArea("services", func(call *script.ScriptCall, area *goja.Object) {
		r := call.Request
		call.Function(area, "registrable", func(arguments goja.FunctionCall) goja.Value {
			names := call.TextsOf(arguments.Argument(0), "the names")
			groups, available := figures.RegistrableGrouping(r, r.Lists, names)
			answer := struct {
				Available bool              `json:"available"`
				Groups    map[string]string `json:"groups"`
			}{Available: available, Groups: groups}
			value := call.Value(answer)
			if object, isObject := value.(*goja.Object); isObject && available {
				if grouped, isGroups := object.Get("groups").(*goja.Object); isGroups {
					_ = grouped.DefineDataPropertySymbol(call.MapMarker, call.VM.ToValue(true), goja.FLAG_FALSE,
						goja.FLAG_FALSE, goja.FLAG_FALSE)
				}
			}
			return value
		})
	})
}
