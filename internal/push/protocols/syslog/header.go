package syslog

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The syslog header, as RFC 5424 (section 6) and RFC 3164 (section 4.1) write it. Nothing here
// knows a program: the header ends where the message begins, and what the message says is the
// subscribing plugin's to read.

// Header is one decoded header and its message.
type Header struct {
	Facility int64
	Severity int64
	// Instant is set when the timestamp carried its offset; Zoneless holds the timestamp's text
	// when it did not (RFC 3164's "Mmm dd hh:mm:ss", or an ISO form without an offset).
	Instant  time.Time
	Zoneless string
	// Hostname, Program (RFC 3164 TAG / RFC 5424 APP-NAME), ProcessID, MessageID and
	// StructuredData are empty when absent or NILVALUE.
	Hostname       string
	Program        string
	ProcessID      string
	MessageID      string
	StructuredData string
	Message        string
	// Version is 1 for RFC 5424 and 0 for RFC 3164.
	Version int
}

// errUnparseable is a line that is not a syslog header: counted as header_unparseable.
var errUnparseable = errors.New("syslog: the line is not an RFC 5424 or RFC 3164 header")

// maxPriority is the highest PRI value: facility 23, severity 7.
const maxPriority = 191

// nilValue is RFC 5424's NILVALUE.
const nilValue = "-"

// utf8BOM begins an RFC 5424 MSG encoded as UTF-8 (section 6.4).
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// ParseHeader decodes one message.
func ParseHeader(line []byte) (Header, error) {
	line = bytes.TrimRight(line, "\r\n\x00")
	if len(line) < 3 || line[0] != '<' {
		return Header{}, errUnparseable
	}
	end := bytes.IndexByte(line[:min(len(line), 5)], '>')
	if end < 2 {
		return Header{}, errUnparseable
	}
	digits := string(line[1:end])
	if len(digits) > 1 && digits[0] == '0' {
		return Header{}, errUnparseable
	}
	priority, err := strconv.Atoi(digits)
	if err != nil || priority < 0 || priority > maxPriority {
		return Header{}, errUnparseable
	}
	header := Header{Facility: int64(priority / 8), Severity: int64(priority % 8)}
	rest := line[end+1:]
	if len(rest) >= 2 && rest[0] == '1' && rest[1] == ' ' {
		return parse5424(header, rest[2:])
	}
	return parse3164(header, rest)
}

// parse5424 reads "TIMESTAMP SP HOSTNAME SP APP-NAME SP PROCID SP MSGID SP STRUCTURED-DATA [SP MSG]".
func parse5424(header Header, rest []byte) (Header, error) {
	header.Version = 1
	fields := make([]string, 0, 5)
	for range 5 {
		space := bytes.IndexByte(rest, ' ')
		if space <= 0 {
			return Header{}, errUnparseable
		}
		fields = append(fields, string(rest[:space]))
		rest = rest[space+1:]
	}
	if fields[0] != nilValue {
		instant, zoneless, ok := parseISO(fields[0])
		if !ok {
			return Header{}, errUnparseable
		}
		header.Instant, header.Zoneless = instant, zoneless
	}
	header.Hostname = nilToEmpty(fields[1])
	header.Program = nilToEmpty(fields[2])
	header.ProcessID = nilToEmpty(fields[3])
	header.MessageID = nilToEmpty(fields[4])
	data, remainder, ok := structuredData(rest)
	if !ok {
		return Header{}, errUnparseable
	}
	header.StructuredData = data
	if len(remainder) > 0 {
		if remainder[0] != ' ' {
			return Header{}, errUnparseable
		}
		remainder = bytes.TrimPrefix(remainder[1:], utf8BOM)
	}
	if !utf8.Valid(remainder) {
		remainder = bytes.ToValidUTF8(remainder, []byte("�"))
	}
	header.Message = string(remainder)
	return header, nil
}

// structuredData reads STRUCTURED-DATA: NILVALUE or one or more SD-ELEMENTs, each "[" SD-ID
// *(SP SD-PARAM) "]", a PARAM-VALUE quoted with \", \\ and \] escaped. It returns the raw text.
func structuredData(rest []byte) (string, []byte, bool) {
	if len(rest) == 0 {
		return "", nil, false
	}
	if rest[0] == '-' {
		if len(rest) == 1 || rest[1] == ' ' {
			return "", rest[1:], true
		}
		return "", nil, false
	}
	position := 0
	for position < len(rest) && rest[position] == '[' {
		position++
		quoted := false
		closed := false
		for position < len(rest) {
			c := rest[position]
			switch {
			case quoted && c == '\\' && position+1 < len(rest):
				position += 2
				continue
			case c == '"':
				quoted = !quoted
			case !quoted && c == ']':
				closed = true
			}
			position++
			if closed {
				break
			}
		}
		if !closed {
			return "", nil, false
		}
	}
	if position == 0 {
		return "", nil, false
	}
	return string(rest[:position]), rest[position:], true
}

func nilToEmpty(field string) string {
	if field == nilValue {
		return ""
	}
	return field
}

// isoLayouts are the RFC 3339 forms RFC 5424 admits, and the same without an offset, which
// syslog-ng writes in RFC 3164 headers when told to and which carries no zone.
var isoLayouts = []string{time.RFC3339Nano, time.RFC3339}

// zonelessLayouts are ISO forms carrying no offset.
var zonelessLayouts = []string{"2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05"}

// parseISO reads an ISO 8601 timestamp: with its offset it is an instant; without one it is
// zoneless text, resolved later with the firewall's measured offset.
func parseISO(text string) (time.Time, string, bool) {
	for _, layout := range isoLayouts {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed.UTC(), "", true
		}
	}
	for _, layout := range zonelessLayouts {
		if _, err := time.Parse(layout, text); err == nil {
			return time.Time{}, text, true
		}
	}
	return time.Time{}, "", false
}

// rfc3164Layout is "Mmm dd hh:mm:ss", the day space-padded.
const rfc3164Layout = "Jan _2 15:04:05"

// parse3164 reads "TIMESTAMP SP [HOSTNAME SP] TAG[\[PID\]]: MSG", the timestamp in RFC 3164's form or
// in ISO 8601 (a syslog-ng variant).
func parse3164(header Header, rest []byte) (Header, error) {
	text := string(rest)
	switch {
	case len(text) >= 16 && text[15] == ' ' && isRFC3164Stamp(text[:15]):
		header.Zoneless = text[:15]
		text = text[16:]
	default:
		space := strings.IndexByte(text, ' ')
		if space <= 0 {
			return Header{}, errUnparseable
		}
		instant, zoneless, ok := parseISO(text[:space])
		if !ok {
			return Header{}, errUnparseable
		}
		header.Instant, header.Zoneless = instant, zoneless
		text = text[space+1:]
	}
	// HOSTNAME is optional: the first token is the TAG when it ends the header with a colon or
	// carries the [pid], and the hostname otherwise.
	first, afterFirst, hasSpace := strings.Cut(text, " ")
	if !hasSpace {
		return Header{}, errUnparseable
	}
	if !isTagToken(first) {
		header.Hostname = first
		text = afterFirst
	}
	tag, message, ok := splitTag(text)
	if !ok {
		return Header{}, errUnparseable
	}
	if program, pid, bracketed := strings.Cut(tag, "["); bracketed {
		if !strings.HasSuffix(pid, "]") {
			return Header{}, errUnparseable
		}
		header.Program, header.ProcessID = program, strings.TrimSuffix(pid, "]")
	} else {
		header.Program = tag
	}
	if header.Program == "" {
		return Header{}, errUnparseable
	}
	header.Message = message
	return header, nil
}

// isRFC3164Stamp reports whether text is "Mmm dd hh:mm:ss".
func isRFC3164Stamp(text string) bool {
	_, err := time.Parse(rfc3164Layout, text)
	return err == nil
}

// isTagToken reports whether a token is a TAG ending the header: "prog:" or "prog[pid]:".
func isTagToken(token string) bool {
	return strings.HasSuffix(token, ":") && !strings.ContainsAny(token[:len(token)-1], ": ")
}

// splitTag splits "TAG: MSG" (the space after the colon optional).
func splitTag(text string) (string, string, bool) {
	colon := strings.IndexByte(text, ':')
	if colon <= 0 {
		return "", "", false
	}
	tag := text[:colon]
	if strings.ContainsAny(tag, " \t") {
		return "", "", false
	}
	message := text[colon+1:]
	message = strings.TrimPrefix(message, " ")
	return tag, message, true
}
