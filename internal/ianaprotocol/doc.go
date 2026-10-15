// Package ianaprotocol holds the keywords of IANA's Assigned Internet Protocol Numbers, so a protocol
// the filter log names can be compared with one an exporter numbers (PS17 of the NetFlow join
// amendment to specs/SPEC-backend-completion-plugins.md: "Protocol is compared by IANA number").
//
// THIS IS A STANDARD, NOT INSTALLATION DATA, like internal/push/ipfixregistry. protocol-numbers.csv
// is the registry's decimal number and keyword, transcribed from
//
//	https://www.iana.org/assignments/protocol-numbers/protocol-numbers-1.csv
//
// retrieved on 11 October 2026, with every other column removed and the ranges and the numbers with
// no keyword dropped.
//
// WHY A KEYWORD MAPS. The filter log stores the protocol's NAME: OPNsense's filterlog writes
// getprotobynumber(3)'s p_name (opnsense/ports 26.7.3, opnsense/filterlog/files/print-ip.c and
// print-ip6.c), and the FreeBSD protocols database it reads names each protocol by IANA's keyword
// in lower case -- tcp, udp, icmp, ipv6-icmp (opnsense/src stable/26.7, lib/libc/net/protocols). A
// name is therefore matched to its number case-insensitively, the registry's "(deprecated)" mark
// set aside. A name the registry does not hold -- FreeBSD's `carp` for 112, which IANA calls VRRP --
// has no number, and a record of it compares equal to nothing: never guessed.
package ianaprotocol
