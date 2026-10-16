package runtime

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/push"
	"github.com/NaejEL/opnview/internal/store/plugindecl"
	"github.com/NaejEL/opnview/internal/store/plugintables"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// What a plugin answers, checked against its kind's contract before anything is handed on.
//
// THE CONTRACT IS THE ONE OF internal/collect/provider, contract.go, and nothing here restates
// it: a field is read only if the contract's table holds it, with the type the contract gives,
// and a required field that is missing or null refuses the whole answer. A refused answer is
// records_invalid and stores nothing -- not the valid records beside the invalid one, because a
// kind that stored half of a page would record a coverage it does not have.

// checkRecord validates one value against a record table and returns its fields with their
// values normalised: text a string, integer an int64, number a float64, boolean a bool,
// text_list a []string, object its JSON text, record a map and records a list of maps. An
// optional field that is absent or null is left out.
func CheckRecord(contract provider.Contract, table string, value any, path string) (map[string]any, error) {
	record, found := provider.LookupRecord(contract, table)
	if !found {
		return nil, fmt.Errorf("%s: the contract of %s has no table %q", path, contract.Kind, table)
	}
	object, isObject := value.(map[string]any)
	if !isObject {
		return nil, fmt.Errorf("%s is not an object", path)
	}
	checked := make(map[string]any, len(object))
	for _, name := range SortedKeys(object) {
		if _, known := record.Field(name); !known {
			return nil, fmt.Errorf("%s.%s is not a field of %s", path, name, record.Name)
		}
	}
	for _, field := range record.Fields {
		raw, present := object[field.Name]
		if !present || raw == nil {
			if field.Required {
				return nil, fmt.Errorf("%s.%s is required and is missing", path, field.Name)
			}
			continue
		}
		converted, err := convertField(contract, field, raw, path+"."+field.Name)
		if err != nil {
			return nil, err
		}
		checked[field.Name] = converted
	}
	return checked, nil
}

// convertField converts one present, non-null value to its field's type.
func convertField(contract provider.Contract, field provider.Field, raw any, path string) (any, error) {
	switch field.Type {
	case provider.FieldText:
		text, isText := raw.(string)
		if !isText {
			return nil, fmt.Errorf("%s is %T, not text", path, raw)
		}
		if len(field.Values) > 0 && !contains(field.Values, text) {
			return nil, fmt.Errorf("%s is %q, which is not one of %v", path, text, field.Values)
		}
		return text, nil
	case provider.FieldInteger:
		number, ok := IntegerOf(raw)
		if !ok {
			return nil, fmt.Errorf("%s is %v, not an integer", path, raw)
		}
		return number, nil
	case provider.FieldNumber:
		number, ok := numberOf(raw)
		if !ok {
			return nil, fmt.Errorf("%s is %v, not a number", path, raw)
		}
		return number, nil
	case provider.FieldBoolean:
		flag, isFlag := raw.(bool)
		if !isFlag {
			return nil, fmt.Errorf("%s is %T, not a boolean", path, raw)
		}
		return flag, nil
	case provider.FieldTextList:
		list, isList := raw.([]any)
		if !isList {
			return nil, fmt.Errorf("%s is not a list", path)
		}
		texts := make([]string, 0, len(list))
		for index, element := range list {
			text, isText := element.(string)
			if !isText {
				return nil, fmt.Errorf("%s[%d] is not text", path, index)
			}
			texts = append(texts, text)
		}
		return texts, nil
	case provider.FieldObject:
		object, isObject := raw.(map[string]any)
		if !isObject {
			return nil, fmt.Errorf("%s is not an object", path)
		}
		encoded, err := json.Marshal(object)
		if err != nil {
			return nil, fmt.Errorf("%s cannot be encoded: %v", path, err)
		}
		return string(encoded), nil
	case provider.FieldRecord:
		return CheckRecord(contract, field.Of, raw, path)
	case provider.FieldRecords:
		list, isList := raw.([]any)
		if !isList {
			return nil, fmt.Errorf("%s is not a list", path)
		}
		records := make([]map[string]any, 0, len(list))
		for index, element := range list {
			checked, err := CheckRecord(contract, field.Of, element, fmt.Sprintf("%s[%d]", path, index))
			if err != nil {
				return nil, err
			}
			records = append(records, checked)
		}
		return records, nil
	}
	return nil, fmt.Errorf("%s has the unknown type %s", path, field.Type)
}

// integerOf reads a whole number, whichever numeric type a script's value exported as.
func IntegerOf(raw any) (int64, bool) {
	switch typed := raw.(type) {
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	case int32:
		return int64(typed), true
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || typed != math.Trunc(typed) ||
			typed > math.MaxInt64 || typed < math.MinInt64 {
			return 0, false
		}
		return int64(typed), true
	}
	return 0, false
}

// numberOf reads a finite number.
func numberOf(raw any) (float64, bool) {
	switch typed := raw.(type) {
	case int64:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return 0, false
		}
		return typed, true
	}
	return 0, false
}

func contains(values []string, value string) bool {
	for _, known := range values {
		if known == value {
			return true
		}
	}
	return false
}

// The typed readers of a checked record.

func Text(record map[string]any, name string) string {
	value, _ := record[name].(string)
	return value
}

func TextPointer(record map[string]any, name string) *string {
	value, present := record[name].(string)
	if !present {
		return nil
	}
	return &value
}

func Integer(record map[string]any, name string) int64 {
	value, _ := record[name].(int64)
	return value
}

func IntegerPointer(record map[string]any, name string) *int64 {
	value, present := record[name].(int64)
	if !present {
		return nil
	}
	return &value
}

func Number(record map[string]any, name string) float64 {
	value, _ := record[name].(float64)
	return value
}

func Flag(record map[string]any, name string, absent bool) bool {
	value, present := record[name].(bool)
	if !present {
		return absent
	}
	return value
}

func Records(record map[string]any, name string) []map[string]any {
	value, _ := record[name].([]map[string]any)
	return value
}

func Texts(record map[string]any, name string) []string {
	value, _ := record[name].([]string)
	return value
}

// resultOf converts a checked `result` record to a provider.Result. Its probe names one of the
// plugin's declared endpoints; its outcome is one of the closed outcome codes.
func ResultOf(checked map[string]any, endpoints map[string]opnsense.Endpoint) (provider.Result, error) {
	result := provider.Result{
		State:     vocab.AvailabilityState(Text(checked, "state")),
		Outcome:   vocab.AvailabilityOutcome(Text(checked, "outcome")),
		Detail:    Text(checked, "detail"),
		Separable: Flag(checked, "separable", false),
	}
	if result.Outcome != "" && !result.Outcome.Valid() {
		return provider.Result{}, fmt.Errorf("result.outcome %q is not one of the closed outcome codes",
			result.Outcome)
	}
	probe := Text(checked, "probe")
	endpoint, declared := endpoints[probe]
	if !declared {
		return provider.Result{}, fmt.Errorf("result.probe %q is not an endpoint the manifest declares", probe)
	}
	result.Probe = endpoint
	return result, nil
}

// checkRows validates the rows a plugin of a declared kind wrote, against its own tables: the
// table must be the plugin's, every column declared, every NOT NULL column present, every value
// of its column's type.
func CheckRows(tables map[string]plugindecl.PluginTable, raw any) (plugintables.PluginRows, error) {
	if raw == nil {
		return plugintables.PluginRows{}, nil
	}
	byTable, isObject := raw.(map[string]any)
	if !isObject {
		return nil, fmt.Errorf("rows is not an object keyed by table")
	}
	checked := plugintables.PluginRows{}
	names := SortedKeys(byTable)
	for _, name := range names {
		table, owned := tables[name]
		if !owned {
			return nil, fmt.Errorf("rows.%s is not a table the plugin declares", name)
		}
		list, isList := byTable[name].([]any)
		if !isList {
			return nil, fmt.Errorf("rows.%s is not a list", name)
		}
		for index, element := range list {
			row, isRow := element.(map[string]any)
			if !isRow {
				return nil, fmt.Errorf("rows.%s[%d] is not an object", name, index)
			}
			converted, err := checkRow(table, row, fmt.Sprintf("rows.%s[%d]", name, index))
			if err != nil {
				return nil, err
			}
			checked[name] = append(checked[name], converted)
		}
	}
	return checked, nil
}

// checkRow validates one row against a table.
func checkRow(table plugindecl.PluginTable, row map[string]any, path string) (map[string]any, error) {
	declared := map[string]plugindecl.PluginColumn{}
	for _, column := range table.Columns {
		declared[column.Name] = column
	}
	keys := make([]string, 0, len(row))
	for key := range row {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, known := declared[key]; !known {
			return nil, fmt.Errorf("%s.%s is not a column of %s", path, key, table.Table)
		}
	}
	converted := make(map[string]any, len(table.Columns))
	for _, column := range table.Columns {
		value, present := row[column.Name]
		if !present || value == nil {
			if !column.Nullable {
				return nil, fmt.Errorf("%s.%s is NOT NULL and is missing", path, column.Name)
			}
			converted[column.Name] = nil
			continue
		}
		switch column.Type {
		case "integer":
			number, ok := IntegerOf(value)
			if !ok {
				return nil, fmt.Errorf("%s.%s is %v, not an integer", path, column.Name, value)
			}
			converted[column.Name] = number
		case "real":
			number, ok := numberOf(value)
			if !ok {
				return nil, fmt.Errorf("%s.%s is %v, not a number", path, column.Name, value)
			}
			converted[column.Name] = number
		case "text":
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("%s.%s is %T, not text", path, column.Name, value)
			}
			converted[column.Name] = text
		default:
			// boolean, ip, mac and instant (PS4 of the push amendment): an instant from a script is
			// a number of UTC epoch seconds, the rest their JSON value or text.
			typed, err := push.Convert(column.Type, value)
			if err != nil {
				return nil, fmt.Errorf("%s.%s: %w", path, column.Name, err)
			}
			converted[column.Name] = typed
		}
	}
	return converted, nil
}

// checkState validates the durable state a plugin returns: an object of strings.
func CheckState(raw any) (map[string]string, error) {
	if raw == nil {
		return nil, nil
	}
	object, isObject := raw.(map[string]any)
	if !isObject {
		return nil, fmt.Errorf("state is not an object")
	}
	state := make(map[string]string, len(object))
	for key, value := range object {
		if key == "" {
			return nil, fmt.Errorf("state holds an empty key")
		}
		text, isText := value.(string)
		if !isText {
			return nil, fmt.Errorf("state.%s is %T; a durable value is a string", key, value)
		}
		state[key] = text
	}
	return state, nil
}
