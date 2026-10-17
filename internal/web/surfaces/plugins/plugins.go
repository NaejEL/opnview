package plugins

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/plugin/contract"
	"github.com/NaejEL/opnview/internal/plugin/listing"
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
	// PathPluginSettingsPattern is the settings of one plugin: read with GET, written with POST.
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
		web.JSONRoute(http.MethodGet, PathPlugins, s.handleListing),
		web.JSONRoute(http.MethodGet, PathPluginSettingsPattern, s.handleSettingsRead),
		web.JSONRoute(http.MethodPost, PathPluginSettingsPattern, s.handleSettings),
	}
}

// Listing is the listing's answer.
type Listing struct {
	Plugins []listing.ListedPlugin `json:"plugins"`
	// FirewallVersion is the firewall's OPNsense version as discovery last read it, null until
	// read: what a plugin's version ranges are selected against (A19).
	FirewallVersion *string `json:"firewall_version"`
}

// SettingsAnswer is a plugin's settings as they now stand, and the pages its manifest groups them in
// (contract.PageDeclaration): the answer of a settings read and of a settings write.
type SettingsAnswer struct {
	Plugin   string                     `json:"plugin"`
	Settings []plugin.SettingValue      `json:"settings"`
	Pages    []contract.PageDeclaration `json:"pages,omitempty"`
}

// SettingsRefusal is a settings write refused, naming the key when one key is the reason.
type SettingsRefusal struct {
	Condition string `json:"condition"`
	Key       string `json:"key,omitempty"`
}

func (s *surface) handleListing(writer http.ResponseWriter, request *http.Request) {
	listed, err := listing.Listing(s.Plugins(), request.Context(), s.Store())
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
	// A plugin providing a service kind -- a dataset it acquires -- is asked to refresh now rather than at its
	// next daily check: its credentials may be what the settings just changed.
	if s.GeoIP() != nil {
		for _, provided := range loaded.Providers {
			if provider.IsServiceKind(provided.Kind) {
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
	widget.WriteJSON(writer, http.StatusOK, SettingsAnswer{Plugin: loaded.Name, Settings: settings,
		Pages: loaded.Manifest.Pages})
}

// handleSettingsRead answers a plugin's settings and the pages its manifest groups them in, for the step-7
// application to draw them as the plugin declares.
func (s *surface) handleSettingsRead(writer http.ResponseWriter, request *http.Request) {
	loaded, found := s.Plugins().Plugin(request.PathValue("name"))
	if !found {
		widget.WriteJSON(writer, http.StatusNotFound, SettingsRefusal{Condition: ConditionUnknownPlugin})
		return
	}
	settings, err := loaded.SettingValues(request.Context(), s.Store())
	if err != nil {
		widget.WriteJSON(writer, http.StatusInternalServerError, SettingsRefusal{Condition: ConditionInternal})
		return
	}
	widget.WriteJSON(writer, http.StatusOK, SettingsAnswer{Plugin: loaded.Name, Settings: settings,
		Pages: loaded.Manifest.Pages})
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
