package script

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/widget/request"
)

// A widget's script: the JavaScript a shape cannot express, run inside the binary by the embedded
// interpreter every plugin runs on (goja, docs/plugins.md, "The interpreter and what it brings").
//
// THE CONTRACT. The script exports `widget.serve`, a function handed the request's host
// (host.go) and returning {data, has_rows}: the response's data, which the host writes as JSON
// byte for byte as the script built it (script_json.go), and whether it holds any row -- the
// difference between reachable and reachable_no_rows. Everything around the data is the core's:
// the parameters were parsed, defaulted and reported, the references resolved, the availability,
// the gaps and the window read, exactly as for every other widget, before the script runs; and the
// condition, the withheld fields and the states are assembled after it returns from what the
// script recorded through the host.
//
// EVERY CALL IS A FRESH RUNTIME. The program is compiled once, at load; each request creates a
// runtime, runs the program so it defines its exports, and calls serve once. Nothing survives from
// one request to the next.
//
// WHAT THE HOST GRANTS, AND WHAT IT DOES NOT. The widget's own declared statements, run on the
// read-only role with bound parameters (internal/widget/hostareas/installation/host_query.go), the
// shared figures of the core, and the envelope's states and conditions. No firewall call, no
// outbound fetch, no write: nothing here can reach one. There is no sandbox beyond that, and none
// is claimed: a widget plugin runs with the service's privileges, as a source plugin does.
//
// A FAILURE IS THE WIDGET'S, NEVER THE SERVICE'S. A script that throws, runs past its time bound or
// answers something that is not an answer ends the request with the closed condition
// script_failed; a statement that fails is the store's failure and ends it as one; the service and
// every other widget carry on.

// scriptCallTimeout bounds one call of a widget script. Every statement it runs is itself bounded
// by the read connections' statement timeout (store, readers.go); this bounds the script's own work
// and the sum of its statements.
const scriptCallTimeout = 20 * time.Second

// ScriptLogger receives what a widget script logs and why one failed, prefixed with its plugin's
// name. It writes to standard error; a test may replace it.
var ScriptLogger = func(plugin, message string) {
	fmt.Fprintf(os.Stderr, "opnview: widget plugin %s: %s\n", plugin, message)
}

// ErrWithheldUnderNoDomains ends the call of a widget that is not built in when it runs a statement
// reading a column that carries a domain name under aggregate_mode = no_domains. The maintainer's
// decision of 12 October 2026: the core withholds what carries a domain from every widget answer,
// built-in and third-party alike, and a third-party author has nothing to do. The statement is not
// run, the script is stopped -- an interruption a script cannot catch -- and the answer is withheld
// whole (condition withheld_by_aggregate_mode, data null), the rule a declarative widget reading
// such a column already follows. Nothing the statement would have read reaches the script.
var ErrWithheldUnderNoDomains = errors.New("a statement carrying a domain name under aggregate_mode = no_domains")

// errScriptTimeout is what a call is interrupted with at its deadline.
var errScriptTimeout = errors.New("the widget script ran past its time bound")

// ScriptWidget is one widget's compiled script and the statements it may run.
type ScriptWidget struct {
	Plugin     string
	File       string
	Program    *goja.Program
	Statements map[string]WidgetStatement
	// Builtin says the widget ships in the binary. A built-in withholds its own domain fields under
	// aggregate_mode = no_domains, field by field, and its tests prove it; any other widget is held to
	// the core's rule instead (errWithheldUnderNoDomains).
	Builtin bool
	// Datasets are the dataset names the widget's plugin declares (DatasetsAdmitted): host.datasets refuses any
	// other with dataset_not_declared.
	Datasets map[string]bool
}

// CompileWidgetScript compiles a widget's script and runs it once, in a runtime of its own, to see
// that it exports widget.serve.
func CompileWidgetScript(file, source string) (*goja.Program, error) {
	program, err := goja.Compile(file, source, false)
	if err != nil {
		return nil, err
	}
	vm, exports := newWidgetRuntime("")
	if _, err := vm.RunProgram(program); err != nil {
		return nil, fmt.Errorf("running %s to read what it exports: %w", file, err)
	}
	if _, found := serveFunction(exports); !found {
		return nil, fmt.Errorf("%s exports no function widget.serve", file)
	}
	return program, nil
}

// newWidgetRuntime creates a runtime with the globals a widget script sees: exports, module and
// console, whose lines go to ScriptLogger.
func newWidgetRuntime(plugin string) (*goja.Runtime, *goja.Object) {
	return newLoggingRuntime(plugin, func(plugin, message string) { ScriptLogger(plugin, message) })
}

// newLoggingRuntime creates a widget script's runtime whose console lines go to log.
func newLoggingRuntime(plugin string, log func(plugin, message string)) (*goja.Runtime, *goja.Object) {
	vm := goja.New()
	exports := vm.NewObject()
	module := vm.NewObject()
	_ = module.Set("exports", exports)
	_ = vm.Set("exports", exports)
	_ = vm.Set("module", module)
	console := vm.NewObject()
	_ = console.Set("log", func(call goja.FunctionCall) goja.Value {
		parts := make([]string, 0, len(call.Arguments))
		for _, argument := range call.Arguments {
			parts = append(parts, argument.String())
		}
		log(plugin, strings.Join(parts, " "))
		return goja.Undefined()
	})
	_ = vm.Set("console", console)
	return vm, exports
}

// serveFunction is the widget.serve function a script exports.
func serveFunction(exports *goja.Object) (goja.Callable, bool) {
	namespace, isObject := exports.Get("widget").(*goja.Object)
	if !isObject {
		return nil, false
	}
	return goja.AssertFunction(namespace.Get("serve"))
}

// Serve answers one request: the ServeFunc of a script widget.
func (w *ScriptWidget) Serve(r *request.Request) (data any, hasRows bool, err error) {
	ctx, cancel := context.WithTimeout(r.Ctx, scriptCallTimeout)
	defer cancel()
	// Every host function reads through the request's context, so the time bound stops a statement
	// the script is waiting on as well as the script itself.
	requestCtx := r.Ctx
	r.Ctx = ctx
	defer func() { r.Ctx = requestCtx }()

	vm, exports := newLoggingRuntime(w.Plugin, func(plugin, message string) {
		// A speculative run holds its lines until its answer is the one served (request.ScriptLog).
		r.ScriptLog(plugin, message, func(plugin, message string) { ScriptLogger(plugin, message) })
	})
	call := newScriptCall(r, vm, w)
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-ctx.Done():
			vm.Interrupt(errScriptTimeout)
		case <-finished:
		}
	}()
	failed := func(format string, arguments ...any) *request.ScriptError {
		return &request.ScriptError{Plugin: w.Plugin, Type: r.Preset.Type, Detail: fmt.Sprintf(format, arguments...)}
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			data, hasRows, err = nil, false, failed("the script panicked: %v", recovered)
		}
	}()

	if _, err := vm.RunProgram(w.Program); err != nil {
		return nil, false, w.runFailure(call, failed, err)
	}
	serve, found := serveFunction(exports)
	if !found {
		return nil, false, failed("%s exports no function widget.serve", w.File)
	}
	result, err := serve(goja.Undefined(), call.host())
	if err != nil {
		return nil, false, w.runFailure(call, failed, err)
	}
	if call.err != nil {
		return nil, false, call.err
	}
	answer, isObject := result.(*goja.Object)
	if !isObject {
		return nil, false, failed("widget.serve returned %s, not an object holding data and has_rows", result)
	}
	rows, isBoolean := ExportOf(answer.Get("has_rows")).(bool)
	if !isBoolean {
		return nil, false, failed("widget.serve answered no boolean has_rows")
	}
	value := answer.Get("data")
	if value == nil || goja.IsUndefined(value) {
		return nil, false, failed("widget.serve answered no data; an answer with nothing honest to show is null")
	}
	encoded, err := call.Encode(value)
	if err != nil {
		return nil, false, failed("its data cannot be written as JSON: %v", err)
	}
	return json.RawMessage(encoded), rows, nil
}

// runFailure names what ended a call: a host function's own failure, the time bound, or the script.
func (w *ScriptWidget) runFailure(call *ScriptCall, failed func(string, ...any) *request.ScriptError, err error) error {
	if call.err != nil {
		return call.err
	}
	var interrupted *goja.InterruptedError
	if errors.As(err, &interrupted) {
		return failed("it ran past %s and was stopped", scriptCallTimeout)
	}
	var exception *goja.Exception
	if errors.As(err, &exception) {
		return failed("it threw: %s", strings.TrimSpace(exception.Error()))
	}
	return failed("%v", err)
}

// WidgetStatement is one statement a plugin widget declares.
type WidgetStatement struct {
	// Registered is the name it is registered under.
	Registered string
	// Template says it is a period template (@period@), run for the period a script names.
	Template bool
	// Domain says it reads a column carrying a domain name.
	Domain bool
	// Parameters are the names it binds, without the colon.
	Parameters []string
}

// DatasetNotDeclared is the closed code a read of an undeclared dataset is refused with (the plugin
// contract's dataset_not_declared, docs/plugins.md "Refusals").
const DatasetNotDeclared = "dataset_not_declared"

// DatasetsAdmitted is the set of datasets a plugin's widget may read: those its manifest declares, and
// its own plugin's tables, which the prefix <plugin>. marks.
func DatasetsAdmitted(plugin string, declared []string) map[string]bool {
	admitted := map[string]bool{plugin + ".": true}
	for _, name := range declared {
		admitted[name] = true
	}
	return admitted
}

// AdmitsDataset reports whether the widget's script may read a dataset.
func (w *ScriptWidget) AdmitsDataset(name string) bool {
	if w.Datasets[name] {
		return true
	}
	if prefix, _, found := strings.Cut(name, "."); found {
		return w.Datasets[prefix+"."]
	}
	return false
}
