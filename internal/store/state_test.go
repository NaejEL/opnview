package store

import (
	"context"
	"testing"
)

// The reconciled-state tests.
//
// The kind exists for one reason and it is tested for that reason: ten of the eleven surveyed
// sources that fit nothing else report a COMPLETE SET, replaced wholesale on each poll, and the
// question such a set answers is what has LEFT it. Everything below is about that.

// exampleStateProvider registers a provider of the reconciled_state kind and returns its id.
//
// The schema ships no such row, deliberately — a registry row is a claim that something
// implements the kind, and no connector for a state-shaped source is written yet — so a test
// that needs one registers it, which is the INSERT a real connector's cycle would add.
func exampleStateProvider(t *testing.T, database *Store) int64 {
	t.Helper()
	ctx := context.Background()
	if _, err := database.DB().ExecContext(ctx,
		`INSERT INTO provider (kind, provider_key, display_name, is_active, registered_at)
		 VALUES ('reconciled_state', 'example-state-source', 'Example state source', 1, 1750000000)`,
	); err != nil {
		t.Fatalf("registering a reconciled-state provider: %v", err)
	}
	if _, err := database.DB().ExecContext(ctx,
		`INSERT INTO source_availability (provider_id, state, probe, detail, checked_at)
		 SELECT id, 'reachable', 'example-probe', NULL, 1750000000
		 FROM provider WHERE kind = 'reconciled_state' AND provider_key = 'example-state-source'`,
	); err != nil {
		t.Fatalf("recording the provider's availability: %v", err)
	}
	id, err := database.ProviderID(ctx, "reconciled_state", "example-state-source")
	if err != nil {
		t.Fatalf("reading the provider back: %v", err)
	}
	return id
}

// TestADepartureFromAReconciledSetIsDetectable is the criterion the whole kind exists for.
//
// A thing in yesterday's set and not in today's has LEFT. The model must be able to say so
// rather than silently keeping it — which is what storing these dumps as append-only events
// would do — and rather than silently dropping it, which is what replacing the rows in place
// would do. Two complete snapshots, and the departure is a row a screen can read.
func TestADepartureFromAReconciledSetIsDetectable(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	providerID := exampleStateProvider(t, database)

	yesterday := int64(1749913600)
	today := int64(1750000000)
	expiry := today + 3600
	attributes := `{"example-field":"example-value"}`

	// Yesterday: three things in the set, one of them carrying a validity end, because a
	// decision from the surveyed source carries a TTL and no timestamp at all.
	if err := database.InsertStateSnapshot(ctx, StateSnapshot{
		ProviderID: providerID, SetKey: "example-set", CapturedAt: yesterday,
		Items: []StateItem{
			{ItemKey: "example-item-stays"},
			{ItemKey: "example-item-leaves", Attributes: &attributes, ValidUntilAt: &expiry},
			{ItemKey: "example-item-also-leaves"},
		},
	}); err != nil {
		t.Fatalf("writing yesterday's set: %v", err)
	}

	// Today: one of them is gone, and one thing is new. The new one is not a departure and
	// must not appear.
	if err := database.InsertStateSnapshot(ctx, StateSnapshot{
		ProviderID: providerID, SetKey: "example-set", CapturedAt: today,
		Items: []StateItem{
			{ItemKey: "example-item-stays"},
			{ItemKey: "example-item-arrives"},
		},
	}); err != nil {
		t.Fatalf("writing today's set: %v", err)
	}

	departures, err := database.StateDepartures(ctx, providerID, "example-set")
	if err != nil {
		t.Fatalf("reading the departures: %v", err)
	}
	if len(departures) != 2 {
		t.Fatalf("%d things are reported as having left, want the two that are gone: %+v",
			len(departures), departures)
	}
	if departures[0].ItemKey != "example-item-also-leaves" ||
		departures[1].ItemKey != "example-item-leaves" {
		t.Fatalf("the departures are %q and %q", departures[0].ItemKey, departures[1].ItemKey)
	}

	// A departure carries WHEN it was last there and WHEN it was first absent, because a screen
	// putting "gone" next to a figure needs both instants and cannot recover either from a
	// boolean.
	departed := departures[1]
	if departed.LastPresentAt != yesterday || departed.AbsentSinceAt != today {
		t.Errorf("the departure is dated last present %d, absent since %d; want %d and %d",
			departed.LastPresentAt, departed.AbsentSinceAt, yesterday, today)
	}
	// And it carries what it looked like while it was there, which is the only copy left.
	if departed.Attributes == nil || *departed.Attributes != attributes {
		t.Errorf("the departed thing's attributes read %v", departed.Attributes)
	}
	if departed.ValidUntilAt == nil || *departed.ValidUntilAt != expiry {
		t.Errorf("the departed thing's validity end reads %v", departed.ValidUntilAt)
	}

	// NOT SILENTLY KEPT: the thing that left is absent from the current set. NOT SILENTLY
	// DROPPED: it is still readable in the snapshot that held it, which is what makes the
	// departure computable at all.
	current, err := database.StateItemsAt(ctx, providerID, "example-set", today)
	if err != nil {
		t.Fatalf("reading today's set back: %v", err)
	}
	for _, item := range current {
		if item.ItemKey == "example-item-leaves" {
			t.Error("the thing that left is still in the current set")
		}
	}
	previous, err := database.StateItemsAt(ctx, providerID, "example-set", yesterday)
	if err != nil {
		t.Fatalf("reading yesterday's set back: %v", err)
	}
	if len(previous) != 3 {
		t.Errorf("yesterday's set now holds %d things, so the newer poll overwrote it", len(previous))
	}

	// A thing that only ARRIVED is not a departure, which is the direction a naive
	// symmetric-difference query gets wrong.
	for _, departure := range departures {
		if departure.ItemKey == "example-item-arrives" {
			t.Error("a thing that arrived is reported as having left")
		}
	}
}

// TestAnEmptyReconciledSetReportsEveryThingAsDeparted is the edge the shape makes meaningful.
//
// A ban list that is now empty has not stopped reporting: it has reported that every ban is
// gone. A collector that skipped writing the empty snapshot would leave the previous set looking
// current for ever, which is the failure this kind's instant exists to prevent.
func TestAnEmptyReconciledSetReportsEveryThingAsDeparted(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	providerID := exampleStateProvider(t, database)

	if err := database.InsertStateSnapshot(ctx, StateSnapshot{
		ProviderID: providerID, SetKey: "example-set", CapturedAt: 1749913600,
		Items: []StateItem{{ItemKey: "example-item-one"}, {ItemKey: "example-item-two"}},
	}); err != nil {
		t.Fatalf("writing the first set: %v", err)
	}
	if err := database.InsertStateSnapshot(ctx, StateSnapshot{
		ProviderID: providerID, SetKey: "example-set", CapturedAt: 1750000000,
	}); err != nil {
		t.Fatalf("writing the empty set: %v", err)
	}

	departures, err := database.StateDepartures(ctx, providerID, "example-set")
	if err != nil {
		t.Fatalf("reading the departures: %v", err)
	}
	if len(departures) != 2 {
		t.Fatalf("%d things left an emptied set of two", len(departures))
	}
}

// TestASetObservedOnceReportsNoDeparture is the honest answer to a question that cannot be
// answered yet: nothing can be said to have left a set seen once.
func TestASetObservedOnceReportsNoDeparture(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	providerID := exampleStateProvider(t, database)

	if err := database.InsertStateSnapshot(ctx, StateSnapshot{
		ProviderID: providerID, SetKey: "example-set", CapturedAt: 1750000000,
		Items: []StateItem{{ItemKey: "example-item-one"}},
	}); err != nil {
		t.Fatalf("writing the only set: %v", err)
	}
	departures, err := database.StateDepartures(ctx, providerID, "example-set")
	if err != nil {
		t.Fatalf("reading the departures: %v", err)
	}
	if len(departures) != 0 {
		t.Fatalf("%d things are reported as having left a set observed once", len(departures))
	}
}

// TestOneProvidersSetsAreIndependent is why a snapshot names WHICH set.
//
// One source commonly reports several: the surveyed engine reports decisions and alerts, and the
// surveyed tunnel reports peers. Without a set key, a poll of one would read as a departure of
// everything in the other.
func TestOneProvidersSetsAreIndependent(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	providerID := exampleStateProvider(t, database)

	for _, setKey := range []string{"example-set-one", "example-set-two"} {
		if err := database.InsertStateSnapshot(ctx, StateSnapshot{
			ProviderID: providerID, SetKey: setKey, CapturedAt: 1749913600,
			Items: []StateItem{{ItemKey: "example-item"}},
		}); err != nil {
			t.Fatalf("writing the first %s: %v", setKey, err)
		}
	}
	// Only one set is polled again, and it is empty this time.
	if err := database.InsertStateSnapshot(ctx, StateSnapshot{
		ProviderID: providerID, SetKey: "example-set-one", CapturedAt: 1750000000,
	}); err != nil {
		t.Fatalf("writing the second example-set-one: %v", err)
	}

	emptied, err := database.StateDepartures(ctx, providerID, "example-set-one")
	if err != nil {
		t.Fatalf("reading the departures from the emptied set: %v", err)
	}
	if len(emptied) != 1 {
		t.Errorf("%d things left the emptied set, want its one member", len(emptied))
	}
	untouched, err := database.StateDepartures(ctx, providerID, "example-set-two")
	if err != nil {
		t.Fatalf("reading the departures from the untouched set: %v", err)
	}
	if len(untouched) != 0 {
		t.Errorf("%d things left a set that was not polled again", len(untouched))
	}
}

// TestRewritingOneSnapshotOfASetIsIdempotent is what a poller restarting inside one interval
// needs: the snapshot identity is (provider, set, instant), so the same poll offered twice is
// one snapshot and one set of members.
func TestRewritingOneSnapshotOfASetIsIdempotent(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	providerID := exampleStateProvider(t, database)

	snapshot := StateSnapshot{
		ProviderID: providerID, SetKey: "example-set", CapturedAt: 1750000000,
		Items: []StateItem{{ItemKey: "example-item-one"}, {ItemKey: "example-item-two"}},
	}
	for pass := 0; pass < 3; pass++ {
		if err := database.InsertStateSnapshot(ctx, snapshot); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
	}
	if stored := count(t, database, "state_snapshot"); stored != 1 {
		t.Errorf("three passes of one poll stored %d snapshots", stored)
	}
	if stored := count(t, database, "state_item"); stored != 2 {
		t.Errorf("three passes of a two-member set stored %d members", stored)
	}
}

// TestAReconciledSetRefusesAThingWithNoIdentityOrWithAttributesThatAreNotAnObject is the
// shape enforcement, and both halves of it matter to a reader.
//
// A member with no identity could not be compared across snapshots, so a departure involving it
// would be undetectable — the one thing this kind exists to make detectable. And attributes are
// read on a detail screen as an object's fields; a bare string or a list there would be a
// provider dumping free text into a column the attribute rule says is displayed structurally.
func TestAReconciledSetRefusesAThingWithNoIdentityOrWithAttributesThatAreNotAnObject(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	providerID := exampleStateProvider(t, database)

	if err := database.InsertStateSnapshot(ctx, StateSnapshot{
		ProviderID: providerID, SetKey: "example-set", CapturedAt: 1750000000,
		Items: []StateItem{{ItemKey: ""}},
	}); err == nil {
		t.Error("a set member with no identity was accepted")
	}

	notAnObject := `"example-bare-string"`
	if err := database.InsertStateSnapshot(ctx, StateSnapshot{
		ProviderID: providerID, SetKey: "example-set", CapturedAt: 1750000001,
		Items: []StateItem{{ItemKey: "example-item", Attributes: &notAnObject}},
	}); err == nil {
		t.Error("a set member whose attributes are not an object was accepted")
	}

	malformed := `{not json at all`
	if err := database.InsertStateSnapshot(ctx, StateSnapshot{
		ProviderID: providerID, SetKey: "example-set", CapturedAt: 1750000002,
		Items: []StateItem{{ItemKey: "example-item", Attributes: &malformed}},
	}); err == nil {
		t.Error("a set member whose attributes are not valid JSON was accepted")
	}

	// A set key with no name would name no set, so one poll of a provider's decisions and one
	// of its peers would be the same set and each would read as the other's total departure.
	if err := database.InsertStateSnapshot(ctx, StateSnapshot{
		ProviderID: providerID, SetKey: "", CapturedAt: 1750000003,
	}); err == nil {
		t.Error("a snapshot of a set with no name was accepted")
	}
}

// TestAHalfWrittenReconciledSetIsNotStoredAtAll is why the write path is one transaction.
//
// A partial snapshot is not a slightly incomplete row: because a snapshot ASSERTS the set was
// complete at its instant, every member missing from it reads as a departure. So a set whose
// second member is rejected must leave no snapshot behind at all, rather than a snapshot that
// states the rest of the set has gone.
func TestAHalfWrittenReconciledSetIsNotStoredAtAll(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	providerID := exampleStateProvider(t, database)

	if err := database.InsertStateSnapshot(ctx, StateSnapshot{
		ProviderID: providerID, SetKey: "example-set", CapturedAt: 1749913600,
		Items: []StateItem{{ItemKey: "example-item-one"}, {ItemKey: "example-item-two"}},
	}); err != nil {
		t.Fatalf("writing the first set: %v", err)
	}

	// The second member carries an instant beyond the upper timestamp bound, so its insert
	// fails after the first member has already been offered.
	beyondTheBound := int64(4102444800)
	if err := database.InsertStateSnapshot(ctx, StateSnapshot{
		ProviderID: providerID, SetKey: "example-set", CapturedAt: 1750000000,
		Items: []StateItem{
			{ItemKey: "example-item-one"},
			{ItemKey: "example-item-two", ValidUntilAt: &beyondTheBound},
		},
	}); err == nil {
		t.Fatal("a set with an unstorable member was accepted whole")
	}

	if stored := count(t, database, "state_snapshot"); stored != 1 {
		t.Errorf("%d snapshots are stored, so the failed poll left one behind", stored)
	}
	departures, err := database.StateDepartures(ctx, providerID, "example-set")
	if err != nil {
		t.Fatalf("reading the departures: %v", err)
	}
	if len(departures) != 0 {
		t.Errorf("a failed poll reports %d departures, which is the false statement the "+
			"transaction exists to prevent", len(departures))
	}
}
