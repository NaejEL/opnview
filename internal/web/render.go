package web

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"sort"
	"strings"
)

// The layout and the one static asset.
//
// BOTH ARE EMBEDDED, and that is the whole of the static-asset story: the binary
// serves its own copy of everything, so a page makes no request to any host. There
// is no build chain, no bundler, no CDN and no favicon fetch. The page bodies are the
// surfaces', embedded in their own packages and compiled inside this layout.
//
//go:embed template/layout.gohtml asset/*.css
var files embed.FS

// renderer holds one compiled template per page.
//
// EACH PAGE IS ITS OWN TEMPLATE SET, not one set with many bodies: Go's template
// namespace is flat, so several files each defining "body" would overwrite one
// another silently. Parsing the layout once per page costs a parse per page at start-up
// and removes the possibility of the wrong body being rendered.
type renderer struct {
	catalogue  Catalogue
	pages      map[string]*template.Template
	sources    map[string]string
	layout     string
	stylesheet []byte
}

// readLayout returns the layout every page is compiled inside.
func readLayout() (string, error) {
	layout, err := files.ReadFile("template/layout.gohtml")
	if err != nil {
		return "", fmt.Errorf("web: reading the layout: %w", err)
	}
	return string(layout), nil
}

// compilesInLayout reports why a page body does not compile inside the layout, or nil. The
// catalogue lookup is stubbed: compiling resolves no key.
func compilesInLayout(name, body string) error {
	layout, err := readLayout()
	if err != nil {
		return err
	}
	stub := template.FuncMap{"t": func(any) (string, error) { return "", nil }}
	compiled, err := template.New(name).Funcs(stub).Parse(layout)
	if err != nil {
		return err
	}
	_, err = compiled.Parse(body)
	return err
}

// newRenderer compiles every accepted surface's pages against one catalogue.
func newRenderer(catalogue Catalogue, accepted []vetted) (*renderer, error) {
	layout, err := readLayout()
	if err != nil {
		return nil, err
	}
	stylesheet, err := files.ReadFile("asset/style.css")
	if err != nil {
		return nil, fmt.Errorf("web: reading the stylesheet: %w", err)
	}

	built := &renderer{
		catalogue:  catalogue,
		pages:      map[string]*template.Template{},
		sources:    map[string]string{},
		layout:     layout,
		stylesheet: stylesheet,
	}
	for _, candidate := range accepted {
		for name, body := range candidate.templates {
			compiled, err := template.New(name).Funcs(built.functions()).Parse(layout)
			if err != nil {
				return nil, fmt.Errorf("web: compiling the layout for %s: %w", name, err)
			}
			if _, err := compiled.Parse(body); err != nil {
				return nil, fmt.Errorf("web: compiling the %s template: %w", name, err)
			}
			built.pages[name] = compiled
			built.sources[name] = body
		}
	}
	return built, nil
}

// functions is the template function map. There is exactly one function, and it
// is the catalogue lookup: a template that wants a string asks for a key.
func (r *renderer) functions() template.FuncMap {
	return template.FuncMap{
		"t": func(key any) (string, error) {
			switch typed := key.(type) {
			case MessageKey:
				return r.catalogue.translate(string(typed))
			case string:
				return r.catalogue.translate(typed)
			default:
				return "", fmt.Errorf("web: %v is not a catalogue key", key)
			}
		},
	}
}

// surfaceNamed is implemented by the data of every page, through the Base it embeds.
type surfaceNamed interface {
	setSurface(page string)
}

// render writes one page.
//
// IT RENDERS INTO A BUFFER FIRST. A template that fails halfway — a catalogue key
// that does not exist, say — would otherwise have already written a status and half
// a document, and the reader would get a broken page with HTTP 200 on it. Buffering
// makes a failed render a failure.
func (r *renderer) render(writer http.ResponseWriter, status int, page string, data any) error {
	compiled, present := r.pages[page]
	if !present {
		return fmt.Errorf("web: there is no page called %q", page)
	}
	// The page's own class, stamped here because this is the one function that knows
	// both the page name and the data. A handler setting it would be many handlers each
	// able to set it to something else.
	if named, ok := data.(surfaceNamed); ok {
		named.setSurface(page)
	}
	var buffer bytes.Buffer
	if err := compiled.ExecuteTemplate(&buffer, "layout", data); err != nil {
		return fmt.Errorf("web: rendering %s: %w", page, err)
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The pages carry a form token and a session's state, so a shared cache must
	// not keep them.
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	// No off-host origin is permitted to load, and nothing on these pages needs
	// one: the policy is the enforceable form of the rule the templates state.
	writer.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'self'; form-action 'self'; base-uri 'none'")
	// The forms carry credentials, so a referrer must not carry the path anywhere.
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.WriteHeader(status)
	_, err := buffer.WriteTo(writer)
	return err
}

// PageTemplates returns the source of the layout and of every page body this server
// compiles, by page name; the layout is under the name "layout". A test reads them to
// assert what no template may contain.
func (s *Server) PageTemplates() map[string]string {
	sources := map[string]string{"layout": s.renderer.layout}
	for name, body := range s.renderer.sources {
		sources[name] = body
	}
	return sources
}

// PageNames returns the name of every page this server draws, sorted.
func (s *Server) PageNames() []string {
	names := make([]string, 0, len(s.pages))
	for name := range s.pages {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// TemplateKeys returns every catalogue key the templates reach as a literal, sorted.
//
// It is here rather than in a test because the extraction has to agree with what
// the templates actually contain, and a test that reimplemented it could agree
// with itself and disagree with the templates.
func (s *Server) TemplateKeys() []string {
	found := map[string]struct{}{}
	for _, source := range s.PageTemplates() {
		for _, key := range literalKeysIn(source) {
			found[key] = struct{}{}
		}
	}
	keys := make([]string, 0, len(found))
	for key := range found {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// literalKeysIn finds every `{{t "…"}}` call in a template source. A `{{t .Field}}`
// call carries a MessageKey chosen by a handler and is covered by the surfaces' own key
// lists instead.
func literalKeysIn(source string) []string {
	var keys []string
	rest := source
	for {
		start := strings.Index(rest, `t "`)
		if start < 0 {
			return keys
		}
		rest = rest[start+len(`t "`):]
		end := strings.Index(rest, `"`)
		if end < 0 {
			return keys
		}
		keys = append(keys, rest[:end])
		rest = rest[end+1:]
	}
}

// TemplateSources returns the source of the layout, under the name "layout", and of every page
// template of every surface linked into this binary, by page name. A test reads them to assert
// what no template may contain; a server built over them compiles exactly these.
func TemplateSources() (map[string]string, error) {
	layout, err := readLayout()
	if err != nil {
		return nil, err
	}
	sources := map[string]string{"layout": layout}
	for _, surface := range RegisteredSurfaces() {
		for _, page := range surface.Pages {
			raw, err := fs.ReadFile(surface.Files, "template/"+page.Name+".gohtml")
			if err != nil {
				return nil, fmt.Errorf("web: the page %q of the surface %q: %w", page.Name, surface.Name, err)
			}
			sources[page.Name] = string(raw)
		}
	}
	return sources, nil
}

// TemplateKeys returns every catalogue key the layout and the page templates of every
// registered surface reach as a literal, sorted.
func TemplateKeys() ([]string, error) {
	sources, err := TemplateSources()
	if err != nil {
		return nil, err
	}
	found := map[string]struct{}{}
	for _, source := range sources {
		for _, key := range literalKeysIn(source) {
			found[key] = struct{}{}
		}
	}
	keys := make([]string, 0, len(found))
	for key := range found {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}
