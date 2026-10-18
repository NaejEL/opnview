package core

import (
	"strings"
	"sync"
	"time"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/live"
	"github.com/NaejEL/opnview/internal/store/push"
	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// LiveSampling is the collector's live state.
type LiveSampling struct {
	Mutex    sync.Mutex
	Interest *live.Interest
	// Last is when each provider's live pass last began, by provider id.
	Last map[int64]time.Time
	// Passes counts the live passes run, for a test.
	Passes int
}

// OrderingHooks let a test stop a pass between its store and its derivation, see a
// purge wait for it, and turn the ordering off to show what the refresh does alone.
// Every field is nil or false outside a test, and nothing else sets them.
type OrderingHooks struct {
	// AfterStore runs in a filter-log pass once its rows are stored, before its
	// derivation.
	AfterStore func()
	// PurgeWaits runs when a purge finds a pass or a derivation in progress and is
	// about to wait for it.
	PurgeWaits func()
	// Unordered makes Purge run the store's purge alone, with no lock and no
	// placement before it.
	Unordered bool
}

// ProbedSource is one implementation as a probe round found it: its registry row and what
// its probe answered.
type ProbedSource struct {
	providerKey string
	providerID  int64
	result      provider.Result
}

// The modes of a provider's paths.
const (
	ModePull    = "pull"
	ModeOverlap = "overlap"
	ModePush    = "push"
)

// PushPaths is the state of every provider fed by two paths.
type PushPaths struct {
	mutex     sync.Mutex
	providers map[string]*PushPath
}

// PushPath is one provider's.
type PushPath struct {
	Mutex   sync.Mutex
	Loaded  bool
	Mode    string
	Periods []push.SourcePeriod
	Held    []vocab.Flow
	// LastPushed is the newest instant the push path delivered in its current period.
	LastPushed int64
	Superseded int64
	Dropped    int64
}

func pathKey(kind, key string) string { return kind + "/" + key }

func (p *PushPaths) Of(kind, key string) *PushPath {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.providers == nil {
		p.providers = map[string]*PushPath{}
	}
	path, found := p.providers[pathKey(kind, key)]
	if !found {
		path = &PushPath{Mode: ModePull}
		p.providers[pathKey(kind, key)] = path
	}
	return path
}

// Known reports whether a provider has ever been fed by two paths in this run or before.
func (p *PushPaths) Known(kind, key string) (*PushPath, bool) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	path, found := p.providers[pathKey(kind, key)]
	return path, found
}

// PathAt is the path whose period holds an instant; pull when no period exists.
func (p *PushPath) PathAt(at int64) string {
	for index := len(p.Periods) - 1; index >= 0; index-- {
		period := p.Periods[index]
		if at >= period.FromAt && (period.ToAt == nil || at <= *period.ToAt) {
			return period.Path
		}
	}
	return push.PathPull
}

// Services is what the collector last heard from each service kind's active provider: the checked
// answers of its last status or refresh (dataset_answer, list_answer), read with provider.Flag and its
// siblings.
type Services struct {
	Mutex       sync.RWMutex
	GeoKey      string
	Geo         provider.Answer
	ListKey     string
	List        provider.Answer
	GeoWake     chan struct{}
	GeoWakeOnce sync.Once
}

// triggers routes the notifications heard to the loops whose provider named them.
type triggers struct {
	mutex  sync.Mutex
	routes map[string][]chan struct{}
	// observers are told the notifications they named, payload and all: a derivation learns what changed
	// (PA4 of the plugin-API amendment).
	observers map[string][]func(sqldb.Notification)
}

// Observe calls observe with every notification heard under one of the names, before any loop is woken.
func (t *triggers) Observe(names []string, observe func(sqldb.Notification)) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if t.observers == nil {
		t.observers = map[string][]func(sqldb.Notification){}
	}
	for _, name := range names {
		t.observers[name] = append(t.observers[name], observe)
	}
}

// WakeOn returns the wake channel of a loop that the notifications named run, or nil when it names
// none. A name is "channel" or "channel:subject", the subject being the kind of a collection pass
// or the plugin of a plugin pass.
func (t *triggers) WakeOn(names []string) <-chan struct{} {
	if len(names) == 0 {
		return nil
	}
	wake := make(chan struct{}, 1)
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if t.routes == nil {
		t.routes = map[string][]chan struct{}{}
	}
	for _, name := range names {
		t.routes[name] = append(t.routes[name], wake)
	}
	return wake
}

// Heard wakes every loop a notification names, without blocking: a loop already woken runs once.
func (t *triggers) Heard(notification sqldb.Notification) {
	subject := ""
	switch notification.Channel {
	case sqldb.ChannelCollectionPass:
		subject, _ = notification.Payload["kind"].(string)
	case sqldb.ChannelPluginPass:
		subject, _ = notification.Payload["plugin"].(string)
	}
	t.mutex.Lock()
	defer t.mutex.Unlock()
	for _, name := range []string{notification.Channel, notification.Channel + ":" + subject} {
		for _, observe := range t.observers[name] {
			observe(notification)
		}
		for _, wake := range t.routes[name] {
			select {
			case wake <- struct{}{}:
			default:
			}
		}
	}
}

// Channels returns the channels any loop listens for.
func (t *triggers) Channels() []string {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	var channels []string
	names := make([]string, 0, len(t.routes)+len(t.observers))
	for name := range t.routes {
		names = append(names, name)
	}
	for name := range t.observers {
		names = append(names, name)
	}
	for _, name := range names {
		channel, _, _ := strings.Cut(name, ":")
		found := false
		for _, held := range channels {
			found = found || held == channel
		}
		if !found {
			channels = append(channels, channel)
		}
	}
	return channels
}
