package widget

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dop251/goja"
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
// read-only role with bound parameters (host_query.go), the shared figures of the core, and the
// envelope's states and conditions. No firewall call, no outbound fetch, no write: nothing here can
// reach one. There is no sandbox beyond that, and none is claimed: a widget plugin runs with the
// service's privileges, as a source plugin does.
//
// A FAILURE IS THE WIDGET'S, NEVER THE SERVICE'S. A script that throws, runs past its time bound or
// answers something that is not an answer ends the request with the closed condition
// script_failed; a statement that fails is the store's failure and ends it as one; the service and
// every other widget carry on.

// ConditionScriptFailed is a request whose widget script failed.
const ConditionScriptFailed = "script_failed"

// scriptCallTimeout bounds one call of a widget script. Every statement it runs is itself bounded
// by the read connections' statement timeout (store, readers.go); this bounds the script's own work
// and the sum of its statements.
const scriptCallTimeout = 20 * time.Second

// scriptLogger receives what a widget script logs and why one failed, prefixed with its plugin's
// name. It writes to standard error; a test may replace it.
var scriptLogger = func(plugin, message string) {
	fmt.Fprintf(os.Stderr, "opnview: widget plugin %s: %s\n", plugin, message)
}

// ScriptError is a widget script that did not answer usably.
type ScriptError struct {
	Plugin string
	Type   string
	Detail string
}

// Error implements error.
func (e *ScriptError) Error() string {
	return fmt.Sprintf("the widget %s failed: %s", e.Type, e.Detail)
}

// errScriptTimeout is what a call is interrupted with at its deadline.
var errScriptTimeout = errors.New("the widget script ran past its time bound")

// scriptWidget is one widget's compiled script and the statements it may run.
type scriptWidget struct {
	plugin     string
	file       string
	program    *goja.Program
	statements map[string]widgetStatement
}

// compileWidgetScript compiles a widget's script and runs it once, in a runtime of its own, to see
// that it exports widget.serve.
func compileWidgetScript(file, source string) (*goja.Program, error) {
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
// console.
func newWidgetRuntime(plugin string) (*goja.Runtime, *goja.Object) {
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
		scriptLogger(plugin, strings.Join(parts, " "))
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

// serve answers one request: the ServeFunc of a script widget.
func (w *scriptWidget) serve(r *Request) (data any, hasRows bool, err error) {
	ctx, cancel := context.WithTimeout(r.Ctx, scriptCallTimeout)
	defer cancel()
	// Every host function reads through the request's context, so the time bound stops a statement
	// the script is waiting on as well as the script itself.
	requestCtx := r.Ctx
	r.Ctx = ctx
	defer func() { r.Ctx = requestCtx }()

	vm, exports := newWidgetRuntime(w.plugin)
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
	failed := func(format string, arguments ...any) *ScriptError {
		return &ScriptError{Plugin: w.plugin, Type: r.Preset.Type, Detail: fmt.Sprintf(format, arguments...)}
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			data, hasRows, err = nil, false, failed("the script panicked: %v", recovered)
		}
	}()

	if _, err := vm.RunProgram(w.program); err != nil {
		return nil, false, w.runFailure(call, failed, err)
	}
	serve, found := serveFunction(exports)
	if !found {
		return nil, false, failed("%s exports no function widget.serve", w.file)
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
	rows, isBoolean := exportOf(answer.Get("has_rows")).(bool)
	if !isBoolean {
		return nil, false, failed("widget.serve answered no boolean has_rows")
	}
	value := answer.Get("data")
	if value == nil || goja.IsUndefined(value) {
		return nil, false, failed("widget.serve answered no data; an answer with nothing honest to show is null")
	}
	encoded, err := call.encode(value)
	if err != nil {
		return nil, false, failed("its data cannot be written as JSON: %v", err)
	}
	return json.RawMessage(encoded), rows, nil
}

// runFailure names what ended a call: a host function's own failure, the time bound, or the script.
func (w *scriptWidget) runFailure(call *scriptCall, failed func(string, ...any) *ScriptError, err error) error {
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
