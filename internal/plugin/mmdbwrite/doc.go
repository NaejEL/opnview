// Package mmdbwrite writes small MaxMind DB files and the gzip tar archives MaxMind serves them
// in, for a plugin's tests: `opnview plugin test` serves what it writes when a test arranges an
// archive (docs/plugins.md, "Testing a plugin"), so a plugin reading MMDB files is tested without a
// real database and without Go.
//
// It writes an IPv6 search tree with 24-bit records, IPv4 networks placed under ::/96 as MaxMind's
// own databases place them, and the data types the GeoLite2 City and ASN records use. It follows
// the published MaxMind DB format, version 2.0 (https://maxmind.github.io/MaxMind-DB/), and nothing
// in it is read from a real database: every record a test looks up is one the test wrote.
package mmdbwrite
