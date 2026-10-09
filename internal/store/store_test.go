package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// openTestStore opens a database under t.TempDir, so no test ever writes a database,
// a -wal or a -shm companion into the working tree.
func openTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	directory := t.TempDir()
	database, err := Open(context.Background(), directory)
	if err != nil {
		t.Fatalf("opening the database: %v", err)
	}
	t.Cleanup(func() {
		// Close is idempotent, so a test that closed the database itself is not
		// double-closed into an error.
		_ = database.Close()
	})
	return database, directory
}

// TestApplyingTheSchemaTwiceChangesNothing is what makes a schema with no migration
// runner safe to apply on every start.
//
// There is no schema_version table and no runner, by decision: nothing is deployed and
// nobody has data. What replaces them is idempotence, and this is that property under
// test — the object count, the registry, the availability rows and the settings are all
// unchanged by a second apply, so a restart cannot reset a probed state or undo a
// setting the user changed.
func TestApplyingTheSchemaTwiceChangesNothing(t *testing.T) {
	t.Parallel()
	database, directory := openTestStore(t)
	ctx := context.Background()

	before := schemaFingerprint(t, database)
	if err := database.Close(); err != nil {
		t.Fatalf("closing before the second apply: %v", err)
	}

	reopened, err := Open(ctx, directory)
	if err != nil {
		t.Fatalf("reopening the database, which applies the schema again: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	after := schemaFingerprint(t, reopened)
	if before != after {
		t.Fatalf("a second apply changed the database:\nbefore %s\nafter  %s", before, after)
	}
}

// TestASettingSurvivesTheSchemaBeingAppliedAgain is the half of idempotence that
// matters most in practice: the defaults are inserted with ON CONFLICT DO NOTHING, so
// a value somebody changed is not overwritten on the next start.
func TestASettingSurvivesTheSchemaBeingAppliedAgain(t *testing.T) {
	t.Parallel()
	database, directory := openTestStore(t)
	ctx := context.Background()

	if err := database.SetSetting(ctx, "retention_seconds", "3600", 1750000000); err != nil {
		t.Fatalf("changing the horizon: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}

	reopened, err := Open(ctx, directory)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	value, present, err := reopened.Setting(ctx, "retention_seconds")
	if err != nil || !present {
		t.Fatalf("reading the horizon back: value %q present %v err %v", value, present, err)
	}
	if value != "3600" {
		t.Fatalf("the horizon is %q after a restart, want 3600: the schema overwrote a user's setting",
			value)
	}
}

// TestAProbedAvailabilityRowSurvivesTheSchemaBeingAppliedAgain is the other half: the
// availability rows are inserted only for providers that have none, so a restart does
// not reset every source to "not yet probed" and make a working firewall look broken.
func TestAProbedAvailabilityRowSurvivesTheSchemaBeingAppliedAgain(t *testing.T) {
	t.Parallel()
	database, directory := openTestStore(t)
	ctx := context.Background()

	providerID, err := database.ProviderID(ctx, "firewall_log", "pf")
	if err != nil {
		t.Fatalf("looking up the filter-log provider: %v", err)
	}
	detail := "probed by this test"
	if err := database.SetAvailability(ctx, providerID, StateReachable,
		"GET /api/diagnostics/firewall/log", &detail, 1750000000); err != nil {
		t.Fatalf("recording availability: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}

	reopened, err := Open(ctx, directory)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	state, probe, checkedAt, err := reopened.Availability(ctx, providerID)
	if err != nil {
		t.Fatalf("reading availability back: %v", err)
	}
	if state != StateReachable || checkedAt != 1750000000 ||
		!strings.Contains(probe, "/api/diagnostics/firewall/log") {
		t.Fatalf("after a restart the row reads state %q probe %q at %d; the schema reset it",
			state, probe, checkedAt)
	}
}

// TestClosingLeavesNoHotJournalAndAConsistentDatabase is the shutdown guarantee the
// restart story rests on.
//
// The write-ahead log is checkpointed and the last connection is closed, so SQLite
// removes the -wal and -shm companions; a hot journal left behind is exactly what a
// restart must not meet. The integrity check is run on a freshly reopened database
// rather than on the one that wrote it, because that is the state the next start sees.
func TestClosingLeavesNoHotJournalAndAConsistentDatabase(t *testing.T) {
	t.Parallel()
	database, directory := openTestStore(t)
	ctx := context.Background()

	// Something to write, so the log is not empty when the checkpoint runs.
	if err := database.SetSetting(ctx, "example_setting", "example value", 1750000000); err != nil {
		t.Fatalf("writing a setting: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}

	for _, companion := range []string{DatabaseFilename + "-wal", DatabaseFilename + "-shm"} {
		if _, err := os.Stat(filepath.Join(directory, companion)); err == nil {
			t.Errorf("%s survived a clean close, which is a hot journal for the next start",
				companion)
		}
	}

	reopened, err := Open(ctx, directory)
	if err != nil {
		t.Fatalf("reopening after a clean close: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	result, err := reopened.IntegrityCheck(ctx)
	if err != nil {
		t.Fatalf("the integrity check did not run: %v", err)
	}
	if result != "ok" {
		t.Fatalf("the integrity check reported %q", result)
	}
}

// TestClosingTwiceIsNotAnError keeps a deferred close from turning a clean stop into a
// reported failure.
func TestClosingTwiceIsNotAnError(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	if err := database.Close(); err != nil {
		t.Fatalf("the first close failed: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("the second close failed: %v", err)
	}
}

// TestOpeningWithNoDataDirectoryIsRefused is decision 11 at the storage layer.
func TestOpeningWithNoDataDirectoryIsRefused(t *testing.T) {
	t.Parallel()
	if _, err := Open(context.Background(), ""); err == nil {
		t.Fatal("opening with no data directory succeeded")
	}
}

// TestAFreshDatabaseHasNoActiveProvider is the modelled-state design working.
//
// Activeness says which implementation opnview reads, and only a probe round against a
// live firewall can decide that. A schema that guessed would be a hardcoded assumption
// about the installation.
func TestAFreshDatabaseHasNoActiveProvider(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()

	// Every kind, including the three that admit several active providers — which are read
	// through the plural accessor, because asking those for THE active one is refused.
	for _, kind := range []string{"firewall_log", "security_event", "flow_volume",
		"dhcp_lease", "dns_lookup", "geo_asn", "measurement_sample", "reconciled_state"} {
		ids, err := database.ActiveProviderIDs(ctx, kind)
		if err != nil {
			t.Fatalf("reading the active %s providers: %v", kind, err)
		}
		if len(ids) != 0 {
			t.Errorf("the %s kind has %d active providers on a fresh database", kind, len(ids))
		}
	}
}

// TestEveryRegisteredProviderHasExactlyOneAvailabilityRow is the guarantee that an
// unavailable source is a state rather than an absent row a screen could not tell from
// a healthy one.
func TestEveryRegisteredProviderHasExactlyOneAvailabilityRow(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()

	providers, err := database.CountRows(ctx, "provider")
	if err != nil {
		t.Fatalf("counting the registry: %v", err)
	}
	availability, err := database.CountRows(ctx, "source_availability")
	if err != nil {
		t.Fatalf("counting the availability rows: %v", err)
	}
	if providers == 0 {
		t.Fatal("the registry is empty")
	}
	if providers != availability {
		t.Fatalf("%d registry rows and %d availability rows", providers, availability)
	}
}

// TestActivatingASecondProviderOfOneKindReplacesTheFirst exercises the partial unique
// index over kind, restated twice over against the rule that replaced universal exclusivity:
// at most one provider per EXCLUSIVE kind is active, and the switch is one transaction so it
// cannot leave two.
//
// The kind it exercises MOVED. It used to be dhcp_lease, and the maintainer put that kind in
// the concurrent set: one server issuing on one VLAN and another on a second is an ordinary
// deployment, and a machine leased by both is one client with two leases because the identity
// cascade keys on the client identifier and then the MAC, neither scoped to an interface. So
// the exclusive example here is now dns_lookup, where two active resolvers would be a genuine
// problem: a lookup that transits both would be counted twice, and dns_resolution.lookup_key
// carries no provider to tell the two records apart.
func TestActivatingASecondProviderOfOneKindReplacesTheFirst(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()

	if err := database.SetActiveProviders(ctx, "dns_lookup", "unbound"); err != nil {
		t.Fatalf("activating the first resolver: %v", err)
	}
	if err := database.SetActiveProviders(ctx, "dns_lookup", "dnsmasq"); err != nil {
		t.Fatalf("activating the second resolver: %v", err)
	}
	id, active, err := database.ActiveProviderID(ctx, "dns_lookup")
	if err != nil || !active {
		t.Fatalf("reading the active resolver: active %v err %v", active, err)
	}
	key, err := database.ProviderKey(ctx, id)
	if err != nil {
		t.Fatalf("reading the active resolver's key: %v", err)
	}
	if key != "dnsmasq" {
		t.Fatalf("the active resolver is %q, want dnsmasq", key)
	}

	// And none is a state of its own, which is what an ambiguous kind ends up in.
	if err := database.SetActiveProviders(ctx, "dns_lookup", ""); err != nil {
		t.Fatalf("leaving the kind with no active provider: %v", err)
	}
	if _, active, err := database.ActiveProviderID(ctx, "dns_lookup"); err != nil {
		t.Fatalf("reading back: %v", err)
	} else if active {
		t.Fatal("the kind still has an active provider after being cleared")
	}
}

// TestActivatingAnUnregisteredProviderIsRefused keeps a typo from silently leaving a
// kind with nothing active.
func TestActivatingAnUnregisteredProviderIsRefused(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	err := database.SetActiveProviders(context.Background(), "dhcp_lease", "not-a-registered-backend")
	if err == nil {
		t.Fatal("activating an unregistered provider succeeded")
	}
}

// TestSplitStatementsKeepsASemicolonInsideAString is a unit test of the splitter the
// schema and the purge both go through, because a splitter that cut a string would
// corrupt a statement rather than fail loudly.
func TestSplitStatementsKeepsASemicolonInsideAString(t *testing.T) {
	t.Parallel()
	statements := SplitStatements(
		"SELECT 'a;b';\n-- a comment\nSELECT 'c';\n\n")
	if len(statements) != 2 {
		t.Fatalf("got %d statements, want 2: %q", len(statements), statements)
	}
	if !strings.Contains(statements[0], "'a;b'") {
		t.Fatalf("the first statement lost its string: %q", statements[0])
	}
}

// schemaFingerprint renders the facts a second apply must not change.
func schemaFingerprint(t *testing.T, database *Store) string {
	t.Helper()
	ctx := context.Background()

	var objects int
	if err := database.DB().QueryRowContext(ctx,
		"SELECT count(*) FROM sqlite_master").Scan(&objects); err != nil {
		t.Fatalf("counting schema objects: %v", err)
	}
	var builder strings.Builder
	builder.WriteString("objects=")
	builder.WriteString(itoa(objects))

	rows, err := database.DB().QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'
		 ORDER BY name`)
	if err != nil {
		t.Fatalf("listing the tables: %v", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("listing the tables: %v", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("listing the tables: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("closing the table listing: %v", err)
	}

	for _, table := range tables {
		count, err := database.CountRows(ctx, table)
		if err != nil {
			t.Fatalf("counting %s: %v", table, err)
		}
		builder.WriteString(" ")
		builder.WriteString(table)
		builder.WriteString("=")
		builder.WriteString(itoa(count))
	}
	return builder.String()
}

// itoa renders a small non-negative integer without pulling in a formatter.
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 12)
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}
