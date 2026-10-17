package line

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/NaejEL/opnview/internal/push"
)

func init() { push.Register(protocol{}) }

// Name is the protocol's name in a push declaration.
const Name = "line"

type protocol struct{}

func (protocol) Name() string         { return Name }
func (protocol) Transports() []string { return []string{push.TransportUDP, push.TransportTCP} }
func (protocol) NewDecoder(push.Limits) push.Decoder {
	return &decoder{}
}
func (protocol) NewFramer(reader io.Reader, maxFrame int) push.Framer {
	return &framer{reader: bufio.NewReaderSize(reader, 64*1024), maxFrame: maxFrame}
}

func (protocol) Compile(filter json.RawMessage) (push.Selector, error) {
	return push.CompilePrefixFilter(Name, filter)
}

// Fields: a line carries no field the core reads; its decode function reads its text.
func (protocol) Fields() map[string]string       { return nil }
func (protocol) FieldType(string) (string, bool) { return "", false }
func (protocol) IndexFields() []string           { return nil }
func (protocol) ScriptDecoded() bool             { return true }

// LossDetectable: TCP is acknowledged and its loss ends the connection, which the listener sees; a line
// over UDP carries no sequence number the core reads, so a datagram lost on the way is invisible.
func (protocol) LossDetectable(transport string) bool { return transport == push.TransportTCP }

// framer reads one connection, a line at a time; the newline, and a carriage return before it, are not
// part of the line, and an empty line is no message.
type framer struct {
	reader   *bufio.Reader
	maxFrame int
}

func (f *framer) Next() ([]byte, error) {
	for {
		var line []byte
		for {
			chunk, err := f.reader.ReadSlice('\n')
			line = append(line, chunk...)
			if f.maxFrame > 0 && len(bytes.TrimRight(line, "\r\n")) > f.maxFrame {
				return nil, push.ErrFrameTooLarge
			}
			if err == nil {
				break
			}
			if errors.Is(err, bufio.ErrBufferFull) {
				continue
			}
			if errors.Is(err, io.EOF) && len(bytes.TrimSpace(line)) > 0 {
				// A last line with no newline is a line: the sender closed after writing it.
				return bytes.TrimRight(line, "\r\n"), nil
			}
			return nil, err
		}
		if trimmed := bytes.TrimRight(line, "\r\n"); len(trimmed) > 0 {
			return trimmed, nil
		}
	}
}

// decoder makes each line one record: a TCP frame is one line, a UDP datagram one or more.
type decoder struct {
	mutex sync.Mutex
	seen  int64
}

func (d *decoder) Decode(payload []byte, meta push.Meta) push.Decoded {
	var records []push.Record
	for index, line := range bytes.Split(payload, []byte("\n")) {
		line = bytes.TrimRight(line, "\r")
		if len(line) == 0 {
			continue
		}
		records = append(records, push.MessageRecord(line, payload, index, true, meta))
	}
	d.mutex.Lock()
	d.seen += int64(len(records))
	d.mutex.Unlock()
	return push.Decoded{Records: records}
}

func (d *decoder) Expire(time.Time) push.Decoded { return push.Decoded{} }

func (d *decoder) State() push.DecoderState {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	return push.DecoderState{Seen: map[string]int64{"lines": d.seen}}
}
