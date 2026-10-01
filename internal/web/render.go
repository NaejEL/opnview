package web

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strings"
)

// The templates and the one static asset.
//
// BOTH ARE EMBEDDED, and that is the whole of the static-asset story: the binary
// serves its own copy of everything, so a page makes no request to any host. There
// is no build chain, no bundler, no CDN and no favicon fetch.
//
//go:embed template/*.gohtml asset/*.css
var files embed.FS

// The page names. Each is one body template; the layout is shared.
const (
	pageSetup    = "setup"
	pageSignIn   = "signin"
	pageSettings = "settings"
	pageRecover  = "recover"
)

// pageNames is every page, so a test can render each one and assert what it does
// and does not contain.
func pageNames() []string {
	return []string{pageSetup, pageSignIn, pageSettings, pageRecover}
}

// renderer holds one compiled template per page.
//
// EACH PAGE IS ITS OWN TEMPLATE SET, not one set with four bodies: Go's template
// namespace is flat, so four files each defining "body" would overwrite one
// another silently. Parsing the layout once per page costs four parses at start-up
// and removes the possibility of the wrong body being rendered.
type renderer struct {
	catalogue  Catalogue
	pages      map[string]*template.Template
	stylesheet []byte
}

// newRenderer compiles every page against one catalogue.
func newRenderer(catalogue Catalogue) (*renderer, error) {
	layout, err := files.ReadFile("template/layout.gohtml")
	if err != nil {
		return nil, fmt.Errorf("web: reading the layout: %w", err)
	}
	stylesheet, err := files.ReadFile("asset/style.css")
	if err != nil {
		return nil, fmt.Errorf("web: reading the stylesheet: %w", err)
	}

	built := &renderer{
		catalogue:  catalogue,
		pages:      map[string]*template.Template{},
		stylesheet: stylesheet,
	}
	for _, name := range pageNames() {
		body, err := files.ReadFile("template/" + name + ".gohtml")
		if err != nil {
			return nil, fmt.Errorf("web: reading the %s template: %w", name, err)
		}
		compiled, err := template.New(name).Funcs(built.functions()).Parse(string(layout))
		if err != nil {
			return nil, fmt.Errorf("web: compiling the layout for %s: %w", name, err)
		}
		if _, err := compiled.Parse(string(body)); err != nil {
			return nil, fmt.Errorf("web: compiling the %s template: %w", name, err)
		}
		built.pages[name] = compiled
	}
	return built, nil
}

// functions is the template function map. There is exactly one function, and it
// is the catalogue lookup: a template that wants a string asks for a key.
func (r *renderer) functions() template.FuncMap {
	return template.FuncMap{
		"t": func(key any) (string, error) {
			switch typed := key.(type) {
			case messageKey:
				return r.catalogue.translate(string(typed))
			case string:
				return r.catalogue.translate(typed)
			default:
				return "", fmt.Errorf("web: %v is not a catalogue key", key)
			}
		},
	}
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

// templateKeys returns every catalogue key the templates reach, sorted.
//
// It is here rather than in a test because the extraction has to agree with what
// the templates actually contain, and a test that reimplemented it could agree
// with itself and disagree with the templates.
func templateKeys() ([]string, error) {
	entries, err := files.ReadDir("template")
	if err != nil {
		return nil, fmt.Errorf("web: listing the templates: %w", err)
	}
	found := map[string]struct{}{}
	for _, entry := range entries {
		raw, err := files.ReadFile("template/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("web: reading %s: %w", entry.Name(), err)
		}
		for _, key := range literalKeysIn(string(raw)) {
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

// literalKeysIn finds every `{{t "…"}}` call in a template source. A `{{t .Field}}`
// call carries a messageKey chosen by a handler and is covered by
// handlerMessageKeys instead.
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
