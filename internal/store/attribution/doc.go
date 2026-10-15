// Package attribution is the part of the store that names traffic: the DNS resource records the
// resolvers hold (records.go), the attribution of each flow to the names looked up before it
// (attribute.go), hostnames resolved across the local data and both address families
// (dualstack.go), and each provider's record rate (recordrate.go). internal/store composes it
// into Store.
package attribution
