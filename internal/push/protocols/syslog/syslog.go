package syslog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/NaejEL/opnview/internal/push"
)

func init() { push.Register(protocol{}) }

// Name is the protocol's name in a push declaration.
const Name = "syslog"

// The fields every syslog record carries, which a declaration may map to columns.
const (
	FieldFacility       = "facility"
	FieldSeverity       = "severity"
	FieldTimestamp      = "timestamp"
	FieldHostname       = "hostname"
	FieldProgram        = "program"
	FieldProcessID      = "process_id"
	FieldMessageID      = "message_id"
	FieldStructuredData = "structured_data"
	FieldMessage        = "message"
)

// fields are the header's fields and their types.
var fields = map[string]string{
	FieldFacility: push.TypeInteger, FieldSeverity: push.TypeInteger, FieldTimestamp: push.TypeInstant,
	FieldHostname: push.TypeText, FieldProgram: push.TypeText, FieldProcessID: push.TypeText,
	FieldMessageID: push.TypeText, FieldStructuredData: push.TypeText, FieldMessage: push.TypeText,
}

// ReasonHeaderUnparseable counts a line that is not a syslog header: dropped, never stored.
const ReasonHeaderUnparseable = "header_unparseable"

type protocol struct{}

func (protocol) Name() string         { return Name }
func (protocol) Transports() []string { return []string{push.TransportUDP, push.TransportTCP} }
func (protocol) NewDecoder(push.Limits) push.Decoder {
	return &decoder{seen: map[string]int64{}}
}
func (protocol) NewFramer(reader io.Reader, maxFrame int) push.Framer {
	return newFramer(reader, maxFrame)
}
func (protocol) Fields() map[string]string {
	copied := make(map[string]string, len(fields))
	for name, columnType := range fields {
		copied[name] = columnType
	}
	return copied
}
func (protocol) FieldType(name string) (string, bool) {
	columnType, found := fields[name]
	return columnType, found
}

// LossDetectable: TCP is acknowledged and its loss ends the connection, which the listener sees;
// UDP syslog carries no sequence number, so a datagram lost on the way is invisible (PS7).
func (protocol) LossDetectable(transport string) bool { return transport == push.TransportTCP }


// Filter is a syslog subscription's header filter: one or more programs by exact name, optionally
// narrowed by facility and severity, named as RFC 5424 section 6.2.1 names them.
type Filter struct {
	Program  []string `json:"program"`
	Facility []string `json:"facility,omitempty"`
	Severity []string `json:"severity,omitempty"`
}

// facilityCodes are RFC 5424's facility codes under the names OPNsense's target form uses
// (survey, Push sources, S1), plus the RFC's own for the codes OPNsense does not list.
var facilityCodes = map[string]int64{
	"kern": 0, "user": 1, "mail": 2, "daemon": 3, "auth": 4, "syslog": 5, "lpr": 6, "news": 7,
	"uucp": 8, "cron": 9, "authpriv": 10, "ftp": 11, "ntp": 12, "security": 13, "console": 14,
	"clock": 15, "local0": 16, "local1": 17, "local2": 18, "local3": 19, "local4": 20, "local5": 21,
	"local6": 22, "local7": 23,
}

// severityCodes are RFC 5424's severities.
var severityCodes = map[string]int64{
	"emerg": 0, "alert": 1, "crit": 2, "err": 3, "warning": 4, "notice": 5, "info": 6, "debug": 7,
}

func (protocol) Compile(raw json.RawMessage) (push.Selector, error) {
	var filter Filter
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&filter); err != nil {
		return nil, fmt.Errorf("the syslog filter is not {program, facility, severity}: %w", err)
	}
	if len(filter.Program) == 0 {
		return nil, fmt.Errorf("the syslog filter names no program: a subscriber names the programs it reads, " +
			"so a line nobody subscribes to is dropped at its header")
	}
	selector := &selector{programs: map[string]bool{}}
	for _, program := range filter.Program {
		if strings.TrimSpace(program) == "" || strings.ContainsAny(program, " \t") {
			return nil, fmt.Errorf("the syslog program %q is not one word", program)
		}
		selector.programs[program] = true
	}
	for _, name := range filter.Facility {
		code, known := facilityCodes[name]
		if !known {
			return nil, fmt.Errorf("the syslog facility %q is not one of RFC 5424's", name)
		}
		if selector.facilities == nil {
			selector.facilities = map[int64]bool{}
		}
		selector.facilities[code] = true
	}
	for _, name := range filter.Severity {
		code, known := severityCodes[name]
		if !known {
			return nil, fmt.Errorf("the syslog severity %q is not one of RFC 5424's", name)
		}
		if selector.severities == nil {
			selector.severities = map[int64]bool{}
		}
		selector.severities[code] = true
	}
	return selector, nil
}

type selector struct {
	programs   map[string]bool
	facilities map[int64]bool
	severities map[int64]bool
}

func (s *selector) Keys() []string {
	keys := make([]string, 0, len(s.programs))
	for program := range s.programs {
		keys = append(keys, program)
	}
	sort.Strings(keys)
	return keys
}

func (s *selector) Admits(record *push.Record) (bool, string) {
	if !s.programs[record.RouteKey] {
		return false, ""
	}
	if s.facilities != nil && !s.facilities[record.Fields[FieldFacility].(int64)] {
		return false, ""
	}
	if s.severities != nil && !s.severities[record.Fields[FieldSeverity].(int64)] {
		return false, ""
	}
	return true, ""
}

// maxProgramsSeen bounds the programs the catalogue lists; any further one is counted under
// otherPrograms.
const maxProgramsSeen = 256

const otherPrograms = "(other programs)"

// decoder decodes one message per datagram or frame. It holds no state but the programs seen.
type decoder struct {
	mutex sync.Mutex
	seen  map[string]int64
}

func (d *decoder) Decode(payload []byte, meta push.Meta) push.Decoded {
	header, err := ParseHeader(payload)
	if err != nil {
		return push.Decoded{Problems: []push.Problem{{Reason: ReasonHeaderUnparseable, Count: 1,
			Detail: "a line that is not an RFC 5424 or RFC 3164 header was dropped"}}}
	}
	d.mutex.Lock()
	if _, known := d.seen[header.Program]; known || len(d.seen) < maxProgramsSeen {
		d.seen[header.Program]++
	} else {
		d.seen[otherPrograms]++
	}
	d.mutex.Unlock()
	record := push.Record{
		RouteKey: header.Program,
		Fields: map[string]any{
			FieldFacility: header.Facility, FieldSeverity: header.Severity,
			FieldHostname: emptyToNil(header.Hostname), FieldProgram: header.Program,
			FieldProcessID: emptyToNil(header.ProcessID), FieldMessageID: emptyToNil(header.MessageID),
			FieldStructuredData: emptyToNil(header.StructuredData), FieldMessage: header.Message,
		},
		Zoneless:     header.Zoneless,
		InstantField: FieldTimestamp,
		Text:         header.Message,
		Sender:       meta.Sender,
		Transport:    meta.Transport,
		ReceivedAt:   meta.ReceivedAt,
	}
	if !header.Instant.IsZero() {
		record.Instant = header.Instant
		record.Fields[FieldTimestamp] = header.Instant
	}
	return push.Decoded{Records: []push.Record{record}}
}

func (d *decoder) Expire(time.Time) push.Decoded { return push.Decoded{} }

func (d *decoder) State() push.DecoderState {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	seen := make(map[string]int64, len(d.seen))
	for program, count := range d.seen {
		seen[program] = count
	}
	return push.DecoderState{Seen: seen}
}

func emptyToNil(text string) any {
	if text == "" {
		return nil
	}
	return text
}
