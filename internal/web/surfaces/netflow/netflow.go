package netflow

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
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/widget"
)

// The surface's marker.
//
//go:embed surface.json
var files embed.FS

func init() {
	web.RegisterSurface(web.Surface{
		Name:  "netflow",
		Files: files,
		Routes: func(host web.Host) []web.Route {
			return (&surface{Host: host}).routes()
		},
	})
}

// PathSettings is the NetFlow settings, read and written.
const PathSettings = "/api/netflow/settings"

// The conditions the surface answers with, beside the configuration's setting codes.
const (
	// ConditionMalformedBody is a write whose body is not one JSON object.
	ConditionMalformedBody = "malformed_body"
	// ConditionReadFailed is a read the store could not complete.
	ConditionReadFailed = "read_failed"
	// ConditionInternal is a failure of the database whose detail is not served.
	ConditionInternal = "internal_error"
)

// maxBody bounds a write's body.
const maxBody = 16 << 10

type surface struct {
	web.Host
}

func (s *surface) routes() []web.Route {
	return []web.Route{
		{Method: http.MethodGet, Pattern: PathSettings, Access: web.AccessAuthenticated, JSON: true,
			Handler: s.handleSettings},
		{Method: http.MethodPost, Pattern: PathSettings, Access: web.AccessAuthenticated, JSON: true,
			Handler: s.handleWrite},
	}
}

// Refusal is a refused request.
type Refusal struct {
	Condition string `json:"condition"`
	Key       string `json:"key,omitempty"`
}

// Setting is one NetFlow setting as it stands.
type Setting struct {
	Key     string `json:"key"`
	Value   int64  `json:"value"`
	Default int64  `json:"default"`
	Minimum int64  `json:"minimum"`
	Maximum int64  `json:"maximum"`
	// Set says a row holds the value; false is the default in force.
	Set bool `json:"set"`
}

// SettingsAnswer is GET /api/netflow/settings and the answer of a write.
type SettingsAnswer struct {
	Settings []Setting `json:"settings"`
}

func (s *surface) settings(ctx context.Context) (SettingsAnswer, error) {
	answer := SettingsAnswer{Settings: []Setting{}}
	for _, key := range config.NetflowSettingKeys() {
		fallback, minimum, maximum, _ := config.NetflowSettingBounds(key)
		setting := Setting{Key: key, Value: fallback, Default: fallback, Minimum: minimum, Maximum: maximum}
		text, present, err := s.Store().Setting(ctx, key)
		if err != nil {
			return answer, err
		}
		if present {
			if value, err := config.ParseNetflowSetting(key, text); err == nil {
				setting.Value, setting.Set = value, true
			}
		}
		answer.Settings = append(answer.Settings, setting)
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
		value, err := config.ParseNetflowSetting(key, number.String())
		if !isNumber && err == nil {
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
	// The tolerance reaches the running collector at once; the retention at the next purge.
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
