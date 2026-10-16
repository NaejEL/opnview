// Package traffic holds the script host's areas that read the logged traffic of a window for a
// widget's script: flows, volumes, the clients' volumes, buckets and series, sites, attribution,
// addresses and peers. Each area is a file that registers itself from its own init()
// (script.RegisterHostArea); internal/widget links the package.
package traffic
