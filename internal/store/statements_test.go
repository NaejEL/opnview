package store

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The embedded statements, and the value sets they match on.

// TestEveryStatementPreparesForEveryPeriod is the statement files' own check: every named
// statement, and every period of every template, is valid SQL against the schema.
func TestEveryStatementPreparesForEveryPeriod(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	names := mustStatementNames(t)
	if len(names) < 40 {
		t.Fatalf("only %d statements were parsed, so the parser is not reaching them", len(names))
	}
	for _, name := range names {
		texts := []string{}
		if IsComposeTemplate(name) {
			composed := 0
			for _, period := range Periods() {
				if _, has := period.Child(); !has {
					continue
				}
				text, err := ComposeStatement(name, period)
				if err != nil {
					t.Fatalf("expanding %s: %v", name, err)
				}
				texts = append(texts, text)
				composed++
			}
			if composed != 3 {
				t.Errorf("the composition %s was expanded for %d periods, not the three with a finer one", name, composed)
			}
		} else if IsPeriodTemplate(name) {
			for _, period := range Periods() {
				text, err := PeriodStatement(name, period)
				if err != nil {
					t.Fatalf("expanding %s: %v", name, err)
				}
				texts = append(texts, text)
			}
		} else {
			text, err := Statement(name)
			if err != nil {
				t.Fatalf("reading %s: %v", name, err)
			}
			texts = append(texts, text)
		}
		for _, text := range texts {
			statement, err := database.DB().PrepareContext(ctx, text)
			if err != nil {
				t.Errorf("the statement %s does not prepare: %v", name, err)
				continue
			}
			_ = statement.Close()
		}
	}
	if _, err := Statement("no_such_statement"); err == nil {
		t.Error("a statement that does not exist was returned")
	}
	if _, err := PeriodStatement("dirty_hours", PeriodHour); err == nil {
		t.Error("a statement that is not a template was expanded as one")
	}
	if _, err := ComposeStatement("compose_volume", PeriodHour); err == nil {
		t.Error("an hour, which has no finer period, was composed")
	}
	if _, err := ComposeStatement("insert_volume", PeriodDay); err == nil {
		t.Error("a statement that is not a composition was expanded as one")
	}
}

// TestTheStatementParserRefusesWhatItCannotRun keeps two statements in one block, and an
// empty block, from reaching the database.
func TestTheStatementParserRefusesWhatItCannotRun(t *testing.T) {
	t.Parallel()
	for _, text := range []string{
		"-- statement: two\nSELECT 1;\nSELECT 2;\n",
		"-- statement: empty\n-- only a comment\n",
		"-- statement: twice\nSELECT 1;\n-- statement: twice\nSELECT 2;\n",
	} {
		if _, err := parseStatements(text); err == nil {
			t.Errorf("the parser accepted %q", text)
		}
	}
	parsed, err := parseStatements("-- a header\n-- statement: one\n-- its comment\nSELECT :value\nFROM setting;\n")
	if err != nil || parsed["one"] != "SELECT :value\nFROM setting" {
		t.Errorf("the parser read %q, %v", parsed["one"], err)
	}
}

// logReasonComparison finds a log reason compared with string literals: an equality, an
// inequality or an IN list beside log_reason, in Go or in SQL.
var logReasonComparison = regexp.MustCompile(`(?is)log_reason\s*(=|<>|!=|\bIN\s*\()\s*('[^']*'(\s*,\s*'[^']*')*)`)

// quotedLiteral finds the single-quoted literals of a match.
var quotedLiteral = regexp.MustCompile(`'([^']*)'`)

// TestNoCodeMatchesALogReasonOutsideTheEstablishedSet is AC5. Every comparison of
// flow.log_reason with a literal, in the schema, the statements, the queries and the Go
// sources, names only values of the established set; and the scan has teeth.
func TestNoCodeMatchesALogReasonOutsideTheEstablishedSet(t *testing.T) {
	t.Parallel()
	established := map[string]bool{}
	for _, reason := range LogReasons {
		established[reason] = true
	}
	matched := 0
	for _, root := range []string{"../../internal", "../../cmd", "../../sql/queries"} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || strings.HasSuffix(entry.Name(), "_test.go") ||
				(!strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), ".sql")) {
				return nil
			}
			source, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, outside := range logReasonsOutsideTheSet(string(source), established) {
				t.Errorf("%s matches the log reason %q, which is not established", path, outside)
			}
			matched += len(logReasonComparison.FindAllString(string(source), -1))
			return nil
		})
		if err != nil {
			t.Fatalf("scanning %s: %v", root, err)
		}
	}
	if matched == 0 {
		t.Fatal("no comparison of a log reason was found, so the scan is not reaching blocked_decision")
	}
	if outside := logReasonsOutsideTheSet(`WHERE log_reason = 'example-invented-reason'`, established); len(outside) != 1 {
		t.Errorf("the scan did not catch an invented reason: %v", outside)
	}
	if outside := logReasonsOutsideTheSet("log_reason IN ('match',\n 'unknown(15)')", established); len(outside) != 1 {
		t.Errorf("the scan did not catch an unestablished value in a list: %v", outside)
	}

	// The view names every established value but `match` as a drop no rule expresses.
	schema, _ := sqlFiles.ReadFile("schema.sql")
	view := string(schema)[strings.Index(string(schema), "CREATE VIEW blocked_decision"):]
	for _, reason := range LogReasons {
		if !strings.Contains(view, "'"+reason+"'") {
			t.Errorf("blocked_decision does not place the established reason %s", reason)
		}
	}
}

func logReasonsOutsideTheSet(source string, established map[string]bool) []string {
	var outside []string
	for _, match := range logReasonComparison.FindAllStringSubmatch(source, -1) {
		for _, literal := range quotedLiteral.FindAllStringSubmatch(match[2], -1) {
			if !established[literal[1]] {
				outside = append(outside, literal[1])
			}
		}
	}
	return outside
}

// TestTheEligibleAnswerSourcesAreTheEstablishedOnes keeps the attribution statement and the
// recorded set in step.
func TestTheEligibleAnswerSourcesAreTheEstablishedOnes(t *testing.T) {
	t.Parallel()
	text, err := Statement("attribution_lookups")
	if err != nil {
		t.Fatalf("reading the statement: %v", err)
	}
	inList := regexp.MustCompile(`answer_source IN \(([^)]*)\)`)
	lists := inList.FindAllStringSubmatch(text, -1)
	if len(lists) != 2 {
		t.Fatalf("the statement names the eligible sources %d times, not twice", len(lists))
	}
	want := append([]string(nil), AttributionAnswerSources...)
	sort.Strings(want)
	for _, list := range lists {
		var got []string
		for _, literal := range quotedLiteral.FindAllStringSubmatch(list[1], -1) {
			got = append(got, literal[1])
		}
		sort.Strings(got)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("the statement names %v, the recorded set is %v", got, want)
		}
	}
}
