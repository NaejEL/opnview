# Extending opnview

opnview is modular at every layer. **A kind of thing is a seam, and an implementation is
a directory behind it**, discovered at start: nothing that ships with the product is edited to add
one. This document is the whole procedure for the three things a third party adds most often. It is
written to be followed by somebody who has never read the source.

| You add | You create | You edit |
|---|---|---|
| a provider (a lease source, a lookup source, a log source...) | one plugin directory | nothing |
| a widget | one directory holding `manifest.json` and a query | nothing |
| a web surface | one directory holding a handler, a template and its catalogue keys | nothing |

"Nothing" is literal for the source tree; the two places a Go build needs a regenerated file are
named where they occur (`go generate ./...`), and a test fails, naming the command, when the generated
file is stale.

## Adding a provider

A **provider** is an implementation of a *kind*: a source of one sort of material the product reads
-- the filter log, the leases, the resolver's lookups, a geolocation database. Every provider is a
**plugin**: a directory holding a `plugin.json` manifest, a JavaScript script, its fixtures and its
tests, with no Go and no build. The built-in ones (`plugins/pf`, `plugins/kea`, `plugins/maxmind`...)
are written exactly as a third party writes one.

To add one, create `<data-dir>/plugins/<name>/` and restart the service. `docs/plugins.md` is the
whole procedure: the manifest and its providers (*The manifest*, *Providers*), the script and what
the host hands it (*The script*), its settings, its tests (`opnview plugin test`), and each kind's
contract (*Kinds*). Nothing that ships with the product is edited.

## Adding a widget

A **widget** is one question the product answers, drawn by one of the twelve shapes of
`docs/widget-catalogue.md`. **A widget is a directory** holding its manifest and its query. You can
add one in two places, with the same two files:

| Where | Who | When it takes effect | Logic beyond the query |
|---|---|---|---|
| `<data-dir>/widgets/<type>/` | an operator, on a running installation | at the next start of opnview | none: declarative |
| `<plugin>/widgets/<type>/` | a plugin's author: `<data-dir>/plugins/<plugin>/` dropped in, or `plugins/<plugin>/` built in | at the next start of opnview (a built-in: the next build) | none, or a script (`docs/plugins.md`, *Widgets*) |

Nothing that ships with the product is edited. The reference for every field, every shape's column
contract and every refusal is `docs/widget-authoring.md`.

### Adding an operator widget, from a blank directory

1. **Pick a type**: `snake_case`, unlike any built-in (`docs/widget-catalogue.md` lists them). It is
   the directory name, the `type` in the manifest and the last element of the API path
   `/api/widget/<type>`.
2. **Pick a shape** by the question: a ranking is `ranked_bars`, a value over time `time_series`, a
   share of a whole `donut`, a list of rows `table`, the latest events `feed`. The shape fixes the
   columns your query returns (`docs/widget-authoring.md`, *Shape contracts*).
3. **Create the directory** `<data-dir>/widgets/<type>/` and write `manifest.json` and `query.sql`
   into it.
4. **Restart opnview.** A widget that breaks a rule is refused with a named reason on the standard
   error (`operator widget not loaded: widget "<type>" refused (<code>): ...`), the service starts
   without it, and the other widgets are unaffected. A widget that loads is served at
   `/api/widget/<type>` with the same envelope, conditions and parameter handling as every built-in.

### A complete worked example

A ranking of the bytes the firewall logged in the window, by protocol. Create
`<data-dir>/widgets/example_bytes_by_protocol/` with these two files. (This page is tested: the test
`TestABrandNewWidgetCreatedOnlyByWritingFilesIsServedOverHTTP` reads the two blocks below out of this
document, writes them as the two files into an empty widgets directory, and serves the widget over
HTTP; a change here that breaks the widget fails that test.)

`manifest.json`:

<!-- example: manifest.json -->
```json
{
  "manifest_version": 1,
  "type": "example_bytes_by_protocol",
  "shape": "ranked_bars",
  "providers": ["firewall_log"],
  "primary": "firewall_log",
  "windowed": true,
  "no_domains": "none",
  "lower_bound": true,
  "measure": "logged",
  "parameters": [
    {"key": "period", "kind": "enum", "values": ["1h", "24h", "7d", "30d"], "default": "24h", "class": "data"},
    {"key": "limit", "kind": "integer", "min": 1, "default": 5, "class": "data"},
    {"key": "action", "kind": "enum", "values": ["any", "pass", "block"], "default": "any", "class": "data"}
  ],
  "queries": ["query.sql"]
}
```

`query.sql`:

<!-- example: query.sql -->
```sql
SELECT protocol AS key,
       protocol AS label,
       sum(packet_bytes) AS value
FROM flow
WHERE observed_at >= :from AND observed_at < :to
  AND (:action = 'any' OR action = :action)
GROUP BY protocol
```

What each part does:

* `"shape": "ranked_bars"` means the query returns `key` and `value` (and may return `label`). The
  engine ranks, applies `limit`, and reports what it dropped as a `remainder`, so the bars plus the
  remainder are the `total`.
* `"providers"` and `"primary"` bind the widget to the `firewall_log` kind: the response lists every
  registered provider of that kind with its availability, and the widget's condition is theirs
  (`unavailable` with nothing stored is *no data*, never a zero).
* `"windowed": true` with a `period` parameter gives the query `:from` and `:to`: the rolling window
  ending now. `limit` does not appear in the query: it is the engine's (a `LIMIT` in the query would
  drop members silently), while `action` is bound as `:action`, the text of the enum.
* `"lower_bound": true` with `"measure": "logged"` makes every figure `{"measure": "logged",
  "lower_bound": true, "value": n}`: opnview sees only what crosses the firewall.
* `"no_domains": "none"` says the widget carries no domain name. A widget that shows one must say
  `"whole"`, and the aggregate mode `no_domains` then withholds it entirely.
* The query is a single `SELECT`, bounded by an indexed time column. It runs on read-only
  connections; a query that writes, or holds a second statement, is refused at start.

Ask for it:

```
GET /api/widget/example_bytes_by_protocol?parameters={"action":"block","limit":3}
```

### Adding a widget a plugin carries

A widget that needs logic a shape cannot express is a widget plugin: the same manifest, its named
statements, and a JavaScript script, in `<plugin>/widgets/<type>/`. Dropped into
`<data-dir>/plugins/`, it is served at the next start; nothing is compiled. `docs/plugins.md`,
*Widgets*, is the procedure and the reference for the script and its host. The built-in widgets are
written this way, in `plugins/` in the repository: their statements are planned by
`sql/schema-checks.sh` at the 100 000- and 1 000 000-row seeds, and a built-in that breaks any rule
of `docs/widget-authoring.md` is a defect of the build: `widget.New` returns an error naming it and
the test suite fails.

## Adding a surface

A **surface** is one part of the interface: a page or a few, the routes that serve them, the
words they use. It is **a directory** below `internal/web/surfaces/`. The core (`internal/web`)
owns what every surface shares -- the server and its route table, sessions, the form token, the
layout and its top bar, the themes, the stylesheet, the string catalogue -- and hands a surface
what it may use through one interface, `web.Host`. A surface depends on `internal/web` and on
nothing in another surface, so a directory can be copied between installations.

The six surfaces that ship are the worked examples of every case: `sources` (a read-only page, a
tab and a top-bar indicator), `interfaces` (forms and refusals), `settings` and `collection`
(cards in one form), `account` (public pages and the key-file route) and `widgetapi` (routes
with no page at all).

### The directory

```
internal/web/surfaces/hello/
    surface.json            the marker: the surface's name (its directory's) and what it is
    surface.go              package hello: the registration and the handlers
    template/hello.gohtml   one file per page; the page's name is the file's base name
    locale/en.json          the surface's own catalogue keys, one file per language
```

Nothing else is required, and nothing outside the directory is edited. The surface's tests sit
beside it (`hello_test.go`, package `hello_test`) and drive it over HTTP through
`internal/web/webtest`.

### The procedure

1. **Create the directory** and its `surface.json`. The name must be the directory's name, and
   the description says in a sentence what the surface is.
2. **Write the template** for each page. A page is a `body` template drawn inside the layout:
   `{{define "body" -}} ... {{- end}}`. The data it reads is a pointer to a struct that embeds
   `web.Base`, so every field of the base (`.CSRFToken`, `.SignedIn`, `.Error`...) is available,
   and so are the surface's own. **No text is a literal**: a template asks the catalogue for
   every word, `{{t "hello.heading"}}`, and a test strips the tags and the actions of every
   template and fails when anything but whitespace is left.
3. **Write the catalogue file** `locale/en.json`: `{"language": "en", "strings": {...}}`. English
   is the source language. A key is unique across the product: a key another surface or the core
   defines is refused, so prefix yours with the surface's name. To reuse a word the layout and
   several surfaces share (`action.save`, `field.login`, the duration units), use the core's key
   (`internal/web/locale/en.json`) and do not redefine it. A second language is a second file,
   `locale/fr.json`, of the same shape.
4. **Write `surface.go`**: embed the three, register the surface in an `init()`, and write the
   handlers. A handler asks the host for the common part of its page (`NewBase`), adds its own
   data, and renders (`Render`); it answers a failure with `FailInternal`, which writes a status
   and a catalogue string and sends the detail to the console, never to the page.
5. **Declare each route's access level.** `AccessAuthenticated` turns away a request with no
   session (a redirect to sign-in, or to setup before the first account); `AccessPublic` is for
   the few pages a reader without a session needs; `AccessKeyFile` is the password reset's. A
   route with none is refused. The middleware does the rest: every `POST` must carry the form
   token (`{{.CSRFToken}}` in the form, name `csrf_token`) or it is refused before the handler
   runs. A handler never checks either.
6. **Link it into the binary**: run `go generate ./...`. It rewrites
   `internal/web/surfaces/surfaces_gen.go`, the one file that imports the surface directories --
   generated from the tree, never edited, and checked by `TestTheGeneratedLinkFileIsCurrent`,
   which fails, naming this command, when a directory was added without it.
7. **Test it** with `webtest.New(t)` (a server over a fresh database and a fake firewall),
   `h.CompleteSetup()` (an account, signed in) and `h.Get` / `h.Post`.

### A complete worked example

The surface below is real: it is `internal/web/webtest/examples/hello`, and
`internal/web/modularity_test.go` registers it, mounts it on a server, requests it over HTTP and
asserts that it is drawn inside the layout, with its own translated words, behind the sign-in,
with its tab in the top bar. `TestTheDocumentationShowsTheWorkedExampleVerbatim` fails when this
page and those files differ. The example lives beside the tests rather than below
`internal/web/surfaces` only so that it is never linked into the product; a shipped surface is
the same files in `internal/web/surfaces/<name>/`.

`surface.json`

```json
{
  "name": "hello",
  "description": "A worked example: one page that says how many accounts the installation has."
}
```

`template/hello.gohtml`

```
{{define "body" -}}
<section class="card">
<h1>{{t "hello.heading"}}</h1>
<p>{{t "hello.greeting"}} <span class="figure">{{.Accounts}}</span></p>
</section>
{{- end}}
```

`locale/en.json`

```json
{
  "language": "en",
  "strings": {
    "hello.title": "Hello",
    "hello.heading": "Hello",
    "hello.greeting": "Accounts on this installation"
  }
}
```

`surface.go`

```go
// Package hello is the worked example of docs/extending.md, "Adding a surface": one page,
// /hello, that says how many accounts the installation has. It is a complete surface -- a
// marker, a template, a catalogue file and a handler -- and it is exercised by
// internal/web/modularity_test.go, which imports it for its effect exactly as
// internal/web/surfaces imports the shipped ones. It lives here, and not below
// internal/web/surfaces, so that it is tested but never linked into the product.
package hello

import (
	"embed"
	"net/http"
	"strconv"

	"github.com/NaejEL/opnview/internal/web"
)

//go:embed template locale surface.json
var files embed.FS

// PathHello is where the page lives.
const PathHello = "/hello"

// The page and the catalogue keys its code chooses. The words are in locale/en.json.
const (
	pageHello                    = "hello"
	msgTitleHello web.MessageKey = "hello.title"
)

func init() {
	web.RegisterSurface(web.Surface{
		Name:  "hello",
		Files: files,
		Pages: []web.Page{{Name: pageHello, TitleKey: msgTitleHello}},
		Routes: func(host web.Host) []web.Route {
			s := &surface{host}
			return []web.Route{
				{Method: http.MethodGet, Pattern: PathHello, Access: web.AccessAuthenticated, Handler: s.handle},
			}
		},
		Tab:  &web.Tab{Page: pageHello, Path: PathHello, TitleKey: msgTitleHello, Order: 90},
		Keys: func() []web.MessageKey { return []web.MessageKey{msgTitleHello} },
	})
}

// surface is the handlers, over what the host grants.
type surface struct{ web.Host }

// view is everything the template may read: the common part, and the one figure.
type view struct {
	web.Base
	Accounts string
}

// handle draws the page.
func (s *surface) handle(writer http.ResponseWriter, request *http.Request) {
	base, err := s.NewBase(writer, request, web.SessionFrom(request), msgTitleHello)
	if err != nil {
		s.FailInternal(writer, request, err)
		return
	}
	count, err := s.Store().CountAccounts(request.Context())
	if err != nil {
		s.FailInternal(writer, request, err)
		return
	}
	built := view{Base: base, Accounts: strconv.Itoa(count)}
	if err := s.Render(writer, http.StatusOK, pageHello, &built); err != nil {
		s.FailInternal(writer, request, err)
	}
}
```

### What a surface receives

`web.Surface` (what the registration declares):

| Field | Meaning |
|---|---|
| `Name` | the surface's name, unique, the directory's |
| `Files` | an `fs.FS` holding `template/<page>.gohtml` and `locale/<language>.json` -- embed them |
| `Pages` | each page's `Name` (the template's base name, unique across the product), its `TitleKey`, and optionally `Refusal`, which builds the page's data when the core must draw it with a refusal on it (a missing form token) |
| `Routes` | called once with the host; returns the surface's `web.Route` rows (`Method`, `Pattern`, `Access`, `Handler`, and `Page`, the page a middleware refusal is drawn on) |
| `Tab` | the surface's link in the top bar: page, path, title key, order |
| `Indicator`, `IndicatorUnknown` | a figure or state drawn in the bar of every signed-in page, and the state shown when reading it fails |
| `Notices`, `Refusals` | the catalogue keys a redirect of the surface may carry in the flash cookie; the list is closed |
| `Keys` | every catalogue key the surface's Go code chooses, for the tests that hold the catalogue to its code |

`web.Host` (what a handler may ask for): `Store`, `Now`, `DataDir`, `Box`, `Credentials`,
`Client`, `SetupToken`, `Sessions`, `PasswordParams`, `DecoyPassword`, `LiveSettings`,
`Collector`, `GeoIP`, `PublicSuffix`, `Widgets`, `Configuration`, `ApplyConfiguration`,
`AccountExists`, `ReadTheme`, `NewBase`, `Render`, `RenderRefusal`, `FailInternal`,
`RedirectWithNotice`, `RedirectWithError`, `RefuseSetupClosed`. A surface cannot reach the route
table, the renderer or the middleware. `web.SessionFrom(request)` returns the session the
middleware resolved.

The shared building blocks of a page with settings are in the core, and are the same on every
surface that has them: `web.SettingField`, `web.DurationField`, `web.TypedDuration`,
`web.SpelledDuration`, `web.ChoiceOption`, `web.Instant`, the `setting-field` template of the
layout, `web.AcceptedByLoad` (every save is validated by `config.Load` itself), and the keys
composed from a registry row (`web.KindLabelKey`, `web.ProviderLabelKey`, `web.GapReasonKey`).

### When a surface is refused

A surface that cannot be served is left out, the server starts without it, and the reason is
printed on the console and returned by `Server.RefusedSurfaces()`. The reasons are specific:
the surface declares no name; its name is taken; a page has no name or no title key; a page has
no template; a template does not compile; its tab names a page it does not declare, or no path;
a catalogue file is malformed; a catalogue key is already defined by the core or by another
surface; a route has no method or path, no access level, no handler, repeats a row, or is
refused by the router; building its routes panicked.

### What is not "only a directory", and why

Go links packages, not directories, so exactly one thing outside the new directory changes, and
it is generated rather than written: `internal/web/surfaces/surfaces_gen.go`, by
`go generate ./...`. Nothing is added to any route table, tab list, catalogue file, template
list or switch; a test (`TestNoShippedFileKeepsAListOfSurfacesByHand`) fails when a shipped file
lists two surfaces by name. The next cycle turns these directories into a plugin format loaded
from the data directory without recompiling; a surface that depends only on `web.Host` and its
own files, as these do, moves there unchanged.
