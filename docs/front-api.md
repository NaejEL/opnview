# Front API

The routes the front -- the interface a browser draws (`docs/front-plugins.md`) -- reads beside the
widget routes: its registry, its strings, the appearance, the widget registry, the reference
candidates and the dashboards (`specs/SPEC-step-7-frontend.md`, A4 to A8). Every answer is JSON;
every write is a JSON write (`docs/widget-api.md`, *Write endpoints*).

## The shell, the assets and the front's registry

- **The shell.** `GET` on every path no other route answers -- `/`, `/canvas/{dashboard}/{canvas}`,
  `/settings`, `/collection`, `/interfaces`, `/sources`, `/people`, `/blocklists`, `/setup`,
  `/sign-in`, `/recover` among them -- answers the one shell document. It is public and carries no
  data; the front reads the session and routes itself.
- **`GET /front/{path}`**, public, answers an asset the front's index declares: the core's scripts
  and stylesheet, the generated palette stylesheet `palettes.css`, each theme file, the shape
  renderers, each widget's renderer entry, the basemap. Any other path answers `404`
  `{"condition": "unknown_asset"}`. The shell and every asset carry the policy of
  `docs/front-plugins.md`, *What the browser is served*, `nosniff`, `Cache-Control: no-cache` and a
  content `ETag`.
- **`GET /api/{anything}`** that no route answers is `404` `{"condition": "unknown_route"}`, never
  the shell.
- **`GET /api/front`**, public: `{themes, languages, assets}`. Each theme is `{id, name, url,
  origin}`, `name` keyed by language code with English always present, `url` the theme file's
  asset; each language `{code, name, origin}`, `name` the language's name in itself; `assets` is
  `{scripts, stylesheets}`, the URLs the shell loads, in order. `origin` is `builtin` or
  `data_directory`.

## Strings

**`GET /api/strings/{language}`**, public, answers `{language, strings}`: the merged catalogue of
the core, the surfaces, every loaded plugin, every widget directory and, for a language of the data
directory, its file. It carries an `ETag`. A key an installed language leaves out is not filled
from English here: the front falls back to English itself, then to the key marked as missing. A
language not installed is `404` `{"condition": "language_unknown"}`.

## Appearance

The appearance replaces the theme setting: `POST /api/settings/theme` and the `theme` member of
`GET /api/settings` are gone.

- **`GET /api/appearance`**, public: `{palette, mode, language, default_palette, palettes, modes,
  languages}`. The first three are the stored values, each `null` while never set -- the
  first-launch behaviour: the mode follows the operating system, the palette is `default_palette`
  (`industrial`), and the language is the browser's first installed one, else English. `palettes`
  are `{id, name}`, `modes` are `system`, `light` and `dark`, and `languages` are the entries of
  `GET /api/front`: the language picker's options. The picker is shown only when two or more are
  installed.
- **`POST /api/appearance`**, authenticated, takes any of `palette`, `mode` and `language`; a
  member set to `null` clears its value. A value not offered is refused `400` with
  `{"condition", "field"}`, the condition `palette_unknown`, `mode_unknown` or `language_unknown`,
  and nothing is written; any other member is `malformed_body`. It answers as the `GET`.

## The widget registry

**`GET /api/widgets`**, authenticated, answers `{widgets, shapes, themes, languages, refused}`:

| Member | What it holds |
|---|---|
| `widgets` | every loaded widget, served types and front-only types alike: `{type, shape, origin, plugin, backend, windowed, providers, primary, parameters, references, renderer_url, title_key, question_key, missing_strings}`. `parameters` is exactly the manifest's, plus `volume_measure` on a type declaring `volumes`; `backend` is false for a front-only widget; `renderer_url` is the widget's own renderer, or `null` when its shape's renderer draws it; `title_key` and `question_key` are its catalogue keys; `missing_strings` lists the keys of its own namespace a widget of the data directory does not carry |
| `shapes` | the core's shape renderers, `{shape, url}` |
| `themes`, `languages` | every loaded theme `{id, origin, file}` and language `{code, origin, file, missing_strings}` |
| `refused` | every widget and front part left out at start, `{part, id, code, path, detail}`, with the closed codes of `docs/front-plugins.md` and `docs/widget-authoring.md`, *Refusals* |

A front-only type is never served: `GET /api/widget/{type}` answers it `404`
`{"condition": "front_only_widget", "type": ...}`.

## References

- **`GET /api/references/{kind}`**, authenticated, lists the candidates of one reference kind of
  `docs/dashboard-format.md`, *The reference object*: `{kind, by, q, limit, candidates,
  remainder}`. `by` are the kind's accepted keys; each candidate carries `keys` -- every accepted
  `by` key with the value a reference by it would carry, or `null` -- a display `label` (data,
  never matched on), `id` for a kind naming a row, and `referenceable`. A client whose only
  identity is its address in an interface is listed with `referenceable: false` and `reason:
  "address_in_interface"`; a randomised MAC carries `unstable_identity`. The parameter `q`
  filters; at most `limit` (200) candidates are answered and `remainder` counts the rest. A `site`
  listing under `no_domains` answers `condition: "withheld_by_aggregate_mode"` and no candidate.
  An unknown kind is `404` `{"condition": "unknown_kind", "kind"}`.
- **`GET /api/references/resolve?references=<JSON array>`**, authenticated, resolves up to 200
  reference objects by the widget API's own rules (`docs/widget-api.md`, *References*) and answers `{aggregate_mode,
  references}`, one entry per object in order: `{index, kind, by, value, label, state}`, `state`
  one of `resolved`, `no_match`, `ambiguous`, `unknown_kind`, `unknown_by`,
  `withheld_by_aggregate_mode` or `not_a_reference_object`. Under `no_domains` a site's `value`
  and `label` are left out. A parameter that is not one JSON array is `400`
  `{"condition": "malformed_parameters", "reason"}` (`not_json`, `not_an_array`,
  `trailing_data`); more than 200 objects is `too_many_references`. A widget's own parameters
  are resolved through its envelope's `unresolved_references`; this route is the canvas scope's,
  and there is no second resolver.

## Dashboards

A dashboard is one document of `docs/dashboard-format.md`, stored whole in the `dashboard` table:
`id`, `position`, `document` (type `json`, so members keep their order and unknown keys their
bytes), `revision`, `origin` (`created`, `imported` or `default`), `source_name` (the imported
file's name, or `null`), `created_at` and `updated_at` (epoch seconds). Every route is
authenticated; every write is a JSON write. A document is answered as the text it was stored with,
byte for byte.

| Route | Body | Answer |
|---|---|---|
| `GET /api/dashboards` | | `{dashboards: [{id, position, title, revision, origin, source_name, canvases: [{id, title}]}], format: {current: 1, minimum: 1}}`, in position order |
| `GET /api/dashboards/{id}` | | `{id, revision, origin, source_name, document}` |
| `POST /api/dashboards` | `{document, origin, source_name}`; `origin` defaults to `created` | the dashboard created: `{id, position, revision, origin, source_name, created_at, updated_at, document}` |
| `POST /api/dashboards/{id}` | `{revision, document}` | the dashboard saved at the next revision; a `revision` that is not the stored one is `409` `{condition: "revision_conflict", revision}`, the stored one |
| `POST /api/dashboards/{id}/delete` | `{}` | the whole deleted dashboard, for an undo |
| `POST /api/dashboards/restore` | a deletion's answer, unchanged | the dashboard recreated at its own position, revision, origin and instants, under its own id when no dashboard holds it, else under a new one |
| `POST /api/dashboards/order` | `{ids}`, every dashboard's id once | `{ids}`, the new order |
| `POST /api/dashboards/default` | `{}` | `{created}`: whether this call stored the default dashboard |

**Structural validation is fail-closed** and runs on every document written, following
`docs/dashboard-format.md`, *Import validation*: a refusal is `400` `{condition, canvas_index,
widget_index, field, path}` and nothing is written. The codes are `malformed_body`,
`format_version_missing`, `format_version_too_new` and `format_version_too_old` (each with
`file_version`, `current` and `minimum`), `canvases_missing`, `canvases_empty`,
`canvas_id_missing`, `canvas_id_malformed`, `canvas_title_missing`, `widget_id_missing`,
`widget_type_and_shape_missing`, `placement_missing`, `placement_not_integer`, `duplicate_id`,
`nesting_too_deep` and `document_too_large` (above 1 MiB). An id naming no dashboard is `404`
`unknown_dashboard`. **Referential validity is never checked on save**: an unknown type, an
unresolved reference or an unknown key is stored as written.

**The default dashboard is offered once, ever** (F7-28). The front asks
`POST /api/dashboards/default` once a reader is signed in. The first ask ever claims a mark in the
settings and, when no dashboard is stored, stores the built-in default
(`front/dashboards/default.json`, portable references only) with origin `default`; every later ask
stores nothing, so deleting every dashboard never brings the default back. It is a write because
a read never changes anything.

