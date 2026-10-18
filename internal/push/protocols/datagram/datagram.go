package datagram

import (
	"encoding/json"
	"io"
	"sync"
	"time"

	"github.com/NaejEL/opnview/internal/push"
)

func init() { push.Register(protocol{}) }

// Name is the protocol's name in a push declaration.
const Name = "datagram"

type protocol struct{}

func (protocol) Name() string         { return Name }
func (protocol) Transports() []string { return []string{push.TransportUDP} }
func (protocol) NewDecoder(push.Limits) push.Decoder {
	return &decoder{}
}

// NewFramer: a datagram is never received over a stream.
func (protocol) NewFramer(io.Reader, int) push.Framer { return nil }

func (protocol) Compile(filter json.RawMessage) (push.Selector, error) {
	return push.CompilePrefixFilter(Name, filter)
}

// Fields: a datagram carries no field the core reads; its decode function reads its octets.
func (protocol) Fields() map[string]string       { return nil }
func (protocol) FieldType(string) (string, bool) { return "", false }
func (protocol) ScriptDecoded() bool             { return true }

// LossDetectable: a datagram carries no sequence number the core reads, so one lost on the way is
// invisible.
func (protocol) LossDetectable(string) bool { return false }

// decoder makes each datagram one record. It holds no state but the count of datagrams seen.
type decoder struct {
	mutex sync.Mutex
	seen  int64
}

func (d *decoder) Decode(payload []byte, meta push.Meta) push.Decoded {
	d.mutex.Lock()
	d.seen++
	d.mutex.Unlock()
	return push.Decoded{Records: []push.Record{push.MessageRecord(payload, payload, 0, false, meta)}}
}

func (d *decoder) Expire(time.Time) push.Decoded { return push.Decoded{} }

func (d *decoder) State() push.DecoderState {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	return push.DecoderState{Seen: map[string]int64{"datagrams": d.seen}}
}
