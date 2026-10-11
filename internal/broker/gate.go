package broker

import (
	"context"
	"time"

	"github.com/NaejEL/opnview/internal/broker/answer"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/opnsense"
)

// The broker as the provider host's gate: a Go reader reads a whole answer, as a body.

var _ provider.Gate = (*Broker)(nil)

// requesterNamed is a requester by the name the host carries: a plugin's, or a core reader's.
func requesterNamed(name string) Requester {
	if Requester(Requester{Name: name}).IsCore() {
		return Requester{Name: name, Version: "core"}
	}
	return Requester{Name: name}
}

// GateCall answers one request read whole, and records it so.
func (b *Broker) GateCall(ctx context.Context, requester string, endpoint opnsense.Endpoint,
	options opnsense.RequestOptions, maxAge time.Duration) (opnsense.Response, error) {
	who := requesterNamed(requester)
	served, err := b.Call(ctx, Request{Requester: who, Endpoint: endpoint, Options: options, MaxAge: maxAge, Whole: true})
	if served == nil {
		return opnsense.Response{Endpoint: endpoint}, err
	}
	if served.Response.OK() {
		reads := answer.NewReads()
		reads.Whole("")
		b.Done(ctx, served, who, maxAge, reads)
	}
	served.Response.Endpoint = endpoint
	return served.Response, err
}

// GateStream answers a bounded read of a stream from its shared subscription.
func (b *Broker) GateStream(ctx context.Context, requester string, endpoint opnsense.Endpoint,
	bound opnsense.StreamBound) (opnsense.StreamResponse, error) {
	return b.Stream(ctx, requesterNamed(requester), endpoint, bound)
}
