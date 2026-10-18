package core

import (
	"context"
	"strconv"
	"time"

	"github.com/NaejEL/opnview/internal/broker"
	"github.com/NaejEL/opnview/internal/broker/infer"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
)

// The collector's side of the broker (A12 and A18 of the broker amendment to
// specs/SPEC-backend-completion-plugins.md).
//
// EVERY READ GOES THROUGH THE BROKER. Discovery and the probe round are core readers whose max age
// is the discovery interval; a core kind's interval setting is its readers' max age; a kind of a
// plugin's own reads at its provider's cadence. The per-kind loops of scheduler.go stay the passes:
// what changed is that a pass's requests are served by the broker, from its mirror when an answer
// is young enough, and that nothing coalesced two requests before.

// Read asks the broker for one core reader's answer, read whole.
func (c *Collector) Read(ctx context.Context, reader string, endpoint opnsense.Endpoint,
	options opnsense.RequestOptions, maxAge time.Duration) (opnsense.Response, error) {
	return c.APIBroker.GateCall(ctx, broker.Core(reader).Name, endpoint, options, maxAge)
}

// intervals returns the intervals in force.
func (c *Collector) intervals() config.Config {
	c.Mutex.RLock()
	defer c.Mutex.RUnlock()
	return c.Cadences
}

// DiscoveryMaxAge is discovery's and the probe round's max age: the discovery interval.
func (c *Collector) DiscoveryMaxAge() time.Duration { return c.intervals().DiscoveryInterval }

// KindMaxAge is a core kind's readers' max age: its poll cadence, as its kind file declares it. The
// probe round reads at the discovery interval. A kind with no poll cadence of its own answers zero, and
// its caller supplies the cadence.
func (c *Collector) KindMaxAge(kind, function string) time.Duration {
	settings := c.intervals()
	if function == "probe" {
		return settings.DiscoveryInterval
	}
	return settings.Interval(kind, provider.CadencePoll)
}

// DeclareCoreEndpoints tells the broker what the core knows of the endpoints it reads itself: an
// answer keyed by interface devices is value-keyed from its root, so no device name ever becomes
// a column (A10). Each correction cites the survey section establishing the shape.
func (c *Collector) DeclareCoreEndpoints() {
	// get_interface_names answers {device: description}: survey, "Runtime discovery".
	c.APIBroker.Declare(broker.Core("discovery").Name, true, opnsense.InterfaceNames, broker.EndpointDeclaration{
		Corrections: infer.Corrections{ValueKeyed: []string{""}}})
}

// KnownNormaliser is the timestamp normaliser the broker's inference reads: the firewall's offset
// as far as it is known, measured by nothing. Measuring it is a firewall call, which the broker must
// never make from inside another one. Before the offset has been read the normaliser says so, and
// no instant is derived from the firewall's local time: the broker learns the shape and orders
// the rows, but mirrors nothing that holds such a time (N4, live validation of 17 October 2026).
// The records a pass stores are normalised by the pass, which measures the offset first.
func (c *Collector) KnownNormaliser(context.Context) (decode.Normaliser, error) {
	c.Mutex.RLock()
	offset, measured := c.OffsetSeconds, c.OffsetMeasured
	c.Mutex.RUnlock()
	return decode.Normaliser{Reference: c.Clock.Now(), OffsetSeconds: offset, OffsetUnknown: !measured}, nil
}

// The mirror's retention settings (A10 and A14 of the broker amendment): the mirror follows
// retention_seconds unless the first overrides it; a stream's detail is kept for the second, a day
// by default, and its minute and hour aggregates for the retention.
const (
	KeyMirrorRetentionSeconds             = "mirror_retention_seconds"
	KeyMirrorStreamDetailRetentionSeconds = "mirror_stream_detail_retention_seconds"
)

// PurgeMirror purges the broker's mirror and its stream recordings, to the exact second.
func (c *Collector) PurgeMirror(ctx context.Context, now int64) error {
	if c.APIBroker.Mirror() == nil {
		return nil
	}
	retention := c.SettingSeconds(ctx, config.KeyRetentionSeconds, config.DefaultRetentionSeconds)
	if override := c.SettingSeconds(ctx, KeyMirrorRetentionSeconds, 0); override > 0 {
		retention = override
	}
	if retention > 0 {
		if err := c.APIBroker.Mirror().Purge(ctx, now-retention); err != nil {
			return err
		}
	}
	detail := c.SettingSeconds(ctx, KeyMirrorStreamDetailRetentionSeconds, 0)
	return c.APIBroker.PurgeStreams(ctx, now, detail, retention)
}

// SettingSeconds reads a whole number of seconds from a setting, the fallback when it is absent
// or unreadable.
func (c *Collector) SettingSeconds(ctx context.Context, key string, fallback int64) int64 {
	text, present, err := c.Store.Setting(ctx, key)
	if err != nil || !present {
		return fallback
	}
	seconds, err := strconv.ParseInt(text, 10, 64)
	if err != nil || seconds < 0 {
		return fallback
	}
	return seconds
}
