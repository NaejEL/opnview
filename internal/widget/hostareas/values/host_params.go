package values

import (
	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/widget/script"
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
	script.RegisterHostArea("params", func(call *script.ScriptCall, area *goja.Object) {
		params := call.Request.Params
		key := func(arguments goja.FunctionCall) string {
			return call.TextOf(arguments.Argument(0), "the parameter key")
		}
		call.Function(area, "text", func(arguments goja.FunctionCall) goja.Value {
			return call.VM.ToValue(params.Text(key(arguments)))
		})
		call.Function(area, "flag", func(arguments goja.FunctionCall) goja.Value {
			return call.VM.ToValue(params.Flag(key(arguments)))
		})
		call.Function(area, "integer", func(arguments goja.FunctionCall) goja.Value {
			return call.VM.ToValue(params.Integer(key(arguments)))
		})
		call.Function(area, "number", func(arguments goja.FunctionCall) goja.Value {
			return call.VM.ToValue(params.Number(key(arguments)))
		})
		call.Function(area, "set", func(arguments goja.FunctionCall) goja.Value {
			return call.Value(params.Set(key(arguments)))
		})
		call.Function(area, "has", func(arguments goja.FunctionCall) goja.Value {
			member := call.TextOf(arguments.Argument(1), "the member")
			return call.VM.ToValue(params.Has(key(arguments), member))
		})
		call.Function(area, "effective", func(goja.FunctionCall) goja.Value {
			return call.Value(params.Effective)
		})
	})
}
