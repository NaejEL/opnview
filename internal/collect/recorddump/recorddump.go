package recorddump

import "github.com/NaejEL/opnview/internal/decode"

// Record is one record of a resolver's records as its endpoint returned it, before the kind
// normalises it: the owner name, the TTL text, the record type and the value, under whatever
// field names the endpoint uses for them. The endpoint's CLASS is not carried: it is IN on every
// record both surveyed endpoints return, and it must never be read as the type.
//
// TTLShape keeps what TTL's text alone cannot: whether the endpoint wrote the ttl as a string, as
// JSON null, as something else, or not at all. A row an implementation knows is not a record at
// all never becomes a Record: it counts it under NotRecords.
type Record struct {
	OwnerName string
	TTL       string
	TTLShape  decode.Shape
	RRType    string
	Value     string
}

// Dump is what one read of a resolver's records returned.
type Dump struct {
	// Records are every record the endpoint returned, of every type: the kind counts the
	// types it does not store rather than the implementation dropping them unseen.
	Records []Record
	// ResponseBytes is the size of the answer, which the availability detail reports:
	// the dump's cost on the firewall grows with the size of what it dumps.
	ResponseBytes int
	// NotRecords are the rows the endpoint returned that its implementation recognised as
	// not being records at all, counted per kind under the implementation's own wording.
	// What makes a row one is a fact about that product's output, so it is decided in the
	// product's directory; the kind only reports the counts, in the order given.
	NotRecords []NotRecordCount
}

// NotRecordCount is a number of rows that are not records, and what they are, worded so
// that "skipped <Count> <Wording>" reads as a sentence of the availability detail.
type NotRecordCount struct {
	Count   int
	Wording string
}
