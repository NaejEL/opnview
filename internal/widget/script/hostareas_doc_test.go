package script_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	// The host's areas register from these packages' init() functions (internal/widget/hostareas.go).
	_ "github.com/NaejEL/opnview/internal/widget/hostareas/installation"
	_ "github.com/NaejEL/opnview/internal/widget/hostareas/traffic"
	_ "github.com/NaejEL/opnview/internal/widget/hostareas/values"
	"github.com/NaejEL/opnview/internal/widget/script"
)

// TestEveryHostAreaIsDocumented: every area of the widget host has its section in docs/widget-host.md,
// and the document describes no area the host does not have.
func TestEveryHostAreaIsDocumented(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "widget-host.md"))
	if err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	for _, match := range regexp.MustCompile("(?m)^#### `host\\.([a-z_]+)`$").FindAllStringSubmatch(string(raw), -1) {
		documented[match[1]] = true
	}
	areas := script.HostAreas()
	if len(areas) == 0 {
		t.Fatal("the host has no area")
	}
	for _, area := range areas {
		if !documented[area] {
			t.Errorf("the host area %s has no section in docs/widget-host.md", area)
		}
		delete(documented, area)
	}
	var extra []string
	for area := range documented {
		extra = append(extra, area)
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		t.Errorf("docs/widget-host.md documents host areas that do not exist: %s", strings.Join(extra, ", "))
	}
}
