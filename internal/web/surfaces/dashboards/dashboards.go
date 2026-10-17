package dashboards

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strconv"

	opnview "github.com/NaejEL/opnview"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/dashboard"
	"github.com/NaejEL/opnview/internal/store/dashboards"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/widget"
)

// The surface's marker.
//
//go:embed surface.json
var files embed.FS

func init() {
	web.RegisterSurface(web.Surface{
		Name:  "dashboards",
		Files: files,
		Routes: func(host web.Host) []web.Route {
			return (&surface{Host: host}).routes()
		},
	})
}

// The paths.
const (
	// PathDashboards lists the dashboards (GET) and creates one (POST).
	PathDashboards = "/api/dashboards"
	// PathDashboardPattern reads one dashboard (GET) and saves it (POST).
	PathDashboardPattern = "/api/dashboards/{id}"
	// PathDashboardDeletePattern deletes one dashboard and answers it whole.
	PathDashboardDeletePattern = "/api/dashboards/{id}/delete"
	// PathDashboardRestore recreates a deleted dashboard from its deletion's answer.
	PathDashboardRestore = "/api/dashboards/restore"
	// PathDashboardOrder gives the dashboards a new order.
	PathDashboardOrder = "/api/dashboards/order"
	// PathDashboardDefault offers the default dashboard, once ever.
	PathDashboardDefault = "/api/dashboards/default"
)

// The conditions a request is refused with beside the document's own (internal/dashboard).
const (
	// ConditionUnknownDashboard is an id naming no dashboard (404).
	ConditionUnknownDashboard = "unknown_dashboard"
	// ConditionRevisionConflict is a save naming a revision that is not the stored one (409).
	ConditionRevisionConflict = "revision_conflict"
)

// maxBody bounds a write's body: a document at its largest and the members around it.
const maxBody = dashboard.MaxDocumentBytes + 64<<10

// surface is the surface's handlers over what the host grants.
type surface struct {
	web.Host
}

func (s *surface) store() *dashboards.DashboardStore { return dashboards.On(s.Store().Handle) }

func (s *surface) routes() []web.Route {
	route := func(method, pattern string, handler http.HandlerFunc) web.Route {
		return web.Route{Method: method, Pattern: pattern, Access: web.AccessAuthenticated, JSON: true,
			Handler: handler}
	}
	return []web.Route{
		route(http.MethodGet, PathDashboards, s.handleList),
		route(http.MethodPost, PathDashboards, s.handleCreate),
		route(http.MethodGet, PathDashboardPattern, s.handleRead),
		route(http.MethodPost, PathDashboardPattern, s.handleSave),
		route(http.MethodPost, PathDashboardDeletePattern, s.handleDelete),
		route(http.MethodPost, PathDashboardRestore, s.handleRestore),
		route(http.MethodPost, PathDashboardOrder, s.handleOrder),
		route(http.MethodPost, PathDashboardDefault, s.handleSeed),
	}
}

// ListedCanvas is one canvas of a listed dashboard.
type ListedCanvas struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// ListedDashboard is one dashboard of the listing.
type ListedDashboard struct {
	ID         int64          `json:"id"`
	Position   int64          `json:"position"`
	Title      *string        `json:"title"`
	Revision   int64          `json:"revision"`
	Origin     string         `json:"origin"`
	SourceName *string        `json:"source_name"`
	Canvases   []ListedCanvas `json:"canvases"`
}

// FormatVersions are the format versions this build writes and reads.
type FormatVersions struct {
	Current int `json:"current"`
	Minimum int `json:"minimum"`
}

// Listing is GET /api/dashboards.
type Listing struct {
	Dashboards []ListedDashboard `json:"dashboards"`
	Format     FormatVersions    `json:"format"`
}

// Dashboard is a whole dashboard as a write answers it, the document apart: it is spliced in as
// the text it was stored with (writeDashboard).
type Dashboard struct {
	ID         int64   `json:"id"`
	Position   int64   `json:"position"`
	Revision   int64   `json:"revision"`
	Origin     string  `json:"origin"`
	SourceName *string `json:"source_name"`
	CreatedAt  int64   `json:"created_at"`
	UpdatedAt  int64   `json:"updated_at"`
}

// ReadAnswer is GET /api/dashboards/{id}, the document apart.
type ReadAnswer struct {
	ID         int64   `json:"id"`
	Revision   int64   `json:"revision"`
	Origin     string  `json:"origin"`
	SourceName *string `json:"source_name"`
}

// Conflict is a save refused for a stale revision, with the stored one.
type Conflict struct {
	Condition string `json:"condition"`
	Revision  int64  `json:"revision"`
}

// Order is the dashboards' ids in order: POST /api/dashboards/order's body and answer.
type Order struct {
	IDs []int64 `json:"ids"`
}

// refuse answers a refusal in the document refusal's shape, outside any canvas.
func refuse(writer http.ResponseWriter, status int, condition, field string) {
	widget.WriteJSON(writer, status, &dashboard.Refusal{Condition: condition, Field: field, Path: field})
}

// fail answers a failure of the store, whose detail goes to the console.
func fail(writer http.ResponseWriter, request *http.Request, err error) {
	web.FailJSON(writer, request, err)
}

// writeWithDocument answers head, a JSON object, with the member "document" appended holding the
// stored text verbatim. encoding/json would compact the text and escape characters in it, so the
// document is never passed through it.
func writeWithDocument(writer http.ResponseWriter, status int, head any, document string) {
	encoded, err := json.Marshal(head)
	if err != nil || len(encoded) < 2 || encoded[len(encoded)-1] != '}' {
		widget.WriteJSON(writer, http.StatusInternalServerError, web.JSONCondition{Condition: web.ConditionInternalError})
		return
	}
	var body bytes.Buffer
	body.Write(encoded[:len(encoded)-1])
	if len(encoded) > 2 {
		body.WriteByte(',')
	}
	body.WriteString(`"document":`)
	body.WriteString(document)
	body.WriteByte('}')
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_, _ = writer.Write(body.Bytes())
}

// writeDashboard answers a whole dashboard.
func writeDashboard(writer http.ResponseWriter, stored dashboards.Dashboard) {
	writeWithDocument(writer, http.StatusOK, Dashboard{ID: stored.ID, Position: stored.Position,
		Revision: stored.Revision, Origin: stored.Origin, SourceName: stored.SourceName,
		CreatedAt: stored.CreatedAt, UpdatedAt: stored.UpdatedAt}, stored.Document)
}

// pathID is the dashboard id the path names, or false.
func pathID(request *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

// readEnvelope reads a write's body: exactly one JSON object within the bound, its members kept
// as their raw text. It answers the refusal itself and returns false when there is none to read.
func readEnvelope(writer http.ResponseWriter, request *http.Request) (map[string]json.RawMessage, bool) {
	raw, err := io.ReadAll(io.LimitReader(request.Body, maxBody+1))
	if err != nil {
		refuse(writer, http.StatusBadRequest, dashboard.ConditionMalformedBody, "")
		return nil, false
	}
	if len(raw) > maxBody {
		refuse(writer, http.StatusBadRequest, dashboard.ConditionDocumentTooLarge, "document")
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var members map[string]json.RawMessage
	if err := decoder.Decode(&members); err != nil || members == nil {
		refuse(writer, http.StatusBadRequest, dashboard.ConditionMalformedBody, "")
		return nil, false
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		refuse(writer, http.StatusBadRequest, dashboard.ConditionMalformedBody, "")
		return nil, false
	}
	return members, true
}

// validDocument reads the envelope's document and validates its structure, answering the refusal
// itself when there is one.
func validDocument(writer http.ResponseWriter, members map[string]json.RawMessage) (string, bool) {
	raw, present := members["document"]
	if !present || bytes.Equal(raw, []byte("null")) {
		refuse(writer, http.StatusBadRequest, dashboard.ConditionMalformedBody, "document")
		return "", false
	}
	if refusal := dashboard.Validate(raw); refusal != nil {
		widget.WriteJSON(writer, http.StatusBadRequest, refusal)
		return "", false
	}
	return string(raw), true
}

// wholeMember reads a member holding a whole number at least minimum.
func wholeMember(members map[string]json.RawMessage, key string, minimum int64) (int64, bool) {
	raw, present := members[key]
	if !present || len(raw) == 0 || raw[0] == '"' {
		// A number written as a string is not a number, though json.Number would accept it.
		return 0, false
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err != nil {
		return 0, false
	}
	value, err := strconv.ParseInt(number.String(), 10, 64)
	if err != nil || value < minimum {
		return 0, false
	}
	return value, true
}

// textMember reads a member holding a string or null; absent and null are nil.
func textMember(members map[string]json.RawMessage, key string) (*string, bool) {
	raw, present := members[key]
	if !present || bytes.Equal(raw, []byte("null")) {
		return nil, true
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false
	}
	return &value, true
}

// validOrigin says whether an origin is one the schema holds.
func validOrigin(origin string) bool {
	return origin == dashboards.OriginCreated || origin == dashboards.OriginImported ||
		origin == dashboards.OriginDefault
}

// DefaultDashboardFile is the default dashboard of the built-in front (F7-28): the step-3 mockup's
// three canvases, restricted to portable references.
//
// ITS TITLES ARE THE DOCUMENT'S DATA, NOT CATALOGUE STRINGS, deliberately. From the moment it is
// stored the default is an ordinary dashboard the reader renames, edits and exports
// (docs/dashboard-format.md): a canvas `title` is a string the file carries, the format has no key
// indirection for it, and an export must not read differently by the language of whoever exports it.
// The catalogue holds the interface's words; the titles are the reader's, written in English once,
// as a new canvas's or a new dashboard's are written in the reader's language when they are made.
const DefaultDashboardFile = "dashboards/default.json"

// DefaultDashboard reads the default dashboard and checks it as any document is checked: a default
// that breaks the format is a defect of the build.
func DefaultDashboard() (string, error) {
	raw, err := fs.ReadFile(opnview.BuiltinFront(), DefaultDashboardFile)
	if err != nil {
		return "", fmt.Errorf("web: reading the default dashboard: %w", err)
	}
	if refusal := dashboard.Validate(raw); refusal != nil {
		return "", fmt.Errorf("web: the default dashboard is refused %s at %s", refusal.Condition, refusal.Path)
	}
	return string(raw), nil
}

// SeedAnswer is POST /api/dashboards/default's answer: whether this call stored the default.
type SeedAnswer struct {
	Created bool `json:"created"`
}

// handleSeed offers the default dashboard (F7-28). The front asks once a reader is signed in; the
// first ask ever leaves a mark and, when no dashboard was created before it, stores the default with
// origin default. Every later ask -- after every dashboard was deleted too -- stores nothing. A
// write, so that a read never changes anything.
func (s *surface) handleSeed(writer http.ResponseWriter, request *http.Request) {
	document, err := DefaultDashboard()
	if err != nil {
		fail(writer, request, err)
		return
	}
	created, err := s.store().SeedDefault(request.Context(), config.KeyDashboardDefaultSeeded, document, s.Now().UTC().Unix())
	if err != nil {
		fail(writer, request, err)
		return
	}
	widget.WriteJSON(writer, http.StatusOK, SeedAnswer{Created: created})
}

func (s *surface) handleList(writer http.ResponseWriter, request *http.Request) {
	stored, err := s.store().Dashboards(request.Context())
	if err != nil {
		fail(writer, request, err)
		return
	}
	listing := Listing{Dashboards: []ListedDashboard{},
		Format: FormatVersions{Current: dashboard.CurrentFormatVersion, Minimum: dashboard.MinimumFormatVersion}}
	for _, row := range stored {
		listed := ListedDashboard{ID: row.ID, Position: row.Position, Revision: row.Revision, Origin: row.Origin,
			SourceName: row.SourceName, Canvases: []ListedCanvas{}}
		var outline struct {
			Title    any `json:"title"`
			Canvases []struct {
				ID    any `json:"id"`
				Title any `json:"title"`
			} `json:"canvases"`
		}
		if err := json.Unmarshal([]byte(row.Document), &outline); err != nil {
			// A stored document passed the structural validation; one that no longer decodes is a
			// defect of the store, not of the reader.
			fmt.Fprintf(os.Stderr, "opnview: web: the stored dashboard %d does not decode: %v\n", row.ID, err)
		}
		if title, ok := outline.Title.(string); ok {
			listed.Title = &title
		}
		for _, canvas := range outline.Canvases {
			id, _ := canvas.ID.(string)
			title, _ := canvas.Title.(string)
			listed.Canvases = append(listed.Canvases, ListedCanvas{ID: id, Title: title})
		}
		listing.Dashboards = append(listing.Dashboards, listed)
	}
	widget.WriteJSON(writer, http.StatusOK, listing)
}

func (s *surface) handleRead(writer http.ResponseWriter, request *http.Request) {
	id, ok := pathID(request)
	if !ok {
		refuse(writer, http.StatusNotFound, ConditionUnknownDashboard, "")
		return
	}
	stored, err := s.store().Dashboard(request.Context(), id)
	if errors.Is(err, dashboards.ErrUnknownDashboard) {
		refuse(writer, http.StatusNotFound, ConditionUnknownDashboard, "")
		return
	}
	if err != nil {
		fail(writer, request, err)
		return
	}
	writeWithDocument(writer, http.StatusOK, ReadAnswer{ID: stored.ID, Revision: stored.Revision,
		Origin: stored.Origin, SourceName: stored.SourceName}, stored.Document)
}

func (s *surface) handleCreate(writer http.ResponseWriter, request *http.Request) {
	members, ok := readEnvelope(writer, request)
	if !ok {
		return
	}
	document, ok := validDocument(writer, members)
	if !ok {
		return
	}
	origin := dashboards.OriginCreated
	if given, ok := textMember(members, "origin"); !ok {
		refuse(writer, http.StatusBadRequest, dashboard.ConditionMalformedBody, "origin")
		return
	} else if given != nil {
		origin = *given
	}
	if !validOrigin(origin) {
		refuse(writer, http.StatusBadRequest, dashboard.ConditionMalformedBody, "origin")
		return
	}
	source, ok := textMember(members, "source_name")
	if !ok {
		refuse(writer, http.StatusBadRequest, dashboard.ConditionMalformedBody, "source_name")
		return
	}
	if source != nil && *source == "" {
		source = nil
	}
	created, err := s.store().CreateDashboard(request.Context(), document, origin, source, s.Now().UTC().Unix())
	if err != nil {
		fail(writer, request, err)
		return
	}
	writeDashboard(writer, created)
}

func (s *surface) handleSave(writer http.ResponseWriter, request *http.Request) {
	id, ok := pathID(request)
	if !ok {
		refuse(writer, http.StatusNotFound, ConditionUnknownDashboard, "")
		return
	}
	members, ok := readEnvelope(writer, request)
	if !ok {
		return
	}
	revision, ok := wholeMember(members, "revision", 1)
	if !ok {
		refuse(writer, http.StatusBadRequest, dashboard.ConditionMalformedBody, "revision")
		return
	}
	document, ok := validDocument(writer, members)
	if !ok {
		return
	}
	saved, err := s.store().SaveDashboard(request.Context(), id, revision, document, s.Now().UTC().Unix())
	var conflict *dashboards.RevisionConflict
	switch {
	case errors.As(err, &conflict):
		widget.WriteJSON(writer, http.StatusConflict, Conflict{Condition: ConditionRevisionConflict,
			Revision: conflict.Current})
	case errors.Is(err, dashboards.ErrUnknownDashboard):
		refuse(writer, http.StatusNotFound, ConditionUnknownDashboard, "")
	case err != nil:
		fail(writer, request, err)
	default:
		writeDashboard(writer, saved)
	}
}

func (s *surface) handleDelete(writer http.ResponseWriter, request *http.Request) {
	id, ok := pathID(request)
	if !ok {
		refuse(writer, http.StatusNotFound, ConditionUnknownDashboard, "")
		return
	}
	deleted, err := s.store().DeleteDashboard(request.Context(), id)
	switch {
	case errors.Is(err, dashboards.ErrUnknownDashboard):
		refuse(writer, http.StatusNotFound, ConditionUnknownDashboard, "")
	case err != nil:
		fail(writer, request, err)
	default:
		writeDashboard(writer, deleted)
	}
}

// maxInstant is the schema's upper bound on an instant, 2100-01-01T00:00:00Z.
const maxInstant = 4102444800

func (s *surface) handleRestore(writer http.ResponseWriter, request *http.Request) {
	members, ok := readEnvelope(writer, request)
	if !ok {
		return
	}
	restored := dashboards.Dashboard{}
	for _, member := range []struct {
		key     string
		minimum int64
		target  *int64
	}{{"id", 1, &restored.ID}, {"position", 0, &restored.Position}, {"revision", 1, &restored.Revision},
		{"created_at", 0, &restored.CreatedAt}, {"updated_at", 0, &restored.UpdatedAt}} {
		value, ok := wholeMember(members, member.key, member.minimum)
		if !ok || (member.key == "created_at" || member.key == "updated_at") && value >= maxInstant {
			refuse(writer, http.StatusBadRequest, dashboard.ConditionMalformedBody, member.key)
			return
		}
		*member.target = value
	}
	if restored.UpdatedAt < restored.CreatedAt {
		refuse(writer, http.StatusBadRequest, dashboard.ConditionMalformedBody, "updated_at")
		return
	}
	origin, ok := textMember(members, "origin")
	if !ok || origin == nil || !validOrigin(*origin) {
		refuse(writer, http.StatusBadRequest, dashboard.ConditionMalformedBody, "origin")
		return
	}
	restored.Origin = *origin
	source, ok := textMember(members, "source_name")
	if !ok || (source != nil && *source == "") {
		refuse(writer, http.StatusBadRequest, dashboard.ConditionMalformedBody, "source_name")
		return
	}
	restored.SourceName = source
	document, ok := validDocument(writer, members)
	if !ok {
		return
	}
	restored.Document = document
	recreated, err := s.store().RestoreDashboard(request.Context(), restored)
	if err != nil {
		fail(writer, request, err)
		return
	}
	writeDashboard(writer, recreated)
}

func (s *surface) handleOrder(writer http.ResponseWriter, request *http.Request) {
	members, ok := readEnvelope(writer, request)
	if !ok {
		return
	}
	var raw []json.Number
	decoder := json.NewDecoder(bytes.NewReader(members["ids"]))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		refuse(writer, http.StatusBadRequest, dashboard.ConditionMalformedBody, "ids")
		return
	}
	ids := make([]int64, 0, len(raw))
	for _, number := range raw {
		value, err := strconv.ParseInt(number.String(), 10, 64)
		if err != nil || value <= 0 {
			refuse(writer, http.StatusBadRequest, dashboard.ConditionMalformedBody, "ids")
			return
		}
		ids = append(ids, value)
	}
	err := s.store().OrderDashboards(request.Context(), ids)
	switch {
	case errors.Is(err, dashboards.ErrUnknownDashboard):
		refuse(writer, http.StatusNotFound, ConditionUnknownDashboard, "ids")
		return
	case errors.Is(err, dashboards.ErrOrderIncomplete):
		refuse(writer, http.StatusBadRequest, dashboard.ConditionMalformedBody, "ids")
		return
	case err != nil:
		fail(writer, request, err)
		return
	}
	stored, err := s.store().Dashboards(request.Context())
	if err != nil {
		fail(writer, request, err)
		return
	}
	answer := Order{IDs: []int64{}}
	for _, row := range stored {
		answer.IDs = append(answer.IDs, row.ID)
	}
	widget.WriteJSON(writer, http.StatusOK, answer)
}
