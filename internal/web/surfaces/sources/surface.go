// Package sources is the Sources surface: one row per registered provider with its
// availability, the recent collection gaps, and the outage indicator the top bar of every
// signed-in page carries.
package sources

import (
	"context"
	"embed"
	"net/http"
	"time"

	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/web"
)

// The surface's files: its page template, its catalogue file and its marker.
//
//go:embed template locale surface.json
var files embed.FS

// PathSources is every source's availability and the recent collection gaps, read from the
// local database.
const PathSources = "/sources"

// pageSources is the surface's one page.
const pageSources = "sources"

// The top bar's outage indicator, in its three states.
const (
	msgIndicatorOutages  messageKey = "indicator.outages"
	msgIndicatorNoOutage messageKey = "indicator.no_outage"
	msgIndicatorUnknown  messageKey = "indicator.unknown"
)

type messageKey = web.MessageKey

func init() {
	web.RegisterSurface(web.Surface{
		Name:  "sources",
		Files: files,
		Pages: []web.Page{{Name: pageSources, TitleKey: msgTitleSources}},
		// The Sources surface reads the local database only.
		Routes: func(host web.Host) []web.Route {
			s := newSurface(host)
			return append([]web.Route{
				{Method: http.MethodGet, Pattern: PathSources, Access: web.AccessAuthenticated,
					Handler: s.handleSourcesForm},
			}, s.apiRoutes()...)
		},
		Tab: &web.Tab{Page: pageSources, Path: PathSources, TitleKey: msgTitleSources, Order: 40},
		Indicator: func(ctx context.Context, host web.Host) (web.Indicator, error) {
			return newSurface(host).indicator(ctx)
		},
		IndicatorUnknown: indicatorUnknown(),
		Keys: func() []web.MessageKey {
			return append(keys(), msgIndicatorOutages, msgIndicatorNoOutage, msgIndicatorUnknown)
		},
	})
}

// surface is the surface's handlers over what the host grants.
type surface struct {
	web.Host
	store *store.Store
	now   func() time.Time
}

// newSurface binds the handlers to a host.
func newSurface(host web.Host) *surface {
	return &surface{Host: host, store: host.Store(), now: host.Now}
}

// view is everything the surface's template may read.
type view struct {
	web.Base
	// Sources and RecentGaps are the surface's two tables, and Outages the list of what the
	// indicator counts, one line per unit of its figure.
	Sources    []sourceRow
	RecentGaps []recentGapRow
	Outages    []outageRow
}

// newView returns a view with everything common to every page filled in.
func (s *surface) newView(writer http.ResponseWriter, request *http.Request,
	session *store.Session, title messageKey) (view, error) {
	base, err := s.NewBase(writer, request, session, title)
	if err != nil {
		return view{}, err
	}
	return view{Base: base}, nil
}
