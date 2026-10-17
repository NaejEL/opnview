package line

import (
	"errors"
	"io"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/push"
)

// TestALineIsOneMessage: a TCP connection is framed at each newline, a carriage return before it and an empty
// line dropped, a last line with no newline kept, and a line above the bound refused; a UDP datagram of
// several lines is several records, each carrying its text and its octets.
func TestALineIsOneMessage(t *testing.T) {
	framer := protocol{}.NewFramer(strings.NewReader("first\r\n\nsecond\nlast"), 64)
	var frames []string
	for {
		frame, err := framer.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		frames = append(frames, string(frame))
	}
	if strings.Join(frames, "|") != "first|second|last" {
		t.Errorf("the frames are %q", frames)
	}
	if _, err := (protocol{}).NewFramer(strings.NewReader(strings.Repeat("x", 65)+"\n"), 64).Next(); !errors.Is(err,
		push.ErrFrameTooLarge) {
		t.Errorf("a line above the bound answered %v", err)
	}
	decoded := (protocol{}).NewDecoder(push.Limits{}).Decode([]byte("one\r\ntwo\n"), push.Meta{
		Sender: netip.MustParseAddr("192.0.2.1"), Transport: push.TransportUDP, ReceivedAt: time.Unix(1, 0)})
	if len(decoded.Records) != 2 || decoded.Records[0].Text != "one" || decoded.Records[1].Text != "two" ||
		string(decoded.Records[1].Fields[push.PayloadField].([]byte)) != "two" || decoded.Records[1].Instant.Unix() != 1 {
		t.Errorf("the records are %+v", decoded.Records)
	}
	if (protocol{}).LossDetectable(push.TransportUDP) || !(protocol{}).LossDetectable(push.TransportTCP) {
		t.Error("a line's loss is detectable over TCP alone")
	}
	selector, err := protocol{}.Compile([]byte(`{"prefix": "two"}`))
	if err != nil {
		t.Fatal(err)
	}
	if admits, _ := selector.Admits(&decoded.Records[0]); admits {
		t.Error("the prefix filter admitted a line it does not begin")
	}
	if admits, _ := selector.Admits(&decoded.Records[1]); !admits {
		t.Error("the prefix filter refused a line it begins")
	}
	if _, err := (protocol{}).Compile([]byte(`{"program": ["x"]}`)); err == nil {
		t.Error("a filter key the protocol does not have was accepted")
	}
}
