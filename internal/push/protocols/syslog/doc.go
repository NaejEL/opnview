// Package syslog is the syslog push protocol: the header of RFC 5424 and RFC 3164, decoded once per
// message, received over UDP and over TCP with either framing of RFC 6587.
//
// IT KNOWS THE HEADER AND NOTHING ELSE. Which programs exist, what any program's message says and
// how to read it are the subscribing plugins' (docs/plugins.md, "Push sources"): a line no plugin
// subscribes to costs one header decode and one index lookup, and is dropped.
package syslog
