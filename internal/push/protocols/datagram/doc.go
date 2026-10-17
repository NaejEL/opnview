// Package datagram is the datagram push protocol (PA7 of the plugin-API amendment to
// specs/SPEC-backend-completion-plugins.md): UDP, one datagram per message, whose octets the core does not
// read. A push entry of it names a decode function of its plugin's script, called with one datagram, which
// returns the records or rows the datagram held.
//
// "Datagram", as the protocol's name, is opnview's own term: neither OPNsense nor RFC 5424 names a
// protocol whose decoder is a script (docs/data-model.md, *Vocabulary*).
package datagram
