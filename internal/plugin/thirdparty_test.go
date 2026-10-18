package plugin_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/collect/providertest"
	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/plugin/listing"
	"github.com/NaejEL/opnview/internal/push"
	"github.com/NaejEL/opnview/internal/store/storetest"
	"github.com/NaejEL/opnview/internal/widget"
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/params"
)

// The third-party proof (PA20 and PA-AC27 of the plugin-API amendment to
// specs/SPEC-backend-completion-plugins.md): testdata/plugins/example_reporter is a plugin a third party
// could write, holding JSON, JavaScript and SQL alone. Copied into a data directory, it receives datagrams
// decoded by its script into its own table, derives from them -- after each stored batch and on a cadence,
// reading the core datasets leg, this_firewall_address and interface -- a measure of its own, carries a
// settings page, and a widget declaring volumes whose statement declares the generic twin. Every figure the
// core serves in the measure is checked against a computation made here from the datagrams alone, and no
// file the product ships is edited for any of it.

const thirdParty = "example_reporter"

// report is one datagram the reporting device sends: the tuple of the direction it counted, and what it
// counted.
type report struct {
	src       string
	srcPort   int64
	dst       string
	dstPort   int64
	bytes     int64
	packets   int64
	estimated bool
}

func (r report) datagram() string {
	estimated := 0
	if r.estimated {
		estimated = 1
	}
	return fmt.Sprintf("REPORT %s %d %s %d %d %d %d", r.src, r.srcPort, r.dst, r.dstPort, r.bytes, r.packets,
		estimated)
}

// connection is one logged connection of the test's network, by its tuple.
type connection struct {
	src     string
	srcPort int64
	dst     string
	dstPort int64
	bytes   int64
}

// expectedCoverage is the measure's figures over some connections, computed from the reports alone: a
// direction is reported when a report names its tuple, a connection complete when both directions are,
// partial when one is, and not known when no report reached the plugin for it (it then has no row).
type expectedCoverage struct {
	bytes, packets                             int64
	complete, partial, unmeasured, notKnown    int64
	reportedConnections, unreportedConnections int64
	estimated                                  int64
}

func expect(connections []connection, reports []report) expectedCoverage {
	var want expectedCoverage
	for _, c := range connections {
		forward, reverse := []report{}, []report{}
		for _, r := range reports {
			switch {
			case r.src == c.src && r.srcPort == c.srcPort && r.dst == c.dst && r.dstPort == c.dstPort:
				forward = append(forward, r)
			case r.src == c.dst && r.srcPort == c.dstPort && r.dst == c.src && r.dstPort == c.srcPort:
				reverse = append(reverse, r)
			}
		}
		if len(forward) == 0 && len(reverse) == 0 {
			want.notKnown++
			continue
		}
		estimated := false
		for _, r := range append(append([]report{}, forward...), reverse...) {
			want.bytes += r.bytes
			want.packets += r.packets
			estimated = estimated || r.estimated
		}
		switch {
		case len(forward) > 0 && len(reverse) > 0:
			want.complete++
			want.reportedConnections++
		default:
			want.partial++
			want.reportedConnections++
			want.unreportedConnections++
		}
		if estimated {
			want.estimated++
		}
	}
	return want
}

// treeDigest is a digest of every file the product ships under the directories named, relative to the
// repository root: the proof that the test edits none of them.
func treeDigest(t *testing.T, root string, directories ...string) string {
	t.Helper()
	hash := sha256.New()
	for _, directory := range directories {
		err := filepath.WalkDir(filepath.Join(root, directory), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			relative, _ := filepath.Rel(root, path)
			fmt.Fprintf(hash, "%s\x00%d\x00", filepath.ToSlash(relative), len(content))
			hash.Write(content)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// copyPlugin copies a fixture plugin into a data directory, refusing any file that is not JSON,
// JavaScript or SQL: a third party adds a feature without Go.
func copyPlugin(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(from, path)
		target := filepath.Join(to, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		switch filepath.Ext(path) {
		case ".json", ".js", ".sql":
		default:
			t.Fatalf("the third-party fixture holds %s, which is not JSON, JavaScript or SQL", relative)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, 0o640)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestAThirdPartyPluginAddsAMeasureInJavaScriptAlone is PA-AC27.
func TestAThirdPartyPluginAddsAMeasureInJavaScriptAlone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	shipped := []string{"plugins", "internal", "cmd"}
	before := treeDigest(t, root, shipped...)

	// The network: one inside interface with one client, the upstream interface holding this firewall's
	// address. Three connections of the client to the outside, and one from the outside to this firewall.
	network := storetest.NewNetwork(t, 1, 1)
	client := network.Clients[0].Address
	var connections []connection
	var specs []storetest.Flow
	for index := int64(0); index < 3; index++ {
		c := connection{src: client, srcPort: 50000 + index, dst: storetest.Outside(int(index), false), dstPort: 443,
			bytes: 100 + index}
		connections = append(connections, c)
		specs = append(specs, storetest.Flow{ObservedAt: network.Now - 600 - index*10, Device: network.Clients[0].Device,
			Direction: "in", Src: c.src, Dst: c.dst, SrcPort: storetest.Ptr(c.srcPort), DstPort: storetest.Ptr(c.dstPort),
			Protocol: "tcp", Action: "pass", Bytes: c.bytes})
	}
	toFirewall := connection{src: storetest.Outside(98, false), srcPort: 40000, dst: network.Upstream.Address,
		dstPort: 22, bytes: 77}
	specs = append(specs, storetest.Flow{ObservedAt: network.Now - 650, Device: network.Upstream.Device,
		Direction: "in", Src: toFirewall.src, Dst: toFirewall.dst, SrcPort: storetest.Ptr(toFirewall.srcPort),
		DstPort: storetest.Ptr(toFirewall.dstPort), Protocol: "tcp", Action: "pass", Bytes: toFirewall.bytes})
	addresses := network.Insert(t, specs)
	network.DerivePairs(t, addresses, network.Now-3600, network.Now)
	network.ClassifyAndRefresh(t, addresses)
	var loggedBytes int64
	for _, c := range connections {
		loggedBytes += c.bytes
	}

	// The plugin, copied as files into a data directory, and loaded beside the built-ins.
	dataDir := t.TempDir()
	copyPlugin(t, filepath.Join("testdata", "plugins", thirdParty), filepath.Join(dataDir, plugin.PluginsDirectory,
		thirdParty))
	loaded, err := plugin.Load(ctx, plugin.Options{DataDir: dataDir, Store: network.DB})
	if err != nil {
		t.Fatal(err)
	}
	for _, refusal := range loaded.Refusals() {
		t.Errorf("refused: %+v", refusal)
	}
	if _, found := loaded.Plugin(thirdParty); !found {
		t.Fatal("the third-party plugin was not loaded")
	}
	clock := collecttest.NewFixedClock(time.Unix(network.Now, 0))
	collector := collecttest.NewTestCollectorWithSources(t, providertest.NewClient(t, providertest.NewFirewall(t)),
		network.DB, clock, loaded.Set)
	var derivation provider.Derivation
	for _, held := range loaded.Set.Derivations() {
		if held.Name() == thirdParty+".attribute" {
			derivation = held
		}
	}
	subscriptions, err := loaded.PushSubscriptions(plugin.PushEnvironment{Store: network.DB, Session: collector,
		Kinds: collector})
	if err != nil {
		t.Fatal(err)
	}
	// The hub the service routes push entries by, with a listener of the entry's protocol, attached to the
	// loaded plugins so that the provider's availability reads its listeners and their liveness.
	sender := netip.MustParseAddr("192.0.2.40")
	hub := push.NewHub(push.Options{Now: clock.Now})
	subscribed := false
	for _, subscription := range subscriptions {
		if subscription.ID == thirdParty+".reports" {
			if err := hub.Subscribe(subscription); err != nil {
				t.Fatal(err)
			}
			subscribed = true
		}
	}
	if derivation == nil || !subscribed {
		t.Fatalf("the derivation (%v) or the push entry's subscription (%v) is missing", derivation, subscribed)
	}
	if err := hub.AddLoopbackListener("datagram", push.TransportUDP); err != nil {
		t.Fatal(err)
	}
	hub.Senders().Set([]netip.Addr{sender})
	loaded.AttachPushHub(hub)
	if derivation.Cadence() != 300*time.Second {
		t.Errorf("the derivation's cadence is %s, not the 300 s its manifest declares", derivation.Cadence())
	}

	// deliver hands datagrams to the push entry through the hub, as its socket would -- decoded by the real
	// datagram protocol and by the plugin's decode function -- and stores them as one batch.
	stored := func() int64 {
		return storetest.QueryInt(t, network.DB, "SELECT count(*) FROM plugin_"+thirdParty+".reports")
	}
	deliver := func(reports ...report) {
		t.Helper()
		before := stored()
		for _, r := range reports {
			if err := hub.Deliver(ctx, "datagram", push.TransportUDP, []byte(r.datagram()), sender); err != nil {
				t.Fatal(err)
			}
		}
		hub.Flush(ctx)
		if after := stored(); after-before != int64(len(reports)) {
			t.Fatalf("the push entry stored %d of %d messages", after-before, len(reports))
		}
	}
	// probe runs the probe round's pass over the plugin's kind -- which records the provider's availability and
	// decides its activation, as for every kind -- and answers whether the provider is active and the outcome
	// its availability row holds.
	probe := func() (bool, string) {
		t.Helper()
		if err := collector.ProbeKind(ctx, "example_report"); err != nil {
			t.Fatalf("the probe round over the plugin's kind: %v", err)
		}
		var active bool
		var outcome string
		if err := network.DB.DB().QueryRowContext(ctx, `SELECT p.is_active, a.outcome FROM provider AS p
			JOIN source_availability AS a ON a.provider_id = p.id
			WHERE p.kind = 'example_report' AND p.provider_key = $1`, thirdParty).Scan(&active, &outcome); err != nil {
			t.Fatal(err)
		}
		return active, outcome
	}
	derive := func() provider.DerivationOutcome {
		t.Helper()
		outcome, err := derivation.Run(ctx, collector.Collector, clock.Now().Unix())
		if err != nil {
			t.Fatalf("the derivation failed: %v", err)
		}
		if _, err := network.DB.RefreshAggregates(ctx, 0, network.Now+5, outcome.Instants); err != nil {
			t.Fatal(err)
		}
		return outcome
	}

	// The widget service, with the plugin's widget.
	service, err := widget.New(network.DB, widget.Options{Now: func() time.Time { return time.Unix(network.Now, 0) },
		Plugins: loaded.WidgetSources()})
	if err != nil {
		t.Fatal(err)
	}
	if refused := service.Refusals(); len(refused) != 0 {
		t.Fatalf("widgets refused: %+v", refused)
	}
	const widgetType = "example_reported_total"
	preset, served := service.Preset(widgetType)
	if !served || !preset.Volumes || strings.Join(preset.Providers, ",") != "example_report" {
		t.Fatalf("the third party's widget is not served as a volume type bound to its own kind: %+v", preset)
	}
	measureOffered := false
	for _, parameter := range preset.Params {
		if parameter.Key == params.VolumeMeasureKey {
			measureOffered = strings.Contains(","+strings.Join(parameter.Values, ",")+",", ",example_reported,")
		}
	}
	if !measureOffered {
		t.Errorf("volume_measure does not offer the plugin's measure: %+v", preset.Params)
	}
	respond := func(parameters string) (envelope.Response, map[string]any) {
		t.Helper()
		response, err := service.Respond(ctx, widgetType, parameters)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(response.Data)
		if err != nil {
			t.Fatal(err)
		}
		var data struct {
			Total map[string]any `json:"total"`
		}
		if err := json.Unmarshal(encoded, &data); err != nil {
			t.Fatal(err)
		}
		return response, data.Total
	}
	measured := `{"period": "1h", "volume_measure": "example_reported"}`
	checkFigures := func(stage string, total map[string]any, want expectedCoverage) {
		t.Helper()
		coverage, _ := total["coverage"].(map[string]any)
		states, _ := coverage["direction_states"].(map[string]any)
		got := fmt.Sprint(total["measure"], total["bytes"], total["packets"], coverage["complete"], coverage["partial"],
			coverage["unmeasured"], coverage["not_known"], states["reported"], states["unreported"],
			coverage["estimated"])
		expected := fmt.Sprint("example_reported", float64(want.bytes), float64(want.packets), float64(want.complete),
			float64(want.partial), float64(want.unmeasured), float64(want.notKnown), float64(want.reportedConnections),
			float64(want.unreportedConnections), float64(want.estimated))
		if got != expected {
			t.Errorf("%s: the measure's figures are %s, the reports sum to %s (%v)", stage, got, expected, total)
		}
	}

	// No active provider of the measure's kind: best_available answers the logged figures, with the
	// measure's own state for it.
	if response, total := respond(`{"period": "1h"}`); total["measure"] != envelope.MeasureLogged ||
		total["bytes"] != float64(loggedBytes) || !params.Contains(response.States, "reporter_off") {
		t.Errorf("with no active provider best_available answered %v %v", total, response.States)
	}
	// The probe round, with the listener open and nothing received: it records that no message arrived, and
	// activates the provider as it activates every implementation the firewall's configuration separates --
	// activeness is not reachability (internal/collect/probe.go).
	if active, outcome := probe(); !active || outcome != "no_message_received" {
		t.Errorf("before any message the probe round left the provider active %v, outcome %s", active, outcome)
	}

	// A first batch: one direction of two connections, and a report about the connection to this firewall,
	// which the derivation leaves out because one end is one of this firewall's addresses.
	first := []report{
		{src: connections[0].src, srcPort: connections[0].srcPort, dst: connections[0].dst, dstPort: 443, bytes: 1000,
			packets: 10},
		{src: connections[1].src, srcPort: connections[1].srcPort, dst: connections[1].dst, dstPort: 443, bytes: 500,
			packets: 5, estimated: true},
		{src: toFirewall.src, srcPort: toFirewall.srcPort, dst: toFirewall.dst, dstPort: toFirewall.dstPort, bytes: 999,
			packets: 9},
	}
	deliver(first...)
	// The entry is receiving: the next probe round finds the provider healthy, and keeps it active.
	if active, outcome := probe(); !active || outcome != "healthy" {
		t.Errorf("with its entry receiving the probe round left the provider active %v, outcome %s", active, outcome)
	}
	select {
	case <-derivation.Wake():
	default:
		t.Error("the stored batch did not wake the derivation")
	}
	if outcome := derive(); !outcome.Ran || outcome.Written == 0 {
		t.Fatalf("the derivation after the first batch: %+v", outcome)
	}
	if left := storetest.QueryInt(t, network.DB, `SELECT count(*) FROM connection_measure AS m
		JOIN flow AS f ON f.id = m.connection_flow_id WHERE m.measure = 'example_reported' AND f.dst_address = $1`,
		toFirewall.dst); left != 0 {
		t.Errorf("the connection to this firewall has %d measure rows", left)
	}
	// Not every connection is covered: best_available falls back to the logged figures with the measure's
	// not-covering state; the measure asked for explicitly answers its figures and the same state.
	response, total := respond(`{"period": "1h"}`)
	if total["measure"] != envelope.MeasureLogged || total["bytes"] != float64(loggedBytes) ||
		!params.Contains(response.States, "reporting_partial") {
		t.Errorf("under partial coverage best_available answered %v %v", total, response.States)
	}
	response, total = respond(measured)
	checkFigures("after the first batch", total, expect(connections, first))
	if !params.Contains(response.States, "reporting_partial") {
		t.Errorf("under partial coverage the measure answered the states %v", response.States)
	}

	// A second batch covers every direction left: best_available now answers the measure.
	second := []report{
		{src: connections[0].dst, srcPort: 443, dst: connections[0].src, dstPort: connections[0].srcPort, bytes: 3000,
			packets: 20, estimated: true},
		{src: connections[1].dst, srcPort: 443, dst: connections[1].src, dstPort: connections[1].srcPort, bytes: 700,
			packets: 7},
		{src: connections[2].src, srcPort: connections[2].srcPort, dst: connections[2].dst, dstPort: 443, bytes: 200,
			packets: 2},
		{src: connections[2].dst, srcPort: 443, dst: connections[2].src, dstPort: connections[2].srcPort, bytes: 300,
			packets: 3},
	}
	deliver(second...)
	if outcome := derive(); !outcome.Ran || outcome.Written == 0 {
		t.Fatalf("the derivation after the second batch: %+v", outcome)
	}
	all := append(append([]report{}, first...), second...)
	response, total = respond(`{"period": "1h"}`)
	if response.Parameters[params.VolumeMeasureKey] != params.VolumeMeasureBestAvailable {
		t.Errorf("best_available is not the default: %v", response.Parameters)
	}
	checkFigures("best_available, every connection covered", total, expect(connections, all))
	if len(response.States) != 0 {
		t.Errorf("a covered window answered the states %v", response.States)
	}
	if response, total := respond(`{"period": "1h", "volume_measure": "logged"}`); total["measure"] !=
		envelope.MeasureLogged || total["bytes"] != float64(loggedBytes) || len(response.States) != 0 {
		t.Errorf("logged answered %v %v", total, response.States)
	}

	// On its cadence, with nothing new stored, the derivation runs and writes nothing.
	if outcome := derive(); !outcome.Ran || outcome.Written != 0 {
		t.Errorf("the cadence run over the same reports: %+v", outcome)
	}

	// The settings and the page are served, as the listing and GET /api/plugins/{name}/settings serve them.
	loadedPlugin, _ := loaded.Plugin(thirdParty)
	values, err := loadedPlugin.SettingValues(ctx, network.DB)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].Key != "lookback_seconds" || values[0].Value != float64(86400) {
		t.Errorf("the settings are %+v", values)
	}
	listed, err := listing.Listing(loaded, ctx, network.DB)
	if err != nil {
		t.Fatal(err)
	}
	var pages, measures []string
	for _, entry := range listed {
		if entry.Name != thirdParty {
			continue
		}
		for _, page := range entry.Pages {
			pages = append(pages, page.Key+":"+strings.Join(page.Settings, ","))
		}
		for _, measure := range entry.Measures {
			measures = append(measures, measure.Name)
		}
	}
	sort.Strings(pages)
	if strings.Join(pages, " ") != "reporting:lookback_seconds" || strings.Join(measures, ",") != "example_reported" {
		t.Errorf("the listing serves the pages %v and the measures %v", pages, measures)
	}

	if after := treeDigest(t, root, shipped...); after != before {
		t.Error("a shipped file changed while the third party's plugin was added")
	}
}
