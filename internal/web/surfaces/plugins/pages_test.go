package plugins_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/web/surfaces/plugins"
	"github.com/NaejEL/opnview/internal/web/webtest"
)

// TestPluginDeclaredPagesAreServedAndTheNetflowSettingsArePluginSettings is PA-AC12: a plugin's declared
// pages are served in the listing and by GET /api/plugins/{name}/settings; the NetFlow surface and its
// configuration file are gone, /api/netflow/settings answering no route; and the two NetFlow settings keep
// their bounds and defaults as the netflow plugin's settings, stored under the keys the Go join reads.
func TestPluginDeclaredPagesAreServedAndTheNetflowSettingsArePluginSettings(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	harness.CompleteSetup()

	listed, _ := readListing(t, harness)
	entry, found := find(listed, "netflow")
	if !found {
		t.Fatal("the netflow plugin is not listed")
	}
	if len(entry.Pages) != 1 || entry.Pages[0].Key != "join" || entry.Pages[0].Label == "" ||
		entry.Pages[0].Help == "" || strings.Join(entry.Pages[0].Settings, ",") !=
		"join_tolerance_seconds,retention_flow_record_seconds" {
		t.Errorf("the listing carries the pages %+v", entry.Pages)
	}

	answer := harness.Get(strings.ReplaceAll(plugins.PathPluginSettingsPattern, "{name}", "netflow"))
	body := webtest.ReadBody(t, answer)
	if answer.StatusCode != http.StatusOK {
		t.Fatalf("GET the netflow settings answered %d: %.300s", answer.StatusCode, body)
	}
	var settings plugins.SettingsAnswer
	if err := json.Unmarshal([]byte(body), &settings); err != nil {
		t.Fatalf("the settings are not JSON: %v", err)
	}
	if len(settings.Pages) != 1 || settings.Pages[0].Key != "join" {
		t.Errorf("GET the settings carries the pages %+v", settings.Pages)
	}
	bounds := map[string][3]float64{}
	for _, value := range settings.Settings {
		if value.Min != nil && value.Max != nil {
			bounds[value.Key] = [3]float64{toFloat(value.Default), *value.Min, *value.Max}
		}
	}
	if bounds["join_tolerance_seconds"] != [3]float64{3, 1, 60} {
		t.Errorf("the join tolerance has the default and bounds %v, want 3, 1 and 60", bounds["join_tolerance_seconds"])
	}
	if bounds["retention_flow_record_seconds"] != [3]float64{0, 0, 9223372036} {
		t.Errorf("the flow record retention has the default and bounds %v, want 0, 0 and the largest duration",
			bounds["retention_flow_record_seconds"])
	}
	// The keys the settings are stored under, the plugin settings' <plugin>_<key> rule (Lot 2 hand-back, decision 2).
	if plugin.StorageKey("netflow", "join_tolerance_seconds") != "netflow_join_tolerance_seconds" ||
		plugin.StorageKey("netflow", "retention_flow_record_seconds") != "netflow_retention_flow_record_seconds" {
		t.Error("the netflow plugin's settings are not stored under <plugin>_<key>")
	}

	// A setting out of its bounds is refused through the plugin settings write, as it was by the NetFlow
	// surface it replaces.
	refused := harness.PostJSON(strings.ReplaceAll(plugins.PathPluginSettingsPattern, "{name}", "netflow"),
		`{"join_tolerance_seconds": 61}`, harness.SessionFormToken(), "")
	if refused.StatusCode != http.StatusBadRequest || condition(t, refused) != plugin.SettingRefusalOutOfRange {
		t.Errorf("a tolerance of 61 s answered %d", refused.StatusCode)
	}

	gone, unknown := harness.Get("/api/netflow/settings"), harness.Get("/api/no_such_route/settings")
	if gone.StatusCode == http.StatusOK || gone.StatusCode != unknown.StatusCode {
		t.Errorf("/api/netflow/settings answered %d, and a path no route serves %d", gone.StatusCode,
			unknown.StatusCode)
	}
	for _, path := range []string{"internal/web/surfaces/netflow", "internal/config/netflow.go"} {
		if _, err := os.Stat(filepath.Join("..", "..", "..", "..", path)); err == nil {
			t.Errorf("%s still exists", path)
		}
	}
}

// toFloat reads a JSON-decoded default as a number.
func toFloat(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case json.Number:
		number, _ := typed.Float64()
		return number
	}
	return -1
}
