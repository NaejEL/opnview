// Package flows is the part of the store every collector writes through: the typed writes
// (write.go), the classification of addresses into interfaces and clients (classify.go), the
// interfaces' networks (network.go), this firewall's own addresses (thisfirewall.go), and the
// pairing of the two logged legs of one forwarded connection (pair.go, pairchunk.go).
// internal/store composes it into Store.
package flows
