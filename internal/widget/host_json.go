package widget

import (
	"github.com/dop251/goja"
)

// host.json: how the data a script returns is written (script_json.go).
//
//	host.json.map(object)   marks an object as a map, written with its keys sorted byte-wise as Go
//	                        writes a map; returns the object
//	host.json.encode(value) the JSON the host would write for a value, as a string

func init() {
	registerHostArea("json", func(call *scriptCall, area *goja.Object) {
		call.function(area, "map", func(arguments goja.FunctionCall) goja.Value {
			object, isObject := arguments.Argument(0).(*goja.Object)
			if !isObject || object.ClassName() != "Object" {
				call.throw("json.map takes a plain object, and was handed %s", describe(arguments.Argument(0)))
			}
			if err := object.DefineDataPropertySymbol(call.mapMarker, call.vm.ToValue(true), goja.FLAG_FALSE,
				goja.FLAG_FALSE, goja.FLAG_FALSE); err != nil {
				call.throw("json.map: %v", err)
			}
			return object
		})
		call.function(area, "encode", func(arguments goja.FunctionCall) goja.Value {
			encoded, err := call.encode(arguments.Argument(0))
			if err != nil {
				call.throw("json.encode: %v", err)
			}
			return call.vm.ToValue(string(encoded))
		})
	})
}
