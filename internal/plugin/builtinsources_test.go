package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/plugin/contract"
)

// TestEveryBuiltinEndpointCitesOPNsense2673 is BR-AC26's first part, the citation test extended to
// the plugins: every endpoint a built-in plugin reads appears verbatim in the survey, cites a
// section the survey holds, and an upstream source pinned at OPNsense 26.7.3.
func TestEveryBuiltinEndpointCitesOPNsense2673(t *testing.T) {
	t.Parallel()
	root := repositoryPlugins(t)
	survey, err := os.ReadFile(filepath.Join(root, "..", "docs", "opnsense-api-survey.md"))
	if err != nil {
		t.Fatal(err)
	}
	manifests, err := filepath.Glob(filepath.Join(root, "*", "plugin.json"))
	if err != nil || len(manifests) == 0 {
		t.Fatalf("no built-in manifest (%v)", err)
	}
	checked := 0
	for _, file := range manifests {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var manifest contract.Manifest
		if err := json.Unmarshal(raw, &manifest); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		for _, endpoint := range manifest.Endpoints {
			checked++
			if !strings.Contains(string(survey), endpoint.Path) {
				t.Errorf("%s reads %s, which the survey does not carry verbatim", manifest.Name, endpoint.Path)
			}
			if endpoint.SurveySection == "" || !strings.Contains(string(survey), endpoint.SurveySection) {
				t.Errorf("%s cites the section %q for %s, which the survey does not hold", manifest.Name,
					endpoint.SurveySection, endpoint.Path)
			}
			if !strings.Contains(endpoint.UpstreamURL, "/26.7.3/") {
				t.Errorf("%s cites %q for %s, not a source pinned at 26.7.3", manifest.Name, endpoint.UpstreamURL,
					endpoint.Path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no built-in endpoint was checked")
	}
}

// TestNoBuiltinScriptReadsABodyWhole is BR-AC27's second part: a built-in plugin reads the fields
// it uses through the observed value, never the whole body or json(), so the mirror stores only
// what is read. The plugins' own tests are not scripts the host runs and are not searched.
func TestNoBuiltinScriptReadsABodyWhole(t *testing.T) {
	t.Parallel()
	root := repositoryPlugins(t)
	scripts, err := filepath.Glob(filepath.Join(root, "*", "*.js"))
	if err != nil || len(scripts) == 0 {
		t.Fatalf("no built-in script (%v)", err)
	}
	whole := regexp.MustCompile(`\.body\b|\.json\(\)`)
	for _, file := range scripts {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for number, line := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") {
				continue
			}
			if whole.MatchString(line) {
				t.Errorf("%s:%d reads an answer whole: %s", filepath.Base(file), number+1, trimmed)
			}
		}
	}
}
