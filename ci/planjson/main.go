// Command planjson is sql/schema-checks.sh's reader of PostgreSQL query plans (P9 and P-AC21 of
// specs/SPEC-backend-completion-plugins.md). The plan checks ask PostgreSQL for its plan as JSON,
// EXPLAIN (FORMAT JSON), and this program does the two things the script cannot do in a shell
// without a JSON tool the development image does not carry:
//
//	planjson explain <statement file> <value>
//
// prints an EXPLAIN (FORMAT JSON) of a named statement with every :name parameter replaced by
// <value> as an untyped literal, which the server types from the place it stands in -- a
// bigint beside a bigint column, text beside a text one -- as the store's driver casts each
// argument to its Go type (internal/store/sqldb/driver.go). A literal is planned for its value, as
// the store's first executions are. A parameter that cannot take an instant -- a BOOLEAN flag -- is
// given its own value by a line "-- plan-with: <name>=<literal>" among its statement's lines, which the
// statement files keep beside the statement as a comment and the store never reads;
//
//	planjson nodes
//
// reads EXPLAIN (FORMAT JSON) output on its standard input and prints one line per plan node,
// of every subplan and init plan too, as six tab-separated fields: the node type, the relation
// it reads -- for a Function Scan, the function it calls -- (or "-"), its alias (or "-"), the
// index it reads (or "-"), its index condition (or "-"), and the type of the node above it (or
// "-"). A view does not appear: PostgreSQL plans the tables beneath it, and a SQL function the
// planner inlined does not either: only one it calls as a function is a Function Scan of it.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fail(errors.New("usage: planjson explain <statement file> <value> | planjson nodes"))
	}
	var err error
	switch os.Args[1] {
	case "explain":
		if len(os.Args) != 4 {
			fail(errors.New("usage: planjson explain <statement file> <value>"))
		}
		err = explain(os.Args[2], os.Args[3])
	case "nodes":
		err = nodes(os.Stdin, os.Stdout)
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "planjson:", err)
	os.Exit(2)
}

// explain prints the EXPLAIN (FORMAT JSON) of one statement file, its parameters replaced.
func explain(path, value string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	quote := func(text string) string { return "'" + strings.ReplaceAll(text, "'", "''") + "'" }
	literals := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		assignment, found := strings.CutPrefix(strings.TrimSpace(line), "-- plan-with: ")
		if !found {
			continue
		}
		name, given, valid := strings.Cut(assignment, "=")
		if !valid || name == "" {
			return fmt.Errorf("%s: %q is not a plan-with line, -- plan-with: <name>=<literal>", path, line)
		}
		literals[strings.TrimSpace(name)] = quote(strings.TrimSpace(given))
	}
	text := replaceParameters(strings.TrimSpace(string(raw)), quote(value), literals)
	text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), ";"))
	fmt.Printf("EXPLAIN (FORMAT JSON)\n%s;\n", text)
	return nil
}

// replaceParameters replaces every :name parameter with its literal -- its own, or the default --
// leaving quoted text,
// quoted identifiers, comments, dollar-quoted bodies and :: casts alone, as the store's driver
// reads a statement.
func replaceParameters(query, literal string, literals map[string]string) string {
	var out strings.Builder
	for i := 0; i < len(query); {
		character := query[i]
		switch {
		case character == '\'' || character == '"':
			end := i + 1
			for end < len(query) {
				if query[end] == character {
					if end+1 < len(query) && query[end+1] == character {
						end += 2
						continue
					}
					break
				}
				end++
			}
			if end >= len(query) {
				end = len(query) - 1
			}
			out.WriteString(query[i : end+1])
			i = end + 1
		case character == '-' && i+1 < len(query) && query[i+1] == '-':
			end := strings.IndexByte(query[i:], '\n')
			if end < 0 {
				end = len(query) - i
			}
			out.WriteString(query[i : i+end])
			i += end
		case character == '$' && i+1 < len(query) && query[i+1] == '$':
			end := strings.Index(query[i+2:], "$$")
			if end < 0 {
				out.WriteString(query[i:])
				i = len(query)
				continue
			}
			out.WriteString(query[i : i+2+end+2])
			i += 2 + end + 2
		case character == ':' && i+1 < len(query) && query[i+1] == ':':
			out.WriteString("::")
			i += 2
		case character == ':' && i+1 < len(query) && isIdentifierStart(query[i+1]):
			end := i + 1
			for end < len(query) && isIdentifierPart(query[end]) {
				end++
			}
			if own, found := literals[query[i+1:end]]; found {
				out.WriteString(own)
			} else {
				out.WriteString(literal)
			}
			i = end
		default:
			out.WriteByte(character)
			i++
		}
	}
	return out.String()
}

func isIdentifierStart(character byte) bool {
	return character == '_' || (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z')
}

func isIdentifierPart(character byte) bool {
	return isIdentifierStart(character) || (character >= '0' && character <= '9')
}

// node is one node of a JSON plan, with the fields the checks read.
type node struct {
	NodeType     string `json:"Node Type"`
	RelationName string `json:"Relation Name"`
	FunctionName string `json:"Function Name"`
	Alias        string `json:"Alias"`
	IndexName    string `json:"Index Name"`
	IndexCond    string `json:"Index Cond"`
	Plans        []node `json:"Plans"`
}

// nodes prints every node of every plan read from input.
func nodes(input io.Reader, output io.Writer) error {
	writer := bufio.NewWriter(output)
	defer func() { _ = writer.Flush() }()
	decoder := json.NewDecoder(input)
	plans := 0
	for {
		var document []struct {
			Plan node `json:"Plan"`
		}
		err := decoder.Decode(&document)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("reading the plan: %w", err)
		}
		for _, entry := range document {
			plans++
			walk(writer, entry.Plan, "")
		}
	}
	if plans == 0 {
		return errors.New("no plan on the standard input")
	}
	return nil
}

func walk(writer *bufio.Writer, current node, parent string) {
	relation := current.RelationName
	if relation == "" {
		relation = current.FunctionName
	}
	_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\n", current.NodeType, dash(relation),
		dash(current.Alias), dash(current.IndexName), dash(strings.Join(strings.Fields(current.IndexCond), " ")),
		dash(parent))
	for _, child := range current.Plans {
		walk(writer, child, current.NodeType)
	}
}

func dash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
