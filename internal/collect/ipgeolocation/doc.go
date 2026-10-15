// Package ipgeolocation is the ip_geolocation kind: a dataset that places public addresses --
// their country, coordinates, autonomous system and operator. "IP geolocation" is MaxMind's own term
// for its GeoLite2 products; the built-in implementation is the maxmind plugin, and a third party
// may drop another (DB-IP, IPinfo: both publish the MMDB format the plugin host reads).
//
// WHAT THE KIND PRODUCES. A dataset's state -- held or not, and its build -- and, for a batch of
// public addresses, what the dataset says of each. What is true of any such dataset is written once
// in internal/collect (services.go): which addresses need placing, never a non-public one; at most
// 20 000 per pass; the answers stored in geo_asn against the provider and the build; every address
// placed again when the active provider or its build changes; one active provider at a time, chosen
// by the operator's selection and defaulting to the built-in.
package ipgeolocation
