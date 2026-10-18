package surfaces_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/web"
)

// The catalogue's own rules, which hold whatever draws its strings. What the front draws -- that
// no text node, label or option it renders is a literal, and that every key it uses exists -- is
// the front's test (specs/SPEC-step-7-frontend.md, F7-AC5).

// TestEveryKeyTheGoCodeChoosesExists is AC21's Go half: every catalogue key the core, a surface
// or a built-in plugin chooses is in the catalogue, so a code a JSON answer carries always has the
// words the front shows for it.
func TestEveryKeyTheGoCodeChoosesExists(t *testing.T) {
	t.Parallel()
	catalogue, err := web.ProductCatalogue(web.SourceLanguage)
	if err != nil {
		t.Fatalf("loading the catalogue: %v", err)
	}
	keys := web.AllHandlerKeys()
	if len(keys) == 0 {
		t.Fatal("the Go code chooses no key, so this assertion is vacuous")
	}
	for _, key := range keys {
		if _, present := catalogue.Strings[string(key)]; !present {
			t.Errorf("the catalogue has no string for %q, which the Go code chooses", key)
		}
	}
}

// TestNoTwoEntriesCarryTheSameWordsUndecided: two entries saying the same thing are either a
// decision, written here, or a duplicate of an entry that is already there.
func TestNoTwoEntriesCarryTheSameWordsUndecided(t *testing.T) {
	t.Parallel()
	catalogue, err := web.ProductCatalogue(web.SourceLanguage)
	if err != nil {
		t.Fatalf("loading the catalogue: %v", err)
	}
	// The entries that carry the same words ON PURPOSE: a view's title and its heading are one
	// sentence, and the action that leads to a view is the words of the view. Every group is a
	// decision written here; a new entry joining one, or a new pair nobody decided on, fails until
	// somebody says which it is.
	declaredSameWording := [][]string{
		{"setup.title", "setup.heading"},
		{"action.sign_in", "sign_in.title", "sign_in.heading"},
		{"action.reset_password", "recover.title", "recover.heading"},
		// The resolver's lookups are one thing, whether named as the kind collected or as the
		// dataset of the data catalogue that serves them: one thing, one wording.
		{"collection.kind.dns_lookup", "dataset.dns"},
	}
	declared := map[string]bool{}
	for _, group := range declaredSameWording {
		sorted := append([]string(nil), group...)
		sort.Strings(sorted)
		declared[strings.Join(sorted, " + ")] = true
	}
	byValue := map[string][]string{}
	for key, value := range catalogue.Strings {
		trimmed := strings.TrimSpace(value)
		byValue[trimmed] = append(byValue[trimmed], key)
	}
	for _, all := range byValue {
		keys := undecidedWithinOneOwner(all)
		if len(keys) < 2 {
			continue
		}
		sort.Strings(keys)
		name := strings.Join(keys, " + ")
		if !declared[name] {
			t.Errorf("%s carry the same words, which nobody decided on: either one of "+
				"them is the entry the other should reuse, or the group belongs in the "+
				"list of deliberate ones", name)
		}
	}
	// And a declared group that no longer shares its wording is stale rather than harmless.
	for _, group := range declaredSameWording {
		for _, key := range group {
			value, present := catalogue.Strings[key]
			if !present {
				t.Errorf("%q is declared to share its wording and is in no catalogue", key)
				continue
			}
			if strings.TrimSpace(value) != strings.TrimSpace(catalogue.Strings[group[0]]) {
				t.Errorf("%q and %q are declared to say the same thing and no longer do", key, group[0])
			}
		}
	}
}

// undecidedWithinOneOwner narrows a group of entries sharing their words to the ones a decision is
// owed for. A WIDGET PLUGIN'S WORDS ARE ITS OWN (CLAUDE.md: a widget is a directory a third party adds
// without touching anything shipped): it cannot borrow another plugin's entry, which may not be
// installed, so two plugins saying the same word is not a duplicate anybody can remove. What stays
// owed is a duplicate one owner could remove: two entries of the core, two of one plugin, or a
// plugin's entry repeating the core's, which the plugin should reuse. The plural forms of one entry
// (.one, .other) saying the same words are one entry.
func undecidedWithinOneOwner(keys []string) []string {
	owner := func(key string) string {
		if rest, found := strings.CutPrefix(key, "plugin."); found {
			name, _, _ := strings.Cut(rest, ".")
			return "plugin." + name
		}
		return "core"
	}
	entry := func(key string) string {
		for _, form := range []string{".one", ".other"} {
			if trimmed, found := strings.CutSuffix(key, form); found {
				return trimmed
			}
		}
		return key
	}
	byOwner := map[string]map[string]string{}
	for _, key := range keys {
		if byOwner[owner(key)] == nil {
			byOwner[owner(key)] = map[string]string{}
		}
		byOwner[owner(key)][entry(key)] = key
	}
	var owed []string
	for name, entries := range byOwner {
		// Entries of one owner, or of one plugin beside the core's.
		if len(entries) > 1 || (name != "core" && byOwner["core"] != nil) {
			for _, key := range entries {
				owed = append(owed, key)
			}
		}
	}
	if core := byOwner["core"]; core != nil && len(owed) > 0 {
		for _, key := range core {
			if !contains(owed, key) {
				owed = append(owed, key)
			}
		}
	}
	if len(owed) < 2 {
		return nil
	}
	return owed
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// TestNoCatalogueEntryIsExplanatoryCopy is AC22's catalogue half.
//
// THE PHRASES ARE THE CHARACTERISTIC ONES OF THE TWO STANDING LIMITATIONS, taken from README.md
// and ROADMAP.md. Both limitations are documented, in those files, and neither is recited on a
// screen: ROADMAP.md step 7 forbids explanatory copy, and the observation-point limit is
// explicitly "documented, not printed". Every string the front can show is a catalogue entry.
func TestNoCatalogueEntryIsExplanatoryCopy(t *testing.T) {
	t.Parallel()
	forbidden := []string{
		// The observation-point limit.
		"only sees what crosses",
		"crosses the router",
		"lower bound",
		"observation point",
		"switched and never reaches",
		// What the key file does and does not protect.
		"copied, backed up",
		"sent by mistake",
		"already has the machine",
		"does not travel with it",
		// Anything that explains the product rather than naming a thing.
		"this page",
		"in order to",
		"you can",
		"you will",
		"for example",
		"please",
		"note that",
	}
	catalogue, err := web.ProductCatalogue(web.SourceLanguage)
	if err != nil {
		t.Fatalf("loading the catalogue: %v", err)
	}
	for key, value := range catalogue.Strings {
		lowered := strings.ToLower(value)
		for _, phrase := range forbidden {
			if strings.Contains(lowered, phrase) {
				t.Errorf("the catalogue entry %s carries the explanatory phrase %q", key, phrase)
			}
		}
	}
}
