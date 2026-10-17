package widgets

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/widget"
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/request"

	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/geo"
	"github.com/NaejEL/opnview/internal/store/storetest"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/suffixlist"
)

// The widget tests' installation.
//
// IT TAKES ITS COUNTS AS PARAMETERS AND CARRIES BOTH ADDRESS FAMILIES, as the spec asks of
// every fixture, and every address comes from the documentation ranges (RFC 5737, RFC
// 3849) and every name from the reserved ones (RFC 2606). Nothing the code under test does
// presupposes any of them: it reads them back out of the rows.
//
// Its shape: `interfaces` inside interfaces, each holding an IPv4 /27 and an IPv6 /64, and
// one upstream interface carrying a gateway; `clients` clients spread over them,
// alternating families, some leased with a MAC (one randomised), two owners; flows of every
// direction over `span` seconds ending at now, a share of the outbound ones logged a second
// time on the upstream interface and paired; outside peers geolocated, missed, pending or
// never looked up; resolver lookups naming some flows by timing and one by the cache's
// answer; refusals by a rule, by two lists and by the security engine; telemetry and
// interface counters.

// fixtureNow is a fixed instant in the middle of a UTC hour.
const fixtureNow int64 = 1790001800

// fixture is a populated database and what built it.
type fixture struct {
	db         *store.Store
	service    *widget.Service
	now        int64
	interfaces []fixtureInterface
	upstream   fixtureInterface
	clients    []fixtureClient
	owners     []int64
	ruleID     int64
	blockRule  int64
	peers      []string
	sites      []string
	digest     int
}

type fixtureInterface struct {
	id     int64
	device string
	v4, v6 string
}

type fixtureClient struct {
	address string
	iface   fixtureInterface
	v6      bool
}

// fixtureSpec are a fixture's counts. peers is the number of distinct outside peers, twelve when
// it is zero.
type fixtureSpec struct {
	interfaces, clients, flows int
	span                       int64
	peers                      int
}

// defaultSpec is the fixture most tests share.
var defaultSpec = fixtureSpec{interfaces: 3, clients: 8, flows: 600, span: 3 * 86400}

// newFixture gives the test a populated database of its own: a clone of the fixture this spec
// builds, built once per process (storetest.Fixture), with what the build learned of it.
func newFixture(t *testing.T, spec fixtureSpec) *fixture {
	t.Helper()
	built, dsn := storetest.Fixture(t, fmt.Sprintf("widget fixture %+v", spec),
		func(t testing.TB, dsn string) fixture { return buildFixture(t.(*testing.T), spec, dsn) })
	database, err := store.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("opening the store: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	f := built
	f.interfaces = append([]fixtureInterface(nil), built.interfaces...)
	f.clients = append([]fixtureClient(nil), built.clients...)
	f.owners = append([]int64(nil), built.owners...)
	f.peers = append([]string(nil), built.peers...)
	f.sites = append([]string(nil), built.sites...)
	f.db = database
	f.service, err = widget.New(database, widget.Options{Now: func() time.Time { return time.Unix(f.now, 0) },
		SuffixLists: exampleSuffixList(t)})
	check(t, err)
	return &f
}

// buildFixture populates a database, closes it, and returns what it put there.
func buildFixture(t *testing.T, spec fixtureSpec, dsn string) fixture {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("opening the store: %v", err)
	}
	f := &fixture{db: database, now: fixtureNow}
	before := f.now - spec.span - 86400

	for index := 0; index < spec.interfaces; index++ {
		vlan := int64(100 + index)
		id, err := database.UpsertInterface(ctx, vocab.Interface{
			Identifier: fmt.Sprintf("example_if_%d", index), Device: fmt.Sprintf("exdev%d", index),
			Description: fmt.Sprintf("example description %d", index), LinkType: "example-link",
			LinkKind: "vlan", VLANTag: &vlan,
		}, before)
		check(t, err)
		iface := fixtureInterface{id: id, device: fmt.Sprintf("exdev%d", index),
			v4: fmt.Sprintf("192.0.2.%d", 32*index+1), v6: fmt.Sprintf("2001:db8:%x::1", index+1)}
		bits4, bits6 := int64(27), int64(64)
		check(t, database.UpsertInterfaceAddress(ctx, vocab.InterfaceAddress{InterfaceID: id,
			SourceField: vocab.SourceFieldAddr4, Address: iface.v4, PrefixLength: &bits4, AddressFamily: 4}, nil, before))
		check(t, database.UpsertInterfaceAddress(ctx, vocab.InterfaceAddress{InterfaceID: id,
			SourceField: vocab.SourceFieldAddr6, Address: iface.v6, PrefixLength: &bits6, AddressFamily: 6}, nil, before))
		check(t, database.DetectNetworks(ctx, id, prefixes(t, fmt.Sprintf("192.0.2.%d/27", 32*index),
			fmt.Sprintf("2001:db8:%x::/64", index+1)), before))
		check(t, database.UpsertInterfaceMapEntry(ctx, iface.device, fmt.Sprintf("example map %d", index), &id, before))
		f.interfaces = append(f.interfaces, iface)
	}
	f.exec(t, "UPDATE interface SET user_label = 'example label' WHERE id = $1", f.interfaces[0].id)

	upstreamID, err := database.UpsertInterface(ctx, vocab.Interface{Identifier: "example_if_upstream",
		Device: "exdevup", Description: "example description upstream", LinkType: "example-link",
		LinkKind: "physical", IsUpstream: true}, before)
	check(t, err)
	f.upstream = fixtureInterface{id: upstreamID, device: "exdevup", v4: "203.0.113.2", v6: "2001:db8:ff::2"}
	bits := int64(24)
	check(t, database.UpsertInterfaceAddress(ctx, vocab.InterfaceAddress{InterfaceID: upstreamID,
		SourceField: vocab.SourceFieldAddr4, Address: f.upstream.v4, PrefixLength: &bits, AddressFamily: 4}, nil, before))
	check(t, database.UpsertInterfaceAddress(ctx, vocab.InterfaceAddress{InterfaceID: upstreamID,
		SourceField: vocab.SourceFieldGateways, Address: "203.0.113.1", AddressFamily: 4}, nil, before))
	check(t, database.DetectNetworks(ctx, upstreamID, prefixes(t, "203.0.113.0/24"), before))
	check(t, database.UpsertInterfaceMapEntry(ctx, "exdevup", "example map upstream", &upstreamID, before))

	f.ruleID, err = database.UpsertRule(ctx, vocab.Rule{PfLabel: "example-pass-label",
		Description: "example pass rule", Action: "pass"}, before)
	check(t, err)
	f.blockRule, err = database.UpsertRule(ctx, vocab.Rule{PfLabel: "example-block-label",
		Description: "example block rule", Action: "block", IsAutomatic: true}, before)
	check(t, err)

	leaseProvider, err := database.ProviderID(ctx, "dhcp_lease", "dnsmasq")
	check(t, err)
	for index := 0; index < spec.clients; index++ {
		iface := f.interfaces[index%len(f.interfaces)]
		host := 2 + index/len(f.interfaces)
		client := fixtureClient{iface: iface, v6: index%2 == 1}
		if client.v6 {
			client.address = fmt.Sprintf("2001:db8:%x::%x", index%len(f.interfaces)+1, host+10)
		} else {
			client.address = fmt.Sprintf("192.0.2.%d", 32*(index%len(f.interfaces))+host+1)
		}
		if index%2 == 0 {
			mac := fmt.Sprintf("00:00:5e:00:53:%02x", index)
			if index == 2 {
				// A locally administered MAC: an unstable identity.
				mac = "02:00:5e:00:53:02"
			}
			hostname := fmt.Sprintf("host%d.example", index)
			id, err := database.UpsertClient(ctx, vocab.Client{Identity: vocab.ClientIdentity{Kind: vocab.IdentityMAC,
				Key: mac}, InterfaceID: &iface.id, MAC: &mac, Hostname: &hostname, LastAddress: &client.address},
				f.now-3600)
			check(t, err)
			expiry := f.now + 3600
			check(t, database.InsertDHCPLease(ctx, leaseProvider, vocab.DHCPLease{ClientID: &id, Backend: "dnsmasq",
				Address: client.address, MAC: &mac, LeaseState: "active", InterfaceID: &iface.id,
				GenerationKey: vocab.GenerationKeyOf(nil, &expiry, 0), ExpiresAt: &expiry, ObservedAt: f.now - 3600}))
		}
		f.clients = append(f.clients, client)
	}

	// Peers: v4 and v6, a few per family.
	peerCount := spec.peers
	if peerCount == 0 {
		peerCount = 12
	}
	for k := 0; k < peerCount; k++ {
		if k%2 == 0 {
			f.peers = append(f.peers, fmt.Sprintf("198.51.100.%d", k+10))
		} else {
			f.peers = append(f.peers, fmt.Sprintf("2001:db8:1000::%x", k+10))
		}
	}
	var addresses []string
	for index := 0; index < spec.flows; index++ {
		client := f.clients[index%len(f.clients)]
		other := f.clients[(index*5+3)%len(f.clients)]
		peer := f.peers[index%len(f.peers)]
		if client.v6 != (index%len(f.peers)%2 == 1) {
			peer = f.peers[(index+1)%len(f.peers)]
		}
		observed := f.now - int64(index)*spec.span/int64(spec.flows+1) - 1
		port := int64(443 + index%3)
		ipID, seq := int64(index%65535), int64(1000+index)
		record := vocab.Flow{ObservedAt: observed, Protocol: "tcp", Action: "pass", PacketBytes: int64(60 + index*37%1400),
			DstPort: &port, Direction: "in", InterfaceDevice: client.iface.device, SrcAddress: client.address,
			DstAddress: peer, IPID: &ipID, TCPSeq: &seq}
		if index%5 == 0 {
			record.Action = "block"
			reason := "match"
			record.LogReason = &reason
		}
		if index%4 == 0 {
			if record.Action == "block" {
				record.RuleID = &f.blockRule
			} else {
				record.RuleID = &f.ruleID
			}
		}
		switch {
		case index%7 == 0:
			record.Protocol = "udp"
		case index%11 == 0:
			record.Protocol, record.DstPort = "icmp", nil
		}
		switch index % 6 {
		case 2:
			record.InterfaceDevice = f.upstream.device
			record.SrcAddress, record.DstAddress = peer, client.address
		case 3:
			if other.v6 == client.v6 && other.address != client.address {
				record.DstAddress = other.address
			}
		case 4:
			if !client.v6 {
				record.DstAddress = f.upstream.v4
			}
		}
		addresses = append(addresses, f.insertFlow(t, record)...)
		// A share of the outbound connections is logged again on the upstream interface,
		// after NAT: the second leg of the same connection.
		if index%6 == 0 && record.Action == "pass" && !client.v6 {
			second := record
			second.InterfaceDevice, second.Direction, second.SrcAddress = f.upstream.device, "out", f.upstream.v4
			addresses = append(addresses, f.insertFlow(t, second)...)
		}
	}
	_, err = database.Reclassify(ctx, addresses, f.now)
	check(t, err)
	_, err = database.Pair(ctx, f.now-spec.span-10, f.now+1, 1)
	check(t, err)

	geoProvider, err := database.ProviderID(ctx, "ip_geolocation", "maxmind_geolite2")
	check(t, err)
	for k, peer := range f.peers {
		switch k % 4 {
		case 3:
			continue // never looked up
		case 2:
			check(t, database.UpsertGeoASN(ctx, geo.GeoASN{Address: peer, ProviderID: geoProvider,
				LookupState: geo.GeoMiss, DatasetBuildAt: f.now - 86400, LookedUpAt: f.now - 60}))
			continue
		}
		country := []string{"AA", "BB", "CC"}[k%3]
		latitude, longitude := float64(k), float64(-k)
		asn := int64(64496 + k%3)
		operator := fmt.Sprintf("example operator %d", k%3)
		check(t, database.UpsertGeoASN(ctx, geo.GeoASN{Address: peer, ProviderID: geoProvider,
			LookupState: geo.GeoResolved, CountryCode: &country, CountryName: &country, Latitude: &latitude,
			Longitude: &longitude, ASN: &asn, Operator: &operator, DatasetBuildAt: f.now - 86400, LookedUpAt: f.now - 60}))
	}

	// Resolver lookups just before a share of the outbound flows, one name each.
	f.sites = []string{"www.example.com", "cdn.example.net", "api.example.org"}
	rows, err := database.DB().QueryContext(ctx, `SELECT f.observed_at, f.src_address, f.id FROM flow AS f
		WHERE f.dst_interface_id IS NULL AND f.src_client_id IS NOT NULL AND f.pair_outcome IS DISTINCT FROM 'second_leg'
		ORDER BY f.id`)
	check(t, err)
	type lookup struct {
		at      int64
		address string
	}
	var lookups []lookup
	for rows.Next() {
		var item lookup
		var id int64
		check(t, rows.Scan(&item.at, &item.address, &id))
		if id%3 != 0 {
			lookups = append(lookups, item)
		}
	}
	check(t, rows.Close())
	recursion := "Recursion"
	for index, item := range lookups {
		check(t, database.InsertDNSResolution(ctx, vocab.DNSResolution{LookupKey: fmt.Sprintf("example-lookup-%d", index),
			ClientAddress: item.address, ClientResolution: vocab.ClientResolutionLoggedAddress,
			Domain: f.sites[index%len(f.sites)], Resolver: "unbound", Action: "pass", AnswerSource: &recursion,
			LookedUpAt: item.at - 1,
			IngestedAt: f.now - 1}))
	}
	// Refused lookups: two lists, one with an assigned purpose, and one refusal attributed to no list.
	for index, list := range []string{"example-list-a", "example-list-b", ""} {
		action := "block"
		check(t, database.InsertDNSResolution(ctx, vocab.DNSResolution{LookupKey: fmt.Sprintf("example-refused-%d", index),
			ClientAddress: f.clients[index%len(f.clients)].address, ClientResolution: vocab.ClientResolutionLoggedAddress,
			Domain: fmt.Sprintf("blocked%d.example", index), Resolver: "unbound", Action: action, BlocklistName: list,
			LookedUpAt: f.now - 600 - int64(index)*3600, IngestedAt: f.now - 1}))
	}
	f.exec(t, "UPDATE blocklist SET purpose = 'advertising', purpose_assigned_at = $1 WHERE name = 'example-list-a'", f.now)
	_, err = database.Attribute(ctx, f.now-spec.span-10, f.now+1, 5, f.now)
	check(t, err)

	// Two owners; three clients assigned to them, by hand.
	for index, name := range []string{"Example Person A", "Example Person B"} {
		f.exec(t, "INSERT INTO owner (display_name, created_at, updated_at) VALUES ($1, $2, $3)", name, f.now, f.now)
		var id int64
		check(t, database.DB().QueryRowContext(ctx, "SELECT id FROM owner WHERE display_name = $1", name).Scan(&id))
		f.owners = append(f.owners, id)
		f.exec(t, `UPDATE client SET owner_id = $1, owner_assigned_at = $2 WHERE id IN
			(SELECT id FROM client WHERE identity_kind <> 'address_in_interface' ORDER BY id LIMIT 1 OFFSET $3)`,
			id, f.now, index)
	}

	// The security engine refused two connections.
	eventProvider, err := database.ProviderID(ctx, "security_event", "suricata")
	check(t, err)
	for index := 0; index < 2; index++ {
		check(t, database.InsertSecurityEvent(ctx, eventProvider, vocab.SecurityEvent{
			ProviderEventKey: fmt.Sprintf("example-event-%d", index), OccurredAt: f.now - 900, IngestedAt: f.now - 1,
			RuleIdentity: "1:2000:1", Signature: "example signature", EventAction: "blocked",
			SrcAddress: f.clients[0].address, DstAddress: f.peers[0]}))
	}

	// Telemetry and interface counters, every five minutes over the last day.
	for at := f.now - 86400 + 300; at <= f.now; at += 300 {
		f.sampleAt(t, at, vocab.SubjectFirewall, "cpu", vocab.MeasureCPUUseRatio, vocab.UnitRatio,
			float64(at%7)/10)
		for _, iface := range f.interfaces {
			f.sampleAt(t, at, vocab.SubjectInterface, iface.device, vocab.MeasureBytesIn, vocab.UnitByte,
				float64((at-f.now+86400)*1000))
			f.sampleAt(t, at, vocab.SubjectInterface, iface.device, vocab.MeasureBytesOut, vocab.UnitByte,
				float64((at-f.now+86400)*500))
		}
	}

	_, err = database.RefreshAggregates(ctx, 0, f.now, nil)
	check(t, err)
	f.allReachable(t)
	check(t, database.Close())
	built := *f
	built.db = nil
	return built
}

// suffixList is a Public Suffix List held in memory, as the refresh holds the one it
// downloaded: a few rules, enough to group the fixture's names.
type suffixList struct{ list *suffixlist.List }

func (s suffixList) RegistrableDomains(_ context.Context, names []string) (map[string]string, bool) {
	domains := map[string]string{}
	for _, name := range names {
		if domain, found := s.list.RegistrableDomain(name); found {
			domains[name] = domain
		}
	}
	return domains, true
}

func exampleSuffixList(t *testing.T) request.SuffixLists {
	t.Helper()
	// Parse refuses a copy with fewer than ten rules, as a truncated download.
	list, err := suffixlist.Parse([]byte("// ===BEGIN ICANN DOMAINS===\ncom\nnet\norg\nexample\ntest\n" +
		"invalid\nlocalhost\nedu\ngov\nint\n// ===END ICANN DOMAINS===\n// ===BEGIN PRIVATE DOMAINS===\n" +
		"// ===END PRIVATE DOMAINS===\n"))
	check(t, err)
	return suffixList{list: list}
}

// insertFlow stores one record unplaced, as the collector does, and returns its addresses.
func (f *fixture) insertFlow(t *testing.T, record vocab.Flow) []string {
	t.Helper()
	f.digest++
	record.LogDigest = fmt.Sprintf("example-digest-%d", f.digest)
	record.IngestedAt = f.now - 1
	record.InterfaceLookupState = vocab.LookupResolved
	record.IPVersion = 4
	for _, character := range record.SrcAddress {
		if character == ':' {
			record.IPVersion = 6
		}
	}
	record.RuleLookupState = vocab.LookupNotFound
	rid := "example-unknown-rid"
	if record.RuleID != nil {
		record.RuleLookupState = vocab.LookupResolved
		if *record.RuleID == f.ruleID {
			rid = "example-pass-label"
		} else {
			rid = "example-block-label"
		}
	}
	record.Rid = &rid
	check(t, f.db.InsertFlow(context.Background(), record))
	return []string{record.SrcAddress, record.DstAddress}
}

// prefixes parses network texts.
func prefixes(t *testing.T, texts ...string) []netip.Prefix {
	t.Helper()
	var result []netip.Prefix
	for _, text := range texts {
		prefix, err := netip.ParsePrefix(text)
		check(t, err)
		result = append(result, prefix)
	}
	return result
}

// sampleAt stores one reading.
func (f *fixture) sampleAt(t *testing.T, at int64, kind vocab.SubjectKind, key string, measure vocab.Measure,
	unit vocab.Unit, value float64) {
	t.Helper()
	err := f.db.InsertMeasurementSample(context.Background(), vocab.MeasurementSample{SubjectKind: kind,
		SubjectKey: key, Measure: measure, Unit: unit, Value: value, SampledAt: at})
	if err != nil {
		// A second reading at one instant of one subject is a collision the store refuses
		// by design; the fixture writes each once.
		t.Fatalf("writing a sample: %v", err)
	}
}

// allReachable marks every provider a served preset binds reachable and active.
func (f *fixture) allReachable(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	// A provider whose availability row a test removed gets it back, as the schema seeds it.
	f.exec(t, `INSERT INTO source_availability (provider_id, state, probe, detail, checked_at, outcome)
		SELECT id, 'unavailable', 'not_yet_probed', NULL, 0, 'not_yet_probed' FROM provider
		WHERE id NOT IN (SELECT provider_id FROM source_availability)`)
	for _, pair := range [][2]string{{"firewall_log", "pf"}, {"dns_lookup", "unbound"}, {"ip_geolocation", "maxmind_geolite2"},
		{"dhcp_lease", "dnsmasq"}, {"security_event", "suricata"}, {"measurement_sample", "insight"},
		{"resolver_cache", "unbound"}} {
		check(t, f.db.SetActiveProviders(ctx, pair[0], pair[1]))
		f.setAvailability(t, pair[0], pair[1], vocab.StateReachable, vocab.OutcomeHealthy, "example probe")
	}
}

// setAvailability writes one provider's availability.
func (f *fixture) setAvailability(t *testing.T, kind, key string, state vocab.AvailabilityState,
	outcome vocab.AvailabilityOutcome, probe string) {
	t.Helper()
	id, err := f.db.ProviderID(context.Background(), kind, key)
	check(t, err)
	check(t, f.db.SetAvailability(context.Background(), id, state, outcome, probe, nil, f.now-30))
}

// exec runs one statement on the fixture's database.
func (f *fixture) exec(t *testing.T, statement string, arguments ...any) {
	t.Helper()
	if _, err := f.db.DB().ExecContext(context.Background(), statement, arguments...); err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
	// A capture configuration written straight into the tables is recorded as the netflow measure's window
	// intervals, as a capture read records it (internal/store/netflow, capture.go).
	if strings.Contains(statement, "capture_") {
		check(t, f.db.RecordMeasureIntervals(context.Background()))
	}
}

// respond asks the service for one type with parameters given as a Go value.
func (f *fixture) respond(t *testing.T, widgetType string, parameters any) envelope.Response {
	t.Helper()
	raw := ""
	if parameters != nil {
		encoded, err := json.Marshal(parameters)
		check(t, err)
		if string(encoded) != "null" {
			raw = string(encoded)
		}
	}
	recordParameters(widgetType, raw)
	response, err := f.service.Respond(context.Background(), widgetType, raw)
	if err != nil {
		t.Fatalf("%s %s: %v", widgetType, raw, err)
	}
	return response
}

// data decodes a response's data into a map, through its JSON, as a client would read it.
func data(t *testing.T, response envelope.Response) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(response.Data)
	check(t, err)
	var decoded map[string]any
	check(t, json.Unmarshal(encoded, &decoded))
	return decoded
}

// query renders parameters as the query string of a request.
func query(t *testing.T, parameters any) string {
	t.Helper()
	encoded, err := json.Marshal(parameters)
	check(t, err)
	return widget.ParametersQueryKey + "=" + url.QueryEscape(string(encoded))
}

// scalar reads one integer from the database.
func (f *fixture) scalar(t *testing.T, statement string, arguments ...any) int64 {
	t.Helper()
	var value int64
	if err := f.db.DB().QueryRowContext(context.Background(), statement, arguments...).Scan(&value); err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
	return value
}

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
