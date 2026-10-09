package store

import (
	"context"
	"testing"
)

// The purge tests.
//
// THE PURGE RUNS FROM THE FIRST DAY, and that is a decision rather than an accident of
// scope: sql/purge.sql and setting.retention_seconds both already existed, and without a
// loop running them the database grows without bound from the first poll.
//
// The horizon is configuration and never a constant. Every statement reads it from the
// setting row through a scalar subquery that yields NULL when the horizon is unlimited,
// so every comparison is NULL and nothing is deleted — which is the second test below.

// purgeHorizon is the window these tests set, in seconds. It is a test parameter and not
// a figure the product carries: the product's horizon lives in the database.
const purgeHorizon = 3600

// TestThePurgeRemovesRowsOlderThanTheHorizonAndNothingNewer is the retention loop's whole
// contract, checked over every growing table the purge names.
func TestThePurgeRemovesRowsOlderThanTheHorizonAndNothingNewer(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()

	now := int64(1750000000)
	cutoff := now - purgeHorizon
	if err := database.SetSetting(ctx, "retention_seconds", "3600", now); err != nil {
		t.Fatalf("setting the horizon: %v", err)
	}

	old, fresh := seedForPurge(t, database, now, cutoff)

	if err := database.Purge(ctx, now); err != nil {
		t.Fatalf("purging: %v", err)
	}

	for table, column := range purgeableTables() {
		remainingOld := countOlderThan(t, database, table, column, cutoff)
		if remainingOld != 0 {
			t.Errorf("%d rows older than the horizon survive in %s", remainingOld, table)
		}
		if old[table] == 0 {
			t.Errorf("the seed put nothing older than the horizon in %s, so the purge proves nothing there",
				table)
		}
		remainingFresh := countAtLeast(t, database, table, column, cutoff)
		if remainingFresh != fresh[table] {
			t.Errorf("%s holds %d rows newer than the horizon, down from %d: the purge took too much",
				table, remainingFresh, fresh[table])
		}
	}

	// A set member has no instant of its own: it belongs to the snapshot that asserted the
	// set complete, and it goes with it through ON DELETE CASCADE. An orphaned member would
	// be a set member with no set and no instant, which is the one thing this kind exists to
	// prevent, so the purge is checked for it directly rather than through the loop above.
	var orphans int
	if err := database.DB().QueryRowContext(ctx,
		`SELECT count(*) FROM state_item AS i
		 WHERE NOT EXISTS (SELECT 1 FROM state_snapshot AS s WHERE s.id = i.snapshot_id)`).
		Scan(&orphans); err != nil {
		t.Fatalf("counting orphaned set members: %v", err)
	}
	if orphans != 0 {
		t.Errorf("%d set members survive their snapshot", orphans)
	}
	if remaining := count(t, database, "state_item"); remaining != 1 {
		t.Errorf("state_item holds %d rows after the purge, want the one member of the "+
			"surviving snapshot", remaining)
	}
}

// TestAnUnlimitedHorizonPurgesNothing is the documented meaning of zero, and it is the
// direction that would be silently destructive if it were wrong.
func TestAnUnlimitedHorizonPurgesNothing(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()

	now := int64(1750000000)
	if err := database.SetSetting(ctx, "retention_seconds", "0", now); err != nil {
		t.Fatalf("setting the horizon to unlimited: %v", err)
	}
	seedForPurge(t, database, now, now-purgeHorizon)

	before := map[string]int{}
	for table := range purgeableTables() {
		before[table] = count(t, database, table)
	}
	if err := database.Purge(ctx, now); err != nil {
		t.Fatalf("purging with an unlimited horizon: %v", err)
	}
	for table, wanted := range before {
		if got := count(t, database, table); got != wanted {
			t.Errorf("%s went from %d rows to %d under an unlimited horizon", table, wanted, got)
		}
	}
}

// TestThePurgeLeavesTheBoundedTablesAndTheUserInputAlone is the rule that a purge removes
// observations and never the work somebody did.
//
// An owner and a blocklist purpose are user input: purging a client removes the machine,
// never the person it was attributed to, and purging a lookup removes the lookup, never
// the purpose somebody assigned to the list that refused it.
func TestThePurgeLeavesTheBoundedTablesAndTheUserInputAlone(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	now := int64(1750000000)

	if err := database.SetSetting(ctx, "retention_seconds", "3600", now); err != nil {
		t.Fatalf("setting the horizon: %v", err)
	}
	seedForPurge(t, database, now, now-purgeHorizon)

	// An owner and a classified blocklist, both created long before the horizon.
	if _, err := database.DB().ExecContext(ctx,
		`INSERT INTO owner (display_name, created_at, updated_at) VALUES (?, ?, ?)`,
		"an owner the maintainer created", now-1000000, now-1000000); err != nil {
		t.Fatalf("creating an owner: %v", err)
	}
	blocklistID, err := database.UpsertBlocklist(ctx, "an observed list name", now-1000000)
	if err != nil {
		t.Fatalf("recording a blocklist: %v", err)
	}
	if _, err := database.DB().ExecContext(ctx,
		"UPDATE blocklist SET purpose = 'threat', purpose_assigned_at = ? WHERE id = ?",
		now-900000, blocklistID); err != nil {
		t.Fatalf("assigning a purpose: %v", err)
	}

	bounded := []string{"interface", "interface_map", "rule", "owner", "blocklist",
		"provider", "provider_rule_info", "source_availability", "eve_ingest_cursor", "setting"}
	before := map[string]int{}
	for _, table := range bounded {
		before[table] = count(t, database, table)
	}

	if err := database.Purge(ctx, now); err != nil {
		t.Fatalf("purging: %v", err)
	}

	for _, table := range bounded {
		if got := count(t, database, table); got != before[table] {
			t.Errorf("%s went from %d rows to %d; it is bounded and is never purged",
				table, before[table], got)
		}
	}

	var purpose *string
	if err := database.DB().QueryRowContext(ctx,
		"SELECT purpose FROM blocklist WHERE id = ?", blocklistID).Scan(&purpose); err != nil {
		t.Fatalf("reading the purpose back: %v", err)
	}
	if purpose == nil || *purpose != "threat" {
		t.Fatal("the purge discarded the classification somebody assigned to a list")
	}
}

// TestThePurgeLeavesTheForeignKeysConsistent is the guarantee that nothing survives
// pointing at a row that has gone.
func TestThePurgeLeavesTheForeignKeysConsistent(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	now := int64(1750000000)

	if err := database.SetSetting(ctx, "retention_seconds", "3600", now); err != nil {
		t.Fatalf("setting the horizon: %v", err)
	}
	seedForPurge(t, database, now, now-purgeHorizon)
	if err := database.Purge(ctx, now); err != nil {
		t.Fatalf("purging: %v", err)
	}

	rows, err := database.DB().QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatalf("running the foreign-key check: %v", err)
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		t.Fatal("the foreign-key check returned a row after the purge")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("running the foreign-key check: %v", err)
	}
}

// purgeableTables maps each growing table the purge names to the column it purges by. The
// two this cycle added are in the list, because a measurement sample and a gap row both
// accumulate one row per pass and would otherwise grow without bound.
func purgeableTables() map[string]string {
	return map[string]string{
		"flow":               "observed_at",
		"dns_resolution":     "looked_up_at",
		"security_event":     "occurred_at",
		"dhcp_lease":         "observed_at",
		"client":             "last_seen_at",
		"geo_asn":            "looked_up_at",
		"collection_gap":     "detected_at",
		"measurement_sample": "sampled_at",
		"state_snapshot":     "captured_at",
	}
}

// seedForPurge writes, into every purgeable table, one row older than the horizon and one
// newer, and returns the two counts per table so the assertions can be exact.
func seedForPurge(t *testing.T, database *Store, now, cutoff int64) (map[string]int, map[string]int) {
	t.Helper()
	ctx := context.Background()

	old := cutoff - 60
	fresh := now - 60

	providerID, err := database.ProviderID(ctx, "firewall_log", "pf")
	if err != nil {
		t.Fatalf("looking up the filter-log provider: %v", err)
	}
	eventProviderID, err := database.ProviderID(ctx, "security_event", "suricata")
	if err != nil {
		t.Fatalf("looking up the security-event provider: %v", err)
	}
	geoProviderID, err := database.ProviderID(ctx, "geo_asn", "maxmind_geolite2")
	if err != nil {
		t.Fatalf("looking up the geo provider: %v", err)
	}
	leaseProviderID, err := database.ProviderID(ctx, "dhcp_lease", "dnsmasq")
	if err != nil {
		t.Fatalf("looking up the DHCP server: %v", err)
	}

	for index, instant := range []int64{old, fresh} {
		suffix := []byte{byte('0' + index)}

		// A flow. It carries no client, so the client rows below are purged on their own
		// guard rather than being held by a surviving flow.
		flow := exampleFlow(index)
		flow.ObservedAt = instant
		flow.LogDigest = "example-purge-digest-" + string(suffix)
		if err := database.InsertFlow(ctx, flow); err != nil {
			t.Fatalf("seeding a flow: %v", err)
		}

		if err := database.InsertDNSResolution(ctx, DNSResolution{
			LookupKey:     "example-purge-lookup-" + string(suffix),
			ClientAddress: "example-address-" + string(suffix),
			Domain:        "example-domain", Resolver: "unbound", Action: "pass",
			LookedUpAt: instant, IngestedAt: instant,
		}); err != nil {
			t.Fatalf("seeding a lookup: %v", err)
		}

		if err := database.InsertSecurityEvent(ctx, eventProviderID, SecurityEvent{
			ProviderEventKey: "example-purge-event-" + string(suffix),
			OccurredAt:       instant, IngestedAt: instant,
			RuleIdentity: "example-rule-identity", Signature: "example signature",
			EventAction: "allowed",
			SrcAddress:  "example-source", DstAddress: "example-destination",
		}); err != nil {
			t.Fatalf("seeding a security event: %v", err)
		}

		if err := database.InsertDHCPLease(ctx, leaseProviderID, DHCPLease{
			Backend: "dnsmasq", Address: "example-address-" + string(suffix),
			LeaseState: "active", StartsAt: nil,
			GenerationKey: GenerationKeyOf(nil, &instant, instant),
			ExpiresAt:     &instant, ObservedAt: instant,
		}); err != nil {
			t.Fatalf("seeding a lease: %v", err)
		}

		if _, err := database.UpsertClient(ctx, Client{
			Identity: ClientIdentity{
				Kind: IdentityMAC, Key: "0a:00:00:00:00:0" + string(suffix),
			},
		}, instant); err != nil {
			t.Fatalf("seeding a client: %v", err)
		}

		buildAt := instant
		if _, err := database.DB().ExecContext(ctx,
			`INSERT INTO geo_asn (address, provider_id, lookup_state, country_code,
			                      dataset_build_at, looked_up_at)
			 VALUES (?, ?, 'resolved', 'ZZ', ?, ?)`,
			"example-address-"+string(suffix), geoProviderID, buildAt, instant); err != nil {
			t.Fatalf("seeding a geo row: %v", err)
		}

		if err := database.RecordCollectionGap(ctx, CollectionGap{
			ProviderID: providerID, IntervalStartAt: instant - 30, IntervalEndAt: instant,
			Reason: GapDigestOutsideWindow, DetectedAt: instant,
		}); err != nil {
			t.Fatalf("seeding a gap: %v", err)
		}

		if err := database.InsertMeasurementSample(ctx, MeasurementSample{
			SubjectKind: SubjectFirewall, SubjectKey: "", Measure: MeasureUptimeSeconds,
			Unit: UnitSecond, Value: float64(instant), SampledAt: instant,
		}); err != nil {
			t.Fatalf("seeding a measurement: %v", err)
		}

		// A reconciled set, with a member, so the purge has both the snapshot and the
		// cascade to remove.
		if err := database.InsertStateSnapshot(ctx, StateSnapshot{
			ProviderID: providerID, SetKey: "example-set", CapturedAt: instant,
			Items: []StateItem{{ItemKey: "example-item-" + string(suffix)}},
		}); err != nil {
			t.Fatalf("seeding a reconciled set: %v", err)
		}
	}

	oldCounts := map[string]int{}
	freshCounts := map[string]int{}
	for table, column := range purgeableTables() {
		oldCounts[table] = countOlderThan(t, database, table, column, cutoff)
		freshCounts[table] = countAtLeast(t, database, table, column, cutoff)
	}
	return oldCounts, freshCounts
}

// countOlderThan counts the rows of one table before an instant.
func countOlderThan(t *testing.T, database *Store, table, column string, cutoff int64) int {
	t.Helper()
	return scalar(t, database,
		"SELECT count(*) FROM \""+table+"\" WHERE \""+column+"\" < ?", cutoff)
}

// countAtLeast counts the rows of one table at or after an instant.
func countAtLeast(t *testing.T, database *Store, table, column string, cutoff int64) int {
	t.Helper()
	return scalar(t, database,
		"SELECT count(*) FROM \""+table+"\" WHERE \""+column+"\" >= ?", cutoff)
}

// scalar runs a counting query. The table and column names come from the map above, which
// holds constants; no user input reaches this.
func scalar(t *testing.T, database *Store, query string, argument int64) int {
	t.Helper()
	var value int
	if err := database.DB().QueryRowContext(context.Background(), query, argument).Scan(&value); err != nil {
		t.Fatalf("running %s: %v", query, err)
	}
	return value
}
