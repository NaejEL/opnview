# 0016 — Named canvases, widgets as plugins, themes and languages as files

Status: Accepted
Decided by: the maintainer, step 3 (the mockup, October 2026) and the recorded preferences in `docs/ui-references.md`

## Context

opnview **is** the view. The front deserves the same documentary rigour as the
API: prior art was surveyed (`docs/ui-references.md`), the maintainer recorded
his preferences per question, and a single-file mockup
(`docs/mockups/canvas-mockup.html`) is the thing every screen is judged
against.

## Decision

- **Named canvases, not fixed screens.** The user creates canvases and composes
  each from widgets, moving between them with **tabs, not menus**. No view is
  reachable only through a menu inside a menu.
- **Dashboards are code**: JSON and/or YAML, portable between installations,
  importable and exportable (`docs/dashboard-format.md`).
- **A widget is a plugin directory** holding its manifest, its query and its
  renderer. The twelve widget shapes and the presets that bind data to them are
  in `docs/widget-catalogue.md`. A purely front widget (a gauge, a curve
  computed from what the API already serves) is a renderer and nothing else.
- **A theme is a file; a language is a file.** Somebody who has never read the
  source adds either from the documentation alone; if adding one means editing a
  file that ships with the product, the rule is not met.
- **No user-visible string is a literal.** Every one comes from the string
  catalogue; English is the source language; the user switches language in the
  interface.
- **Palette.** The maintainer's industrial palette is the default; Tokyo Night,
  Dracula, Nord, Rosé Pine and Catppuccin are offered **at the values published
  on https://lequellec.xyz**, reproduced in `docs/ui-references.md`. That site,
  not each palette's upstream, is the authority; upstream values must not be
  substituted. On first launch the theme follows the operating system; the
  override persists.
- **A generic settings page** is drawn from the settings each plugin declares;
  no plugin writes its own screen.
- The front is a JavaScript application embedded in the binary, served by it.

## Consequences

- Three documents are binding on any interface work: `docs/ui-references.md`,
  `docs/widget-catalogue.md`, `docs/dashboard-format.md`. A design decision that
  contradicts one of them without saying so is a defect.
- No half-baked preview: the maintainer is shown only work that runs on live
  data and matches the mockup.
