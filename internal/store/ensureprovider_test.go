package store_test

import (
	"context"
	"testing"

	"github.com/NaejEL/opnview/internal/store/vocab"
)

// TestEnsureProviderRegistersAnImplementationTheSchemaDoesNotSeed is what lets a provider be added
// as a directory: the registry row and the availability row the schema gives every registry row
// are created from the implementation's own declaration, in the state the seed uses.
func TestEnsureProviderRegistersAnImplementationTheSchemaDoesNotSeed(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()

	id, created, err := database.EnsureProvider(ctx, "dhcp_lease", "example-new", "Example new", 1700000000)
	if err != nil || !created || id == 0 {
		t.Fatalf("registering a new implementation gave %d, %v, %v", id, created, err)
	}
	state, probe, checkedAt, err := database.Availability(ctx, id)
	if err != nil {
		t.Fatalf("reading its availability row: %v", err)
	}
	if state != vocab.StateUnavailable || probe != "not_yet_probed" || checkedAt != 0 {
		t.Errorf("the new row starts as %q / %q / %d", state, probe, checkedAt)
	}
	outcome, err := database.AvailabilityOutcomeOf(ctx, id)
	if err != nil || outcome != vocab.OutcomeNotYetProbed {
		t.Errorf("the new row's outcome is %q, %v", outcome, err)
	}
	active, err := database.ActiveProviderIDs(ctx, "dhcp_lease")
	if err != nil {
		t.Fatal(err)
	}
	for _, activeID := range active {
		if activeID == id {
			t.Error("a registered implementation is active before any probe round chose it")
		}
	}
}

// TestEnsureProviderLeavesAnExistingRowExactlyAsItIs: the second call, and a call for a row the
// schema seeded, change nothing -- not its display name, not its availability.
func TestEnsureProviderLeavesAnExistingRowExactlyAsItIs(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()

	seeded, err := database.ProviderID(ctx, "dhcp_lease", "kea")
	if err != nil {
		t.Fatal(err)
	}
	id, created, err := database.EnsureProvider(ctx, "dhcp_lease", "kea", "A different name", 1700000000)
	if err != nil || created || id != seeded {
		t.Errorf("ensuring a seeded row gave %d, %v, %v", id, created, err)
	}
	var display string
	if err := database.DB().QueryRowContext(ctx,
		"SELECT display_name FROM provider WHERE id = $1", seeded).Scan(&display); err != nil {
		t.Fatal(err)
	}
	if display != "Kea DHCP" {
		t.Errorf("the seeded display name became %q", display)
	}

	first, _, err := database.EnsureProvider(ctx, "dhcp_lease", "example-twice", "Example", 1700000000)
	if err != nil {
		t.Fatal(err)
	}
	second, created, err := database.EnsureProvider(ctx, "dhcp_lease", "example-twice", "Example", 1700000001)
	if err != nil || created || second != first {
		t.Errorf("the second registration gave %d, %v, %v", second, created, err)
	}
}

// TestEnsureProviderRefusesAKindTheSchemaDoesNotAdmit: since the plugin cycle no list of kinds is
// written in the schema -- a plugin may declare a kind of its own -- and what the registry's CHECK
// keeps is the shape of a kind name, a snake_case word; the refusal is the schema's own.
func TestEnsureProviderRefusesAKindTheSchemaDoesNotAdmit(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	for _, kind := range []string{"Not A Kind", "kind-with-dashes", ""} {
		if _, _, err := database.EnsureProvider(context.Background(), kind, "x", "X", 1700000000); err == nil {
			t.Errorf("the kind %q, which is not a snake_case word, was registered", kind)
		}
	}
}
