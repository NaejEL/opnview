package traffic

import (
	"strconv"

	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/request"
)

// The flows of a window by their columns: the parts every composition by protocol, service port or
// blocking reason is summed from, the recent part of a composition, and the destination levels of a
// connection tree. Each reads the flows through classified_flow, whichever source wrote them, so a
// source pushing flows feeds them as the filter log does (flows.statements.sql).

// The decisions of a logged record (flows.statements.sql): allowed, the blocked_decision view's
// engine kinds of a refused record, and unknown for an action nobody read.
const (
	DecisionAllowed                   = "allowed"
	DecisionFirewallRule              = "firewall_rule"
	DecisionFirewallNoRule            = "firewall_no_rule"
	DecisionFirewallReasonNotRecorded = "firewall_reason_not_recorded"
	DecisionUnknown                   = "unknown"
)

// The key of a service no port was recorded for: a protocol carrying no port, or a record whose port
// was not logged.
const ServicePortNotRecorded = "port:not_recorded"

// ServiceKey is the key of a service: its port and its protocol, as IANA identifies a service by
// both, so 53/udp and 53/tcp are two services. A record with no port is ServicePortNotRecorded; one
// whose protocol is not recorded carries "not_recorded" in its place.
func ServiceKey(protocol *string, port *int64) string {
	if port == nil {
		return ServicePortNotRecorded
	}
	name := "not_recorded"
	if protocol != nil && *protocol != "" {
		name = *protocol
	}
	return "port:" + strconv.FormatInt(*port, 10) + "/" + name
}

// FlowFilter narrows the flows a part is read from: a traffic scope ("all" for every one), and
// inside interfaces and clients, nil for no restriction.
type FlowFilter struct {
	Scope      string
	Interfaces []int64
	Clients    []int64
}

func (f FlowFilter) parameters(from, to int64) map[string]any {
	scope := f.Scope
	if scope == "" {
		scope = "all"
	}
	return map[string]any{"from": from, "to": to, "scope": scope, "interface_ids": request.JsonIDs(f.Interfaces),
		"client_ids": request.JsonIDs(f.Clients)}
}

// flowPart is the window's flows of one direction, protocol, port and decision.
type flowPart struct {
	TrafficDirection string  `json:"traffic_direction"`
	Protocol         *string `json:"protocol"`
	Port             *int64  `json:"port"`
	Decision         string  `json:"decision"`
	SummableFigures
}

// scanFigures reads the seven summable columns that close a part's row.
func scanFigures(figures *SummableFigures) []any {
	return []any{&figures.Bytes, &figures.AllowedBytes, &figures.BlockedBytes, &figures.UnknownBytes,
		&figures.AllowedConnections, &figures.BlockedConnections, &figures.UnknownConnections}
}

// FlowParts reads the window's flows per direction, protocol, destination port and decision.
func FlowParts(r *request.Request, filter FlowFilter) ([]flowPart, error) {
	parts := []flowPart{}
	err := r.Db.WidgetRows(r.Ctx, r.InMeasure("widget_flow_parts"), vocab.Period{},
		r.WithMeasure(filter.parameters(r.Window.From, r.Window.To)),
		func(row reads.RowScanner) error {
			var (
				part     flowPart
				protocol reads.NullString
				port     reads.NullInt64
				bytes    reads.NullInt64
				measured reads.VolumeFigures
			)
			targets := []any{&part.TrafficDirection, &protocol, &port, &part.Decision, &bytes}
			targets = append(targets, scanFigures(&part.SummableFigures)[1:]...)
			if measure := r.MeasureRead(); measure != "" {
				targets = append(targets, measured.MeasureTarget(measure))
			}
			if err := row.Scan(targets...); err != nil {
				return err
			}
			part.Protocol, part.Port, part.Bytes = request.NullableText(protocol), request.NullableNumber(port), bytes.Int64
			part.MeasureMembers = Summable(measured).MeasureMembers
			parts = append(parts, part)
			return nil
		})
	return parts, err
}

// recentPart is one bucket's flows of one direction, protocol, port, decision, inside interface and
// client.
type recentPart struct {
	BucketStart      int64   `json:"bucket_start"`
	TrafficDirection string  `json:"traffic_direction"`
	Protocol         *string `json:"protocol"`
	Port             *int64  `json:"port"`
	Decision         string  `json:"decision"`
	InterfaceID      *int64  `json:"interface_id"`
	ClientID         *int64  `json:"client_id"`
	SummableFigures
}

// recentBucket is one bucket of the recent window.
type recentBucket struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
	// Gap says the bucket is not complete: after complete_until, before the earliest flow kept, or
	// meeting a collection gap of the filter log. A gap bucket is never read as a zero.
	Gap bool `json:"gap"`
}

// recentFlows is the trailing window of logged flows, bucketed.
type recentFlows struct {
	From          int64          `json:"from"`
	To            int64          `json:"to"`
	BucketSeconds int64          `json:"bucket_seconds"`
	CompleteUntil *int64         `json:"complete_until"`
	Buckets       []recentBucket `json:"buckets"`
	Parts         []recentPart   `json:"parts"`
}

// RecentBucketsPerWindow is how many buckets a recent window is cut into, at most: the width is
// the window divided by it, rounded up to a whole second.
const RecentBucketsPerWindow = 60

// FirewallLogKind is the provider kind whose passes date the logged flows.
const FirewallLogKind = "firewall_log"

// CompleteUntil is the instant up to which a provider kind's records are complete: when the last
// pass of its active provider ended healthy, never later than now; null when no active provider has
// ended a pass healthy, so nothing can be said complete.
func CompleteUntil(r *request.Request, kind string) *int64 {
	var until *int64
	for _, provider := range r.Response.Availability {
		if provider.Kind != kind || !provider.Active || provider.Outcome == nil || provider.CheckedAt == nil {
			continue
		}
		if *provider.Outcome != string(vocab.OutcomeHealthy) && *provider.Outcome != string(vocab.OutcomeHealthyEmpty) {
			continue
		}
		at := min(*provider.CheckedAt, r.Now)
		if until == nil || at > *until {
			until = &at
		}
	}
	return until
}

// RecentFlows reads the logged flows of the trailing seconds before now, in buckets. Every bucket
// after the filter log's complete_until, before the earliest flow kept or meeting one of its
// collection gaps is a gap.
func RecentFlows(r *request.Request, seconds int64, filter FlowFilter) (recentFlows, error) {
	if seconds < 1 {
		seconds = 1
	}
	width := (seconds + RecentBucketsPerWindow - 1) / RecentBucketsPerWindow
	answer := recentFlows{From: r.Now - seconds, To: r.Now, BucketSeconds: width,
		CompleteUntil: CompleteUntil(r, FirewallLogKind), Buckets: []recentBucket{}, Parts: []recentPart{}}
	window := envelope.Interval{From: answer.From, To: answer.To}
	parameters := filter.parameters(window.From, window.To)
	parameters["width"] = width
	err := r.Db.WidgetRows(r.Ctx, r.InMeasure("widget_flow_recent_parts"), vocab.Period{}, r.WithMeasure(parameters),
		func(row reads.RowScanner) error {
			var (
				part                     recentPart
				protocol                 reads.NullString
				port, iface, client, sum reads.NullInt64
				measured                 reads.VolumeFigures
			)
			targets := []any{&part.BucketStart, &part.TrafficDirection, &protocol, &port, &part.Decision, &iface,
				&client, &sum}
			targets = append(targets, scanFigures(&part.SummableFigures)[1:]...)
			if measure := r.MeasureRead(); measure != "" {
				targets = append(targets, measured.MeasureTarget(measure))
			}
			if err := row.Scan(targets...); err != nil {
				return err
			}
			part.Protocol, part.Port, part.Bytes = request.NullableText(protocol), request.NullableNumber(port), sum.Int64
			part.InterfaceID, part.ClientID = request.NullableNumber(iface), request.NullableNumber(client)
			part.MeasureMembers = Summable(measured).MeasureMembers
			answer.Parts = append(answer.Parts, part)
			return nil
		})
	if err != nil {
		return answer, err
	}
	earliest, has, err := r.Earliest("widget_earliest_flow")
	if err != nil {
		return answer, err
	}
	var covered *envelope.Interval
	if has && answer.CompleteUntil != nil {
		interval := envelope.Interval{From: max(window.From, earliest), To: min(window.To, *answer.CompleteUntil)}
		if interval.To > interval.From {
			covered = &interval
		}
	}
	gaps, err := r.ReadGaps([]string{FirewallLogKind}, window)
	if err != nil {
		return answer, err
	}
	for _, bucket := range BucketIntervals(window, width) {
		answer.Buckets = append(answer.Buckets, recentBucket{From: bucket.From, To: bucket.To,
			Gap: IsGapBucket(bucket, covered, gaps)})
	}
	return answer, nil
}

// destinationRow is the window's connections of one inside end, direction, outside destination,
// site name, port and protocol.
type destinationRow struct {
	InterfaceID      *int64  `json:"interface_id"`
	ClientID         *int64  `json:"client_id"`
	OwnerID          *int64  `json:"owner_id"`
	TrafficDirection string  `json:"traffic_direction"`
	PeerAddress      *string `json:"peer_address"`
	LookupState      *string `json:"lookup_state"`
	ASN              *int64  `json:"asn"`
	SiteName         *string `json:"site_name"`
	Port             *int64  `json:"port"`
	Protocol         *string `json:"protocol"`
	Connections      int64   `json:"connections"`
	Bytes            int64   `json:"bytes"`
	Blocked          int64   `json:"blocked"`
	BlockedBytes     int64   `json:"blocked_bytes"`
	Newest           int64   `json:"newest"`
	// The members a row read in a declared measure carries beyond its bytes (host.figures): a widget hands
	// the row to host.figures.cover for the core to judge the connections it counts.
	MeasureMembers
}

// DestinationRows reads the window's connections by destination. Under aggregate_mode = no_domains
// the core reads no site name at all, so every destination is in address form and the answer is
// the one the database would give with no attribution stored. The outside peers of its rows are
// placed (request.Place), so their geolocations are read together at the first a script asks for.
func DestinationRows(r *request.Request) ([]destinationRow, error) {
	name := "widget_flow_destinations"
	if r.NoDomains() {
		name = "widget_flow_destinations_no_domains"
	}
	name = r.InMeasure(name)
	rows := []destinationRow{}
	err := r.Db.WidgetRows(r.Ctx, name, vocab.Period{}, r.WithMeasure(r.WindowParameters()), func(row reads.RowScanner) error {
		var (
			item                                        destinationRow
			iface, client, owner, asn, port, bytes, blk reads.NullInt64
			peer, state, site, protocol                 reads.NullString
			measured                                    reads.VolumeFigures
		)
		targets := []any{&iface, &client, &owner, &item.TrafficDirection, &peer, &state, &asn, &site, &port,
			&protocol, &item.Connections, &bytes, &item.Blocked, &blk, &item.Newest}
		if measure := r.MeasureRead(); measure != "" {
			targets = append(targets, measured.MeasureTarget(measure))
		}
		if err := row.Scan(targets...); err != nil {
			return err
		}
		item.InterfaceID, item.ClientID, item.OwnerID = request.NullableNumber(iface), request.NullableNumber(client), request.NullableNumber(owner)
		item.PeerAddress, item.LookupState, item.ASN = request.NullableText(peer), request.NullableText(state), request.NullableNumber(asn)
		item.SiteName, item.Port, item.Protocol = request.NullableText(site), request.NullableNumber(port), request.NullableText(protocol)
		item.Bytes, item.BlockedBytes = bytes.Int64, blk.Int64
		item.MeasureMembers = Summable(measured).MeasureMembers
		if item.PeerAddress != nil {
			r.Place(*item.PeerAddress)
		}
		rows = append(rows, item)
		return nil
	})
	return rows, err
}
