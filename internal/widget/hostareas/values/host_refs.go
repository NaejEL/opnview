package values

import (
	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/widget/request"
	"github.com/NaejEL/opnview/internal/widget/script"
)

// host.refs: the references the core resolved before the script runs. A reference that matched
// nothing has already made the response unresolved_reference -- or, inside a series entry, failed
// that entry alone -- so a script reads only what resolved.
//
//	host.refs.ids(key)              the resolved row ids of a reference or reference_list key, or null
//	                                when the key holds none, which means "no restriction"
//	host.refs.texts(key)            the resolved texts (a country code, a site) of a key, or null
//	host.refs.entry(index, key)     the resolved reference {id, text} of one series entry's key, or null
//	host.refs.failed(index)         whether a series entry holds a reference that did not resolve
//	host.refs.withheld(index)       whether that reference is a site the aggregate mode withholds
//	host.refs.jsonIds(ids)          an id list as the JSON text a statement's set parameter takes, or
//	                                null for null: what every statement binding a set expects
//	host.refs.jsonTexts(texts)      the same for a list of strings

func init() {
	script.RegisterHostArea("refs", func(call *script.ScriptCall, area *goja.Object) {
		r := call.Request
		key := func(arguments goja.FunctionCall) string {
			return call.TextOf(arguments.Argument(0), "the parameter key")
		}
		call.Function(area, "ids", func(arguments goja.FunctionCall) goja.Value {
			ids := r.Ids(key(arguments))
			if ids == nil {
				return goja.Null()
			}
			return call.Value(ids)
		})
		call.Function(area, "texts", func(arguments goja.FunctionCall) goja.Value {
			resolved := r.Refs[key(arguments)]
			if len(resolved) == 0 {
				return goja.Null()
			}
			texts := make([]string, 0, len(resolved))
			for _, value := range resolved {
				texts = append(texts, value.Text)
			}
			return call.Value(texts)
		})
		call.Function(area, "entry", func(arguments goja.FunctionCall) goja.Value {
			index := int(call.IntegerOf(arguments.Argument(0), "the series entry index"))
			name := call.TextOf(arguments.Argument(1), "the key")
			value, found := r.Entries[index][name]
			if !found {
				return goja.Null()
			}
			return call.Value(map[string]any{"id": value.Id, "text": value.Text})
		})
		call.Function(area, "failed", func(arguments goja.FunctionCall) goja.Value {
			return call.VM.ToValue(r.Failed[int(call.IntegerOf(arguments.Argument(0), "the series entry index"))])
		})
		call.Function(area, "withheld", func(arguments goja.FunctionCall) goja.Value {
			return call.VM.ToValue(r.WithheldSeries[int(call.IntegerOf(arguments.Argument(0), "the series entry index"))])
		})
		call.Function(area, "jsonIds", func(arguments goja.FunctionCall) goja.Value {
			if script.Absent(arguments.Argument(0)) {
				return goja.Null()
			}
			return call.VM.ToValue(request.JsonIDs(call.IntegersOf(arguments.Argument(0), "the ids")))
		})
		call.Function(area, "jsonTexts", func(arguments goja.FunctionCall) goja.Value {
			if script.Absent(arguments.Argument(0)) {
				return goja.Null()
			}
			return call.VM.ToValue(request.JsonTexts(call.TextsOf(arguments.Argument(0), "the texts")))
		})
	})
}
