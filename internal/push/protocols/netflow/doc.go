// Package netflow is the NetFlow push protocol: version 9 (RFC 3954) and IPFIX (RFC 7011), received
// over UDP, decoded through their templates into fields named and typed by the IANA IPFIX
// Information Elements registry (internal/push/ipfixregistry).
//
// IT KNOWS THE STANDARDS AND NOTHING OF ANY EXPORTER. Which elements an exporter sends, and what a
// subscriber stores of them, are the subscribing plugin's. What OPNsense's exporter actually sends --
// v9 only, no direction element, several sequence counters under one observation domain -- is
// recorded in docs/opnsense-api-survey.md, "Push sources", S5, S10 and S11.
package netflow
