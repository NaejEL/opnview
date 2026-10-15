// Package ipfixregistry holds the IANA IPFIX Information Elements registry, so the NetFlow v9 and
// IPFIX decoder can name and type every field it decodes by the standard's own words
// (sourceIPv4Address, octetDeltaCount, ...) rather than by numbers or by names opnview invented.
//
// THIS IS A STANDARD, NOT INSTALLATION DATA, like internal/specialpurpose. elements.csv is the
// registry's element id, name, abstract data type and data type semantics, transcribed from
//
//	https://www.iana.org/assignments/ipfix/ipfix-information-elements.csv
//
// retrieved on 11 October 2026, with every other column removed and the reserved and unassigned
// rows dropped. NetFlow v9 (RFC 3954) field types 1 to 127 are the same numbers with the same
// meaning, which is why one registry serves both versions (RFC 7012, section 1).
package ipfixregistry
