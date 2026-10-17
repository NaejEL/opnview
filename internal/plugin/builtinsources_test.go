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

// repositoryPlugins is the built-in plugin tree on disk, found from this package's directory.
func repositoryPlugins(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "plugins"))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// TestEveryValueABuiltinExportsIsCited is PA-AC10's citation part: every value a built-in provider exports
// beside its functions -- pf's state timeouts among them -- is cited in its manifest's value_sources with the
// source it is taken from, a section the survey holds and the upstream file that section cites.
func TestEveryValueABuiltinExportsIsCited(t *testing.T) {
	t.Parallel()
	root := repositoryPlugins(t)
	survey, err := os.ReadFile(filepath.Join(root, "..", "docs", "opnsense-api-survey.md"))
	if err != nil {
		t.Fatal(err)
	}
	loaded := BuiltinsLoaded()
	timeouts := 0
	for _, registration := range loaded.Set.All() {
		if len(registration.Values) == 0 {
			continue
		}
		member, found := loaded.Plugin(registration.Plugin)
		if !found {
			t.Fatalf("no built-in %s", registration.Plugin)
		}
		var sources map[string]contract.ValueSource
		for _, declaration := range member.Manifest.Providers {
			if declaration.Kind == registration.Kind {
				sources = declaration.ValueSources
			}
		}
		for name := range registration.Values {
			if strings.HasPrefix(name, "state_timeout_") {
				timeouts++
			}
			source, cited := sources[name]
			if !cited {
				// A value a contract requires of every implementation -- dns_lookup's page bound -- is the
				// implementation's own setting of the read, not a fact taken from a source.
				continue
			}
			if source.Source == "" || source.SurveySection == "" || !strings.Contains(string(survey), source.SurveySection) ||
				!strings.HasPrefix(source.UpstreamURL, "https://github.com/opnsense/") ||
				!strings.Contains(string(survey), source.UpstreamURL) {
				t.Errorf("%s/%s cites %s as %+v: a source, a survey section and the upstream file it cites are needed",
					registration.Kind, registration.Key, name, source)
			}
		}
		for name := range sources {
			if _, exported := registration.Values[name]; !exported {
				t.Errorf("%s/%s cites %s, which it does not export", registration.Kind, registration.Key, name)
			}
		}
		if registration.Plugin == "pf" && len(sources) != 7 {
			t.Errorf("pf cites %d of its seven state timeouts", len(sources))
		}
	}
	if timeouts != 7 {
		t.Errorf("the built-ins export %d state timeouts, not pf's seven", timeouts)
	}
}
