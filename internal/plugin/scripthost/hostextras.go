package scripthost

import (
	"context"
	"time"

	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/plugin/fetch"
)

// The host functions a script is given beyond the firewall: its own settings, its own tables, and
// the fetch facility over its own data directory. Each is present only where the plugin has the
// thing it reaches -- declared settings, declared tables, a data directory -- so a script that
// declares none of them sees the bare port, and the listing of what a plugin may do is its manifest.

// Extensions are what a loaded plugin's script may reach beyond the firewall.
type Extensions struct {
	// Settings reads the plugin's declared settings.
	Settings func(ctx context.Context) (map[string]any, map[string]string, error)
	// Query runs a read-only query against the plugin's own tables.
	Query func(ctx context.Context, text string, parameters map[string]any) ([]map[string]any, error)
	// Facility is the fetch facility and the data directory.
	Facility *fetch.Facility
}

// queryTimeout bounds one query a script runs against its own tables.
const queryTimeout = 10 * time.Second

// extend adds the extensions to a host object.
func (h *hostCalls) extend(vm *goja.Runtime, host *goja.Object, extra *Extensions) {
	if extra == nil {
		return
	}
	if extra.Settings != nil {
		settings, states, err := extra.Settings(h.ctx)
		if err == nil {
			_ = host.Set("settings", settings)
			_ = host.Set("secret_states", states)
		}
	}
	if extra.Query != nil {
		_ = host.Set("query", func(call goja.FunctionCall) goja.Value {
			parameters, _ := call.Argument(1).Export().(map[string]any)
			ctx, cancel := context.WithTimeout(h.ctx, queryTimeout)
			defer cancel()
			rows, err := extra.Query(ctx, call.Argument(0).String(), parameters)
			if err != nil {
				Throw(vm, "the query failed: %v", err)
			}
			list := make([]any, 0, len(rows))
			for _, row := range rows {
				list = append(list, row)
			}
			return vm.ToValue(list)
		})
	}
	if extra.Facility != nil {
		h.extendFetch(vm, host, extra.Facility)
	}
}

// extendFetch adds host.fetch and host.files.
func (h *hostCalls) extendFetch(vm *goja.Runtime, host *goja.Object, facility *fetch.Facility) {
	_ = host.Set("fetch", func(call goja.FunctionCall) goja.Value {
		options, isObject := call.Argument(0).(*goja.Object)
		if !isObject {
			Throw(vm, "fetch takes an options object")
		}
		text := func(key string) string {
			value := options.Get(key)
			if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
				return ""
			}
			return value.String()
		}
		request := fetch.Request{URL: text("url"), Method: text("method"), File: text("file")}
		if bytes := options.Get("max_bytes"); bytes != nil && !goja.IsUndefined(bytes) {
			request.MaxBytes = bytes.ToInteger()
		}
		if conditional := options.Get("conditional"); conditional != nil && !goja.IsUndefined(conditional) {
			request.Conditional = conditional.ToBoolean()
		}
		if headers, isMap := ExportOf(options.Get("headers")).(map[string]any); isMap {
			request.Headers = map[string]string{}
			for name, value := range headers {
				if text, isText := value.(string); isText {
					request.Headers[name] = text
				}
			}
		}
		if check, isFunction := goja.AssertFunction(options.Get("check")); isFunction {
			request.Check = func(temporary string) (bool, string) {
				answer, err := check(goja.Undefined(), vm.ToValue(temporary))
				if err != nil {
					return false, err.Error()
				}
				if object, isObject := answer.(*goja.Object); isObject {
					accepted := object.Get("ok")
					detail := object.Get("detail")
					why := ""
					if detail != nil && !goja.IsUndefined(detail) && !goja.IsNull(detail) {
						why = detail.String()
					}
					return accepted != nil && accepted.ToBoolean(), why
				}
				return answer.ToBoolean(), ""
			}
		}
		result := facility.Fetch(h.ctx, request)
		answer := vm.NewObject()
		_ = answer.Set("outcome", result.Outcome)
		_ = answer.Set("status_code", result.StatusCode)
		_ = answer.Set("detail", result.Detail)
		_ = answer.Set("last_modified", result.LastModified)
		_ = answer.Set("etag", result.ETag)
		return answer
	})

	files := vm.NewObject()
	fail := func(err error) {
		if err != nil {
			Throw(vm, "%v", err)
		}
	}
	_ = files.Set("exists", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(facility.Exists(call.Argument(0).String()))
	})
	_ = files.Set("readText", func(call goja.FunctionCall) goja.Value {
		text, err := facility.ReadText(call.Argument(0).String())
		if err != nil {
			return goja.Null()
		}
		return vm.ToValue(text)
	})
	_ = files.Set("writeText", func(call goja.FunctionCall) goja.Value {
		fail(facility.WriteText(call.Argument(0).String(), call.Argument(1).String()))
		return goja.Undefined()
	})
	_ = files.Set("remove", func(call goja.FunctionCall) goja.Value {
		fail(facility.Remove(call.Argument(0).String()))
		return goja.Undefined()
	})
	_ = files.Set("replace", func(call goja.FunctionCall) goja.Value {
		fail(facility.Replace(call.Argument(0).String(), call.Argument(1).String()))
		return goja.Undefined()
	})
	_ = files.Set("lines", func(call goja.FunctionCall) goja.Value {
		lines, err := facility.Lines(call.Argument(0).String(), int(call.Argument(1).ToInteger()))
		fail(err)
		list := make([]any, 0, len(lines))
		for _, line := range lines {
			list = append(list, line)
		}
		return vm.ToValue(list)
	})
	_ = files.Set("sha256", func(call goja.FunctionCall) goja.Value {
		digest, err := facility.SHA256(call.Argument(0).String())
		fail(err)
		return vm.ToValue(digest)
	})
	_ = files.Set("extractTarGz", func(call goja.FunctionCall) goja.Value {
		name, err := facility.ExtractTarGz(call.Argument(0).String(), call.Argument(1).String(),
			call.Argument(2).ToInteger())
		if err != nil {
			result := vm.NewObject()
			_ = result.Set("error", err.Error())
			return result
		}
		result := vm.NewObject()
		_ = result.Set("file", name)
		return result
	})
	_ = files.Set("gunzip", func(call goja.FunctionCall) goja.Value {
		name, err := facility.Gunzip(call.Argument(0).String(), call.Argument(1).ToInteger())
		if err != nil {
			result := vm.NewObject()
			_ = result.Set("error", err.Error())
			return result
		}
		result := vm.NewObject()
		_ = result.Set("file", name)
		return result
	})
	_ = files.Set("openMMDB", func(call goja.FunctionCall) goja.Value {
		database, err := facility.OpenMMDB(call.Argument(0).String(), call.Argument(1).ToBoolean())
		if err != nil {
			result := vm.NewObject()
			_ = result.Set("error", err.Error())
			return result
		}
		result := vm.NewObject()
		_ = result.Set("database_type", database.DatabaseType())
		_ = result.Set("build_epoch", database.BuildEpoch())
		_ = result.Set("lookup", func(lookup goja.FunctionCall) goja.Value {
			record, found, err := database.Lookup(lookup.Argument(0).String())
			if err != nil {
				Throw(vm, "%v", err)
			}
			if !found {
				return goja.Null()
			}
			return vm.ToValue(record)
		})
		return result
	})
	_ = files.Set("openSuffixList", func(call goja.FunctionCall) goja.Value {
		list, err := facility.OpenSuffixList(call.Argument(0).String())
		if err != nil {
			result := vm.NewObject()
			_ = result.Set("error", err.Error())
			return result
		}
		result := vm.NewObject()
		_ = result.Set("version", list.Version)
		_ = result.Set("rules", list.Rules())
		_ = result.Set("registrableDomain", func(lookup goja.FunctionCall) goja.Value {
			domain, registrable := list.RegistrableDomain(lookup.Argument(0).String())
			if !registrable {
				return goja.Null()
			}
			return vm.ToValue(domain)
		})
		return result
	})
	_ = host.Set("files", files)
}
