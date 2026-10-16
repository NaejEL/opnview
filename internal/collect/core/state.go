package core

import (
	"strings"
	"sync"
	"time"

	"github.com/NaejEL/opnview/internal/collect/firewalllog"
	"github.com/NaejEL/opnview/internal/collect/ipgeolocation"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/collect/registrabledomain"
	"github.com/NaejEL/opnview/internal/live"
	"github.com/NaejEL/opnview/internal/store/netflow"
	"github.com/NaejEL/opnview/internal/store/push"
	"github.com/NaejEL/opnview/internal/store/sqldb"
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

// NetflowState is what the collector holds for the flow_record kind.
type NetflowState struct {
	Mutex sync.Mutex
	// ReadThrough is the filter log's read-through instant: every record the firewall logged before it
	// has been stored, or recorded as a gap. A flow record is decided only once it has passed the
	// record's start plus the tolerance (PS19 rule 8).
	ReadThrough int64
	// ToleranceSeconds is netflow_join_tolerance_seconds.
	ToleranceSeconds int64
	// Histories caches each provider's capture configurations and stream conditions.
	Histories map[int64]*CaptureHistory
	// Dirty is the connection window and legs whose measured volume must be computed again.
	Dirty     bool
	DirtyFrom int64
	DirtyTo   int64
	DirtyLegs map[int64]bool
	// NotActive counts the records of a provider that is not the active one, which are not stored.
	NotActive int64
	// OnCaptureChange is told when a provider's configuration changed, or a read followed a stream
	// condition: the receiver then follows the exporter's sequence numbers afresh.
	OnCaptureChange func(key string)
}

// CaptureHistory is one provider's capture configurations and stream conditions.
type CaptureHistory struct {
	Configurations []netflow.CaptureConfiguration
	Conditions     []netflow.FlowRecordCondition
}

// ConfigurationAt returns the configuration in force at an instant, or nil when it is unknown
// (store.CaptureConfigurationAt, which the widget API's measure reads too).
func (h *CaptureHistory) ConfigurationAt(at int64) *netflow.CaptureConfiguration {
	return netflow.CaptureConfigurationAt(h.Configurations, at)
}

// Suspended reports whether a stream condition's suspension holds an instant: from the condition
// until the next configuration read (store.CaptureSuspendedAt).
func (h *CaptureHistory) Suspended(at int64) bool {
	return netflow.CaptureSuspendedAt(h.Conditions, at)
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
	Held    []firewalllog.Record
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

// Services is what the collector last heard from each service kind's active provider.
type Services struct {
	Mutex       sync.RWMutex
	GeoKey      string
	Geo         ipgeolocation.Dataset
	ListKey     string
	List        registrabledomain.List
	GeoWake     chan struct{}
	GeoWakeOnce sync.Once
}

// triggers routes the notifications heard to the loops whose provider named them.
type triggers struct {
	mutex  sync.Mutex
	routes map[string][]chan struct{}
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
	for name := range t.routes {
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
