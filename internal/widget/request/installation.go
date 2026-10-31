package request

// GeoInfo is the geolocation of one address, or the state of its absence, as the declarative shape
// engine still names it (internal/widget/shapes). The reads of the installation model, the clients,
// the owners, the rules and the geolocations a request once cached here are the figures library's
// areas since ADR 0021 (plugins/figures), over each widget's own statements.
type GeoInfo struct {
	// LookupState is resolved, miss or pending, or no_row for an address never looked up.
	LookupState    string   `json:"lookup_state"`
	CountryCode    *string  `json:"country_code"`
	CountryName    *string  `json:"country_name"`
	Latitude       *float64 `json:"latitude"`
	Longitude      *float64 `json:"longitude"`
	ASN            *int64   `json:"asn"`
	Operator       *string  `json:"operator"`
	DatasetBuildAt *int64   `json:"dataset_build_at"`
}

// GeoNoRow is the state of an address no geolocation lookup has stored a row for.
const GeoNoRow = "no_row"
