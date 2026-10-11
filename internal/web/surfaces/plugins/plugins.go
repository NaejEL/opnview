// Package plugins mounts the plugin listing and the plugin settings write endpoint (A5 and A6 of
// the backend-completion cycle, docs/widget-api.md, "Plugins"). It draws no page and carries no
// catalogue: it answers JSON only, for the step-7 application.
//
//   - GET /api/plugins lists every plugin discovered, built-ins included: what each provides,
//     reads, contacts, owns and lets the operator set, or why it was refused. A secret setting
//     reports only whether it is set.
//   - POST /api/plugins/{name}/settings writes settings a plugin declares, validated against the
//     declaration: every key is declared and every value within its bounds, or nothing is written.
//     It is a JSON write, so the middleware asks it for the session's form token in a header.
package plugins

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/NaejEL/opnview/internal/collect/ipgeolocation"
	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/widget"
)

// The surface's marker.
//
//go:embed surface.json
var files embed.FS

func init() {
	web.RegisterSurface(web.Surface{
		Name:  "plugins",
		Files: files,
		Routes: func(host web.Host) []web.Route {
			return (&surface{Host: host}).routes()
		},
	})
}

// The paths.
const (
	// PathPlugins is the listing.
	PathPlugins = "/api/plugins"
	// PathPluginSettingsPattern is the settings write of one plugin.
	PathPluginSettingsPattern = "/api/plugins/{name}/settings"
)

// The conditions a settings write is refused with, beside the plugin package's own setting codes.
const (
	// ConditionUnknownPlugin is a write naming no loaded plugin.
	ConditionUnknownPlugin = "unknown_plugin"
	// ConditionMalformedBody is a body that is not one JSON object.
	ConditionMalformedBody = "malformed_body"
	// ConditionInternal is a failure of the database or of a stored secret, whose detail is not
	// served.
	ConditionInternal = "internal_error"
)

// maxSettingsBody bounds a settings write's body.
const maxSettingsBody = 64 << 10

// surface is the surface's handlers over what the host grants.
type surface struct {
	web.Host
}

func (s *surface) routes() []web.Route {
	return []web.Route{
		{Method: http.MethodGet, Pattern: PathPlugins, Access: web.AccessAuthenticated, JSON: true,
			Handler: s.handleListing},
		{Method: http.MethodPost, Pattern: PathPluginSettingsPattern, Access: web.AccessAuthenticated, JSON: true,
			Handler: s.handleSettings},
	}
}

// Listing is the listing's answer.
type Listing struct {
	Plugins []plugin.ListedPlugin `json:"plugins"`
	// FirewallVersion is the firewall's OPNsense version as discovery last read it, null until
	// read: what a plugin's version ranges are selected against (A19).
	FirewallVersion *string `json:"firewall_version"`
}

// SettingsAnswer is a settings write's answer: the plugin's settings as they now stand.
type SettingsAnswer struct {
	Plugin   string                `json:"plugin"`
	Settings []plugin.SettingValue `json:"settings"`
}

// SettingsRefusal is a settings write refused, naming the key when one key is the reason.
type SettingsRefusal struct {
	Condition string `json:"condition"`
	Key       string `json:"key,omitempty"`
}

func (s *surface) handleListing(writer http.ResponseWriter, request *http.Request) {
	listed, err := s.Plugins().Listing(request.Context(), s.Store())
	if err != nil {
		widget.WriteJSON(writer, http.StatusInternalServerError, SettingsRefusal{Condition: ConditionInternal})
		return
	}
	answer := Listing{Plugins: listed}
	if version, known := s.Broker().FirewallVersion(); known {
		answer.FirewallVersion = &version
	}
	widget.WriteJSON(writer, http.StatusOK, answer)
}

func (s *surface) handleSettings(writer http.ResponseWriter, request *http.Request) {
	loaded, found := s.Plugins().Plugin(request.PathValue("name"))
	if !found {
		widget.WriteJSON(writer, http.StatusNotFound, SettingsRefusal{Condition: ConditionUnknownPlugin})
		return
	}
	submitted, ok := decodeObject(io.LimitReader(request.Body, maxSettingsBody+1))
	if !ok {
		widget.WriteJSON(writer, http.StatusBadRequest, SettingsRefusal{Condition: ConditionMalformedBody})
		return
	}
	var sealer plugin.Sealer
	if box := s.Box(); box != nil {
		sealer = box
	}
	if err := loaded.WriteSettings(request.Context(), s.Store(), sealer, submitted, s.Now().Unix()); err != nil {
		var refusal *plugin.SettingRefusal
		if errors.As(err, &refusal) {
			widget.WriteJSON(writer, http.StatusBadRequest, SettingsRefusal{Condition: refusal.Code, Key: refusal.Key})
			return
		}
		widget.WriteJSON(writer, http.StatusInternalServerError, SettingsRefusal{Condition: ConditionInternal})
		return
	}
	// A plugin placing addresses is asked to refresh now rather than at its next daily check, as a
	// save of the MaxMind credentials on the settings page asks.
	if s.GeoIP() != nil {
		for _, provided := range loaded.Providers {
			if provided.Kind == ipgeolocation.Kind {
				s.GeoIP().Wake()
				break
			}
		}
	}
	settings, err := loaded.SettingValues(request.Context(), s.Store())
	if err != nil {
		widget.WriteJSON(writer, http.StatusInternalServerError, SettingsRefusal{Condition: ConditionInternal})
		return
	}
	widget.WriteJSON(writer, http.StatusOK, SettingsAnswer{Plugin: loaded.Name, Settings: settings})
}

// decodeObject reads a body that is exactly one JSON object, its numbers kept as written.
func decodeObject(body io.Reader) (map[string]any, bool) {
	raw, err := io.ReadAll(body)
	if err != nil || len(raw) > maxSettingsBody {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, false
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, false
	}
	return object, true
}
