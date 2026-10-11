// Package live holds the live-interest lease (C3 of the backend-completion cycle,
// docs/widget-api.md "Live figures").
//
// LIVE INTEREST is opnview's own term -- OPNsense's dashboard polls the firewall itself and has no
// word for it -- for "somebody is looking at a live figure now". Every authenticated request for a
// live figure (a widget answer carrying a current rate, the rolling strip) renews the lease; while
// it holds, the plugins declaring a live pass run it at their live cadence, through the broker,
// and when it lapses -- live_interest_seconds after the last renewal, 30 s by default -- they stop.
// The widget API itself never calls the firewall: it renews the lease and reads stored samples.
//
// The package has no dependency, so the widget service, the web surfaces and the collector share
// one holder without importing each other.
package live
