package harness

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"

	opnview "github.com/NaejEL/opnview"

	"github.com/NaejEL/opnview/internal/broker"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/plugin/contract"
	"github.com/NaejEL/opnview/internal/plugin/fetch"
	"github.com/NaejEL/opnview/internal/plugin/mmdbwrite"
	"github.com/NaejEL/opnview/internal/plugin/scripthost"
	"github.com/NaejEL/opnview/internal/secret"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/accounts"
	"github.com/NaejEL/opnview/internal/store/flows"
	"github.com/NaejEL/opnview/internal/widget"
)

// A plugin's tests: JavaScript files in its own tests/ directory, run by the same interpreter that
// runs the plugin (docs/plugins.md, "Testing a plugin").
//
// NOBODY WRITING A PLUGIN WRITES, BUILDS OR RUNS GO. A plugin's tests are tests/*.test.js, each
// calling test(name, function (t) {...}); t is the test API below. They run here, in the binary:
// one generic test of the Go suite runs every built-in plugin's tests, and `opnview plugin test
// <directory>` runs a third party's, with nothing else installed.
//
// EACH TEST RUNS AGAINST A WORLD OF ITS OWN: a fake firewall answering only what the test arranged
// -- every other declared endpoint answers 404, the honest default, which is a component that is
// not installed -- through opnview's real OPNsense client, so a call is built, guarded and
// classified exactly as in production; a fresh database and data directory for settings, tables and
// fetched files; and a web that answers only what the test arranged, through the real fetch
// facility, so an undeclared host is refused exactly as in production. The plugin is loaded through
// the loader every plugin goes through, and t.call runs a provider through the same adapter the
// collector runs it through, contract check included.

// testReference is the instant a test's clock is stopped at, unless the test sets another.
var testReference = time.Date(2026, time.September, 27, 0, 0, 0, 0, time.UTC)

// TestResult is one test of a plugin.
type TestResult struct {
	File     string   `json:"file"`
	Name     string   `json:"name"`
	Passed   bool     `json:"passed"`
	Failures []string `json:"failures,omitempty"`
}

// RunTests loads the plugin in a directory and runs every test of its tests/ directory. A plugin
// that does not load is an error naming its refusal; a plugin with no test is no result.
func RunTests(ctx context.Context, directory string) ([]TestResult, error) {
	files, err := filepath.Glob(filepath.Join(directory, "tests", "*.test.js"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var results []TestResult
	for _, file := range files {
		source, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		fileResults, err := runTestFile(ctx, directory, filepath.Base(file), string(source))
		if err != nil {
			return nil, err
		}
		results = append(results, fileResults...)
	}
	return results, nil
}

// declaredTest is one test(name, fn) a test file declared.
type declaredTest struct {
	name     string
	function goja.Callable
}

// runTestFile runs every test one file declares.
func runTestFile(ctx context.Context, directory, file, source string) ([]TestResult, error) {
	vm := goja.New()
	var tests []declaredTest
	_ = vm.Set("test", func(call goja.FunctionCall) goja.Value {
		function, isFunction := goja.AssertFunction(call.Argument(1))
		if !isFunction {
			scripthost.Throw(vm, "test(name, function (t) {...})")
		}
		tests = append(tests, declaredTest{name: call.Argument(0).String(), function: function})
		return goja.Undefined()
	})
	_ = vm.Set("opnview", scripthost.Helpers(vm))
	if _, err := vm.RunScript(file, source); err != nil {
		return nil, fmt.Errorf("plugin: %s does not run: %w", file, err)
	}
	var results []TestResult
	for _, declared := range tests {
		result := TestResult{File: file, Name: declared.name}
		world, err := newTestWorld(ctx, directory)
		if err != nil {
			return nil, err
		}
		api := world.api(vm, &result)
		if _, err := declared.function(goja.Undefined(), api); err != nil {
			if !stopped(err) {
				result.Failures = append(result.Failures, "the test threw: "+err.Error())
			}
		}
		result.Failures = append(result.Failures, world.credentialLeaks()...)
		world.close()
		result.Passed = len(result.Failures) == 0
		results = append(results, result)
	}
	return results, nil
}

// testStop ends a test at its first fatal assertion.
type testStop struct{}

func (*testStop) Error() string { return "the test stopped" }

// testWorld is one test's firewall, database, data directory, web and plugin.
type testWorld struct {
	directory string
	dataDir   string
	database  *store.Store
	// testDatabase is the database of a plugin that owns tables, dropped at the end; settings is
	// where its settings are held: that database, or memory.
	testDatabase *store.TestDatabase
	settings     plugin.SettingStore
	box          *secret.Box
	plugin       *plugin.Plugin
	set          *provider.Set
	reference    time.Time

	server *httptest.Server
	client *opnsense.Client
	// gates are the brokers the calls went through, one per call, each closed with the world.
	gates []*broker.Broker

	mutex    sync.Mutex
	replies  map[string]testReply
	requests []testRequest
	web      map[string]testReply
	fetched  []string
	// key and secret are the generated credentials, and hostAnswers everything the host answered
	// the script, encoded: no answer may carry either, nor the firewall's address (BR-AC16).
	key, secret string
	hostAnswers []string
	// firewallVersion is the OPNsense version the test says the firewall runs, empty for unknown.
	firewallVersion string
	// widgets is the widget service serving the plugin's widgets, built at the first t.widget.
	widgets *widget.Service
	// push is the hub the push functions deliver through, built at the first one (testpush.go).
	push *pushWorld
}

// testReply is an arranged answer.
type testReply struct {
	status  int
	body    []byte
	headers map[string]string
	events  [][]byte
	forever bool
}

// testRequest is one request the fake firewall received.
type testRequest struct {
	Method    string         `json:"method"`
	Path      string         `json:"path"`
	Arguments []string       `json:"arguments"`
	Query     map[string]any `json:"query"`
	Form      map[string]any `json:"form"`
	JSON      any            `json:"json"`
	endpoint  string
}

// newTestWorld builds a world and loads the plugin into it.
func newTestWorld(ctx context.Context, directory string) (*testWorld, error) {
	dataDir, err := os.MkdirTemp("", "opnview-plugin-test-")
	if err != nil {
		return nil, err
	}
	world := &testWorld{directory: directory, dataDir: dataDir, replies: map[string]testReply{},
		web: map[string]testReply{}, reference: testReference}
	// A PLUGIN THAT OWNS NO TABLE IS TESTED WITH NO DATABASE: its settings are held in memory,
	// through the same validation, so `opnview plugin test` needs nothing but the binary. One that
	// declares tables gets a database of its own on the test instance OPNVIEW_TEST_DATABASE names
	// (store.NewTestDatabase), dropped when the test ends.
	world.settings = newMemorySettings()
	if ownsTables(directory) {
		database, err := store.NewTestDatabase(ctx, store.TestDatabaseOptions{})
		if err != nil {
			_ = os.RemoveAll(dataDir)
			return nil, fmt.Errorf("plugin: the plugin declares tables, so its tests need a PostgreSQL test "+
				"instance: %w", err)
		}
		world.testDatabase = database
		world.database, err = store.Open(ctx, database.DSN)
		if err != nil {
			world.close()
			return nil, err
		}
		world.settings = world.database
	}
	world.box, err = secret.OpenOrCreate(dataDir)
	if err != nil {
		world.close()
		return nil, err
	}
	world.server = httptest.NewServer(http.HandlerFunc(world.serveFirewall))
	// The fake firewall checks no credential; the client is handed generated ones, so no key or
	// secret is ever a literal.
	world.key, world.secret = randomCredential(), randomCredential()
	world.client = opnsense.NewClient(opnsense.Credentials{BaseURL: world.server.URL, APIKey: world.key,
		APISecret: world.secret}, nil, 10*time.Second)
	world.set = provider.NewSet()
	name := filepath.Base(directory)
	options := plugin.Options{DataDir: dataDir, Store: world.database, Settings: world.settings, Sealer: world.box,
		CallTimeout: 30 * time.Second, FetchTransport: world, HostAnswers: world.heard,
		// The core's values a SQL-only derivation reads, at their defaults: what a fresh installation runs with.
		DerivationParameters: func(context.Context) (map[string]any, error) {
			return map[string]any{"held_margin": int64(flows.DefaultDiscoveryIntervalSeconds),
				"leg_pairing_window": int64(flows.DefaultPairingWindowSeconds), "retention": config.DefaultRetentionSeconds}, nil
		}}
	taken := map[string]string{}
	required, err := requiredDirectories(directory)
	if err != nil {
		world.close()
		return nil, err
	}
	for _, each := range required {
		if _, refusal := plugin.LoadOne(ctx, world.set, each.directory, each.name, each.origin, each.location, each.onDisk,
			taken, options); refusal != nil {
			world.close()
			return nil, fmt.Errorf("the required plugin %s: %w", each.name, refusal)
		}
		taken[each.name] = each.origin
	}
	plugin, refusal := plugin.LoadOne(ctx, world.set, os.DirFS(directory), name, plugin.OriginDataDirectory, directory, directory,
		taken, options)
	if refusal != nil {
		world.close()
		return nil, refusal
	}
	world.plugin = plugin
	return world, nil
}

func (w *testWorld) close() {
	w.mutex.Lock()
	gates := w.gates
	w.mutex.Unlock()
	for _, gate := range gates {
		gate.Close()
	}
	if w.server != nil {
		w.server.Close()
	}
	if w.database != nil {
		_ = w.database.Close()
	}
	if w.testDatabase != nil {
		_ = w.testDatabase.Drop(context.Background())
	}
	_ = os.RemoveAll(w.dataDir)
}

// serveFirewall answers one request of the fake firewall: the arranged reply of the longest
// endpoint path the request names, or 404.
func (w *testWorld) serveFirewall(writer http.ResponseWriter, request *http.Request) {
	body, _ := io.ReadAll(request.Body)
	matched, name := "", ""
	for endpointName, endpoint := range w.plugin.Endpoints {
		// Every route of a versioned endpoint answers with the endpoint's arranged reply.
		paths := []string{endpoint.Path}
		for _, held := range w.plugin.Routes[endpointName] {
			paths = append(paths, held.Endpoint.Path)
		}
		for _, path := range paths {
			if request.URL.Path == path || strings.HasPrefix(request.URL.Path, path+"/") {
				if len(path) > len(matched) {
					matched, name = path, endpointName
				}
			}
		}
	}
	recorded := testRequest{Method: request.Method, Path: request.URL.Path, endpoint: name,
		Query: valuesMap(request.URL.Query()), Arguments: []string{}}
	if matched != "" && len(request.URL.Path) > len(matched) {
		recorded.Arguments = strings.Split(strings.TrimPrefix(request.URL.Path, matched+"/"), "/")
	}
	if strings.HasPrefix(request.Header.Get("Content-Type"), "application/json") {
		_ = json.Unmarshal(body, &recorded.JSON)
	} else if form, err := url.ParseQuery(string(body)); err == nil {
		recorded.Form = valuesMap(form)
	}
	w.mutex.Lock()
	w.requests = append(w.requests, recorded)
	reply, arranged := w.replies[name]
	w.mutex.Unlock()
	if name == "" || !arranged {
		writer.WriteHeader(http.StatusNotFound)
		_, _ = writer.Write([]byte(`{"errorMessage":"Endpoint not found","errorTitle":"Not Found"}`))
		return
	}
	if reply.events != nil {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		for _, event := range reply.events {
			_, _ = writer.Write([]byte("event: message\ndata: " + string(event) + "\n\n"))
		}
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		if reply.forever {
			<-request.Context().Done()
		}
		return
	}
	writer.WriteHeader(reply.status)
	_, _ = writer.Write(reply.body)
}

// RoundTrip answers the fetch facility's requests: the arranged reply of the URL without its query,
// or of the URL in full, or a refusal. Nothing leaves the process.
func (w *testWorld) RoundTrip(request *http.Request) (*http.Response, error) {
	w.mutex.Lock()
	authorization := ""
	if request.Header.Get("Authorization") != "" {
		authorization = " (authorized)"
	}
	w.fetched = append(w.fetched, request.Method+" "+request.URL.String()+authorization)
	reply, arranged := w.web[request.URL.String()]
	if !arranged {
		withoutQuery := *request.URL
		withoutQuery.RawQuery = ""
		reply, arranged = w.web[withoutQuery.String()]
	}
	w.mutex.Unlock()
	if !arranged {
		return nil, errors.New("plugin test: nothing is arranged for " + request.URL.String())
	}
	header := http.Header{}
	for name, value := range reply.headers {
		header.Set(name, value)
	}
	if etag := header.Get("ETag"); etag != "" && request.Header.Get("If-None-Match") == etag {
		return &http.Response{StatusCode: http.StatusNotModified, Header: header, Body: io.NopCloser(bytes.NewReader(nil)),
			Request: request}, nil
	}
	body := reply.body
	if request.Method == http.MethodHead {
		body = nil
	}
	return &http.Response{StatusCode: reply.status, Header: header, Body: io.NopCloser(bytes.NewReader(body)),
		Request: request}, nil
}

func valuesMap(values url.Values) map[string]any {
	result := map[string]any{}
	for key, list := range values {
		if len(list) == 1 {
			result[key] = list[0]
			continue
		}
		items := make([]any, 0, len(list))
		for _, item := range list {
			items = append(items, item)
		}
		result[key] = items
	}
	return result
}

// session is the port a call is handed: the broker over opnview's client and the fake firewall,
// with no mirror -- a plugin's tests read the firewall's answers as they are -- a firewall on UTC and
// a clock stopped at the world's reference. Each call has a broker of its own, so a stream it reads is
// a subscription of its own, as each pass's was.
func (w *testWorld) session() provider.Session {
	reference := w.reference
	// The broker reads the world's clock, so an answer's fetched_at is the world's instant: the
	// reference, advanced by the real time the test has run, which keeps every wait the broker makes
	// finite.
	gate := broker.New(w.client, nil, broker.Options{Clock: worldClock{reference: reference, started: time.Now()}})
	w.mutex.Lock()
	w.gates = append(w.gates, gate)
	w.mutex.Unlock()
	if w.plugin != nil {
		w.plugin.DeclareTo(gate)
	}
	w.mutex.Lock()
	version := w.firewallVersion
	w.mutex.Unlock()
	if version != "" {
		gate.SetFirewallVersion(version)
	}
	return provider.NewHost(gate, func(context.Context) (decode.Normaliser, error) {
		return decode.Normaliser{Reference: reference}, nil
	}, func() int64 { return reference.UTC().Unix() })
}

// fixture reads one of the plugin's recorded answers: testdata/<name>, in the labelled format
// {"__provenance__": {...}, "body": ...}, whose body is what the firewall answered.
func (w *testWorld) fixture(name string) ([]byte, error) {
	if strings.ContainsAny(name, `/\`) {
		return nil, fmt.Errorf("%q is not a file name of testdata/", name)
	}
	raw, err := os.ReadFile(filepath.Join(w.directory, "testdata", name))
	if err != nil {
		return nil, err
	}
	var labelled struct {
		Body json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(raw, &labelled); err != nil || labelled.Body == nil {
		return nil, fmt.Errorf("testdata/%s is not a labelled fixture {__provenance__, body}", name)
	}
	return labelled.Body, nil
}

// replyOf reads an arranged answer: {status, fixture | json | body, headers, events, forever}.
func (w *testWorld) replyOf(vm *goja.Runtime, value goja.Value) testReply {
	reply := testReply{status: http.StatusOK}
	object, isObject := value.(*goja.Object)
	if !isObject {
		scripthost.Throw(vm, "an answer is an object: {status, fixture | json | body, events}")
	}
	if status := object.Get("status"); status != nil && !goja.IsUndefined(status) {
		reply.status = int(status.ToInteger())
	}
	if fixture := object.Get("fixture"); fixture != nil && !goja.IsUndefined(fixture) {
		body, err := w.fixture(fixture.String())
		if err != nil {
			scripthost.Throw(vm, "%v", err)
		}
		if object.Get("stream") != nil && object.Get("stream").ToBoolean() {
			var stream struct {
				Events []json.RawMessage `json:"events"`
			}
			if err := json.Unmarshal(body, &stream); err != nil {
				scripthost.Throw(vm, "the stream fixture %s holds no events", fixture.String())
			}
			reply.events = [][]byte{}
			for _, event := range stream.Events {
				var compact bytes.Buffer
				_ = json.Compact(&compact, event)
				reply.events = append(reply.events, compact.Bytes())
			}
			reply.forever = true
		} else {
			reply.body = body
		}
	}
	if raw := object.Get("json"); raw != nil && !goja.IsUndefined(raw) {
		encoded, err := json.Marshal(raw.Export())
		if err != nil {
			scripthost.Throw(vm, "the json answer cannot be encoded: %v", err)
		}
		reply.body = encoded
	}
	if body := object.Get("body"); body != nil && !goja.IsUndefined(body) {
		reply.body = []byte(body.String())
	}
	if file := object.Get("file"); file != nil && !goja.IsUndefined(file) {
		raw, err := os.ReadFile(filepath.Join(w.directory, "testdata", filepath.Base(file.String())))
		if err != nil {
			scripthost.Throw(vm, "%v", err)
		}
		reply.body = raw
	}
	if archive := object.Get("mmdb_archive"); archive != nil && !goja.IsUndefined(archive) {
		raw, err := mmdbArchiveOf(archive.Export())
		if err != nil {
			scripthost.Throw(vm, "%v", err)
		}
		reply.body = raw
	}
	if events := object.Get("events"); events != nil && !goja.IsUndefined(events) {
		reply.events = [][]byte{}
		if list, isList := events.Export().([]any); isList {
			for _, event := range list {
				encoded, _ := json.Marshal(event)
				reply.events = append(reply.events, encoded)
			}
		}
		if forever := object.Get("forever"); forever != nil && !goja.IsUndefined(forever) {
			reply.forever = forever.ToBoolean()
		}
	}
	if headers, isMap := scripthost.ExportOf(object.Get("headers")).(map[string]any); isMap {
		reply.headers = map[string]string{}
		for name, value := range headers {
			reply.headers[name] = fmt.Sprint(value)
		}
	}
	return reply
}

// mmdbArchiveOf writes the archive an answer's mmdb_archive arranges: {edition, release,
// database_type, build_epoch, records: [{network, data}]}, a MaxMind DB of database_type (the
// edition when absent) in the gzip tar archive MaxMind serves.
func mmdbArchiveOf(value any) ([]byte, error) {
	arranged, isMap := value.(map[string]any)
	if !isMap {
		return nil, errors.New("mmdb_archive is {edition, release, database_type, build_epoch, records}")
	}
	edition, _ := arranged["edition"].(string)
	if edition == "" {
		return nil, errors.New("mmdb_archive names no edition")
	}
	databaseType, _ := arranged["database_type"].(string)
	if databaseType == "" {
		databaseType = edition
	}
	release, _ := arranged["release"].(string)
	if release == "" {
		release = "20260101"
	}
	var build uint64
	switch number := arranged["build_epoch"].(type) {
	case int64:
		build = uint64(number)
	case float64:
		build = uint64(number)
	}
	var records []mmdbwrite.Record
	list, _ := arranged["records"].([]any)
	for _, item := range list {
		entry, _ := item.(map[string]any)
		network, err := netip.ParsePrefix(fmt.Sprint(entry["network"]))
		if err != nil {
			return nil, fmt.Errorf("mmdb_archive: %w", err)
		}
		data, err := mmdbwrite.FromScript(entry["data"])
		if err != nil {
			return nil, err
		}
		fields, isFields := data.(map[string]any)
		if !isFields {
			return nil, errors.New("mmdb_archive: a record's data is an object")
		}
		records = append(records, mmdbwrite.Record{Network: network, Data: fields})
	}
	database, err := mmdbwrite.Build(databaseType, build, records)
	if err != nil {
		return nil, err
	}
	return mmdbwrite.Archive(edition, release, database)
}

// api is the t object a test is handed.
func (w *testWorld) api(vm *goja.Runtime, result *TestResult) *goja.Object {
	t := vm.NewObject()
	fail := func(message string) { result.Failures = append(result.Failures, message) }
	set := func(name string, function func(goja.FunctionCall) goja.Value) { _ = t.Set(name, function) }
	w.pushAPI(vm, set)

	set("answer", func(call goja.FunctionCall) goja.Value {
		name := call.Argument(0).String()
		if _, declared := w.plugin.Endpoints[name]; !declared {
			scripthost.Throw(vm, "the endpoint %q is not declared in the plugin's manifest", name)
		}
		reply := w.replyOf(vm, call.Argument(1))
		w.mutex.Lock()
		w.replies[name] = reply
		w.mutex.Unlock()
		return goja.Undefined()
	})
	set("firewallVersion", func(call goja.FunctionCall) goja.Value {
		w.mutex.Lock()
		w.firewallVersion = call.Argument(0).String()
		w.mutex.Unlock()
		return goja.Undefined()
	})
	set("web", func(call goja.FunctionCall) goja.Value {
		reply := w.replyOf(vm, call.Argument(1))
		w.mutex.Lock()
		w.web[call.Argument(0).String()] = reply
		w.mutex.Unlock()
		return goja.Undefined()
	})
	set("webRequests", func(goja.FunctionCall) goja.Value {
		w.mutex.Lock()
		defer w.mutex.Unlock()
		list := make([]any, 0, len(w.fetched))
		for _, fetched := range w.fetched {
			list = append(list, fetched)
		}
		return vm.ToValue(list)
	})
	set("requests", func(call goja.FunctionCall) goja.Value {
		all, name := goja.IsUndefined(call.Argument(0)), call.Argument(0).String()
		w.mutex.Lock()
		defer w.mutex.Unlock()
		var list []any
		for _, request := range w.requests {
			if all || request.endpoint == name {
				encoded, _ := json.Marshal(request)
				var decoded any
				_ = json.Unmarshal(encoded, &decoded)
				list = append(list, decoded)
			}
		}
		if list == nil {
			list = []any{}
		}
		return vm.ToValue(list)
	})
	set("clock", func(call goja.FunctionCall) goja.Value {
		w.reference = time.Unix(call.Argument(0).ToInteger(), 0).UTC()
		return goja.Undefined()
	})
	set("now", func(goja.FunctionCall) goja.Value { return vm.ToValue(w.reference.Unix()) })
	set("setting", func(call goja.FunctionCall) goja.Value {
		submitted := map[string]any{call.Argument(0).String(): call.Argument(1).Export()}
		if number, isInteger := submitted[call.Argument(0).String()].(int64); isInteger {
			submitted[call.Argument(0).String()] = float64(number)
		}
		if err := w.plugin.WriteSettings(context.Background(), w.settings, w.box, submitted,
			w.reference.Unix()); err != nil {
			scripthost.Throw(vm, "%v", err)
		}
		return goja.Undefined()
	})
	set("settingValues", func(goja.FunctionCall) goja.Value {
		values, err := w.plugin.SettingValues(context.Background(), w.settings)
		if err != nil {
			scripthost.Throw(vm, "%v", err)
		}
		encoded, _ := json.Marshal(values)
		var decoded any
		_ = json.Unmarshal(encoded, &decoded)
		return vm.ToValue(decoded)
	})
	// dataFile writes a file into the plugin's own data directory, as a previous run or a damaged
	// disk would have left it; dataFiles lists that directory, hidden files included.
	set("dataFile", func(call goja.FunctionCall) goja.Value {
		name := call.Argument(0).String()
		if name != filepath.Base(name) || strings.HasPrefix(name, ".") {
			scripthost.Throw(vm, "%q is not a file name of the plugin's data directory", name)
		}
		directory := fetch.DataDirectory(w.dataDir, w.plugin.Name)
		if err := os.MkdirAll(directory, 0o750); err != nil {
			scripthost.Throw(vm, "%v", err)
		}
		if err := os.WriteFile(filepath.Join(directory, name), []byte(call.Argument(1).String()), 0o640); err != nil {
			scripthost.Throw(vm, "%v", err)
		}
		return goja.Undefined()
	})
	set("dataFiles", func(goja.FunctionCall) goja.Value {
		entries, err := os.ReadDir(fetch.DataDirectory(w.dataDir, w.plugin.Name))
		list := []any{}
		if err == nil {
			for _, entry := range entries {
				list = append(list, entry.Name())
			}
		}
		return vm.ToValue(list)
	})
	set("registered", func(call goja.FunctionCall) goja.Value {
		kind := call.Argument(0).String()
		return vm.ToValue(w.plugin.Provides(kind))
	})
	set("value", func(call goja.FunctionCall) goja.Value {
		value, err := w.plugin.ExportedForTests(call.Argument(0).String(), call.Argument(1).String())
		if err != nil {
			scripthost.Throw(vm, "%v", err)
		}
		return vm.ToValue(value)
	})
	set("helper", func(call goja.FunctionCall) goja.Value {
		var arguments []any
		for _, argument := range call.Arguments[1:] {
			arguments = append(arguments, scripthost.JSONShape(argument.Export()))
		}
		answer, err := w.plugin.Call(context.Background(), nil, "helpers", call.Argument(0).String(), arguments...)
		if err != nil {
			scripthost.Throw(vm, "%v", err)
		}
		return vm.ToValue(answer)
	})
	set("discovery", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(provider.DiscoveryView(discoveryOf(call.Argument(0))))
	})
	set("call", func(call goja.FunctionCall) goja.Value {
		var arguments []any
		for _, argument := range call.Arguments[2:] {
			arguments = append(arguments, argument.Export())
		}
		return w.callProvider(vm, call.Argument(0).String(), call.Argument(1).String(), arguments)
	})
	set("assert", func(call goja.FunctionCall) goja.Value {
		if !call.Argument(0).ToBoolean() {
			fail(call.Argument(1).String())
		}
		return goja.Undefined()
	})
	set("equal", func(call goja.FunctionCall) goja.Value {
		got, _ := json.Marshal(scripthost.JSONShape(call.Argument(0).Export()))
		want, _ := json.Marshal(scripthost.JSONShape(call.Argument(1).Export()))
		if !bytes.Equal(got, want) {
			fail(fmt.Sprintf("%s: got %s, want %s", call.Argument(2).String(), got, want))
		}
		return goja.Undefined()
	})
	set("fatal", func(call goja.FunctionCall) goja.Value {
		fail(call.Argument(0).String())
		panic(vm.ToValue(&testStop{}))
	})
	w.widgetAPI(vm, t, fail)
	return t
}

// discoveryOf reads a test's discovery: {identifiers: {identifier: device}, devices: [names]}.
func discoveryOf(value goja.Value) provider.Discovery {
	snapshot := provider.Discovery{InterfaceIDByIdentifier: map[string]int64{}, InterfaceIDByDevice: map[string]int64{},
		Devices: map[string]struct{}{}, RuleIDByPfLabel: map[string]int64{}, Upstream: map[int64]struct{}{}}
	object, isObject := value.Export().(map[string]any)
	if !isObject {
		return snapshot
	}
	next := int64(1)
	if interfaces, isMap := object["interfaces"].(map[string]any); isMap {
		identifiers := make([]string, 0, len(interfaces))
		for identifier := range interfaces {
			identifiers = append(identifiers, identifier)
		}
		sort.Strings(identifiers)
		for _, identifier := range identifiers {
			device := fmt.Sprint(interfaces[identifier])
			snapshot.InterfaceIDByIdentifier[identifier] = next
			snapshot.InterfaceIDByDevice[device] = next
			snapshot.Devices[device] = struct{}{}
			next++
		}
	}
	if identifiers, isList := object["identifiers"].([]any); isList {
		for _, identifier := range identifiers {
			snapshot.Identifiers = append(snapshot.Identifiers, opnsense.InterfaceName(fmt.Sprint(identifier)))
		}
	} else {
		for identifier := range snapshot.InterfaceIDByIdentifier {
			snapshot.Identifiers = append(snapshot.Identifiers, opnsense.InterfaceName(identifier))
		}
	}
	if devices, isList := object["devices"].([]any); isList {
		for _, device := range devices {
			snapshot.Devices[fmt.Sprint(device)] = struct{}{}
		}
	}
	return snapshot
}

// callProvider runs one function of one of the plugin's providers through its adapter, contract
// check included, and returns what the kind would see: the checked answer under the contract's
// field names, its result, and its error, its unsupported reason or its failure code.
func (w *testWorld) callProvider(vm *goja.Runtime, kind, function string, arguments []any) goja.Value {
	reply := map[string]any{"error": nil, "unsupported": nil, "failure": nil}
	if function == "collect" && len(arguments) < 2 {
		// A test calling collect without saying which provider it is: the plugin's provider of the kind, with
		// the registry id its rows name and the kind's page size, as the collector hands them (ADR 0020 wave 4).
		arguments = append(arguments, w.providerArgument(kind))
	}
	if cadence, isLive := w.plugin.LiveCadenceFallback(kind, function); isLive && len(arguments) < 1 {
		// A live pass is handed the provider and its live cadence, at the declared default.
		argument := w.providerArgument(kind)
		argument["live_cadence_seconds"] = cadence
		arguments = append(arguments, argument)
	}
	checked, err := w.plugin.CallProvider(context.Background(), w.session(), kind, function, arguments)
	var refusal *plugin.CallRefusal
	if errors.As(err, &refusal) {
		scripthost.Throw(vm, "%s", refusal.Detail)
	}
	for key, value := range checked {
		reply[key] = value
	}
	if gaps, reported := checked["gaps"].([]provider.GapRead); reported {
		// The gaps a collect reported, as the script wrote them.
		list := make([]any, 0, len(gaps))
		for _, gap := range gaps {
			list = append(list, map[string]any{"from": gap.From, "to": gap.To, "reason": gap.Reason, "detail": gap.Detail})
		}
		reply["gaps"] = list
	}
	var failed *scripthost.Failure
	switch {
	case err == nil:
	case errors.As(err, &failed):
		reply["failure"] = string(failed.Code)
		reply["error"] = failed.Detail
	case errors.Is(err, provider.ErrUnsupportedRead):
		reply["unsupported"] = err.Error()
	default:
		reply["error"] = err.Error()
	}
	encoded, _ := json.Marshal(reply)
	var decoded any
	_ = json.Unmarshal(encoded, &decoded)
	return vm.ToValue(decoded)
}

// stopped reports whether a test ended at its first fatal assertion rather than by throwing.
func stopped(err error) bool {
	var exception *goja.Exception
	if !errors.As(err, &exception) {
		return false
	}
	_, isStop := exception.Value().Export().(*testStop)
	return isStop
}

// ownsTables reports whether the plugin in a directory needs a database to be tested: it declares
// tables, or it carries widgets, which read opnview's own. It is read from the manifest before the
// plugin is loaded; a manifest that does not read is left for the loader to refuse.
func ownsTables(directory string) bool {
	raw, err := os.ReadFile(filepath.Join(directory, contract.ManifestFile))
	if err != nil {
		return false
	}
	manifest, err := contract.DecodeManifest(raw)
	return err == nil && (len(manifest.Tables) > 0 || len(manifest.Widgets) > 0 || len(manifest.Relations) > 0 ||
		len(manifest.Statements) > 0 || len(manifest.Requires) > 0)
}

// requiredDirectories are the directories of the plugins a plugin under test requires (ADR 0018), each a
// sibling directory of the plugin's, or a built-in of that name: they load into the test world first, as
// they load into the service first.
func requiredDirectories(directory string) ([]requiredPlugin, error) {
	raw, err := os.ReadFile(filepath.Join(directory, contract.ManifestFile))
	if err != nil {
		return nil, nil
	}
	manifest, err := contract.DecodeManifest(raw)
	if err != nil {
		return nil, nil
	}
	// Transitively: a required plugin's own requirements come before it, each once.
	var ordered []requiredPlugin
	seen := map[string]bool{}
	var visit func(name string) error
	visit = func(name string) error {
		if seen[name] {
			return nil
		}
		seen[name] = true
		found, err := requiredPluginNamed(directory, name)
		if err != nil {
			return err
		}
		rawRequired, err := fs.ReadFile(found.directory, contract.ManifestFile)
		if err == nil {
			if requiredManifest, err := contract.DecodeManifest(rawRequired); err == nil {
				for _, further := range requiredManifest.Requires {
					if err := visit(further); err != nil {
						return err
					}
				}
			}
		}
		ordered = append(ordered, found)
		return nil
	}
	for _, name := range manifest.Requires {
		if err := visit(name); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

// requiredPluginNamed finds a required plugin: a sibling directory of the plugin under test, or a
// built-in of that name.
func requiredPluginNamed(directory, name string) (requiredPlugin, error) {
	sibling := filepath.Join(filepath.Dir(directory), name)
	if info, err := os.Stat(sibling); err == nil && info.IsDir() {
		return requiredPlugin{name: name, directory: os.DirFS(sibling), location: sibling, onDisk: sibling,
			origin: plugin.OriginDataDirectory}, nil
	}
	builtin, err := fs.Sub(opnview.BuiltinPlugins(), name)
	if err != nil {
		return requiredPlugin{}, fmt.Errorf("the plugin requires %s, which is neither a sibling directory nor a built-in", name)
	}
	if _, err := fs.Stat(builtin, contract.ManifestFile); err != nil {
		return requiredPlugin{}, fmt.Errorf("the plugin requires %s, which is neither a sibling directory nor a built-in", name)
	}
	return requiredPlugin{name: name, directory: builtin, location: path.Join(plugin.PluginsDirectory, name),
		origin: plugin.OriginBuiltin}, nil
}

// requiredPlugin is one plugin a test world loads before the plugin under test.
type requiredPlugin struct {
	name      string
	directory fs.FS
	location  string
	onDisk    string
	origin    string
}

// memorySettings holds a test's settings and sealed secrets in memory.
type memorySettings struct {
	mutex       sync.Mutex
	values      map[string]string
	credentials map[string]accounts.EncryptedCredential
}

func newMemorySettings() *memorySettings {
	return &memorySettings{values: map[string]string{}, credentials: map[string]accounts.EncryptedCredential{}}
}

func (m *memorySettings) Setting(_ context.Context, key string) (string, bool, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	value, present := m.values[key]
	return value, present, nil
}

func (m *memorySettings) SetSetting(_ context.Context, key, value string, _ int64) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.values[key] = value
	return nil
}

func (m *memorySettings) DeleteSetting(_ context.Context, key string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	delete(m.values, key)
	return nil
}

func (m *memorySettings) Credential(_ context.Context, name string) (accounts.EncryptedCredential, bool, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	credential, present := m.credentials[name]
	return credential, present, nil
}

func (m *memorySettings) SetCredential(_ context.Context, credential accounts.EncryptedCredential) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.credentials[credential.Name] = credential
	return nil
}

// randomCredential is a generated credential for the fake firewall.
func randomCredential() string {
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}

// heard keeps one host answer, encoded.
func (w *testWorld) heard(function string, answer any) {
	encoded, err := json.Marshal(answer)
	if err != nil {
		encoded = []byte(fmt.Sprint(answer))
	}
	w.mutex.Lock()
	w.hostAnswers = append(w.hostAnswers, function+" "+string(encoded))
	w.mutex.Unlock()
}

// credentialLeaks names every host answer that carries the API key, the secret, the firewall's
// address or an Authorization value: a credential never reaches a plugin (BR-AC16).
func (w *testWorld) credentialLeaks() []string {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	forbidden := map[string]string{"the API key": w.key, "the API secret": w.secret, "the base URL": w.server.URL,
		"the firewall's address": w.server.Listener.Addr().String(), "an Authorization header": "Authorization",
		"a Basic credential": "Basic "}
	names := make([]string, 0, len(forbidden))
	for name := range forbidden {
		names = append(names, name)
	}
	sort.Strings(names)
	var leaks []string
	for _, heard := range w.hostAnswers {
		for _, name := range names {
			if strings.Contains(heard, forbidden[name]) {
				function, _, _ := strings.Cut(heard, " ")
				leaks = append(leaks, fmt.Sprintf("host.%s answered the script %s", function, name))
			}
		}
	}
	return leaks
}

// worldClock is a test world's clock as the broker reads it: the world's reference, advanced by the
// real time elapsed since the call began.
type worldClock struct {
	reference, started time.Time
}

// Now is the reference plus the real time elapsed.
func (c worldClock) Now() time.Time { return c.reference.Add(time.Since(c.started)) }

// After waits in real time.
func (worldClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// providerArgument is what the collector hands a collect as its provider: the plugin's provider of the
// kind, its registry id on the test database when there is one, and the kind's page size at its default.
func (w *testWorld) providerArgument(kind string) map[string]any {
	key := w.plugin.Name
	for _, declared := range w.plugin.Providers {
		if declared.Kind == kind {
			key = declared.Key
		}
	}
	argument := map[string]any{"kind": kind, "key": key, "id": int64(0), "page_size": int64(0)}
	if contract, core := provider.ContractOf(kind); core && contract.PageSize != nil {
		argument["page_size"] = int64(contract.PageSize.Default)
	}
	if w.database != nil {
		if id, err := w.database.ProviderID(context.Background(), kind, key); err == nil {
			argument["id"] = id
		}
	}
	return argument
}
