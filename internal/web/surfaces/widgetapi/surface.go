// Package widgetapi mounts the widget API of step 5B on the server: one authenticated GET
// route per served widget type, built from the widget service's own list, and one route for
// every other type under the same prefix. It draws no page and carries no catalogue: it
// answers JSON only.
package widgetapi

import (
	"embed"

	"github.com/NaejEL/opnview/internal/web"
)

// The surface's marker.
//
//go:embed surface.json
var files embed.FS

func init() {
	web.RegisterSurface(web.Surface{
		Name: "widgetapi",
		// The files hold no page and no catalogue; the field is the marker's home.
		Files: files,
		Routes: func(host web.Host) []web.Route {
			return (&surface{Host: host}).widgetRoutes()
		},
	})
}

// surface is the surface's handlers over what the host grants.
type surface struct {
	web.Host
}
