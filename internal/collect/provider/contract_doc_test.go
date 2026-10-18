package provider

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The contract tables of docs/plugin-kinds.md (cited by docs/plugins.md, *Kinds*) are the contracts of the kind files (kinds/<kind>.json), rendered. A13 of the
// backend-completion cycle: the document a third party writes a plugin from cannot drift from the
// code that reads the plugin. Each kind's tables sit between <!-- contract:<kind> --> and
// <!-- /contract:<kind> -->, and the shared result between <!-- contract:shared --> and
// <!-- /contract:shared -->.

// renderRecord renders one record table.
func renderRecord(record Record) string {
	var out strings.Builder
	out.WriteString("#### `" + record.Name + "`\n\n" + record.Description + "\n\n")
	out.WriteString("| Field | Type | Required | Unit or values | Description |\n|---|---|---|---|---|\n")
	for _, field := range record.Fields {
		fieldType := string(field.Type)
		if field.Of != "" {
			fieldType += " of `" + field.Of + "`"
		}
		required := "no"
		if field.Required {
			required = "yes"
		}
		detail := field.Unit
		if len(field.Values) > 0 {
			quoted := make([]string, 0, len(field.Values))
			for _, value := range field.Values {
				quoted = append(quoted, "`"+value+"`")
			}
			detail = strings.Join(quoted, ", ")
		}
		out.WriteString("| `" + field.Name + "` | " + fieldType + " | " + required + " | " + detail + " | " +
			strings.ReplaceAll(field.Description, "|", "\\|") + " |\n")
	}
	return out.String()
}

// renderContract renders one kind's contract.
func renderContract(contract Contract) string {
	var out strings.Builder
	out.WriteString("### `" + contract.Kind + "`\n\n" + contract.Description + "\n\n")
	out.WriteString("| Function | Called with | Returns | When |\n|---|---|---|---|\n")
	for _, function := range contract.Functions {
		out.WriteString("| `" + function.Name + "` | `" + function.Arguments + "` | `" + function.Returns + "` | " +
			function.Description + " |\n")
	}
	if len(contract.Values) > 0 {
		out.WriteString("\nValues the script exports beside the functions:\n\n")
		out.WriteString(renderRecord(Record{Name: "values", Description: "The exported values.",
			Fields: contract.Values}))
	}
	for _, record := range contract.Records {
		out.WriteString("\n" + renderRecord(record))
	}
	return out.String()
}

// documentedBlocks reads every marked block of docs/plugin-kinds.md by its name.
func documentedBlocks(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "plugin-kinds.md"))
	if err != nil {
		t.Fatalf("reading docs/plugin-kinds.md: %v", err)
	}
	blocks := map[string]string{}
	pattern := regexp.MustCompile(`(?s)<!-- contract:([a-z_]+) -->\n(.*?)<!-- /contract:([a-z_]+) -->`)
	for _, match := range pattern.FindAllStringSubmatch(string(raw), -1) {
		if match[1] != match[3] {
			t.Errorf("the block opened as %s is closed as %s", match[1], match[3])
		}
		blocks[match[1]] = strings.TrimSpace(match[2])
	}
	return blocks
}

// TestTheDocumentedContractsAreTheContracts is A13.
func TestTheDocumentedContractsAreTheContracts(t *testing.T) {
	documented := documentedBlocks(t)
	expected := map[string]string{"shared": strings.TrimSpace(renderRecord(SharedRecords()[0]))}
	for _, contract := range Contracts() {
		expected[contract.Kind] = strings.TrimSpace(renderContract(contract))
	}
	for name, want := range expected {
		got, present := documented[name]
		if !present {
			t.Errorf("docs/plugin-kinds.md has no block for %s; it is:\n<!-- contract:%s -->\n%s\n<!-- /contract:%s -->",
				name, name, want, name)
			continue
		}
		if got != want {
			t.Errorf("docs/plugin-kinds.md's block for %s differs from its kind file; it is:\n<!-- contract:%s -->\n%s\n"+
				"<!-- /contract:%s -->", name, name, want, name)
		}
	}
	for name := range documented {
		if _, known := expected[name]; !known {
			t.Errorf("docs/plugin-kinds.md documents %s, which no kind file defines", name)
		}
	}
}
