// Package readers holds the script host's areas that read the installation's data for a widget's
// script: flows, volumes, buckets and series, sites, attribution, peers, rates, samples,
// datasets, the installation, and the widget's own statements. Each area is a file that registers
// itself from its own init() (script.RegisterHostArea); internal/widget links the package.
package readers
