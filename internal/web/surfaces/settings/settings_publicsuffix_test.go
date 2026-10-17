package settings_test

import (
	"context"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect/registrabledomain"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/surfaces/settings"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"github.com/NaejEL/opnview/internal/web/webtest/collectiontest"
	"github.com/NaejEL/opnview/internal/web/webtest/settingstest"
)

// publicSuffixOf is the Public Suffix List part of the settings answer.
func publicSuffixOf(t *testing.T, h *webtest.Harness) map[string]any {
	t.Helper()
	part, _ := settingstest.GetJSON(h, settings.PathAPISettings)["public_suffix"].(map[string]any)
	if part == nil {
		t.Fatal("the settings answer has no Public Suffix List part")
	}
	return part
}

// TestEachRefreshOutcomeAnswersItsOwnCode is AC14.
func TestEachRefreshOutcomeAnswersItsOwnCode(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	if state := publicSuffixOf(t, h)["state"]; state != web.ConditionOf(msgPublicSuffixNotYetRefreshed) {
		t.Errorf("a list never refreshed answers %v", state)
	}
	for _, outcome := range []struct {
		probe string
		state vocab.AvailabilityState
		key   messageKey
	}{
		{registrabledomain.ProbeDownloaded, vocab.StateReachable, msgPublicSuffixDownloaded},
		{registrabledomain.ProbeNotModified, vocab.StateReachable, msgPublicSuffixNotModified},
		{registrabledomain.ProbeFailed, vocab.StateUnavailable, msgPublicSuffixFailed},
		{registrabledomain.ProbeMalformed, vocab.StateUnavailable, msgPublicSuffixMalformed},
		{reads.ProbeNotYetProbed, vocab.StateUnavailable, msgPublicSuffixNotYetRefreshed},
		{"a_probe_nobody_wrote", vocab.StateReachable, msgPublicSuffixFailed},
	} {
		settingstest.SetPublicSuffixProbe(h, outcome.state, outcome.probe, nil)
		if state := publicSuffixOf(t, h)["state"]; state != web.ConditionOf(outcome.key) {
			t.Errorf("the probe %q answers %v, not %s", outcome.probe, state, web.ConditionOf(outcome.key))
		}
	}
}

// TestTheCopysFiguresComeFromTheProcess is AC15.
func TestTheCopysFiguresComeFromTheProcess(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	// A detail that reads like figures, and no copy held: nothing of it is answered.
	misleading := "VERSION 1999-12-31_00-00-00_UTC, 4321 rules"
	settingstest.SetPublicSuffixProbe(h, vocab.StateReachable, registrabledomain.ProbeDownloaded, &misleading)
	part := publicSuffixOf(t, h)
	if part["in_use"] != false || part["version"] != nil || part["rules"] != nil || part["confirmed_at"] != nil {
		t.Errorf("with no copy held the answer is %v", part)
	}

	// A copy held: its version and rule count, from the process.
	confirmed := h.Clock.Now().Unix() - 600
	h.Suffix.Hold("2026-10-09_08-15-00_UTC", 9876, confirmed)
	part = publicSuffixOf(t, h)
	if part["in_use"] != true || part["version"] != "2026-10-09_08-15-00_UTC" || part["rules"] != float64(9876) ||
		part["confirmed_at"] != float64(confirmed) {
		t.Errorf("with a copy held the answer is %v", part)
	}

	// A failed refresh over the copy still in use answers both facts.
	settingstest.SetPublicSuffixProbe(h, vocab.StateReachable, registrabledomain.ProbeFailed, nil)
	part = publicSuffixOf(t, h)
	if part["state"] != web.ConditionOf(msgPublicSuffixFailed) || part["rules"] != float64(9876) {
		t.Errorf("a failed refresh over a held copy does not answer both the failure and the copy: %v", part)
	}

	// THE GREP HALF: no code of the interface reads an availability detail except the
	// Sources surface, which answers it verbatim as data.
	err := filepath.WalkDir("../..", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() {
			if strings.HasPrefix(name, "_") || name == "webtest" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(source), ".Detail") && !strings.HasSuffix(filepath.ToSlash(path), "surfaces/sources/status.go") {
			t.Errorf("%s reads an availability or gap detail", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("listing the interface: %v", err)
	}
}

// TestTheRefreshIntervalIsSavedAndRefused: the save writes the interval and hands it to the
// running service; a refusal stores nothing.
func TestTheRefreshIntervalIsSavedAndRefused(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	if status, answer := settingstest.SendJSON(h, settings.PathAPIPublicSuffix,
		map[string]any{"refresh_interval_seconds": 43200}); status != http.StatusOK {
		t.Fatalf("saving the interval answered %d %v", status, answer)
	}
	if got := collectiontest.Setting(h, webtest.FieldPublicSuffixInterval); got != "43200" {
		t.Errorf("the stored interval is %q", got)
	}
	if h.Live.Get().PublicSuffixRefreshInterval.Seconds() != 43200 {
		t.Error("the live holder was not handed the saved interval")
	}
	status, answer := settingstest.SendJSON(h, settings.PathAPIPublicSuffix,
		map[string]any{"refresh_interval_seconds": "daily"})
	if status != http.StatusBadRequest || answer["condition"] != "collection_value_invalid" ||
		answer["field"] != "refresh_interval_seconds" {
		t.Errorf("a refused interval answered %d %v", status, answer)
	}
	if got := collectiontest.Setting(h, webtest.FieldPublicSuffixInterval); got != "43200" {
		t.Errorf("a refused interval was stored: %q", got)
	}
}

// TestTheRefreshIntervalIsStoredOnlyWhenItChanges: saving the interval in force -- the default --
// leaves no row behind; a different interval is stored, and an absent one changes nothing.
func TestTheRefreshIntervalIsStoredOnlyWhenItChanges(t *testing.T) {
	t.Parallel()
	h := collectiontest.SignedInHarness(t)
	inForce := int64(config.Defaults().PublicSuffixRefreshInterval / time.Second)
	if status, answer := settingstest.SendJSON(h, settings.PathAPIPublicSuffix,
		map[string]any{"refresh_interval_seconds": inForce}); status != http.StatusOK {
		t.Fatalf("saving the interval in force answered %d %v", status, answer)
	}
	if _, present, err := h.Store.Setting(context.Background(), webtest.FieldPublicSuffixInterval); err != nil || present {
		t.Error("saving the interval in force turned the default into a stored row")
	}
	if status, answer := settingstest.SendJSON(h, settings.PathAPIPublicSuffix, map[string]any{}); status != http.StatusOK {
		t.Fatalf("a save carrying no interval answered %d %v", status, answer)
	}
	if _, present, err := h.Store.Setting(context.Background(), webtest.FieldPublicSuffixInterval); err != nil || present {
		t.Error("a save carrying no interval stored one")
	}
	if status, answer := settingstest.SendJSON(h, settings.PathAPIPublicSuffix,
		map[string]any{"refresh_interval_seconds": 43200}); status != http.StatusOK {
		t.Fatalf("saving 43200 answered %d %v", status, answer)
	}
	if got := publicSuffixOf(t, h)["refresh_interval_seconds"]; got != float64(43200) {
		t.Errorf("the answer reports the interval in force as %v", got)
	}
}
