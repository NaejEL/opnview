// Package specialpurpose holds the IANA special-purpose address registries, so the widget
// API can say whether an address an upstream interface holds is public or lies in a range
// the Internet never routes to it -- which is how *Public address* tells that an
// installation is behind another NAT, by arithmetic on the address and never by a guess
// about a name (docs/widget-catalogue.md, "Public address").
//
// THESE ARE STANDARDS, NOT INSTALLATION DATA. The address blocks below are transcribed
// from the two IANA registries, each block with the RFC the registry cites for it:
//
//	IPv4 Special-Purpose Address Registry:
//	    https://www.iana.org/assignments/iana-ipv4-special-registry
//	IPv6 Special-Purpose Address Registry:
//	    https://www.iana.org/assignments/iana-ipv6-special-registry
//
// read on 10 October 2026. They are the one place in the program an address block is a
// literal, and internal/opnsense/source_policy_test.go exempts this file and no other from
// its literal scan, for that reason (specs/SPEC-step-5b-widget-api.md, AC39). No address of
// any network opnview observes is here.
package specialpurpose
