package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/plugin/scripthost"
	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/NaejEL/opnview/internal/widget"
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/params"
	"github.com/NaejEL/opnview/internal/widget/request"
)

// What a widget plugin's tests add to the test API (docs/plugins.md, "Testing a widget").
//
// A WIDGET IS TESTED AGAINST A DATABASE: the plugin's world gets one of its own, with opnview's
// schema applied, as a plugin that declares tables does. The test arranges rows with t.sql, asks
// the widget with t.widget exactly as GET /api/widget/{type} asks it -- the same parameter parsing,
// the same envelope, the same script host -- and compares the answer with a golden file it keeps
// in its own tests/ directory with t.golden.
//
//	t.sql(statement, parameters)     runs one statement on the test database, as its owner: the
//	                                 arrangement of a test, never what a widget can do
//	t.query(text, parameters)        the rows a SELECT returns, on the read role, as a test reads them
//	t.derive(name)                   runs one of the plugin's derivations now: {ran, written, error}
//	t.widget(type, parameters)       {status, condition, body, raw}: the response as the API writes
//	                                 it -- body parsed, raw the exact bytes
//	t.golden(file, raw)              compares raw with tests/<file>; with OPNVIEW_WRITE_GOLDEN=1 in the
//	                                 environment, writes it instead

// goldenFileName reports whether a name is a golden file's: a plain .json file of tests/.
func goldenFileName(name string) bool {
	return name == filepath.Base(name) && strings.HasSuffix(name, ".json") && !strings.HasPrefix(name, ".")
}

// widgetAPI adds the widget functions to a test's t object.
func (w *testWorld) widgetAPI(vm *goja.Runtime, t *goja.Object, fail func(string)) {
	set := func(name string, function func(goja.FunctionCall) goja.Value) { _ = t.Set(name, function) }
	set("sql", func(call goja.FunctionCall) goja.Value {
		if w.database == nil {
			scripthost.Throw(vm, "t.sql needs a database: the plugin declares neither a table nor a widget")
		}
		parameters := map[string]any{}
		if object, isObject := call.Argument(1).(*goja.Object); isObject {
			parameters, _ = object.Export().(map[string]any)
		}
		if _, err := sqldb.Exec(context.Background(), w.database.DB(), call.Argument(0).String(), parameters); err != nil {
			scripthost.Throw(vm, "t.sql: %v", err)
		}
		return goja.Undefined()
	})
	set("query", func(call goja.FunctionCall) goja.Value {
		if w.database == nil {
			scripthost.Throw(vm, "t.query needs a database: the plugin declares neither a table nor a widget")
		}
		parameters := map[string]any{}
		if object, isObject := call.Argument(1).(*goja.Object); isObject {
			parameters, _ = object.Export().(map[string]any)
		}
		rows, err := w.database.ReadRows(context.Background(), call.Argument(0).String(), parameters)
		if err != nil {
			scripthost.Throw(vm, "t.query: %v", err)
		}
		list := make([]any, 0, len(rows))
		for _, row := range rows {
			list = append(list, plugin.ReadableRow(row))
		}
		return vm.ToValue(list)
	})
	set("derive", func(call goja.FunctionCall) goja.Value {
		outcome, err := w.plugin.RunDerivation(context.Background(), w.session(), call.Argument(0).String(),
			w.reference.Unix())
		var refusal *plugin.CallRefusal
		if errors.As(err, &refusal) {
			scripthost.Throw(vm, "t.derive: %s", refusal.Detail)
		}
		result := vm.NewObject()
		_ = result.Set("ran", outcome.Ran)
		_ = result.Set("written", outcome.Written)
		if err != nil {
			_ = result.Set("error", err.Error())
		} else {
			_ = result.Set("error", nil)
		}
		return result
	})
	set("widget", func(call goja.FunctionCall) goja.Value {
		widgetType := call.Argument(0).String()
		service, err := w.widgetService()
		if err != nil {
			scripthost.Throw(vm, "t.widget: %v", err)
		}
		for _, refusal := range service.Refusals() {
			if refusal.Type == widgetType {
				scripthost.Throw(vm, "t.widget: %v", &refusal)
			}
		}
		raw := ""
		if parameters := call.Argument(1); !goja.IsUndefined(parameters) && !goja.IsNull(parameters) {
			encoded, err := json.Marshal(parameters.Export())
			if err != nil {
				scripthost.Throw(vm, "t.widget: the parameters: %v", err)
			}
			raw = string(encoded)
		}
		status, body := w.respond(service, widgetType, raw)
		var parsed any
		_ = json.Unmarshal(body, &parsed)
		condition := ""
		if object, isObject := parsed.(map[string]any); isObject {
			condition, _ = object["condition"].(string)
		}
		result := vm.NewObject()
		_ = result.Set("status", status)
		_ = result.Set("condition", condition)
		_ = result.Set("body", vm.ToValue(parsed))
		_ = result.Set("raw", string(body))
		return result
	})
	set("golden", func(call goja.FunctionCall) goja.Value {
		name := call.Argument(0).String()
		if !goldenFileName(name) {
			scripthost.Throw(vm, "t.golden: %q is not a .json file name of the plugin's tests/ directory", name)
		}
		var got bytes.Buffer
		if err := json.Indent(&got, []byte(call.Argument(1).String()), "", "  "); err != nil {
			scripthost.Throw(vm, "t.golden: the value is not JSON: %v", err)
		}
		got.WriteByte('\n')
		path := filepath.Join(w.directory, "tests", name)
		if os.Getenv("OPNVIEW_WRITE_GOLDEN") == "1" {
			if err := os.WriteFile(path, got.Bytes(), 0o644); err != nil {
				scripthost.Throw(vm, "t.golden: %v", err)
			}
			return goja.Undefined()
		}
		want, err := os.ReadFile(path)
		if err != nil {
			fail(fmt.Sprintf("t.golden: %s cannot be read (%v); write it with OPNVIEW_WRITE_GOLDEN=1", name, err))
			return goja.Undefined()
		}
		var compactWant, compactGot bytes.Buffer
		if err := json.Compact(&compactWant, want); err != nil {
			fail(fmt.Sprintf("t.golden: %s is not JSON: %v", name, err))
			return goja.Undefined()
		}
		_ = json.Compact(&compactGot, got.Bytes())
		if !bytes.Equal(compactWant.Bytes(), compactGot.Bytes()) {
			fail(fmt.Sprintf("t.golden: the answer differs from %s\nwant: %s\ngot:  %s", name, compactWant.String(),
				compactGot.String()))
		}
		return goja.Undefined()
	})
}

// widgetService is the widget service over the test's database, serving the plugin's widgets at the
// test's instant.
func (w *testWorld) widgetService() (*widget.Service, error) {
	if w.database == nil {
		return nil, errors.New("the plugin carries no widget")
	}
	if w.widgets != nil {
		return w.widgets, nil
	}
	service, err := widget.New(w.database, widget.Options{Now: func() time.Time { return w.reference },
		Plugins: w.plugin.Widgets})
	if err != nil {
		return nil, err
	}
	w.widgets = service
	return service, nil
}

// respond answers one request as GET /api/widget/{type} answers it: its status and its body.
func (w *testWorld) respond(service *widget.Service, widgetType, parameters string) (int, []byte) {
	response, err := service.Respond(context.Background(), widgetType, parameters)
	var (
		malformed params.ErrMalformed
		failed    *request.ScriptError
	)
	var (
		status int
		body   any
	)
	switch {
	case errors.As(err, &failed):
		status, body = 500, widget.Failure{Condition: request.ConditionScriptFailed, Type: widgetType, Reason: failed.Detail}
	case errors.As(err, &malformed):
		status, body = 400, widget.Failure{Condition: envelope.ConditionMalformedParameters, Type: widgetType}
	case errors.Is(err, widget.ErrUnknownType):
		status, body = 404, widget.Failure{Condition: envelope.ConditionUnknownWidgetType, Type: widgetType}
	case err != nil:
		status, body = 500, widget.Failure{Condition: "read_failed", Type: widgetType, Reason: err.Error()}
	default:
		status, body = 200, response
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return 500, []byte(`{"condition":"encoding_failed"}`)
	}
	return status, encoded
}
