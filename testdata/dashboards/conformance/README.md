# Dashboard conformance fixtures

The shared fixtures of the two structural validators of a dashboard document
(`specs/SPEC-step-7-frontend.md`, A7, D13 and F7-AC10): the authoritative one in Go,
`internal/dashboard`, and the editor's mirror of it, `front/core/document.js`. Both are run
over every case of `cases.json`, and a test fails when either disagrees with the expectation
or with the other.

## Provenance

Every document here is **synthesised** for these tests and describes no real installation.
Canvas and widget ids, titles and labels are invented; the interface identifiers (`opt3`,
`opt5`) are example OPNsense identifiers of the kind docs/dashboard-format.md's worked example
uses; host names are under `example.org` (RFC 2606); the one MAC is in the documentation range
`00:00:5e:00:53:00/24` (RFC 7042). No value was taken from a running system.

## Format

`cases.json` is an array of cases, each:

| Member | Meaning |
|---|---|
| `name` | the case's name, unique |
| `file` | a document under `documents/`, read as bytes; or |
| `text` | the document's text inline, for a one-line case |
| `pad_to_bytes` | optional: the text is followed by spaces up to this many bytes (still the same JSON value), so the size bound is exercised without committing a megabyte |
| `expect` | `null` for a document a save stores, or the refusal: `condition`, `canvas_index` and `widget_index` (null outside a canvas or a widget), `field`, `path`, and for a format version refusal `file_version`, `current` and `minimum` |

A refusal is the **first** structural defect in document order: the format version, then the
canvases list, then each canvas in order (its id, the id's shape, a duplicate id, its title, its
scope, its widgets list), then each of its widgets in order (its id, a duplicate id, its type or
shape, its placement and each of `x`, `y`, `w`, `h`, its parameters). Inside a widget's
`parameters` or a canvas's `scope`, members are visited in sorted order and elements in order.
