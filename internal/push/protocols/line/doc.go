// Package line is the line push protocol (PA7 of the plugin-API amendment to
// specs/SPEC-backend-completion-plugins.md): text framed by newlines, over TCP -- each line one frame of the
// connection -- or UDP -- each line of a datagram one message. The core reads no structure in a line: a push
// entry of it names a decode function of its plugin's script, called with one line, which returns the
// records or rows the line held.
//
// "Line", as the protocol's name, is opnview's own term: neither OPNsense nor RFC 5424 names a protocol
// whose decoder is a script (docs/data-model.md, *Vocabulary*).
package line
