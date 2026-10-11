package syslog

import (
	"errors"
	"io"
	"net/netip"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/NaejEL/opnview/internal/push"
)

// pushMeta is a datagram's metadata, from a documentation address.
func pushMeta() push.Meta {
	return push.Meta{Sender: netip.MustParseAddr("192.0.2.1"), Transport: push.TransportTCP,
		ReceivedAt: time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)}
}

// frames reads every frame of a stream.
func frames(t *testing.T, reader io.Reader, maxFrame int) ([]string, error) {
	t.Helper()
	framer := newFramer(reader, maxFrame)
	var got []string
	for {
		frame, err := framer.Next()
		if errors.Is(err, io.EOF) {
			return got, nil
		}
		if err != nil {
			return got, err
		}
		got = append(got, string(frame))
	}
}

// TestBothFramingsAreDecoded is PS-AC5: octet-counted and newline-framed frames, split across
// reads and several in one read, on one connection or each on its own.
func TestBothFramingsAreDecoded(t *testing.T) {
	t.Parallel()
	first, second := "<13>Oct 11 01:02:03 prog: one", "<13>Oct 11 01:02:04 prog: two words"
	streams := map[string]string{
		"newline":        first + "\n" + second + "\n",
		"newline crlf":   first + "\r\n" + second + "\r\n",
		"newline no end": first + "\n" + second,
		"octet counted":  "29 " + first + "35 " + second,
		"mixed":          "29 " + first + second + "\n",
	}
	for name, stream := range streams {
		for _, reader := range map[string]io.Reader{
			"whole":       strings.NewReader(stream),
			"byte a read": iotest.OneByteReader(strings.NewReader(stream)),
			"half a read": iotest.HalfReader(strings.NewReader(stream)),
		} {
			got, err := frames(t, reader, push.MaxFrame)
			if err != nil {
				t.Errorf("%s: %v", name, err)
				continue
			}
			if len(got) != 2 || got[0] != first || got[1] != second {
				t.Errorf("%s: frames %q", name, got)
			}
		}
	}
}

// TestAFrameAboveTheBoundIsRefused is PS-AC5: a frame above the bound ends the connection.
func TestAFrameAboveTheBoundIsRefused(t *testing.T) {
	t.Parallel()
	long := "<13>Oct 11 01:02:03 prog: " + strings.Repeat("x", 100)
	if _, err := frames(t, strings.NewReader(long+"\n"), 64); !errors.Is(err, push.ErrFrameTooLarge) {
		t.Errorf("a long newline frame: %v", err)
	}
	if _, err := frames(t, strings.NewReader("9999 "+long), 64); !errors.Is(err, push.ErrFrameTooLarge) {
		t.Errorf("a long counted frame: %v", err)
	}
	if _, err := frames(t, strings.NewReader("12345678901234 x"), 1<<30); !errors.Is(err, push.ErrFrameTooLarge) {
		t.Errorf("an absurd count: %v", err)
	}
	if _, err := frames(t, strings.NewReader("50 short"), push.MaxFrame); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("a counted frame cut short: %v", err)
	}
}

// FuzzHeader is the syslog header's fuzz target (PS15): no input panics, and a decoded header's
// facility and severity are in range.
func FuzzHeader(f *testing.F) {
	for _, seed := range []string{"<134>Sep 26 23:51:06 example-host filterlog[1000]: 12,,,a",
		`<165>1 2026-09-26T23:51:06.123456+02:00 h app 42 ID47 [a@1 b="c\]"] m`, "<34>1 - - - - - -", "<0>", "<",
		"<13>2026-09-26T23:51:06 h p: m", "<191>1 2026-09-26T23:51:06Z h a p m - \xEF\xBB\xBF\xff"} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, line []byte) {
		header, err := ParseHeader(line)
		if err != nil {
			return
		}
		if header.Facility < 0 || header.Facility > 23 || header.Severity < 0 || header.Severity > 7 {
			t.Errorf("facility %d severity %d", header.Facility, header.Severity)
		}
		decoded := (&decoder{seen: map[string]int64{}}).Decode(line, pushMeta())
		if len(decoded.Records) != 1 {
			t.Errorf("a header that parses decoded into %d records", len(decoded.Records))
		}
	})
}

// FuzzFraming is the TCP framing's fuzz target (PS15): no stream panics or returns a frame above
// the bound.
func FuzzFraming(f *testing.F) {
	for _, seed := range []string{"29 <13>Oct 11 01:02:03 prog: one", "<13>a\n<13>b\n", "9999 x", "\n\n\x00", "1 x2 yy"} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, stream []byte) {
		framer := newFramer(strings.NewReader(string(stream)), 256)
		for range 10000 {
			frame, err := framer.Next()
			if err != nil {
				return
			}
			if len(frame) > 256 {
				t.Fatalf("a frame of %d octets passed a bound of 256", len(frame))
			}
		}
	})
}
