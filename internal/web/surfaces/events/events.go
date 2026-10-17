package events

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/NaejEL/opnview/internal/web"
)

// The surface's marker.
//
//go:embed surface.json
var files embed.FS

func init() {
	web.RegisterSurface(web.Surface{
		Name:  "events",
		Files: files,
		Routes: func(host web.Host) []web.Route {
			return (&surface{hub: newHub(host.Store(), host.Now), now: host.Now, heartbeat: heartbeat,
				lifetime: lifetime}).routes()
		},
	})
}

// PathEvents is the event stream.
const PathEvents = "/api/events"

// The stream's bounds. A heartbeat keeps an idle connection open through intermediaries and finds
// a reader that went away; the lifetime closes the stream so the browser opens it again through
// the session check. The retry is what the browser waits before opening it again.
const (
	heartbeat = 15 * time.Second
	lifetime  = 5 * time.Minute
)

// retryMilliseconds is the reconnection delay the stream asks the browser for.
const retryMilliseconds = 2000

type surface struct {
	hub       *hub
	now       func() time.Time
	heartbeat time.Duration
	lifetime  time.Duration
}

func (s *surface) routes() []web.Route {
	return []web.Route{
		// A JSON route: every refusal, a missing session first, is answered in JSON by the middleware.
		{Method: http.MethodGet, Pattern: PathEvents, Access: web.AccessAuthenticated, JSON: true, Handler: s.handleEvents},
	}
}

// handleEvents holds one stream open: hello, then every event heard, a heartbeat comment between,
// and end once its lifetime has elapsed or as soon as the reader goes away.
func (s *surface) handleEvents(writer http.ResponseWriter, request *http.Request) {
	controller := http.NewResponseController(writer)
	header := writer.Header()
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	// A buffering reverse proxy would hold the events back until the stream ends.
	header.Set("X-Accel-Buffering", "no")

	open := s.hub.subscribe()
	defer s.hub.unsubscribe(open)

	writer.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprintf(writer, "retry: %d\n\n", retryMilliseconds); err != nil {
		return
	}
	hello := Event{Type: TypeHello, At: s.now().Unix(), HeartbeatSeconds: int64(s.heartbeat / time.Second),
		LifetimeSeconds: int64(s.lifetime / time.Second)}
	if writeEvent(writer, hello) != nil || controller.Flush() != nil {
		return
	}

	beat := time.NewTicker(s.heartbeat)
	defer beat.Stop()
	ending := time.NewTimer(s.lifetime)
	defer ending.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case <-ending.C:
			_ = writeEvent(writer, Event{Type: TypeEnd, At: s.now().Unix()})
			_ = controller.Flush()
			return
		case <-beat.C:
			if _, err := io.WriteString(writer, ": heartbeat\n\n"); err != nil || controller.Flush() != nil {
				return
			}
		case event := <-open.events:
			if writeEvent(writer, event) != nil {
				return
			}
			if open.lost.Swap(false) {
				if writeEvent(writer, Event{Type: TypeResync, At: s.now().Unix()}) != nil {
					return
				}
			}
			if controller.Flush() != nil {
				return
			}
		}
	}
}

// writeEvent writes one event: its type as the SSE event name and itself, in JSON, as its data.
func writeEvent(writer io.Writer, event Event) error {
	encoded, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("events: encoding a %s event: %w", event.Type, err)
	}
	_, err = fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", event.Type, encoded)
	return err
}
