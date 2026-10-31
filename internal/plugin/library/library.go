// Package library is the JavaScript library a plugin ships for other plugins (ADR 0021, decision
// 1): a file the manifest lists under `libraries`, compiled once at load, evaluated once per
// runtime, and bound under host.lib.<plugin> for the plugin's own scripts and for every plugin
// that requires it. A library is pure JavaScript: it is handed nothing of the host, and what it
// needs it takes as arguments. The package names no product and knows no library: it compiles
// files and binds them.
package library

import (
	"fmt"
	"sort"

	"github.com/dop251/goja"
)

// Library is one compiled library file of a plugin.
type Library struct {
	// Plugin is the plugin that ships it, the name it is bound under.
	Plugin string
	// File is the file's name in the plugin's directory, for a message.
	File    string
	program *goja.Program
}

// Compile compiles a library file. The source is wrapped in a function handed `exports` and
// `module`, on the source's own first line so a line number in a message is the file's.
func Compile(plugin, file, source string) (Library, error) {
	program, err := goja.Compile(file, "(function (exports, module) {"+source+"\n})", false)
	if err != nil {
		return Library{}, fmt.Errorf("%s does not compile: %w", file, err)
	}
	return Library{Plugin: plugin, File: file, program: program}, nil
}

// Bind evaluates every library in a runtime and returns the host.lib object: one member per
// plugin shipping a library the caller may use, holding what its files export. Several files of
// one plugin export into one object. A member of no such plugin throws a TypeError naming the
// refusal, library_not_required: the plugin does not require the one that ships the library, or
// none ships it.
func Bind(vm *goja.Runtime, libraries []Library) (*goja.Object, error) {
	bound := map[string]*goja.Object{}
	for _, library := range libraries {
		exports, found := bound[library.Plugin]
		if !found {
			exports = vm.NewObject()
			bound[library.Plugin] = exports
		}
		module := vm.NewObject()
		_ = module.Set("exports", exports)
		value, err := vm.RunProgram(library.program)
		if err != nil {
			return nil, fmt.Errorf("the library %s of %s: %w", library.File, library.Plugin, err)
		}
		function, isFunction := goja.AssertFunction(value)
		if !isFunction {
			return nil, fmt.Errorf("the library %s of %s did not evaluate to its function", library.File, library.Plugin)
		}
		if _, err := function(goja.Undefined(), exports, module); err != nil {
			return nil, fmt.Errorf("the library %s of %s fails while defining its exports: %w", library.File, library.Plugin, err)
		}
	}
	return vm.NewDynamicObject(&libObject{vm: vm, bound: bound}), nil
}

// libObject is host.lib: a member per plugin whose library the script may use.
type libObject struct {
	vm    *goja.Runtime
	bound map[string]*goja.Object
}

// RefusalNotRequired is the refusal named by the TypeError a script meets reading host.lib.<name>
// for a plugin it does not require, or that ships no library.
const RefusalNotRequired = "library_not_required"

func (l *libObject) Get(key string) goja.Value {
	if exports, found := l.bound[key]; found {
		return exports
	}
	panic(l.vm.NewTypeError("%s: host.lib.%s: the plugin ships no library, or this plugin does not require it", RefusalNotRequired, key))
}

func (l *libObject) Set(string, goja.Value) bool { return false }

func (l *libObject) Has(key string) bool {
	_, found := l.bound[key]
	return found
}

func (l *libObject) Delete(string) bool { return false }

func (l *libObject) Keys() []string {
	keys := make([]string, 0, len(l.bound))
	for key := range l.bound {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
