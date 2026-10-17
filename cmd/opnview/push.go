package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/NaejEL/opnview/internal/collect"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/push"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// Push sources: what the firewall sends to opnview, when the user configures it to (PS1-PS16 of the
// push amendment, docs/plugins.md "Push sources"). opnview writes nothing to the firewall: the
// exports are the user's to switch on, in OPNsense's own pages (README, "Push sources").

// pushListen is one --push-listen: a protocol, a transport and the address to listen on.
type pushListen struct {
	protocol, transport, address string
}

// pushListens is the repeatable --push-listen flag. IT HAS NO DEFAULT, for the reason --listen has
// none: a port is one installation's configuration. With none given, no push socket is opened, and
// every push declaration reports listener_not_configured.
type pushListens []pushListen

func (p *pushListens) String() string {
	parts := make([]string, 0, len(*p))
	for _, listen := range *p {
		parts = append(parts, listen.protocol+"/"+listen.transport+"="+listen.address)
	}
	return strings.Join(parts, ",")
}

// Set reads "<protocol>/<transport>=<host:port>", such as syslog/tcp=<host:port>; the port is the
// installation's, so none is written here.
func (p *pushListens) Set(value string) error {
	head, address, found := strings.Cut(value, "=")
	protocol, transport, slashed := strings.Cut(head, "/")
	if !found || !slashed || protocol == "" || transport == "" || address == "" {
		return fmt.Errorf("%q is not <protocol>/<transport>=<host:port>", value)
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		return fmt.Errorf("%q: %w", value, err)
	}
	*p = append(*p, pushListen{protocol: protocol, transport: transport, address: address})
	return nil
}

// sendersRefresh is how often the accepted senders are derived again: the base URL's host is
// resolved and the firewall's own addresses read from what discovery stored.
const sendersRefresh = time.Minute

// pushService is the push receiver of the process and what it is wired to.
type pushService struct {
	hub       *push.Hub
	database  *store.Store
	collector *collect.Collector
	providers map[string][2]string
	replacing map[string]bool
	events    chan livenessEvent
	report    func(error)

	mutex   sync.Mutex
	silence map[string]time.Time
}

// livenessEvent is one liveness change, handled off the decoding path.
type livenessEvent struct {
	subscription string
	alive        bool
	at           time.Time
}

// startPush builds the hub, subscribes every push declaration of the loaded plugins, opens the
// listeners asked for and starts it. It returns once every socket is bound; ctx stops it.
func startPush(ctx context.Context, listens pushListens, database *store.Store, collector *collect.Collector,
	plugins *plugin.Loaded, credentials *config.FirewallCredentials, report func(error)) (*pushService, error) {
	service := &pushService{database: database, collector: collector, providers: plugins.PushProviders(),
		replacing: map[string]bool{}, events: make(chan livenessEvent, 1024), report: report,
		silence: map[string]time.Time{}}
	for _, loaded := range plugins.Plugins() {
		for _, entry := range loaded.PushEntries() {
			if entry.Replaces != "" && entry.Kind != "" {
				service.replacing[loaded.Name+"."+entry.Name] = true
			}
		}
	}
	service.hub = push.NewHub(push.Options{
		Resolve: collector.ZonelessInstant,
		// A NetFlow record's interface indexes, mapped through the broker at discovery's max age (PS11).
		Interfaces: collector.InterfaceIndexes,
		Gaps:       service,
		History:    service,
		OnLiveness: func(subscription string, alive bool, at time.Time) {
			select {
			case service.events <- livenessEvent{subscription: subscription, alive: alive, at: at}:
			default:
				report(fmt.Errorf("push: a liveness change of %s was not handled: the queue is full", subscription))
			}
		},
		Report: report,
	})
	subscriptions, err := plugins.PushSubscriptions(plugin.PushEnvironment{Store: database, Session: collector,
		Kinds: collector, Report: report})
	if err != nil {
		return nil, err
	}
	for _, subscription := range subscriptions {
		if err := service.hub.Subscribe(subscription); err != nil {
			return nil, err
		}
	}
	// After every read of a sequence bound a push entry declares (its `sequence_bound`), the decoders of the
	// entry's protocol are given how many sequence counters the sender runs; when its configuration changed,
	// they also follow its sequence numbers afresh, its counters having been recreated (NJ7, as PA7 of the
	// plugin-API amendment restates it).
	bounds := plugins.SequenceBounds()
	collector.SetSequenceBoundObserver(func(kind, key, name string, changed bool, counters int) {
		for _, bound := range bounds {
			if bound.Kind != kind || bound.Key != key || bound.Value != name {
				continue
			}
			if changed {
				service.hub.ResetSequences(bound.Protocol)
			}
			service.hub.SetSequenceCounters(bound.Protocol, counters)
		}
	})
	plugins.AttachPushHub(service.hub)
	collector.Broker().SetPushCatalogue(service.hub.Catalogue)
	service.refreshSenders(ctx, credentials)
	service.hub.Start(ctx)
	for _, listen := range listens {
		if err := service.hub.Listen(ctx, listen.protocol, listen.transport, listen.address); err != nil {
			return nil, err
		}
	}
	go service.loop(ctx, credentials)
	return service, nil
}

// loop refreshes the accepted senders, handles liveness changes and extends the gaps of sources
// that went silent, until ctx ends.
func (s *pushService) loop(ctx context.Context, credentials *config.FirewallCredentials) {
	senders := time.NewTicker(sendersRefresh)
	defer senders.Stop()
	silence := time.NewTicker(push.HeartbeatInterval)
	defer silence.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-senders.C:
			s.refreshSenders(ctx, credentials)
		case event := <-s.events:
			s.liveness(ctx, event)
		case <-silence.C:
			s.extendSilences(ctx)
		}
	}
}

// refreshSenders derives the accepted senders: the configured base URL's host, resolved, and the
// firewall's own addresses as discovery read them. Nothing else is accepted.
func (s *pushService) refreshSenders(ctx context.Context, credentials *config.FirewallCredentials) {
	var addresses []netip.Addr
	if base := credentials.Credentials().BaseURL; base != "" {
		if parsed, err := url.Parse(base); err == nil && parsed.Hostname() != "" {
			if literal, err := netip.ParseAddr(parsed.Hostname()); err == nil {
				addresses = append(addresses, literal)
			} else if resolved, err := net.DefaultResolver.LookupNetIP(ctx, "ip", parsed.Hostname()); err == nil {
				addresses = append(addresses, resolved...)
			}
		}
	}
	held, err := s.database.CurrentFirewallAddresses(ctx)
	if err != nil {
		s.report(err)
	}
	addresses = append(addresses, held...)
	s.hub.Senders().Set(addresses)
}

// liveness tells the collector of a replacing entry's change (PS10), and turns a source's silence
// into a gap of its provider, so a dataset bucket outside the source's liveness is a gap and never a
// zero (PS13).
func (s *pushService) liveness(ctx context.Context, event livenessEvent) {
	provider, known := s.providers[event.subscription]
	if !known {
		return
	}
	if s.replacing[event.subscription] {
		if err := s.collector.PushLiveness(ctx, provider[0], provider[1], event.alive, event.at.Unix()); err != nil {
			s.report(err)
		}
	}
	s.mutex.Lock()
	from, silent := s.silence[event.subscription]
	if event.alive {
		delete(s.silence, event.subscription)
	} else if !silent {
		s.silence[event.subscription] = event.at
	}
	s.mutex.Unlock()
	switch {
	case event.alive && silent:
		s.recordSilence(ctx, event.subscription, from, event.at)
	case !event.alive && !silent:
		s.recordSilence(ctx, event.subscription, event.at, time.Now())
	}
}

// extendSilences lengthens the gap of every source still silent.
func (s *pushService) extendSilences(ctx context.Context) {
	s.mutex.Lock()
	open := make(map[string]time.Time, len(s.silence))
	for subscription, from := range s.silence {
		open[subscription] = from
	}
	s.mutex.Unlock()
	for subscription, from := range open {
		s.recordSilence(ctx, subscription, from, time.Now())
	}
}

func (s *pushService) recordSilence(ctx context.Context, subscription string, from, to time.Time) {
	if err := s.RecordPushGap(ctx, subscription, string(vocab.GapPushNotAlive), from, to, 0,
		"the push source sent nothing for longer than its liveness span"); err != nil {
		s.report(err)
	}
}

// RecordPushGap records a gap of a push entry under the provider it belongs to.
func (s *pushService) RecordPushGap(ctx context.Context, subscription, reason string, from, to time.Time, count int64,
	detail string) error {
	provider, known := s.providers[subscription]
	if !known {
		return fmt.Errorf("push: the subscription %s belongs to no provider", subscription)
	}
	providerID, err := s.database.ProviderID(ctx, provider[0], provider[1])
	if err != nil {
		return err
	}
	if count > 0 {
		detail = fmt.Sprintf("%s (%d)", detail, count)
	}
	return s.database.RecordPushGap(ctx, providerID, vocab.GapReason(reason), from.Unix(), to.Unix(), detail,
		time.Now().Unix())
}

// ListenerAliveAt and RecordListenerAlive keep when each listener was last heard alive.
func (s *pushService) ListenerAliveAt(ctx context.Context, protocol, transport string) (time.Time, bool, error) {
	at, found, err := s.database.ListenerAliveAt(ctx, protocol, transport)
	if err != nil || !found {
		return time.Time{}, found, err
	}
	return time.Unix(at, 0).UTC(), true, nil
}

func (s *pushService) RecordListenerAlive(ctx context.Context, protocol, transport string, at time.Time) error {
	return s.database.RecordListenerAlive(ctx, protocol, transport, at.Unix())
}

// errNoPushHub is a push service asked for before it was started.
var errNoPushHub = errors.New("push: the receiver is not started")

// Wait returns once the hub has stopped and flushed every queue.
func (s *pushService) Wait() error {
	if s == nil || s.hub == nil {
		return errNoPushHub
	}
	s.hub.Wait()
	return nil
}
