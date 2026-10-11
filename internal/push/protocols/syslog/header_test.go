package syslog

import (
	"testing"
	"time"
)

// TestHeaderDecodesEveryForm is PS-AC3: RFC 3164 with and without a hostname, with and without a
// [pid], with an ISO timestamp; RFC 5424 with structured data, NILVALUEs, a BOM, fractional
// seconds and offsets.
func TestHeaderDecodesEveryForm(t *testing.T) {
	t.Parallel()
	at := func(text string) time.Time {
		parsed, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			t.Fatal(err)
		}
		return parsed.UTC()
	}
	cases := []struct {
		name string
		line string
		want Header
	}{
		{"3164 with host and pid", "<134>Sep 26 23:51:06 example-host filterlog[1000]: 12,,,a",
			Header{Facility: 16, Severity: 6, Zoneless: "Sep 26 23:51:06", Hostname: "example-host",
				Program: "filterlog", ProcessID: "1000", Message: "12,,,a"}},
		{"3164 without host", "<13>Oct  1 01:02:03 su: a message",
			Header{Facility: 1, Severity: 5, Zoneless: "Oct  1 01:02:03", Program: "su", Message: "a message"}},
		{"3164 without pid with host", "<13>Oct 11 01:02:03 example-host prog: text: with colons",
			Header{Facility: 1, Severity: 5, Zoneless: "Oct 11 01:02:03", Hostname: "example-host", Program: "prog",
				Message: "text: with colons"}},
		{"3164 without host with pid", "<13>Oct 11 01:02:03 prog[7]: hello",
			Header{Facility: 1, Severity: 5, Zoneless: "Oct 11 01:02:03", Program: "prog", ProcessID: "7",
				Message: "hello"}},
		{"3164 with an ISO timestamp", "<13>2026-09-26T23:51:06+02:00 example-host prog[7]: hello",
			Header{Facility: 1, Severity: 5, Instant: at("2026-09-26T21:51:06Z"), Hostname: "example-host",
				Program: "prog", ProcessID: "7", Message: "hello"}},
		{"3164 with a zoneless ISO timestamp", "<13>2026-09-26T23:51:06 example-host prog: hello",
			Header{Facility: 1, Severity: 5, Zoneless: "2026-09-26T23:51:06", Hostname: "example-host",
				Program: "prog", Message: "hello"}},
		{"5424 with structured data and fraction", `<165>1 2026-09-26T23:51:06.123456+02:00 example-host app 42 ID47 [exampleSDID@32473 iut="3" eventSource="App\"lication"][other@1 a="b\]c"] the message`,
			Header{Version: 1, Facility: 20, Severity: 5, Instant: at("2026-09-26T21:51:06.123456Z"),
				Hostname: "example-host", Program: "app", ProcessID: "42", MessageID: "ID47",
				StructuredData: `[exampleSDID@32473 iut="3" eventSource="App\"lication"][other@1 a="b\]c"]`,
				Message:        "the message"}},
		{"5424 with NILVALUEs and a BOM", "<34>1 2026-09-26T23:51:06Z - - - - - \xEF\xBB\xBFbom message",
			Header{Version: 1, Facility: 4, Severity: 2, Instant: at("2026-09-26T23:51:06Z"), Message: "bom message"}},
		{"5424 with no message", "<34>1 2026-09-26T23:51:06-05:30 h a p m -",
			Header{Version: 1, Facility: 4, Severity: 2, Instant: at("2026-09-27T05:21:06Z"), Hostname: "h",
				Program: "a", ProcessID: "p", MessageID: "m"}},
		{"5424 with a NIL timestamp", "<34>1 - h a - - - text",
			Header{Version: 1, Facility: 4, Severity: 2, Hostname: "h", Program: "a", Message: "text"}},
		{"5424 zoneless", "<34>1 2026-09-26T23:51:06 h a - - - text",
			Header{Version: 1, Facility: 4, Severity: 2, Zoneless: "2026-09-26T23:51:06", Hostname: "h", Program: "a",
				Message: "text"}},
	}
	for _, c := range cases {
		got, err := ParseHeader([]byte(c.line))
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !got.Instant.Equal(c.want.Instant) {
			t.Errorf("%s: instant %v, want %v", c.name, got.Instant, c.want.Instant)
		}
		got.Instant, c.want.Instant = time.Time{}, time.Time{}
		if got != c.want {
			t.Errorf("%s:\n got %+v\nwant %+v", c.name, got, c.want)
		}
	}
}

// TestMalformedLinesAreUnparseable is PS-AC3's last clause: a line that is not a header is refused.
func TestMalformedLinesAreUnparseable(t *testing.T) {
	t.Parallel()
	for _, line := range []string{"", "no priority", "<>x", "<192>Sep 26 23:51:06 h p: m", "<01>Sep 26 23:51:06 h p: m",
		"<13>not a timestamp at all", "<13>Sep 26 23:51:06 nocolon", "<34>1 2026-99-99T00:00:00Z h a - - - m",
		"<34>1 2026-09-26T23:51:06Z h a - - [unclosed m", "<13>Sep 26 23:51:06 h p[7: m"} {
		if _, err := ParseHeader([]byte(line)); err == nil {
			t.Errorf("%q parsed", line)
		}
	}
}

// TestUnparseableLinesAreCountedAndStoreNothing: the decoder reports header_unparseable and no record.
func TestUnparseableLinesAreCountedAndStoreNothing(t *testing.T) {
	t.Parallel()
	decoded := (&decoder{seen: map[string]int64{}}).Decode([]byte("garbage"), pushMeta())
	if len(decoded.Records) != 0 || len(decoded.Problems) != 1 || decoded.Problems[0].Reason != ReasonHeaderUnparseable {
		t.Errorf("decoded %+v", decoded)
	}
}
