package plugin

import (
	"context"
	"net/netip"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/push"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// discardSink stores nothing; the availability tests read liveness, not rows.
type discardSink struct{}

func (discardSink) Prepare(_ context.Context, records []push.Record) push.Prepared {
	return push.Prepared{Rows: len(records)}
}
func (discardSink) Store(context.Context, push.Prepared) error { return nil }

// hubFor builds a hub holding the plugin's push entries as subscriptions, with a loopback listener of
// each entry's protocol and transports, and attaches it to the load.
func hubFor(t *testing.T, loaded *Loaded, plugin *Plugin) *push.Hub {
	t.Helper()
	hub := push.NewHub(push.Options{})
	for _, binding := range plugin.pushes {
		if err := hub.Subscribe(push.Subscription{ID: binding.ID(), Protocol: binding.protocol.Name(),
			Selector: binding.selector, Transports: binding.transports, Liveness: binding.liveness,
			Sink: discardSink{}}); err != nil {
			t.Fatal(err)
		}
		transports := binding.transports
		if len(transports) == 0 {
			transports = binding.protocol.Transports()
		}
		for _, transport := range transports {
			if err := hub.AddLoopbackListener(binding.protocol.Name(), transport); err != nil {
				t.Fatal(err)
			}
		}
	}
	loaded.AttachPushHub(hub)
	return hub
}

// TestASilentPushProviderNamesItsRefusedSenders is D7 of the second live validation: a listening push
// provider that received nothing names the refused senders in its availability detail, with their
// counts, so a network that rewrites source addresses is visible without logging in; once the operator
// accepts the rewritten address, a message from it makes the provider healthy.
func TestASilentPushProviderNamesItsRefusedSenders(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	loaded := loadManifest(t, pushManifest(), "")
	plugin, _ := loaded.Plugin("example_push")
	adapter := pushProviderAdapter{plugin: plugin, kind: "example_push_lines", key: plugin.pushes[0].providerKey}
	if result := adapter.result(); result.Outcome != vocab.OutcomeListenerNotConfigured {
		t.Fatalf("with no hub the outcome is %s", result.Outcome)
	}
	hub := hubFor(t, loaded, plugin)
	firewall, gateway := netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("198.51.100.1")
	hub.Senders().Set([]netip.Addr{firewall})

	silent := adapter.result()
	if silent.Outcome != vocab.OutcomeNoMessageReceived || strings.Contains(silent.Detail, "refused") {
		t.Errorf("silent with no refusal: %s, %q", silent.Outcome, silent.Detail)
	}

	line := []byte("<13>1 2026-10-17T14:27:21Z host example-program - - - word 3")
	for range 2 {
		if err := hub.Deliver(ctx, "syslog", push.TransportUDP, line, gateway); err != nil {
			t.Fatal(err)
		}
	}
	refused := adapter.result()
	if refused.Outcome != vocab.OutcomeNoMessageReceived || !strings.Contains(refused.Detail, "198.51.100.1 (2)") ||
		!strings.Contains(refused.Detail, "--push-extra-senders") {
		t.Errorf("silent with refusals: %s, %q", refused.Outcome, refused.Detail)
	}

	hub.Senders().SetExtra([]netip.Addr{gateway})
	if err := hub.Deliver(ctx, "syslog", push.TransportUDP, line, gateway); err != nil {
		t.Fatal(err)
	}
	if healthy := adapter.result(); healthy.Outcome != vocab.OutcomeHealthy {
		t.Errorf("after the extra sender was accepted: %s, %q", healthy.Outcome, healthy.Detail)
	}
}

// TestAFlowRecordProviderIsNotHealthyWhileItsPushEntryReceivesNothing is D1's availability half: the
// netflow provider's capture configuration is read by polling and its records arrive by push, so a
// healthy read is not a healthy provider while its push entry receives nothing. The result stays
// separable, so the probe round still activates the provider, and a failed read keeps its own outcome.
func TestAFlowRecordProviderIsNotHealthyWhileItsPushEntryReceivesNothing(t *testing.T) {
	t.Parallel()
	manifest, script := netflowManifest(t)
	loaded := loadManifest(t, manifest, script)
	plugin, _ := loaded.Plugin("example_push")
	binding := plugin.pushes[0]
	kind, key := binding.providerKind, binding.providerKey
	read := provider.Result{State: vocab.StateReachable, Outcome: vocab.OutcomeHealthy, Separable: true}

	unconfigured := plugin.withPushAvailability(kind, key, read)
	if unconfigured.Outcome != vocab.OutcomeListenerNotConfigured || !unconfigured.Separable {
		t.Errorf("with no listener: %+v", unconfigured)
	}
	failed := provider.Result{State: vocab.StateUnavailable, Outcome: vocab.OutcomeTransportFailure}
	if kept := plugin.withPushAvailability(kind, key, failed); kept.Outcome != vocab.OutcomeTransportFailure {
		t.Errorf("a failed read became %s", kept.Outcome)
	}
	if other := plugin.withPushAvailability(kind, "another_key", read); other.Outcome != vocab.OutcomeHealthy {
		t.Errorf("a provider no push entry feeds became %s", other.Outcome)
	}

	hubFor(t, loaded, plugin)
	silent := plugin.withPushAvailability(kind, key, read)
	if silent.Outcome != vocab.OutcomeNoMessageReceived || silent.State != vocab.StateUnavailable || !silent.Separable {
		t.Errorf("listening and silent: %+v", silent)
	}
}
