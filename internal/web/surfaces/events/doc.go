// Package events mounts the event stream (docs/widget-api.md, "Events"): GET /api/events,
// authenticated, answers text/event-stream and announces, as it is committed, every change a
// widget's answer can depend on, so the front asks again the widgets that read it rather than
// polling them.
//
// WHAT IT RELAYS. The core already publishes a notification when what it stored is committed
// (internal/store/sqldb, notify.go; docs/plugins.md, "Notifications"): a collection pass of a core
// kind, a pass of a plugin's own kind, an aggregate refresh -- which follows every derivation, so
// every filter-log pass and every pushed batch -- and a live pass's fine samples. The stream is
// those notifications, one event each, named by type and carrying what they name: the kind and
// provider, the plugin, the periods and ranges of the slots rewritten and their families, the
// instant of the newest live sample. It carries no figure: an event says what changed, and the
// widget API stays the one place an answer is composed, which is also what renews the
// live-interest lease.
//
// ONE LISTENER, ANY NUMBER OF READERS. The surface holds one database listener while at least one
// stream is open and none otherwise; every notification heard is fanned out to every open stream.
// A reader too slow to take an event loses it, and is told so with a resync event, never silently.
//
// A STREAM IS BOUNDED. A comment line every heartbeat keeps intermediaries from closing an idle
// connection and finds a reader that went away; after its lifetime the stream says end and closes,
// and the browser's EventSource opens it again, through the session check like any request, so a
// stream never outlives the session that opened it by more than one lifetime.
package events
