// Package maxmind is the second of the three outbound calls opnview makes: the
// MaxMind GeoLite2 City and ASN databases, downloaded on the operator's own
// account, refreshed, and read locally to place the addresses opnview has seen.
//
// IT IS ONE OF EXACTLY THREE PACKAGES ALLOWED TO BUILD AN HTTP REQUEST; the firewall
// client in internal/opnsense and the Public Suffix List download in
// internal/publicsuffix are the others, and a source-policy test in internal/opnsense
// holds the three. Nothing here contacts anything but the two hosts below, and a
// lookup contacts nothing at all: it reads a file on disk.
//
// See specs/SPEC-maxmind-geolite2.md for what MaxMind requires and why each choice
// here was made.
package maxmind

// The hosts, as MaxMind's documentation names them on the page cited below, read on
// 3 October 2026. A download is asked of the first and redirected to the second, an
// R2 presigned URL; no other host is ever contacted, redirects included.
const (
	// downloadHost answers the permalinks, and is the only host the credentials go to.
	downloadHost = "download.maxmind.com"
	// redirectHost is where MaxMind redirects a download.
	redirectHost = "mm-prod-geoip-databases.a2649acb697e2c09b632799562c076f2.r2.cloudflarestorage.com"
	// downloadBase is the scheme and host of the permalinks.
	downloadBase = "https://download.maxmind.com"
	// documentation is the page that establishes the permalink form, the Basic
	// authentication with the account ID and the licence key, the redirect, the
	// free HEAD and the daily download limit.
	documentation = "https://dev.maxmind.com/geoip/updating-databases/"
)

// Edition is a MaxMind database edition, as its permalink and its own metadata name
// it.
type Edition string

// The two editions opnview reads.
const (
	// EditionCity answers country, coordinates and the country's name.
	EditionCity Edition = "GeoLite2-City"
	// EditionASN answers the autonomous system number and its organisation.
	EditionASN Edition = "GeoLite2-ASN"
)

// editions is the order the two are refreshed in.
func editions() []Edition { return []Edition{EditionCity, EditionASN} }

// permalink is the download address of one edition's binary database, a gzip tar
// archive, under base.
func permalink(base string, edition Edition) string {
	return base + "/geoip/databases/" + string(edition) + "/download?suffix=tar.gz"
}
