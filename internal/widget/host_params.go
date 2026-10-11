package widget

import (
	"github.com/dop251/goja"
)

// host.params: the request's effective parameters, after the core parsed, defaulted and reported
// them exactly as for every other widget. A key the manifest does not declare reads as its zero.
//
//	host.params.text(key)        an enum's value, as a string
//	host.params.flag(key)        a boolean
//	host.params.integer(key)     an integer
//	host.params.number(key)      a number
//	host.params.set(key)         an enum_set's or string_list's members, as an array of strings
//	host.params.has(key, value)  whether an enum_set holds a value
//	host.params.effective()      every effective parameter, as the response echoes them

func init() {
	registerHostArea("params", func(call *scriptCall, area *goja.Object) {
		params := call.request.Params
		key := func(arguments goja.FunctionCall) string {
			return call.textOf(arguments.Argument(0), "the parameter key")
		}
		call.function(area, "text", func(arguments goja.FunctionCall) goja.Value {
			return call.vm.ToValue(params.Text(key(arguments)))
		})
		call.function(area, "flag", func(arguments goja.FunctionCall) goja.Value {
			return call.vm.ToValue(params.Flag(key(arguments)))
		})
		call.function(area, "integer", func(arguments goja.FunctionCall) goja.Value {
			return call.vm.ToValue(params.Integer(key(arguments)))
		})
		call.function(area, "number", func(arguments goja.FunctionCall) goja.Value {
			return call.vm.ToValue(params.Number(key(arguments)))
		})
		call.function(area, "set", func(arguments goja.FunctionCall) goja.Value {
			return call.value(params.Set(key(arguments)))
		})
		call.function(area, "has", func(arguments goja.FunctionCall) goja.Value {
			member := call.textOf(arguments.Argument(1), "the member")
			return call.vm.ToValue(params.Has(key(arguments), member))
		})
		call.function(area, "effective", func(goja.FunctionCall) goja.Value {
			return call.value(params.Effective)
		})
	})
}
