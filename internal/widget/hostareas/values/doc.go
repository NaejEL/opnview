// Package values holds the script host's areas that work on values a script already holds:
// figures and their sums, envelopes and conditions, buckets and periods, parameters and
// references, the request, the map's and matrix's helpers, JSON, special-purpose addresses,
// services and settings. Each area is a file that registers itself from its own init()
// (script.RegisterHostArea); internal/widget links the package.
package values
