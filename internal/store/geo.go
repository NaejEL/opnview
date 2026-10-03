package store

import (
	"context"
	"database/sql"
	"fmt"
)

// The geo and ASN enrichment of the addresses opnview has seen. The dataset that
// answers is the active geo_asn provider's — today the MaxMind GeoLite2 City and
// ASN databases, read by internal/maxmind. This file only stores the answers and
// says which addresses still need one.

// GeoLookupState is geo_asn.lookup_state.
type GeoLookupState string

const (
	// GeoResolved is an address the dataset placed: a country at least.
	GeoResolved GeoLookupState = "resolved"
	// GeoMiss is an address the dataset was asked about and does not hold.
	GeoMiss GeoLookupState = "miss"
)

// GeoASN is one row of geo_asn: what one dataset build said about one address.
// The optional fields are nil when the dataset gave no value for them.
type GeoASN struct {
	// Address is the address looked up, as flow stores it.
	Address string
	// ProviderID is the provider whose dataset answered.
	ProviderID int64
	// LookupState is resolved or miss.
	LookupState GeoLookupState
	// CountryCode is the ISO 3166-1 alpha-2 code; required on a resolved row.
	CountryCode *string
	// CountryName is the country's English name.
	CountryName *string
	// Latitude and Longitude are the dataset's approximate location.
	Latitude  *float64
	Longitude *float64
	// ASN is the autonomous system number, and Operator the organisation the
	// dataset names for it.
	ASN      *int64
	Operator *string
	// DatasetBuildAt is the build of the dataset that answered, so a stale answer
	// is visible; it is recorded on a miss as well, so a miss is asked again when a
	// newer build arrives.
	DatasetBuildAt int64
	// LookedUpAt is when the answer was stored; the purge reads it.
	LookedUpAt int64
}

// UpsertGeoASN stores the answer for one address, replacing any earlier one.
func (s *Store) UpsertGeoASN(ctx context.Context, row GeoASN) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO geo_asn (address, provider_id, lookup_state, country_code, country_name,
		        latitude, longitude, asn, operator, dataset_build_at, looked_up_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (address) DO UPDATE SET
		        provider_id = excluded.provider_id,
		        lookup_state = excluded.lookup_state,
		        country_code = excluded.country_code,
		        country_name = excluded.country_name,
		        latitude = excluded.latitude,
		        longitude = excluded.longitude,
		        asn = excluded.asn,
		        operator = excluded.operator,
		        dataset_build_at = excluded.dataset_build_at,
		        looked_up_at = excluded.looked_up_at`,
		row.Address, row.ProviderID, string(row.LookupState), row.CountryCode, row.CountryName,
		row.Latitude, row.Longitude, row.ASN, row.Operator, row.DatasetBuildAt, row.LookedUpAt)
	if err != nil {
		return fmt.Errorf("store: storing the geolocation of %s: %w", row.Address, err)
	}
	return nil
}

// AddressesToGeolocate returns every address the stored flows carry, as source or
// destination, that has no geo_asn row or one answered by a build older than
// build, in address order.
//
// Every address is returned, private ones included: whether an address is worth
// asking a dataset about is the caller's decision, made on a parsed address rather
// than on text in SQL.
func (s *Store) AddressesToGeolocate(ctx context.Context, build int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT seen.address
		   FROM (SELECT src_address AS address FROM flow
		         UNION
		         SELECT dst_address FROM flow) AS seen
		   LEFT JOIN geo_asn AS g ON g.address = seen.address
		  WHERE seen.address IS NOT NULL
		    AND (g.address IS NULL OR g.dataset_build_at IS NULL OR g.dataset_build_at < ?)
		  ORDER BY seen.address`,
		build)
	if err != nil {
		return nil, fmt.Errorf("store: listing the addresses to geolocate: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var addresses []string
	for rows.Next() {
		var address sql.NullString
		if err := rows.Scan(&address); err != nil {
			return nil, fmt.Errorf("store: listing the addresses to geolocate: %w", err)
		}
		addresses = append(addresses, address.String)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing the addresses to geolocate: %w", err)
	}
	return addresses, nil
}
