package plugin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/NaejEL/opnview/internal/broker"
	"github.com/NaejEL/opnview/internal/broker/mirror"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store/storetest"
)

// stoppedClock is a clock the test moves by hand; its timers never fire.
type stoppedClock struct {
	mutex sync.Mutex
	now   time.Time
}

func (c *stoppedClock) Now() time.Time {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.now
}

func (c *stoppedClock) After(time.Duration) <-chan time.Time { return make(chan time.Time) }

func (c *stoppedClock) advance(by time.Duration) {
	c.mutex.Lock()
	c.now = c.now.Add(by)
	c.mutex.Unlock()
}

// builtinPlugin is one built-in plugin's directory, as the loader reads a tree of built-ins.
func builtinPlugin(t *testing.T, name string) fstest.MapFS {
	t.Helper()
	tree := fstest.MapFS{}
	directory := os.DirFS(path.Join("..", "..", "plugins", name))
	err := fs.WalkDir(directory, ".", func(file string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		raw, err := fs.ReadFile(directory, file)
		if err != nil {
			return err
		}
		tree[path.Join(name, file)] = &fstest.MapFile{Data: raw}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

// TestPfStatesReadsTheStateTableOnceACadence is D3 of the live validation of 17 October 2026: the
// pf_states pass asked the broker for a five-second max age, so a pass run again within its 60-second
// cadence walked the whole state table again -- and the broker kept the table that fresh between
// passes. A pass now states no max age and is held to its cadence: a pass within the cadence shares
// the last read, and one a cadence later reads the table again.
func TestPfStatesReadsTheStateTableOnceACadence(t *testing.T) {
	t.Parallel()
	calls := &atomic.Int64{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"rows": [{"id": "00000000000000a1/00000001", "iface": "exdev0", "proto": "tcp",
			"direction": "in", "dst_port": "443", "age": "00:06:00", "bytes": [100, 200], "pkts": [1, 1]}],
			"rowCount": 1, "total": 1, "current": 1}`))
	}))
	t.Cleanup(server.Close)
	generated := func() string {
		raw := make([]byte, 12)
		_, _ = rand.Read(raw)
		return hex.EncodeToString(raw)
	}
	client := opnsense.NewClient(opnsense.Credentials{BaseURL: server.URL, APIKey: generated(),
		APISecret: generated()}, http.DefaultTransport, 5*time.Second)
	clock := &stoppedClock{now: time.Unix(1790000400, 0)}
	now := func() int64 { return clock.Now().Unix() }
	gate := broker.New(client, mirror.New(storetest.Open(t), now), broker.Options{Clock: clock})
	t.Cleanup(gate.Close)
	session := provider.NewHost(gate, func(context.Context) (decode.Normaliser, error) {
		return decode.Normaliser{Reference: clock.Now()}, nil
	}, now)

	loaded, err := Load(context.Background(), Options{Builtins: builtinPlugin(t, "pf_states")})
	if err != nil {
		t.Fatal(err)
	}
	if refusals := loaded.Refusals(); len(refusals) != 0 {
		t.Fatalf("refused: %+v", refusals)
	}
	plugin, _ := loaded.Plugin("pf_states")
	cadence := plugin.script.Cadences["pf_state_rate"]
	if cadence != time.Minute {
		t.Fatalf("the provider's cadence is %v", cadence)
	}
	pass := func() {
		t.Helper()
		if _, _, failed := plugin.script.Call(context.Background(), session, "pf_state_rate", "collect",
			map[string]any{}); failed != nil {
			t.Fatalf("the pass failed: %+v", failed)
		}
	}
	pass()
	clock.advance(10 * time.Second)
	pass()
	if got := calls.Load(); got != 1 {
		t.Errorf("a pass ten seconds after the last read the state table again: %d calls", got)
	}
	clock.advance(cadence)
	pass()
	if got := calls.Load(); got != 2 {
		t.Errorf("a pass a cadence after the last read made %d calls in all, want 2", got)
	}
}
