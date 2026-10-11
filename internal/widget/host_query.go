package widget

import (
	"math"
	"sort"

	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/store"
)

// host.query: the widget's own statements -- those its manifest's `queries` files declare, by the
// name each file gives them -- run on the read-only role with bound parameters, under the request's
// time bound. Nothing else of the database is reachable from a script: no other widget's statement,
// no text the script writes.
//
//	host.query.rows(name, parameters, period)  every row, each an object of its columns in order
//	                                           -- under the netflow measure, the rows of the
//	                                           statement's twin name_netflow when the widget declares
//	                                           one (measure.go)
//	host.query.row(name, parameters, period)   the first row, or null when there is none
//	host.query.carriesDomain(name)             whether the statement reads a column carrying a
//	                                           domain name, which aggregate_mode = no_domains withholds
//
// parameters is an object binding the statement's :name parameters: a key the statement does not
// name is refused, and one it names that is left out is bound to null. A number, a string, a
// boolean and null bind as themselves; an array binds as its JSON text, which is how every statement
// of the tree takes a set (host.refs.jsonIds). period names the period a template statement
// (@period@) runs at -- "1h", "24h", "7d" or "30d" -- and is left out for any other.
//
// A cell is a number, a string or null: the store reads an integer sum as an integer and a
// comparison as 0 or 1 (internal/store, driver.go). A column name a result repeats is refused,
// since an object holds a name once: give the column a name of its own with AS.

func init() {
	registerHostArea("query", func(call *scriptCall, area *goja.Object) {
		call.function(area, "rows", func(arguments goja.FunctionCall) goja.Value {
			columns, rows, failed := call.runStatement(arguments)
			if failed {
				return goja.Undefined()
			}
			list := make([]any, 0, len(rows))
			for _, row := range rows {
				list = append(list, call.rowObject(columns, row))
			}
			return call.vm.NewArray(list...)
		})
		call.function(area, "row", func(arguments goja.FunctionCall) goja.Value {
			columns, rows, failed := call.runStatement(arguments)
			if failed {
				return goja.Undefined()
			}
			if len(rows) == 0 {
				return goja.Null()
			}
			return call.rowObject(columns, rows[0])
		})
		call.function(area, "carriesDomain", func(arguments goja.FunctionCall) goja.Value {
			statement := call.statementOf(arguments.Argument(0))
			return call.vm.ToValue(statement.domain)
		})
	})
}

// statementOf is a statement the widget declares, by the name its file gives it.
func (c *scriptCall) statementOf(value goja.Value) widgetStatement {
	name := c.textOf(value, "the statement name")
	statement, declared := c.widget.statements[name]
	if !declared {
		c.throw("the widget declares no statement %q in its query files", name)
	}
	return statement
}

// runStatement runs the statement a call of rows or row names. It reports a failed read, which
// has already stopped the script.
func (c *scriptCall) runStatement(arguments goja.FunctionCall) ([]string, [][]any, bool) {
	name := c.textOf(arguments.Argument(0), "the statement name")
	statement := c.statementOf(arguments.Argument(0))
	if c.request.Netflow() {
		// Under the netflow measure a statement that declares its twin is run as its twin (measure.go).
		if twin, declared := c.widget.statements[name+netflowStatementSuffix]; declared {
			name, statement = name+netflowStatementSuffix, twin
		}
	}
	bound := c.bindParameters(name, statement, arguments.Argument(1))
	period := store.Period{}
	switch {
	case statement.template && absent(arguments.Argument(2)):
		c.throw("the statement %s is a period template and runs at a period: \"1h\", \"24h\", \"7d\" or \"30d\"", name)
	case statement.template:
		named, err := store.PeriodNamed(c.textOf(arguments.Argument(2), "the period"))
		if err != nil {
			c.throw("the statement %s: %v", name, err)
		}
		period = named
	case !absent(arguments.Argument(2)):
		c.throw("the statement %s is not a period template and runs at no period", name)
	}
	r := c.request
	if statement.domain && r.NoDomains() && !c.widget.builtin {
		c.fail(errWithheldUnderNoDomains)
		return nil, nil, true
	}
	columns, rows, err := r.Db.WidgetQuery(r.Ctx, statement.registered, period, bound)
	if err != nil {
		c.fail(err)
		return nil, nil, true
	}
	seen := map[string]bool{}
	for _, column := range columns {
		if seen[column] {
			c.throw("the statement %s returns two columns named %q; name each with AS", name, column)
		}
		seen[column] = true
	}
	return columns, rows, false
}

// bindParameters reads a call's parameters object against the statement's parameters.
func (c *scriptCall) bindParameters(name string, statement widgetStatement, value goja.Value) map[string]any {
	bound := map[string]any{}
	for _, parameter := range statement.parameters {
		bound[parameter] = nil
	}
	if absent(value) {
		return bound
	}
	object, isObject := value.(*goja.Object)
	if !isObject || object.ClassName() != "Object" {
		c.throw("the parameters of %s are %s, not an object", name, describe(value))
	}
	keys := object.Keys()
	sort.Strings(keys)
	for _, key := range keys {
		if _, named := bound[key]; !named {
			c.throw("the statement %s names no parameter :%s", name, key)
		}
		bound[key] = c.parameterValue(name, key, object.Get(key))
	}
	return bound
}

// parameterValue is one bound value as the store binds it.
func (c *scriptCall) parameterValue(name, key string, value goja.Value) any {
	switch typed := exportOf(value).(type) {
	case nil:
		return nil
	case bool, string, int64:
		return typed
	case float64:
		if whole, isWhole := wholeNumber(typed); isWhole {
			return whole
		}
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			c.throw("the parameter :%s of %s is %v, which binds as nothing", key, name, typed)
		}
		return typed
	case []any:
		encoded, err := c.encode(value)
		if err != nil {
			c.throw("the parameter :%s of %s: %v", key, name, err)
		}
		return string(encoded)
	}
	c.throw("the parameter :%s of %s is %s, which binds as nothing; bind a number, a string, a boolean, "+
		"null or an array", key, name, describe(value))
	return nil
}

// rowObject is one row as the script reads it: an object of its columns, in order.
func (c *scriptCall) rowObject(columns []string, row []any) *goja.Object {
	object := c.vm.NewObject()
	for index, column := range columns {
		var cell goja.Value
		switch typed := row[index].(type) {
		case nil:
			cell = goja.Null()
		case int64, float64, string, bool:
			cell = c.vm.ToValue(typed)
		default:
			cell = c.value(typed)
		}
		_ = object.Set(column, cell)
	}
	return object
}
