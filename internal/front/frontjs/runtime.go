package frontjs

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/dop251/goja"

	opnview "github.com/NaejEL/opnview"
)

//go:embed dom.js
var domShim string

// Request is one request the front made.
type Request struct {
	Method  string
	Path    string
	Body    string
	Headers map[string]string
}

// Answer is the fake server's answer to one request: a status and a body, marshalled to JSON.
type Answer struct {
	Status int
	Body   any
}

// Options set the browser the front runs in.
type Options struct {
	// Handler answers every request; nil answers 404.
	Handler func(Request) Answer
	// PrefersDark is the operating system's colour-scheme preference.
	PrefersDark bool
	// Languages are the browser's languages, most wanted first; nil is English.
	Languages []string
}

// Runtime is one page of the front.
type Runtime struct {
	t        testing.TB
	VM       *goja.Runtime
	options  Options
	mutex    sync.Mutex
	requests []Request
	now      int64
	timers   []*timer
	next     int64
}

type timer struct {
	id   int64
	due  int64
	call goja.Callable
}

// sources caches the front's files, read once.
var (
	sourcesOnce sync.Once
	sourcesList []source
	sourcesErr  error
)

type source struct {
	name    string
	text    string
	program *goja.Program
}

// compiled compiles every source once: a page runs the compiled programs, so a test that opens many
// pages pays for the parsing once.
func compiled(sources []source) error {
	for index := range sources {
		if sources[index].program != nil {
			continue
		}
		program, err := goja.Compile(sources[index].name, sources[index].text, false)
		if err != nil {
			return fmt.Errorf("compiling %s: %w", sources[index].name, err)
		}
		sources[index].program = program
	}
	return nil
}

// ShellScripts returns the files the shell loads, in its order, below the front's root.
func ShellScripts() ([]string, error) {
	raw, err := fs.ReadFile(opnview.BuiltinFront(), "shell.html")
	if err != nil {
		return nil, err
	}
	var scripts []string
	rest := string(raw)
	for {
		start := strings.Index(rest, `src="/front/`)
		if start < 0 {
			return scripts, nil
		}
		rest = rest[start+len(`src="/front/`):]
		end := strings.Index(rest, `"`)
		scripts = append(scripts, rest[:end])
		rest = rest[end:]
	}
}

func frontSources() ([]source, error) {
	sourcesOnce.Do(func() {
		scripts, err := ShellScripts()
		if err != nil {
			sourcesErr = err
			return
		}
		for _, name := range scripts {
			text, err := fs.ReadFile(opnview.BuiltinFront(), name)
			if err != nil {
				sourcesErr = err
				return
			}
			sourcesList = append(sourcesList, source{name: name, text: string(text)})
		}
		sourcesErr = compiled(sourcesList)
	})
	return sourcesList, sourcesErr
}

// renderer sources: every shape renderer of the built-in front and every renderer a built-in
// widget's manifest names, read once. Nothing lists them: the shapes are the files of front/shapes/,
// the widgets' renderers the entries their manifests declare.
var (
	renderersOnce sync.Once
	renderersList []source
	renderersErr  error
)

// RendererSources returns the shape renderers and the built-in widgets' renderers, by path.
func RendererSources() ([]struct{ Name, Text string }, error) {
	renderersOnce.Do(func() {
		front := opnview.BuiltinFront()
		shapes, err := fs.ReadDir(front, "shapes")
		if err != nil {
			renderersErr = err
			return
		}
		for _, entry := range shapes {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".js") {
				continue
			}
			text, err := fs.ReadFile(front, "shapes/"+entry.Name())
			if err != nil {
				renderersErr = err
				return
			}
			renderersList = append(renderersList, source{name: "front/shapes/" + entry.Name(), text: string(text)})
		}
		plugins := opnview.BuiltinPlugins()
		manifests, err := fs.Glob(plugins, "*/widgets/*/manifest.json")
		if err != nil {
			renderersErr = err
			return
		}
		sort.Strings(manifests)
		for _, manifest := range manifests {
			raw, err := fs.ReadFile(plugins, manifest)
			if err != nil {
				renderersErr = err
				return
			}
			var declared struct {
				Renderer *struct {
					Entry string `json:"entry"`
				} `json:"renderer"`
			}
			if err := json.Unmarshal(raw, &declared); err != nil {
				renderersErr = fmt.Errorf("%s: %w", manifest, err)
				return
			}
			if declared.Renderer == nil {
				continue
			}
			name := strings.TrimSuffix(manifest, "manifest.json") + declared.Renderer.Entry
			text, err := fs.ReadFile(plugins, name)
			if err != nil {
				renderersErr = err
				return
			}
			renderersList = append(renderersList, source{name: "plugins/" + name, text: string(text)})
		}
		renderersErr = compiled(renderersList)
	})
	out := make([]struct{ Name, Text string }, 0, len(renderersList))
	for _, file := range renderersList {
		out = append(out, struct{ Name, Text string }{file.name, file.text})
	}
	return out, renderersErr
}

// LoadRenderers runs every shape renderer and every built-in widget's renderer in the page, as the
// canvas loads them in a browser.
func (r *Runtime) LoadRenderers() {
	r.t.Helper()
	if _, err := RendererSources(); err != nil {
		r.t.Fatalf("frontjs: reading the renderers: %v", err)
	}
	for _, file := range renderersList {
		if _, err := r.VM.RunProgram(file.program); err != nil {
			r.t.Fatalf("frontjs: running %s: %v", file.name, err)
		}
	}
}

// New starts a page: the document, the fake browser, and every file the shell loads.
func New(t testing.TB, options Options) *Runtime {
	t.Helper()
	runtime := &Runtime{t: t, VM: goja.New(), options: options, next: 1}
	if err := runtime.install(); err != nil {
		t.Fatalf("frontjs: installing the browser: %v", err)
	}
	sources, err := frontSources()
	if err != nil {
		t.Fatalf("frontjs: reading the front: %v", err)
	}
	for _, file := range sources {
		if _, err := runtime.VM.RunProgram(file.program); err != nil {
			t.Fatalf("frontjs: running %s: %v", file.name, err)
		}
	}
	return runtime
}

// install defines the document and what the front reads of the window.
func (r *Runtime) install() error {
	vm := r.VM
	if _, err := vm.RunScript("dom.js", domShim); err != nil {
		return err
	}
	global := vm.GlobalObject()
	_ = global.Set("window", global)
	_ = global.Set("__fetch", func(call goja.FunctionCall) goja.Value {
		answer := r.serve(call.Argument(0).String(), call.Argument(1).String(), call.Argument(2).String(),
			call.Argument(3).String())
		return vm.ToValue(answer)
	})
	_ = global.Set("setTimeout", func(call goja.FunctionCall) goja.Value {
		callback, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			panic(vm.NewTypeError("setTimeout takes a function"))
		}
		r.mutex.Lock()
		defer r.mutex.Unlock()
		id := r.next
		r.next++
		r.timers = append(r.timers, &timer{id: id, due: r.now + call.Argument(1).ToInteger(), call: callback})
		return vm.ToValue(id)
	})
	_ = global.Set("clearTimeout", func(call goja.FunctionCall) goja.Value {
		id := call.Argument(0).ToInteger()
		r.mutex.Lock()
		defer r.mutex.Unlock()
		for index, pending := range r.timers {
			if pending.id == id {
				r.timers = append(r.timers[:index], r.timers[index+1:]...)
				break
			}
		}
		return goja.Undefined()
	})
	languages, err := json.Marshal(r.options.Languages)
	if err != nil {
		return err
	}
	if r.options.Languages == nil {
		languages = []byte(`["en"]`)
	}
	_, err = vm.RunScript("browser.js", fmt.Sprintf(`(function (root) {
  "use strict";
  var windowListeners = {};
  root.addEventListener = function (type, handler) { (windowListeners[type] || (windowListeners[type] = [])).push(handler); };
  root.__windowEvent = function (type) { (windowListeners[type] || []).forEach(function (handler) { handler({ type: type }); }); };
  root.location = { pathname: "/", search: "" };
  function setURL(url) {
    var cut = url.indexOf("?");
    root.location.pathname = cut < 0 ? url : url.slice(0, cut);
    root.location.search = cut < 0 ? "" : url.slice(cut);
  }
  root.history = {
    entries: [],
    pushState: function (state, title, url) { this.entries.push(url); setURL(url); },
    replaceState: function (state, title, url) { setURL(url); }
  };
  root.__setURL = setURL;
  root.navigator = { languages: %s, language: %s[0] };
  root.__prefersDark = %t;
  root.matchMedia = function (query) {
    return { matches: query.indexOf("dark") >= 0 ? root.__prefersDark : !root.__prefersDark,
      addEventListener: function () {} };
  };
  root.fetch = function (path, init) {
    var settings = init || {};
    var answer = root.__fetch(settings.method || "GET", path, JSON.stringify(settings.headers || {}),
      settings.body === undefined ? "" : settings.body);
    return Promise.resolve({ status: answer.status, text: function () { return Promise.resolve(answer.body); } });
  };
})(this);`, languages, languages, r.options.PrefersDark))
	return err
}

// serve answers one request through the test's handler, and records it.
func (r *Runtime) serve(method, path, headers, body string) map[string]any {
	request := Request{Method: method, Path: path, Body: body, Headers: map[string]string{}}
	_ = json.Unmarshal([]byte(headers), &request.Headers)
	r.mutex.Lock()
	r.requests = append(r.requests, request)
	r.mutex.Unlock()
	answer := Answer{Status: 404, Body: map[string]string{"condition": "unknown_route"}}
	if r.options.Handler != nil {
		answer = r.options.Handler(request)
	}
	encoded := ""
	if answer.Body != nil {
		raw, err := json.Marshal(answer.Body)
		if err != nil {
			r.t.Errorf("frontjs: the fake answer to %s %s does not encode: %v", method, path, err)
		}
		encoded = string(raw)
	}
	return map[string]any{"status": answer.Status, "body": encoded}
}

// Run runs a script and returns its value, failing the test when it throws.
func (r *Runtime) Run(script string) goja.Value {
	r.t.Helper()
	value, err := r.VM.RunString(script)
	if err != nil {
		r.t.Fatalf("frontjs: %v\nin: %s", err, script)
	}
	return value
}

// Requests returns every request made so far, in order.
func (r *Runtime) Requests() []Request {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return append([]Request(nil), r.requests...)
}

// Writes returns every POST made so far, in order.
func (r *Runtime) Writes() []Request {
	var writes []Request
	for _, request := range r.Requests() {
		if request.Method == "POST" {
			writes = append(writes, request)
		}
	}
	return writes
}

// Advance moves the fake clock and runs every timer that falls due, in order.
func (r *Runtime) Advance(milliseconds int64) {
	r.t.Helper()
	target := r.now + milliseconds
	for {
		r.mutex.Lock()
		sort.SliceStable(r.timers, func(i, j int) bool { return r.timers[i].due < r.timers[j].due })
		if len(r.timers) == 0 || r.timers[0].due > target {
			r.now = target
			r.mutex.Unlock()
			return
		}
		due := r.timers[0]
		r.timers = r.timers[1:]
		r.now = due.due
		r.mutex.Unlock()
		if _, err := due.call(goja.Undefined()); err != nil {
			r.t.Fatalf("frontjs: a timer threw: %v", err)
		}
	}
}

// Start opens the front at a path in a fresh container, as the shell does in a browser.
func (r *Runtime) Start(path string) {
	r.t.Helper()
	r.Run(fmt.Sprintf(`(function () {
  __setURL(%q);
  var app = document.createElement("div");
  app.setAttribute("id", "app");
  document.body.appendChild(app);
  opnview.shell.start(app);
})();`, path))
}

// Go moves the front to a path, as following a link does.
func (r *Runtime) Go(path string) {
	r.t.Helper()
	r.Run(fmt.Sprintf(`opnview.shell.go(%q);`, path))
}

// Path is the address bar's path.
func (r *Runtime) Path() string {
	return r.Run(`location.pathname + location.search`).String()
}

// Count returns how many elements match a selector.
func (r *Runtime) Count(selector string) int {
	r.t.Helper()
	return int(r.Run(fmt.Sprintf(`document.querySelectorAll(%q).length`, selector)).ToInteger())
}

// Text returns the text of the first element matching a selector, failing when none does.
func (r *Runtime) Text(selector string) string {
	r.t.Helper()
	r.must(selector)
	return r.Run(fmt.Sprintf(`document.querySelector(%q).textContent`, selector)).String()
}

// Attribute returns an attribute of the first element matching a selector.
func (r *Runtime) Attribute(selector, name string) string {
	r.t.Helper()
	r.must(selector)
	value := r.Run(fmt.Sprintf(`document.querySelector(%q).getAttribute(%q)`, selector, name))
	if goja.IsNull(value) || goja.IsUndefined(value) {
		return ""
	}
	return value.String()
}

func (r *Runtime) must(selector string) {
	r.t.Helper()
	if r.Count(selector) == 0 {
		r.t.Fatalf("frontjs: nothing matches %q in:\n%s", selector, r.Outline())
	}
}

// Input types a value into a field and tells the page, as a reader typing does.
func (r *Runtime) Input(selector, value string) {
	r.t.Helper()
	r.must(selector)
	r.Run(fmt.Sprintf(`(function () {
  var element = document.querySelector(%q);
  element.focus();
  element.value = %q;
  element.dispatchEvent(__makeEvent("input"));
})();`, selector, value))
}

// Choose selects an option of a select, or sets a box, and tells the page.
func (r *Runtime) Choose(selector, value string) {
	r.t.Helper()
	r.must(selector)
	r.Run(fmt.Sprintf(`(function () {
  var element = document.querySelector(%q);
  if (element.getAttribute("type") === "checkbox") { element.checked = %q === "true"; } else { element.value = %q; }
  element.dispatchEvent(__makeEvent("change"));
})();`, selector, value, value))
}

// Click clicks an element: a submit button submits its form.
func (r *Runtime) Click(selector string) {
	r.t.Helper()
	r.must(selector)
	r.Run(fmt.Sprintf(`(function () {
  var element = document.querySelector(%q);
  element.focus();
  element.dispatchEvent(__makeEvent("click"));
})();`, selector))
}

// Outline is the document as an indented outline of elements and text, for a failure message.
func (r *Runtime) Outline() string {
	value := r.Run(`(function () {
  var lines = [];
  (function walk(node, depth) {
    var pad = new Array(depth + 1).join("  ");
    if (node.nodeType === 3) { if (node.nodeValue.trim()) { lines.push(pad + JSON.stringify(node.nodeValue)); } return; }
    var attributes = Object.keys(node.attributes).sort().map(function (name) {
      return name + "=" + JSON.stringify(node.attributes[name]);
    }).join(" ");
    lines.push(pad + "<" + node.tagName.toLowerCase() + (attributes ? " " + attributes : "") + ">");
    node.childNodes.forEach(function (child) { walk(child, depth + 1); });
  })(document.body, 0);
  return lines.join("\n");
})()`)
	return value.String()
}
