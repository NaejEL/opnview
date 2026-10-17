package live

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/live/strip"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/widget"
)

// The surface's marker.
//
//go:embed surface.json
var files embed.FS

func init() {
	web.RegisterSurface(web.Surface{
		Name:  "live",
		Files: files,
		Routes: func(host web.Host) []web.Route {
			return (&surface{Host: host}).routes()
		},
	})
}

// The paths.
const (
	// PathStrip is the rolling strip.
	PathStrip = "/api/live/strip"
	// PathSettings is the live-rate settings, read and written.
	PathSettings = "/api/live/settings"
)

// The conditions the surface answers with, beside the configuration's setting codes.
const (
	// ConditionMalformedParameters is a strip request whose parameters are not one JSON object, or
	// whose window_seconds is not a whole number.
	ConditionMalformedParameters = "malformed_parameters"
	// ConditionWindowOutOfRange is a window_seconds outside the bounds the answer names.
	ConditionWindowOutOfRange = "window_out_of_range"
	// ConditionMalformedBody is a settings write whose body is not one JSON object.
	ConditionMalformedBody = "malformed_body"
	// ConditionReadFailed is a read the store could not complete.
	ConditionReadFailed = "read_failed"
	// ConditionInternal is a failure of the database whose detail is not served.
	ConditionInternal = "internal_error"
)

// maxBody bounds a settings write's body.
const maxBody = 16 << 10

type surface struct {
	web.Host
}

func (s *surface) routes() []web.Route {
	return []web.Route{
		web.JSONRoute(http.MethodGet, PathStrip, s.handleStrip),
		web.JSONRoute(http.MethodGet, PathSettings, s.handleSettings),
		web.JSONRoute(http.MethodPost, PathSettings, s.handleWrite),
	}
}

// Refusal is a refused request.
type Refusal struct {
	Condition string `json:"condition"`
	Key       string `json:"key,omitempty"`
	Minimum   *int64 `json:"minimum,omitempty"`
	Maximum   *int64 `json:"maximum,omitempty"`
}

// cadence is the strip's bucket width: the finest live cadence a loaded plugin declares, in force
// now, or the measurement interval where none declares a live pass.
func (s *surface) cadence(ctx context.Context) (int64, error) {
	finest := int64(0)
	for _, pass := range s.Plugins().LivePasses() {
		seconds := int64(pass.Cadence(ctx, s.Store()).Seconds())
		if finest == 0 || seconds < finest {
			finest = seconds
		}
	}
	if finest > 0 {
		return finest, nil
	}
	return s.Store().MeasurementIntervalSeconds(ctx)
}

// stripParameters is the strip's one parameter.
type stripParameters struct {
	WindowSeconds *json.Number `json:"window_seconds"`
}

func (s *surface) handleStrip(writer http.ResponseWriter, request *http.Request) {
	cadence, err := s.cadence(request.Context())
	if err != nil {
		widget.WriteJSON(writer, http.StatusInternalServerError, Refusal{Condition: ConditionReadFailed})
		return
	}
	window := strip.DefaultWindowSeconds
	if raw := request.URL.Query().Get(widget.ParametersQueryKey); raw != "" {
		var parameters stripParameters
		decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
		decoder.UseNumber()
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&parameters); err != nil {
			widget.WriteJSON(writer, http.StatusBadRequest, Refusal{Condition: ConditionMalformedParameters})
			return
		}
		if parameters.WindowSeconds != nil {
			value, err := strconv.ParseInt(parameters.WindowSeconds.String(), 10, 64)
			if err != nil {
				widget.WriteJSON(writer, http.StatusBadRequest, Refusal{Condition: ConditionMalformedParameters,
					Key: "window_seconds"})
				return
			}
			window = value
		}
	}
	answer, err := strip.Read(request.Context(), s.Store(), strip.Request{Now: s.Now(), CadenceSeconds: cadence,
		WindowSeconds: window, Interest: s.Widgets().LiveInterest()})
	var outOfRange *strip.ErrWindowOutOfRange
	switch {
	case errors.As(err, &outOfRange):
		minimum, maximum := outOfRange.Minimum, outOfRange.Maximum
		widget.WriteJSON(writer, http.StatusBadRequest, Refusal{Condition: ConditionWindowOutOfRange,
			Key: "window_seconds", Minimum: &minimum, Maximum: &maximum})
		return
	case err != nil:
		widget.WriteJSON(writer, http.StatusInternalServerError, Refusal{Condition: ConditionReadFailed})
		return
	}
	widget.WriteJSON(writer, http.StatusOK, answer)
}

// Setting is one live setting as it stands.
type Setting struct {
	Key     string `json:"key"`
	Value   int64  `json:"value"`
	Default int64  `json:"default"`
	Minimum int64  `json:"minimum"`
	Maximum int64  `json:"maximum"`
	// Set says a row holds the value; false is the default in force.
	Set bool `json:"set"`
}

// Cadence is one live pass's cadence as it stands.
type Cadence struct {
	Plugin      string `json:"plugin"`
	Kind        string `json:"kind"`
	ProviderKey string `json:"provider_key"`
	// SettingKey is the plugin's own setting key, written through POST /api/plugins/{name}/settings.
	SettingKey string `json:"setting_key"`
	// CadenceSeconds is the cadence in force: the setting, never below its lower bound nor the
	// freshness floor of the endpoints it reads.
	CadenceSeconds int64 `json:"cadence_seconds"`
	FloorSeconds   int64 `json:"floor_seconds"`
}

// SettingsAnswer is GET /api/live/settings and the answer of a write.
type SettingsAnswer struct {
	Settings []Setting `json:"settings"`
	Cadences []Cadence `json:"cadences"`
}

func (s *surface) settings(ctx context.Context) (SettingsAnswer, error) {
	answer := SettingsAnswer{Settings: []Setting{}, Cadences: []Cadence{}}
	for _, key := range config.LiveSettingKeys() {
		fallback, minimum, maximum, _ := config.LiveSettingBounds(key)
		setting := Setting{Key: key, Value: fallback, Default: fallback, Minimum: minimum, Maximum: maximum}
		text, present, err := s.Store().Setting(ctx, key)
		if err != nil {
			return answer, err
		}
		if present {
			if value, err := config.ParseLiveSetting(key, text); err == nil {
				setting.Value, setting.Set = value, true
			}
		}
		answer.Settings = append(answer.Settings, setting)
	}
	for _, pass := range s.Plugins().LivePasses() {
		answer.Cadences = append(answer.Cadences, Cadence{Plugin: pass.Plugin, Kind: pass.Kind,
			ProviderKey: pass.ProviderKey, SettingKey: pass.SettingKey,
			CadenceSeconds: int64(pass.Cadence(ctx, s.Store()).Seconds()), FloorSeconds: pass.FloorSeconds})
	}
	return answer, nil
}

func (s *surface) handleSettings(writer http.ResponseWriter, request *http.Request) {
	answer, err := s.settings(request.Context())
	if err != nil {
		widget.WriteJSON(writer, http.StatusInternalServerError, Refusal{Condition: ConditionReadFailed})
		return
	}
	widget.WriteJSON(writer, http.StatusOK, answer)
}

func (s *surface) handleWrite(writer http.ResponseWriter, request *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(request.Body, maxBody+1))
	if err != nil || len(raw) > maxBody {
		widget.WriteJSON(writer, http.StatusBadRequest, Refusal{Condition: ConditionMalformedBody})
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var submitted map[string]any
	if err := decoder.Decode(&submitted); err != nil || submitted == nil {
		widget.WriteJSON(writer, http.StatusBadRequest, Refusal{Condition: ConditionMalformedBody})
		return
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		widget.WriteJSON(writer, http.StatusBadRequest, Refusal{Condition: ConditionMalformedBody})
		return
	}
	// Every value is checked before anything is written: one refused value writes nothing.
	values := map[string]int64{}
	for _, key := range sortedKeys(submitted) {
		number, isNumber := submitted[key].(json.Number)
		text := ""
		if isNumber {
			text = number.String()
		}
		if _, _, _, known := config.LiveSettingBounds(key); !known {
			widget.WriteJSON(writer, http.StatusBadRequest, Refusal{Condition: config.LiveSettingUnknown, Key: key})
			return
		}
		value, err := config.ParseLiveSetting(key, text)
		if !isNumber {
			err = &config.LiveSettingRefusal{Code: config.LiveSettingInvalid, Key: key}
		}
		var refusal *config.LiveSettingRefusal
		if errors.As(err, &refusal) {
			widget.WriteJSON(writer, http.StatusBadRequest, Refusal{Condition: refusal.Code, Key: key})
			return
		}
		values[key] = value
	}
	now := s.Now().Unix()
	for _, key := range sortedKeys(submitted) {
		if err := s.Store().SetSetting(request.Context(), key, strconv.FormatInt(values[key], 10), now); err != nil {
			widget.WriteJSON(writer, http.StatusInternalServerError, Refusal{Condition: ConditionInternal})
			return
		}
	}
	// The lease length is read from the live configuration at every renewal; the retention at the
	// next purge.
	if err := s.ApplyConfiguration(request.Context()); err != nil {
		widget.WriteJSON(writer, http.StatusInternalServerError, Refusal{Condition: ConditionInternal})
		return
	}
	answer, err := s.settings(request.Context())
	if err != nil {
		widget.WriteJSON(writer, http.StatusInternalServerError, Refusal{Condition: ConditionReadFailed})
		return
	}
	widget.WriteJSON(writer, http.StatusOK, answer)
}

// sortedKeys returns an object's keys in order, so a refusal names the same key every time.
func sortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
