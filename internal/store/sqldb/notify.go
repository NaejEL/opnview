package sqldb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// The notifications the core publishes on commit (P7 of specs/SPEC-backend-completion-plugins.md):
// a closed list of channels, each with a JSON payload, documented in docs/plugins.md. A provider of
// a kind of its own may be triggered by one, in addition to its cadence. PostgreSQL delivers a
// notification issued inside a transaction only when the transaction commits, and never when it
// rolls back, so a listener hears of what was stored and of nothing else.

// The channels.
const (
	// ChannelCollectionPass is a collection pass of a core kind that stored its rows:
	// {"kind": ..., "provider_key": ...}.
	ChannelCollectionPass = "opnview_collection_pass"
	// ChannelPluginPass is a pass of a plugin's own kind that stored its rows:
	// {"plugin": ..., "provider": ...}.
	ChannelPluginPass = "opnview_plugin_pass"
	// ChannelAggregateRefresh is an aggregate refresh that rewrote slots: {"periods": [...]}.
	ChannelAggregateRefresh = "opnview_aggregate_refresh"
)

// Channels are every channel the core publishes on.
func Channels() []string {
	return []string{ChannelCollectionPass, ChannelPluginPass, ChannelAggregateRefresh}
}

// Notification is one notification heard.
type Notification struct {
	Channel string
	Payload map[string]any
}

// Notify publishes a notification inside the caller's transaction, or at once on q when it is the
// pool.
func Notify(ctx context.Context, q Querier, channel string, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, "SELECT pg_notify($1, $2)", channel, string(encoded)); err != nil {
		return fmt.Errorf("store: notifying %s: %w", channel, err)
	}
	return nil
}

// NotifyCollectionPass publishes that a collection pass of one provider of a core kind stored its
// rows.
func (s *Handle) NotifyCollectionPass(ctx context.Context, kind, providerKey string) error {
	return Notify(ctx, s.SQL, ChannelCollectionPass, map[string]string{"kind": kind, "provider_key": providerKey})
}

// Listen holds a connection of its own listening on the channels and hands every notification to
// handle, until the context ends. A connection that drops is opened again.
func (s *Handle) Listen(ctx context.Context, channels []string, handle func(Notification)) error {
	for ctx.Err() == nil {
		if err := s.listenOnce(ctx, channels, handle); err != nil && ctx.Err() == nil {
			select {
			case <-ctx.Done():
			case <-timeAfter(listenRetry):
			}
		}
	}
	return nil
}

func (s *Handle) listenOnce(ctx context.Context, channels []string, handle func(Notification)) error {
	connection, err := s.SQL.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = connection.Close() }()
	return connection.Raw(func(raw any) error {
		inner, ok := raw.(*stdlib.Conn)
		if !ok {
			return errors.New("store: the connection is not a pgx connection")
		}
		pgxConn := inner.Conn()
		for _, channel := range channels {
			if _, err := pgxConn.Exec(ctx, "LISTEN "+pgx.Identifier{channel}.Sanitize()); err != nil {
				return err
			}
		}
		for {
			heard, err := pgxConn.WaitForNotification(ctx)
			if err != nil {
				return err
			}
			var payload map[string]any
			_ = json.Unmarshal([]byte(heard.Payload), &payload)
			handle(Notification{Channel: heard.Channel, Payload: payload})
		}
	})
}

// listenRetry is how long a listener waits before opening a dropped connection again.
const listenRetry = 2 * time.Second

var timeAfter = time.After
