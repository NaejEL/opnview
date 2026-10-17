package modularity

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// pfTimeoutValues are pf's default state timeouts the NetFlow join reads (PFTM_*_VAL of pf.h; survey S12):
// TCP first 120 s, established 86 400 s, closing 900 s, closed 90 s, UDP multiple 60 s, ICMP first 20 s,
// other multiple 60 s.
var pfTimeoutValues = map[int64]bool{120: true, 86400: true, 900: true, 90: true, 60: true, 20: true}

// pfTimeoutWords mark a line as speaking of a state timeout: a protocol pf keeps states of, or the
// timeout itself.
var pfTimeoutWords = []string{"tcp", "udp", "icmp", "statetimeout", "state_timeout", "pftm"}

// TestNoGoFileHoldsAPfStateTimeout is PA-AC10's grep: pf's state timeouts are values of the firewall_log
// contract, which the pf plugin exports and cites (plugins/pf), and which a derivation reads through the
// provider_value dataset or the store; no Go file under cmd/ or internal/ outside the tests holds one. A
// whole-number literal equal to one of the defaults on a line naming TCP, UDP, ICMP or a state timeout is
// one.
func TestNoGoFileHoldsAPfStateTimeout(t *testing.T) {
	root := repositoryRoot(t)
	var found []string
	for _, scope := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, scope), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			files := token.NewFileSet()
			file, err := parser.ParseFile(files, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			lines := map[int]string{}
			ast.Inspect(file, func(node ast.Node) bool {
				literal, isLiteral := node.(*ast.BasicLit)
				if !isLiteral || literal.Kind != token.INT {
					return true
				}
				value, err := strconv.ParseInt(literal.Value, 0, 64)
				if err != nil || !pfTimeoutValues[value] {
					return true
				}
				lines[files.Position(literal.Pos()).Line] = literal.Value
				return true
			})
			if len(lines) == 0 {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			text := strings.Split(string(content), "\n")
			for line, value := range lines {
				lowered := strings.ToLower(text[line-1])
				for _, word := range pfTimeoutWords {
					if strings.Contains(lowered, word) {
						relative, _ := filepath.Rel(root, path)
						found = append(found, filepath.ToSlash(relative)+":"+strconv.Itoa(line)+": "+value)
						break
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(found) > 0 {
		t.Errorf("Go files hold pf state timeouts, which are the pf plugin's exported values: %v", found)
	}
}
