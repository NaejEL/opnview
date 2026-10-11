package widget

import (
	"github.com/dop251/goja"
)

// host.services: what the two service kinds answered, as stored -- the registrable-domain grouping
// of the Public Suffix List in use, and the IP geolocation of the dataset in use. A widget never
// downloads, looks up or refreshes anything: it reads what the core holds.
//
//	host.services.registrable(names)   {available, groups}: groups maps each name to its registrable
//	                                   domain (a name that is itself a public suffix stands for itself);
//	                                   available is false, and groups null, while no list is held
//	host.services.geolocation(address) the stored geolocation of one address: {lookup_state,
//	                                   country_code, country_name, latitude, longitude, asn, operator,
//	                                   dataset_build_at}; lookup_state is resolved, miss, pending, or
//	                                   no_row for an address never looked up

func init() {
	registerHostArea("services", func(call *scriptCall, area *goja.Object) {
		r := call.request
		call.function(area, "registrable", func(arguments goja.FunctionCall) goja.Value {
			names := call.textsOf(arguments.Argument(0), "the names")
			groups, available := r.RegistrableGrouping(r.Lists, names)
			answer := struct {
				Available bool              `json:"available"`
				Groups    map[string]string `json:"groups"`
			}{Available: available, Groups: groups}
			value := call.value(answer)
			if object, isObject := value.(*goja.Object); isObject && available {
				if grouped, isGroups := object.Get("groups").(*goja.Object); isGroups {
					_ = grouped.DefineDataPropertySymbol(call.mapMarker, call.vm.ToValue(true), goja.FLAG_FALSE,
						goja.FLAG_FALSE, goja.FLAG_FALSE)
				}
			}
			return value
		})
		call.function(area, "geolocation", func(arguments goja.FunctionCall) goja.Value {
			info, err := r.Geolocation(call.textOf(arguments.Argument(0), "the address"))
			if err != nil {
				return call.fail(err)
			}
			return call.value(info)
		})
	})
}
