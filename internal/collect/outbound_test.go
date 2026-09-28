package collect

import (
	"context"
	"testing"

	"github.com/NaejEL/opnview/internal/opnsense"
)

// TestDiscoveryAndAllFiveCollectorsReachTheFirewallAndNothingElse is the one-outbound-
// destination rule, exercised over the whole of this cycle's collection rather than one
// call at a time.
//
// The transport behind the fake fails any host that is not the configured firewall, and it
// fails the test as well as the call, so a second destination cannot pass quietly. Every
// path the fake receives also has to be a registry entry, so a URL built by hand is caught
// too.
//
// What this rules out, concretely: the MaxMind download belongs to cycle 4C and must not
// have wandered into this one; and there is no CDN, no telemetry and no version check
// anywhere in the collection path.
func TestDiscoveryAndAllFiveCollectorsReachTheFirewallAndNothingElse(t *testing.T) {
	harness := newProbeHarness(t)
	ctx := context.Background()

	// A firewall with every source answering, so each collector actually does its work
	// rather than returning early on an unavailable provider.
	fake := harness.fake
	fake.answerFixture(opnsense.InterfacesInfo, "interfaces_info.json")
	fake.answerFixture(opnsense.InterfaceNames, "get_interface_names.json")
	fake.answerFixture(opnsense.SearchRule, "search_rule.json")
	fake.answerFixture(opnsense.FirewallLog, "firewall_log.json")
	fake.answerFixture(opnsense.IDSStatus, "ids_status_running.json")
	fake.answerFixture(opnsense.AlertLogs, "get_alert_logs.json")
	fake.answerFixture(opnsense.QueryAlerts, "query_alerts.json")
	fake.answerFixture(opnsense.NetflowIsEnabled, "netflow_is_enabled_local.json")
	fake.answerFixture(opnsense.TrafficTop, "traffic_top.json")
	fake.answerFixture(opnsense.TrafficInterface, "traffic_interface.json")
	fake.answerFixture(opnsense.SystemResources, "system_resources.json")
	fake.answerFixture(opnsense.SystemTemperature, "system_temperature.json")
	fake.answerFixture(opnsense.SystemTime, "system_time.json")
	fake.answerFixture(opnsense.SystemDisk, "system_disk.json")
	fake.answerFixture(opnsense.Activity, "activity.json")
	fake.answerFixture(opnsense.KeaStatus, "kea_status_disabled.json")
	fake.answerFixture(opnsense.DnsmasqStatus, "dnsmasq_status_running.json")
	fake.answerFixture(opnsense.DnsmasqSettings, "dnsmasq_settings.json")
	fake.answerFixture(opnsense.DnsmasqLeases, "dnsmasq_leases.json")
	fake.answerFixture(opnsense.ARPTable, "get_arp.json")
	fake.answerFixture(opnsense.NDPTable, "get_ndp.json")
	fake.answerFixture(opnsense.UnboundStatus, "unbound_status_running.json")
	fake.answerFixture(opnsense.UnboundSettings, "unbound_settings.json")
	fake.answerFixture(opnsense.UnboundIsEnabled, "unbound_is_enabled_on.json")
	fake.answerFixture(opnsense.SearchQueries, "search_queries_page1.json")

	if err := harness.collector.RefreshDiscovery(ctx); err != nil {
		t.Fatalf("runtime discovery: %v", err)
	}
	if err := harness.collector.ProbeAll(ctx); err != nil {
		t.Fatalf("the probe round: %v", err)
	}

	for name, collect := range map[string]func(context.Context) error{
		"the filter log":          harness.collector.CollectFirewallLog,
		"the security events":     harness.collector.CollectSecurityEvent,
		"the sampled measurement": harness.collector.CollectMeasurement,
		"the DHCP leases":         harness.collector.CollectDHCPLease,
		"the resolver lookups":    harness.collector.CollectDNSLookup,
	} {
		if err := collect(ctx); err != nil {
			t.Errorf("collecting %s: %v", name, err)
		}
	}

	// The transport has already failed the test if anything left for another host. This is
	// the other half: nothing asked for a path outside the registry.
	fake.assertEveryPathIsRegistered()

	// And the whole of it wrote something, so the run above was not a sequence of early
	// returns that happen to make no request at all.
	for _, table := range []string{"flow", "security_event", "dns_resolution", "dhcp_lease",
		"measurement_sample", "interface", "interface_map", "rule", "client"} {
		if stored := countRows(t, harness.store, table); stored == 0 {
			t.Errorf("%s is empty, so this run proved nothing about that collector", table)
		}
	}
}

// TestNoCollectorWritesToTheFirewall is the read-only rule over the whole collection path.
//
// The client refuses a mutating command before a request exists, so this is the recording
// half: every method the fake saw is a read, and no path names a command that could write.
func TestNoCollectorWritesToTheFirewall(t *testing.T) {
	harness := newProbeHarness(t)
	ctx := context.Background()
	arrangeDiscoverableFirewall(t, harness.fake, harness.collector)
	harness.fake.answerFixture(opnsense.FirewallLog, "firewall_log.json")
	harness.fake.answerFixture(opnsense.DnsmasqStatus, "dnsmasq_status_running.json")
	harness.fake.answerFixture(opnsense.DnsmasqSettings, "dnsmasq_settings.json")
	harness.fake.answerFixture(opnsense.ARPTable, "get_arp.json")
	harness.fake.answerFixture(opnsense.NDPTable, "get_ndp.json")

	if err := harness.collector.ProbeAll(ctx); err != nil {
		t.Logf("the probe round reported: %v", err)
	}
	if err := harness.collector.CollectFirewallLog(ctx); err != nil {
		t.Fatalf("collecting the filter log: %v", err)
	}
	if err := harness.collector.CollectDHCPLease(ctx); err != nil {
		t.Logf("collecting the leases reported: %v", err)
	}

	if harness.fake.requestCount() == 0 {
		t.Fatal("no request was made, so this assertion is vacuous")
	}
	harness.fake.mutex.Lock()
	defer harness.fake.mutex.Unlock()
	for _, request := range harness.fake.requests {
		switch request.method {
		case "GET", "POST":
			// Both are read-only here: OPNsense has no framework-level method routing and
			// its search and get helpers contain no write path at all, which is why the
			// path rather than the verb is what has to be checked.
		default:
			t.Errorf("a request used the method %s", request.method)
		}
		if command := opnsense.MutatingCommand(request.path); command != "" {
			t.Errorf("a request named the mutating command %q in %s", command, request.path)
		}
	}
}
