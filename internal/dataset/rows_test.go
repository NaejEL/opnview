package dataset

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestAScriptsReadIsOneStatementWhateverItsKeys is PA3's set-based read at the compiler: a key set of ten
// tuples and one of a thousand compile to the same text, each column's values bound as one array; a tuple
// listed twice is kept once; an empty set keeps no row; a set beyond MaxScriptValues is refused; and the
// core datasets a join reads are declared.
func TestAScriptsReadIsOneStatementWhateverItsKeys(t *testing.T) {
	t.Parallel()
	core, err := Core()
	if err != nil {
		t.Fatal(err)
	}
	declarations := map[string]Declaration{}
	for _, declaration := range core {
		declarations[declaration.Name] = declaration
	}
	for _, name := range []string{"leg", "interface", "this_firewall_address", "provider_value"} {
		if declaration, found := declarations[name]; !found || Strings("en")[declaration.Label] == "" {
			t.Errorf("the core dataset %s is not declared with its label", name)
		}
	}
	leg := declarations["leg"]
	compile := func(count int) Statement {
		t.Helper()
		values := make([][]any, 0, count)
		for index := 0; index < count; index++ {
			values = append(values, []any{index + 1, "exdev0"})
		}
		raw, _ := json.Marshal(map[string]any{"columns": []string{"flow_id", "device"}, "values": values})
		keys, refusal := ParseKeys(raw, leg)
		if refusal != nil {
			t.Fatal(refusal)
		}
		filters, refusal := ParseScriptFilters(json.RawMessage(`[{"column": "direction", "op": "in", "value": ["in", "out"]}]`), leg)
		if refusal != nil {
			t.Fatal(refusal)
		}
		return CompileRows(leg, filters, keys, Window{From: 1, To: 2}, []string{"flow_id", "action"}, 10)
	}
	ten, thousand := compile(10), compile(1000)
	if ten.Text != thousand.Text || len(ten.Args) != len(thousand.Args) || len(ten.Args) != 6 {
		t.Errorf("10 keys compile to %q with %d arguments and 1 000 to %q with %d", ten.Text, len(ten.Args),
			thousand.Text, len(thousand.Args))
	}
	if ids, _ := thousand.Args[3].([]int64); len(ids) != 1000 || !strings.Contains(thousand.Text, "unnest(") {
		t.Errorf("the key set is not bound as one array per column: %v", thousand.Args)
	}
	twice, refusal := ParseKeys(json.RawMessage(`{"columns": ["flow_id"], "values": [1, 1, 2]}`), leg)
	if refusal != nil || twice.Size() != 2 {
		t.Errorf("a key listed twice is kept %d times (%v)", twice.Size(), refusal)
	}
	empty, refusal := ParseKeys(json.RawMessage(`{"columns": ["flow_id"], "values": []}`), leg)
	if refusal != nil || !strings.Contains(CompileRows(leg, &Query{}, empty, Window{}, []string{"flow_id"}, 1).Text, "AND FALSE") {
		t.Errorf("an empty key set does not keep no row (%v)", refusal)
	}
	var many strings.Builder
	for index := 0; index <= MaxScriptValues; index++ {
		if index > 0 {
			many.WriteString(",")
		}
		fmt.Fprintf(&many, "%d", index)
	}
	if _, refusal := ParseKeys(json.RawMessage(`{"columns": ["flow_id"], "values": [`+many.String()+`]}`), leg); refusal == nil ||
		refusal.Code != RefusalTooManyRows {
		t.Errorf("a key set beyond %d tuples was not refused: %v", MaxScriptValues, refusal)
	}
	if _, refusal := ParseKeys(json.RawMessage(`{"columns": ["no_such_column"], "values": [1]}`), leg); refusal == nil ||
		refusal.Code != RefusalUnknownColumn {
		t.Errorf("a key on an undeclared column was not refused: %v", refusal)
	}
}
