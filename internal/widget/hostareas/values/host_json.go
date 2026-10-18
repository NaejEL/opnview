package values

import (
	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/widget/script"
)

// host.json: how the data a script returns is written (internal/widget/script/script_json.go).
//
//	host.json.map(object)   marks an object as a map, written with its keys sorted byte-wise as Go
//	                        writes a map; returns the object
//	host.json.encode(value) the JSON the host would write for a value, as a string

func init() {
	script.RegisterHostArea("json", func(call *script.ScriptCall, area *goja.Object) {
		call.Function(area, "map", func(arguments goja.FunctionCall) goja.Value {
			object, isObject := arguments.Argument(0).(*goja.Object)
			if !isObject || object.ClassName() != "Object" {
				call.Throw("json.map takes a plain object, and was handed %s", script.Describe(arguments.Argument(0)))
			}
			if err := call.MarkMap(object); err != nil {
				call.Throw("json.map: %v", err)
			}
			return object
		})
		call.Function(area, "encode", func(arguments goja.FunctionCall) goja.Value {
			encoded, err := call.Encode(arguments.Argument(0))
			if err != nil {
				call.Throw("json.encode: %v", err)
			}
			return call.VM.ToValue(string(encoded))
		})
	})
}
