package push

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// What the protocols whose messages a script decodes share (ScriptDecoded): their filter and their
// records. Neither knows anything of a sender's message format; the subscribing plugin's decode function
// does.

// PrefixFilter is the filter of a script-decoded protocol's subscription: every message, or only those
// whose octets begin with Prefix, so two plugins listening on one port each take their own.
type PrefixFilter struct {
	Prefix string `json:"prefix,omitempty"`
}

// CompilePrefixFilter reads a PrefixFilter strictly.
func CompilePrefixFilter(protocol string, raw json.RawMessage) (Selector, error) {
	var filter PrefixFilter
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&filter); err != nil {
		return nil, fmt.Errorf("the %s filter is not {prefix}: %w", protocol, err)
	}
	return prefixSelector{prefix: []byte(filter.Prefix)}, nil
}

// prefixSelector admits a record whose payload begins with its prefix. It is asked of every record: a
// script-decoded protocol has no route key.
type prefixSelector struct{ prefix []byte }

func (prefixSelector) Keys() []string { return nil }

func (s prefixSelector) Admits(record *Record) (bool, string) {
	payload, _ := record.Fields[PayloadField].([]byte)
	return bytes.HasPrefix(payload, s.prefix), ""
}

// MessageRecord is the record of one message of a script-decoded protocol: its octets under PayloadField,
// its text when the protocol's messages are text, its delivery's digest -- the octets of the datagram or
// frame it came in -- and its place in that delivery. Its instant is its reception's: the message carries none
// the core reads, and a decode function returning a row with its own instant overrides it.
func MessageRecord(message, delivery []byte, position int, text bool, meta Meta) Record {
	digest := sha256.Sum256(delivery)
	record := Record{
		Fields:     map[string]any{PayloadField: append([]byte(nil), message...)},
		Sender:     meta.Sender,
		Transport:  meta.Transport,
		Instant:    meta.ReceivedAt,
		ReceivedAt: meta.ReceivedAt,
		Delivery:   hex.EncodeToString(digest[:]),
		Position:   position,
	}
	if text {
		record.Text = string(message)
	}
	return record
}
