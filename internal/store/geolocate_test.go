package store_test

import (
	"context"
	"slices"
	"testing"

	"github.com/NaejEL/opnview/internal/store/geo"
)

// TestTheAddressesToGeolocateAreListedOnPostgreSQL is D2 of the second live validation: the query ran
// SQLite's null-safe `IS NOT ?`, which PostgreSQL refuses, so no address was ever geolocated. On the
// real database it lists every address with no answer, an answer from an older build, or an answer
// from another provider, and leaves out one the active provider's current build answered.
func TestTheAddressesToGeolocateAreListedOnPostgreSQL(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	for index := range 2 {
		if err := database.InsertFlow(ctx, exampleFlow(index)); err != nil {
			t.Fatalf("storing a flow: %v", err)
		}
	}
	geoProviderID, err := database.ProviderID(ctx, "ip_geolocation", "maxmind_geolite2")
	if err != nil {
		t.Fatal(err)
	}
	otherProviderID, err := database.ProviderID(ctx, "firewall_log", "pf")
	if err != nil {
		t.Fatal(err)
	}
	const build = int64(1750000000)
	answer := func(address string, providerID, builtAt int64) {
		t.Helper()
		if err := database.UpsertGeoASN(ctx, geo.GeoASN{Address: address, ProviderID: providerID,
			LookupState: geo.GeoMiss, DatasetBuildAt: builtAt, LookedUpAt: build}); err != nil {
			t.Fatal(err)
		}
	}
	answer("example-source-0", geoProviderID, build)
	answer("example-destination-0", geoProviderID, build-1)
	answer("example-source-1", otherProviderID, build)

	addresses, err := database.AddressesToGeolocate(ctx, geoProviderID, build)
	if err != nil {
		t.Fatalf("listing the addresses to geolocate: %v", err)
	}
	want := []string{"example-destination-0", "example-destination-1", "example-source-1"}
	if !slices.Equal(addresses, want) {
		t.Errorf("addresses %v, want %v", addresses, want)
	}
}
