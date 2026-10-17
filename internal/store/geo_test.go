package store_test

import (
	"context"
	"reflect"
	"testing"
)

// TestTheAddressesToGeolocateAreListedOnPostgreSQL runs the listing every geolocation pass starts with
// against PostgreSQL. It once compared a provider with SQLite's IS NOT, which PostgreSQL refuses as a
// syntax error, and the pass failed on every run of the live service: an address without an answer, one
// answered by an older build and one answered by another provider are listed; one answered by this
// provider's current build is not.
func TestTheAddressesToGeolocateAreListedOnPostgreSQL(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	for _, flow := range examplePage() {
		if err := database.InsertFlow(ctx, flow); err != nil {
			t.Fatal(err)
		}
	}
	const provider, build = int64(1), int64(1750000000)
	every, err := database.AddressesToGeolocate(ctx, provider, build)
	if err != nil {
		t.Fatalf("listing the addresses to geolocate: %v", err)
	}
	if len(every) < 2 {
		t.Fatalf("the flows carry %d addresses, want at least two", len(every))
	}
	exec := func(statement string, arguments ...any) {
		t.Helper()
		if _, err := database.DB().ExecContext(ctx, statement, arguments...); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	// The first address answered by this provider's current build; the second by another provider.
	exec(`INSERT INTO provider (id, kind, provider_key, display_name, is_active, registered_at)
		VALUES (900, 'ip_geolocation', 'example-other-dataset', 'Example other dataset', false, 1750000000)`)
	exec(`INSERT INTO geo_asn (address, provider_id, lookup_state, dataset_build_at, looked_up_at)
		VALUES ($1, $2, 'miss', $3, $3), ($4, 900, 'miss', $3, $3)`, every[0], provider, build, every[1])
	left, err := database.AddressesToGeolocate(ctx, provider, build)
	if err != nil {
		t.Fatalf("listing the addresses to geolocate: %v", err)
	}
	if want := every[1:]; !reflect.DeepEqual(left, want) {
		t.Errorf("after one answer of this build and one of another provider, the listing is %v, want %v", left,
			want)
	}
	// A newer build asks every address again.
	again, err := database.AddressesToGeolocate(ctx, provider, build+1)
	if err != nil {
		t.Fatalf("listing the addresses to geolocate: %v", err)
	}
	if !reflect.DeepEqual(again, every) {
		t.Errorf("under a newer build the listing is %v, want every address %v", again, every)
	}
}
