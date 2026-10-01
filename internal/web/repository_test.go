package web

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The grep-shaped assertions over what this cycle added.
//
// THEY LOOK AT FILES ON DISK, deliberately. Every other test in this package runs
// against behaviour; these two run against the source, because what they forbid is a
// value being WRITTEN DOWN rather than a behaviour being wrong. A hardcoded address
// in a comment is invisible to every behavioural test there is, and it is exactly what
// has been shipped before.

// newPackages are the directories this cycle added or extended with a surface.
func newPackages() []string {
	return []string{
		"internal/web",
		"internal/auth",
		"internal/secret",
		"internal/config",
		"cmd/opnview",
	}
}

// repositoryRoot is two directories above this package.
func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving the repository root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("the repository root does not look like one: %v", err)
	}
	return root
}

// The patterns that would be a network's own configuration written into the product.
var (
	// A dotted quad. It catches an address written as a default, a placeholder, an
	// example or a comment.
	patternIPv4 = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)
	// A CIDR suffix on one.
	patternCIDR = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}/\d{1,2}\b`)
	// A host:port or a bare :port that would be a listen or a target address.
	patternPort = regexp.MustCompile(`(?:^|[^\w.])(?:localhost|\w+\.\w+)?:\d{2,5}\b`)
	// An interface or VLAN name of the kind OPNsense uses. Classifying anything by
	// one of these is forbidden outright, and writing one down is how it starts.
	patternInterfaceName = regexp.MustCompile(`\b(?:igb|em|re|ix|vtnet|vlan|lagg|bridge)\d+\b`)
)

// allowedNumericPatterns are the strings the patterns above match that are NOT a
// network's configuration. Each is listed with what it actually is, and the list is
// short on purpose: a long one would be this assertion being negotiated away.
func allowedNumericPatterns() []string {
	return []string{
		// Version numbers of OPNsense, cited in comments.
		"26.7.3", "26.7",
		// RFC and CSS values, not addresses.
		"9106",
		// The timestamp sentinel the schema uses, and durations.
		"4102444800",
	}
}

// TestNoNetworkConfigurationIsWrittenIntoTheProduct is AC19.
func TestNoNetworkConfigurationIsWrittenIntoTheProduct(t *testing.T) {
	root := repositoryRoot(t)
	inspected := 0

	for _, packagePath := range newPackages() {
		walkSourceFiles(t, filepath.Join(root, packagePath), func(path, content string) {
			inspected++
			relative, err := filepath.Rel(root, path)
			if err != nil {
				relative = path
			}
			for _, line := range strings.Split(content, "\n") {
				if allowedLine(line) {
					continue
				}
				for name, pattern := range map[string]*regexp.Regexp{
					"an address":        patternIPv4,
					"a CIDR":            patternCIDR,
					"a host or port":    patternPort,
					"an interface name": patternInterfaceName,
				} {
					if match := pattern.FindString(line); match != "" {
						t.Errorf("%s carries %s (%q) in: %s",
							relative, name, strings.TrimSpace(match), strings.TrimSpace(line))
					}
				}
			}
		})
	}
	if inspected == 0 {
		t.Fatal("no source file was inspected, so this assertion is vacuous")
	}

	// And the two flags that could have carried a default carry none.
	main, err := os.ReadFile(filepath.Join(root, "cmd", "opnview", "main.go"))
	if err != nil {
		t.Fatalf("reading the entry point: %v", err)
	}
	for _, flagName := range []string{`flag.String("data-dir", ""`, `flag.String("listen", ""`} {
		if !strings.Contains(string(main), flagName) {
			t.Errorf("the entry point does not declare %s with an empty default", flagName)
		}
	}
}

// allowedLine reports whether a line's numeric content is something other than a
// network's configuration.
func allowedLine(line string) bool {
	// A test file generates its values, and the harness says so; the assertion is
	// about the product. Test sources are walked all the same, because a fixture with
	// a real address in it is the same defect — so only the explicit allow-list below
	// exempts anything.
	for _, allowed := range allowedNumericPatterns() {
		if strings.Contains(line, allowed) {
			return true
		}
	}
	// A duration, a size or a count written as a Go expression is not an address.
	for _, shape := range []string{
		"time.", "Duration", "KiB", "MiB", "* 1024", "http.Status", "0o", "0x",
		"rem", "px", "rgba(", "#", "--", "sha256", "SHA-256",
	} {
		if strings.Contains(line, shape) {
			return true
		}
	}
	return false
}

// TestNoSecretIsCommitted is AC20.
//
// IT LOOKS FOR TWO THINGS. First, a key file or a database anywhere in the working
// tree: both live in the data directory, which is outside the bind-mounted tree in the
// compose environment, exactly as the database already does. Second, a field name from
// the credential vocabulary sitting beside a literal value, which is what a
// hardcoded key looks like.
func TestNoSecretIsCommitted(t *testing.T) {
	root := repositoryRoot(t)

	t.Run("no key file and no database is in the tree", func(t *testing.T) {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == ".git" || entry.Name() == "factory-logs" {
					return filepath.SkipDir
				}
				return nil
			}
			switch {
			case entry.Name() == "opnview.key",
				strings.HasSuffix(entry.Name(), ".db"),
				strings.HasSuffix(entry.Name(), ".db-wal"),
				strings.HasSuffix(entry.Name(), ".db-shm"),
				strings.HasSuffix(entry.Name(), ".mmdb"):
				relative, _ := filepath.Rel(root, path)
				t.Errorf("%s is in the working tree and must not be", relative)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking the tree: %v", err)
		}
	})

	t.Run("the key file is named in gitignore", func(t *testing.T) {
		ignored, err := os.ReadFile(filepath.Join(root, ".gitignore"))
		if err != nil {
			t.Fatalf("reading .gitignore: %v", err)
		}
		for _, required := range []string{"*.db", "opnview.key"} {
			if !strings.Contains(string(ignored), required) {
				t.Errorf(".gitignore does not carry %q", required)
			}
		}
	})

	t.Run("no credential is assigned a literal value", func(t *testing.T) {
		// A credential field assigned a non-empty string literal. The empty string is
		// what internal/collect's own comment calls the honest starting state, so it is
		// permitted and nothing else is.
		assignment := regexp.MustCompile(
			`(?i)(APISecret|APIKey|password|licence_key|license_key|secret)\s*[:=]\s*"([^"]+)"`)
		for _, packagePath := range newPackages() {
			walkSourceFiles(t, filepath.Join(root, packagePath), func(path, content string) {
				relative, err := filepath.Rel(root, path)
				if err != nil {
					relative = path
				}
				for _, line := range strings.Split(content, "\n") {
					match := assignment.FindStringSubmatch(line)
					if match == nil {
						continue
					}
					value := match[2]
					// A catalogue key, a setting key, a form field name or a column name is
					// not a credential. Each is a lower-case identifier with no space; a
					// real credential is not.
					if isIdentifierLike(value) {
						continue
					}
					t.Errorf("%s assigns a literal to %s: %s",
						relative, match[1], strings.TrimSpace(line))
				}
			})
		}
	})
}

// isIdentifierLike reports whether a literal is a key, a column name or a field name
// rather than a value somebody would authenticate with.
func isIdentifierLike(value string) bool {
	if value == "" {
		return true
	}
	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z':
		case character >= '0' && character <= '9':
		case character == '_' || character == '.' || character == '-':
		default:
			return false
		}
	}
	return true
}

// walkSourceFiles calls visit for every file under directory that a person wrote.
func walkSourceFiles(t *testing.T, directory string, visit func(path, content string)) {
	t.Helper()
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		switch filepath.Ext(path) {
		case ".go", ".gohtml", ".css", ".json", ".sql", ".html":
		default:
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		visit(path, string(content))
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", directory, err)
	}
}
