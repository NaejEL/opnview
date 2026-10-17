package collect_test

import (
	"context"
	"testing"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/opnsense"
)

// TestDiscoveryAndEveryCollectorReachTheFirewallAndNothingElse is the one-outbound-
// destination rule, exercised over the whole of this cycle's collection rather than one
// call at a time.
//
// The transport behind the fake fails any host that is not the configured firewall, and it
// fails the test as well as the call, so a second destination cannot pass quietly. Every
// path the fake receives also has to be a registry entry, so a URL built by hand is caught
// too.
//
// What this rules out, concretely: the MaxMind download belongs to the maxmind plugin and must
// not have wandered into collection; and there is no CDN, no telemetry and no version check
// anywhere in the collection path.
func TestDiscoveryAndEveryCollectorReachTheFirewallAndNothingElse(t *testing.T) {
	t.Parallel()
	harness := collecttest.NewProbeHarness(t)
	ctx := context.Background()

	// A firewall with every source answering, so each collector actually does its work
	// rather than returning early on an unavailable provider.
	fake := harness.Fake
	fake.AnswerFixture(opnsense.InterfacesInfo, "interfaces_info.json")
	fake.AnswerFixture(opnsense.SystemTime, "system_time.json")
	fake.AnswerFixture(opnsense.InterfaceNames, "get_interface_names.json")
	fake.AnswerFixture(opnsense.SearchRule, "search_rule.json")
	fake.AnswerFixture(opnsense.FirewallLog, "firewall_log.json")
	fake.AnswerFixture(collecttest.IDSStatus, "ids_status_running.json")
	fake.AnswerFixture(collecttest.AlertLogs, "get_alert_logs.json")
	fake.AnswerFixture(collecttest.QueryAlerts, "query_alerts.json")
	fake.AnswerFixture(collecttest.NetflowIsEnabled, "netflow_is_enabled_local.json")
	fake.AnswerFixture(collecttest.TrafficTop, "traffic_top.json")
	fake.AnswerFixture(collecttest.TrafficInterface, "traffic_interface.json")
	fake.AnswerFixture(collecttest.SystemResources, "system_resources.json")
	fake.AnswerFixture(collecttest.SystemTemperature, "system_temperature.json")
	fake.AnswerFixture(opnsense.SystemTime, "system_time.json")
	fake.AnswerFixture(collecttest.SystemDisk, "system_disk.json")
	fake.AnswerFixture(collecttest.KeaStatus, "kea_status_disabled.json")
	fake.AnswerFixture(collecttest.DnsmasqStatus, "dnsmasq_status_running.json")
	fake.AnswerFixture(collecttest.DnsmasqSettings, "dnsmasq_settings.json")
	fake.AnswerFixture(collecttest.DnsmasqLeases, "dnsmasq_leases.json")
	fake.AnswerFixture(opnsense.ARPTable, "get_arp.json")
	fake.AnswerFixture(opnsense.NDPTable, "get_ndp.json")
	fake.AnswerFixture(collecttest.UnboundStatus, "unbound_status_running.json")
	fake.AnswerFixture(collecttest.UnboundSettings, "unbound_settings.json")
	fake.AnswerFixture(collecttest.UnboundIsEnabled, "unbound_is_enabled_on.json")
	fake.AnswerFixture(collecttest.SearchQueries, "search_queries_page1.json")

	if err := harness.Collector.RefreshDiscovery(ctx); err != nil {
		t.Fatalf("runtime discovery: %v", err)
	}
	if err := harness.Collector.ProbeAll(ctx); err != nil {
		t.Fatalf("the probe round: %v", err)
	}

	for name, collect := range map[string]func(context.Context) error{
		"the filter log":          harness.Collector.CollectFirewallLog,
		"the security events":     harness.Collector.CollectSecurityEvent,
		"the sampled measurement": harness.Collector.CollectMeasurement,
		"the DHCP leases":         harness.Collector.CollectDHCPLease,
		"the resolver lookups":    harness.Collector.CollectDNSLookup,
	} {
		if err := collect(ctx); err != nil {
			t.Errorf("collecting %s: %v", name, err)
		}
	}

	// The transport has already failed the test if anything left for another host. This is
	// the other half: nothing asked for a path outside the registry.
	fake.AssertEveryPathIsRegistered()

	// And the whole of it wrote something, so the run above was not a sequence of early
	// returns that happen to make no request at all.
	for _, table := range []string{"flow", "security_event", "dns_resolution", "dhcp_lease",
		"measurement_sample", "interface", "interface_map", "rule", "client"} {
		if stored := collecttest.CountRows(t, harness.Store, table); stored == 0 {
			t.Errorf("%s is empty, so this run proved nothing about that collector", table)
		}
	}
}

// TestNoCollectorWritesToTheFirewall is the read-only rule over the whole collection path.
//
// The client refuses a mutating command before a request exists, so this is the recording
// half: every method the fake saw is a read, and no path names a command that could write.
func TestNoCollectorWritesToTheFirewall(t *testing.T) {
	t.Parallel()
	harness := collecttest.NewProbeHarness(t)
	ctx := context.Background()
	collecttest.ArrangeDiscoverableFirewall(t, harness.Fake, harness.Collector)
	harness.Fake.AnswerFixture(opnsense.FirewallLog, "firewall_log.json")
	harness.Fake.AnswerFixture(collecttest.DnsmasqStatus, "dnsmasq_status_running.json")
	harness.Fake.AnswerFixture(collecttest.DnsmasqSettings, "dnsmasq_settings.json")
	harness.Fake.AnswerFixture(opnsense.ARPTable, "get_arp.json")
	harness.Fake.AnswerFixture(opnsense.NDPTable, "get_ndp.json")

	if err := harness.Collector.ProbeAll(ctx); err != nil {
		t.Logf("the probe round reported: %v", err)
	}
	if err := harness.Collector.CollectFirewallLog(ctx); err != nil {
		t.Fatalf("collecting the filter log: %v", err)
	}
	if err := harness.Collector.CollectDHCPLease(ctx); err != nil {
		t.Logf("collecting the leases reported: %v", err)
	}

	if harness.Fake.RequestCount() == 0 {
		t.Fatal("no request was made, so this assertion is vacuous")
	}
	for _, request := range harness.Fake.Requests() {
		switch request.Method {
		case "GET", "POST":
			// Both are read-only here: OPNsense has no framework-level method routing and
			// its search and get helpers contain no write path at all, which is why the
			// path rather than the verb is what has to be checked.
		default:
			t.Errorf("a request used the method %s", request.Method)
		}
		if command := opnsense.MutatingCommand(request.Path); command != "" {
			t.Errorf("a request named the mutating command %q in %s", command, request.Path)
		}
	}
}
