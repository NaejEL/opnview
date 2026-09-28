# Interface and dashboard-format research

Step-3 preparation for `ROADMAP.md`. Fifteen products were studied: eight for
how a modular dashboard actually works, seven for what belongs inside the
widgets, and one palette family for the visual language. A fourth family, the
moodboard, collects interfaces worth stealing from wherever they came from.
Nothing here is a mockup and nothing here chooses the final look; this document
is the written reference the mockups will be judged against.

Companion documents produced in the same cycle: `docs/widget-catalogue.md`
(what the widgets are) and `docs/dashboard-format.md` (how a dashboard is
described as a file).

## The product as decided

Five decisions are settled. This research establishes how to do them well; it
does not reopen them, and no section below contradicts one.

1. **Named canvases, not fixed screens.** The user creates as many dashboards
   as they want and fills each with widgets they add, place and size. Tabs
   replace menus. There is no fixed seven-screen structure and no view reachable
   only through a submenu.
2. **Everything is describable as code, in JSON and/or YAML** — both, and
   never XML.
3. **Dashboards are portable.** A dashboard exported from one installation
   imports into another. Where the importing installation has no data for a
   widget, that widget renders as an explained empty state — never an error,
   never a fabricated zero.
4. **Palettes.** The maintainer's industrial palette is the default. Tokyo
   Night, Dracula, Nord, Rosé Pine and Catppuccin are offered as named options
   **at the values published on the maintainer's own site,
   https://lequellec.xyz** — that site, not each palette's upstream project, is
   the authoritative source for what those five names mean in `opnview`. See
   *The five named palettes* below, which records both and says which governs.
5. **Theme on first launch follows the operating system**, user-overridable
   afterwards. This replaces the ROADMAP's "light by default, dark never the
   default"; see *Proposed ROADMAP amendment*.

A sixth point is a maintainer decision taken during this cycle and recorded here
because the whole of *Conclusions for opnview* rests on it: `opnview` does
**not** attempt to invent a universal identifier that resolves magically on
import. It accepts that an imported file carries references the local
installation may not have, and makes each unresolved reference **visible and
repairable** through two coordinated surfaces — a JSON/YAML editor that
highlights in place the exact lines whose reference failed, and a no-code
selector whose dropdowns are populated from what is actually available on this
installation. Both edit the same model. Family A's *UI, file, or both* and
*Portability* sub-headings are the prior art for exactly that pair, and they are
why the decision is defensible rather than merely convenient.

## Family C — the visual language

This section **records options; it does not choose one.** Naming `opnview`'s own
semantic tokens, picking an accent, choosing typography and iconography and
deciding the final look are design decisions belonging to the mockup cycle. Every
colour value below is a **record of an external source, reproduced so the
document remains checkable** — none of it is a stylesheet for `opnview`, and no
rule here is intended for `opnview`'s own CSS.

**The language these palettes share is angular, dense and high-contrast.** The
industrial palette's corner radius is `0.25rem` — four pixels at a default root
size, which reads as a chamfer rather than a curve. Its light background is a
near-white grey rather than white, its dark background a near-black grey rather
than black, and its text sits at the far end of the contrast range in both. The
five named palettes are, without exception, developer colour schemes designed
for dense text on a dark ground. **This is not the rounded, light, airy UniFi
aesthetic that `ROADMAP.md` step 3 specifies**, and the two cannot both be the
reference. That contradiction is recorded here and carried into *Proposed
ROADMAP amendment*; the maintainer has since resolved it in favour of the
angular language (see *The maintainer's recorded preferences*), but this section
still records rather than decides.

### The industrial palette

Source: `C:\Users\fuzzz\Downloads\maestro-espidf-components-main\maestro-espidf-components-main\tb_http_server\www\style.css`,
an absolute path outside this repository and outside the container bind mount.
*Scope and method* explains why it is cited that way and why every property is
reproduced rather than referenced. The file declares its own intent in two
comments: "Industrial high-contrast palette" for the light set and "Industrial
low-light high-contrast palette" for the dark set.

**Non-colour properties.** These are declared once, on `:root`, and are not
overridden in the dark block.

| Custom property | Value |
|---|---|
| `--font-family` | `-apple-system, BlinkMacSystemFont, "Segoe UI", "Roboto", "Oxygen", "Ubuntu", "Cantarell", "Fira Sans", "Droid Sans", "Helvetica Neue", Arial, sans-serif` |
| `--font-mono` | `"SF Mono", "Monaco", "Inconsolata", "Fira Mono", "Droid Sans Mono", "Source Code Pro", "Consolas", "Liberation Mono", monospace` |
| `--line-height` | `1.6` |
| `--font-weight` | `500` |
| `--font-weight-bold` | `700` |
| `--font-size-base` | `1rem` |
| `--spacing` | `1rem` |
| `--spacing-sm` | `0.5rem` |
| `--spacing-md` | `1rem` |
| `--spacing-lg` | `2rem` |
| **`--border-radius`** | **`0.25rem`** |
| `--border-width` | `1px` |
| `--border-width-thick` | `0.125rem` |

Note the font stack: **entirely system fonts**, with no web font anywhere. That
is not incidental — it is exactly what `ROADMAP.md`'s "no resource loaded from a
CDN" rule requires, and it means the industrial palette can be adopted whole
without adding a single byte of downloaded typography. The body weight of `500`
rather than `400` is the other deliberate choice: the file's own comment calls
it "Industrial bold system fonts for maximum readability".

**Colour and effect properties**, light and dark. Every property in the file is
listed; the dark column is the value from the `@media (prefers-color-scheme:
dark)` block, and a value identical in both is shown in both columns rather than
elided, so the table can be diffed against the source mechanically.

| Custom property | Light | Dark |
|---|---|---|
| `--bg-primary` | `#f8f9fa` | `#1c1c1e` |
| `--bg-secondary` | `#e9ecef` | `#2c2c2e` |
| `--text-primary` | `#212529` | `#f5f5f7` |
| `--text-secondary` | `#495057` | `#d1d1d6` |
| `--text-muted` | `#6c757d` | `#8e8e93` |
| `--border-color` | `#ced4da` | `#48484a` |
| `--input-bg` | `#ffffff` | `#2c2c2e` |
| `--input-border` | `#adb5bd` | `#636366` |
| `--button-bg` | `#2c5f8d` | `#3a7ca5` |
| `--button-hover-bg` | `#1e4164` | `#5a9cc8` |
| `--button-text` | `#ffffff` | `#ffffff` |
| `--button-color` | `#ffffff` | `#ffffff` |
| `--link-color` | `#2c5f8d` | `#5a9cc8` |
| `--link-hover` | `#1e4164` | `#7eb8db` |
| `--accent-focus` | `rgba(44, 95, 141, 0.25)` | `rgba(58, 124, 165, 0.35)` |
| `--status-success` | `#28a745` | `#30d158` |
| `--status-error` | `#dc3545` | `#ff453a` |
| `--status-warning` | `#fd7e14` | `#ff9f0a` |
| `--status-error-dark` | `#c0392b` | `#c0392b` |
| `--section-header-bg` | `#495057` | `#5a8fab` |
| `--section-header-border` | `#1e4164` | `#3a7ca5` |
| `--on-accent` | `#ffffff` | `#ffffff` |
| `--shadow-inset` | `rgba(0, 0, 0, 0.06)` | `rgba(0, 0, 0, 0.5)` |
| `--shadow-md` | `rgba(0, 0, 0, 0.15)` | `rgba(0, 0, 0, 0.35)` |
| `--shadow-lg` | `rgba(0, 0, 0, 0.3)` | `rgba(0, 0, 0, 0.45)` |
| `--shadow-lg-hover` | `rgba(0, 0, 0, 0.2)` | `rgba(0, 0, 0, 0.55)` |
| `--overlay-bg` | `rgba(0, 0, 0, 0.7)` | `rgba(0, 0, 0, 0.75)` |
| `--modal-surface-overlay` | `rgba(0, 0, 0, 0.2)` | `rgba(0, 0, 0, 0.35)` |
| `--spinner-border` | `rgba(255, 255, 255, 0.3)` | `rgba(255, 255, 255, 0.35)` |
| `--pico-red` | `#ee402e` | `#ee402e` |
| `--pico-green` | `#62af9a` | `#62af9a` |
| `--pico-orange` | `#f0a844` | `#f0a844` |
| `--ota-gradient-start` | `#667eea` | `#667eea` |
| `--ota-gradient-end` | `#764ba2` | `#764ba2` |
| `--ota-progress-bg` | `#e3f2fd` | `rgba(227, 242, 253, 0.18)` |
| `--counter-safe` | `#666666` | `#a1a1aa` |
| `--counter-warn` | `#f59e0b` | `#fbbf24` |
| `--counter-critical` | `#ef4444` | `#f87171` |
| `--notice-info` | `#3b82f6` | `#60a5fa` |
| `--notice-success` | `#10b981` | `#34d399` |
| `--notice-error` | `#ef4444` | `#f87171` |
| `--notice-info-bg` | `rgba(59, 130, 246, 0.1)` | `rgba(96, 165, 250, 0.12)` |
| `--notice-success-bg` | `rgba(16, 185, 129, 0.1)` | `rgba(52, 211, 153, 0.12)` |
| `--notice-error-bg` | `rgba(239, 68, 68, 0.1)` | `rgba(248, 113, 113, 0.12)` |
| `--modal-surface` | `#f2f4f6` | `#1f2328` |
| `--modal-surface-alt` | `#ffffff` | `#24282f` |
| `--modal-row-bg` | `#ffffff` | `#262b33` |
| `--modal-row-border` | `#d8dee4` | `#3a414c` |
| `--button-secondary-bg` | `#f1f3f5` | `#2b313a` |
| `--button-secondary-text` | `#334155` | `#e5e7eb` |
| `--button-secondary-border` | `#cbd5e1` | `#3a414c` |
| `--button-cancel-bg` | `#f8f9fb` | `#232831` |
| `--button-cancel-text` | `#475569` | `#d1d5db` |
| `--button-cancel-border` | `#d7dee7` | `#3a414c` |
| `--button-cancel-hover-bg` | `#eef2f6` | `#2b313a` |
| `--button-cancel-hover-text` | `#334155` | `#f3f4f6` |
| `--log-level-warn-text` | `#111111` | `#111111` |
| `--log-level-debug-bg` | `#6b7280` | `#6b7280` |
| `--log-level-raw-bg` | `#64748b` | `#64748b` |
| `--log-highlight-bg` | `rgba(255, 193, 7, 0.35)` | `rgba(255, 193, 7, 0.35)` |
| `--sse-connected-color` | `#4caf50` | `#66bb6a` |
| `--sse-connected-shadow` | `rgba(76, 175, 80, 0.6)` | `rgba(102, 187, 106, 0.6)` |
| `--sse-disconnected-color` | `#f44336` | `#ef5350` |
| `--sse-disconnected-shadow` | `rgba(244, 67, 54, 0.6)` | `rgba(239, 83, 80, 0.6)` |

**The semantic status colours**, separated out because they are the ones a
network tool uses constantly and the ones a later cycle will have to map onto
`opnview`'s own vocabulary:

| Meaning | Light | Dark |
|---|---|---|
| Success / healthy | `--status-success` `#28a745` | `#30d158` |
| Error / blocked | `--status-error` `#dc3545` | `#ff453a` |
| Warning / degraded | `--status-warning` `#fd7e14` | `#ff9f0a` |
| Severe error | `--status-error-dark` `#c0392b` | `#c0392b` |
| Counter, safe | `--counter-safe` `#666666` | `#a1a1aa` |
| Counter, warning | `--counter-warn` `#f59e0b` | `#fbbf24` |
| Counter, critical | `--counter-critical` `#ef4444` | `#f87171` |
| Notice, informational | `--notice-info` `#3b82f6` | `#60a5fa` |
| Notice, success | `--notice-success` `#10b981` | `#34d399` |
| Notice, error | `--notice-error` `#ef4444` | `#f87171` |
| Live connection up | `--sse-connected-color` `#4caf50` | `#66bb6a` |
| Live connection down | `--sse-disconnected-color` `#f44336` | `#ef5350` |

Two observations, recorded as observations rather than as recommendations.
First, the palette has **three overlapping red vocabularies** (`--status-error`,
`--counter-critical`, `--notice-error`, plus `--pico-red`) and **three greens**;
whatever `opnview` adopts will have to pick one per meaning rather than carrying
all of them. Second, the file uses the OS-preference media query and **has no
manual override mechanism at all** — it follows the operating system and offers
the user no switch. `opnview`'s settled decision 5 is *follow the OS on first
launch, user-overridable afterwards*, which is a superset: the same default,
plus a control this file does not have.

**Layout: navigation bar, sidebar, main.** The file's own layout, reproduced as
a record because it is the shape the palette was designed for.

| Element | Rules, verbatim in substance |
|---|---|
| `nav` | `position: fixed; top: 0; left: 0; right: 0; height: 60px; z-index: 200; background-color: var(--bg-secondary); padding: var(--spacing-sm) var(--spacing); border-bottom: 1px solid var(--border-color)` |
| `.nav-wrapper` | `display: flex; align-items: center; justify-content: flex-start; gap: var(--spacing); height: 100%` |
| `.page-layout` | `display: flex; min-height: calc(100vh - 60px)` |
| `.sidebar` | `width: 200px; position: fixed; left: 0; top: 60px; height: calc(100vh - 60px); overflow-y: auto; z-index: 100; background-color: var(--bg-secondary); border-right: 1px solid var(--border-color); padding: var(--spacing); display: flex; flex-direction: column; gap: var(--spacing-sm); transition: transform 0.3s ease` |
| `.sidebar.hidden` | `transform: translateX(-100%)` |
| `.sidebar-button` | `background-color: var(--bg-primary); border: 1px solid var(--border-color); color: var(--text-primary); padding: var(--spacing); border-radius: var(--border-radius); text-align: left; width: 100%` — and, when `.active`, the button background and border become `var(--button-bg)` with `var(--button-text)` at `var(--font-weight-bold)` |
| `.main-content` | `flex: 1; margin-left: 200px; padding: var(--spacing-lg) var(--spacing); max-width: 100%; transition: margin-left 0.3s ease` |
| `.main-content > *` | `max-width: 1200px; margin-left: auto; margin-right: auto` |
| `.main-content.expanded` | `margin-left: 0` |
| Headings | `font-weight: var(--font-weight-bold); line-height: 1.2`, sized `h1: 2rem` down to `h6: 1rem` |

The shape is: **a fixed 60-pixel bar across the top, a collapsible 200-pixel
sidebar beneath it on the left, and a main column whose content is capped at
1200 pixels and centred.** Both the bar and the sidebar sit on `--bg-secondary`,
so the page reads as a lighter content area inset into a slightly darker frame —
which is the same structural idea as Cloudflare Radar's left rail and scoped
header, and is why the maintainer's two stated preferences are consistent with
each other rather than in tension.

### The five named palettes

**Which source governs.** `opnview` takes these five palettes from the
maintainer's own site, **https://lequellec.xyz**, where they are already
implemented as a seven-token set per palette — `--bg`, `--panel`, `--fg`,
`--fg-strong`, `--fg-muted`, `--accent`, `--line` — in a light and a dark
variant. Those are the values the product uses, and they are authoritative.

The upstream tables that follow are **not** those values. They are the record of
where each name comes from, kept because a palette's own project is the right
place to check a name, a role or a disputed hex. They are reference, not
implementation. The two differ — the site's Tokyo Night puts `#16161e` at the
ground and `#1a1b26` at the surface, where the upstream tables invite the
opposite — and substituting the upstream values produced a washed-out rendering
that the maintainer caught and rejected. Do not take these tables as the
palette.

#### The values `opnview` uses

Read from the maintainer's site. Seven tokens carry the interface; `--vif` is
the site's accent-of-last-resort and is recorded with them because the mockup
uses it for the one series that must not be mistaken for another.

| Palette | Mode | `--bg` | `--panel` | `--fg` | `--fg-strong` | `--fg-muted` | `--accent` | `--line` | `--vif` |
|---|---|---|---|---|---|---|---|---|---|
| Tokyo Night | dark | `#16161e` | `#1a1b26` | `#c0caf5` | `#e6ebff` | `#8b93ba` | `#7aa2f7` | `#2a2e42` | `#f7768e` |
| Tokyo Night | light | `#e1e2e7` | `#d8d9df` | `#343b58` | `#1c2033` | `#565a6e` | `#2a54c0` | `#bcbec9` | `#8c1f4f` |
| Dracula | dark | `#21222c` | `#282a36` | `#f8f8f2` | `#ffffff` | `#a9aed0` | `#bd93f9` | `#3b3d4d` | `#ff79c6` |
| Dracula | light | `#fffbeb` | `#f6f0d8` | `#2a2823` | `#1f1f1f` | `#5f5940` | `#5a3fc0` | `#ddd6bd` | `#a3144d` |
| Nord | dark | `#2e3440` | `#3b4252` | `#d8dee9` | `#eceff4` | `#a9b4c6` | `#88c0d0` | `#4c566a` | `#c9a3c4` |
| Nord | light | `#eceff4` | `#e5e9f0` | `#3b4252` | `#2e3440` | `#4c566a` | `#2f5f88` | `#d0d6e0` | `#94357f` |
| Rosé Pine | dark | `#191724` | `#1f1d2e` | `#e0def4` | `#f2f0ff` | `#a9a4c9` | `#c4a7e7` | `#302c48` | `#eb6f92` |
| Rosé Pine | light | `#faf4ed` | `#f2e9e1` | `#575279` | `#3d3a55` | `#68647a` | `#7a4d94` | `#dfd6cd` | `#a03a58` |
| Catppuccin | dark | `#181825` | `#1e1e2e` | `#cdd6f4` | `#eff1f5` | `#a6adc8` | `#cba6f7` | `#313244` | `#f38ba8` |
| Catppuccin | light | `#eff1f5` | `#e6e9ef` | `#4c4f69` | `#3a3c52` | `#5c5f77` | `#7c2fd4` | `#ccd0da` | `#a02a45` |

Two consequences, both of which contradict something written elsewhere before
this table existed and both of which this table settles:

- **Ground and surface are ordered.** `--bg` is the page, `--panel` is the card,
  and `--bg` is the darker of the two in every dark variant. Inverting them is
  what produced the rejected rendering.
- **Nord has a light variant here.** Upstream Nord publishes none, and the
  earlier text therefore specified a fallback for a light-preferring system with
  Nord selected. The maintainer's site defines Nord light, so no fallback is
  needed and none should be built.

Each upstream entry below is at its official published values, with at least one
URL to the palette's own source. Where a value could only be read from a
generated artefact rather than a hand-authored one, that is stated. **These are
records of external sources, reproduced so `opnview` never has to fetch anything
at runtime** — which is the whole reason for reproducing them rather than
linking them.

#### Dracula

Sources: the official specification at https://spec.draculatheme.com/ and the
palette table in the canonical repository's README at
https://raw.githubusercontent.com/dracula/dracula-theme/master/README.md. The
two agree. The scheme named "Dracula" is **dark-only**; the project also
publishes **Alucard**, an official light counterpart, in the same README table.
Dracula publishes **no machine-readable palette file** — the palette is a
Markdown table and prose.

| Role | Dracula (dark) | Alucard (light) |
|---|---|---|
| Background | `#282a36` | `#fffbeb` |
| Current Line | `#44475a` | `#6c664b` |
| Selection | `#44475a` | `#cfcfde` |
| Foreground | `#f8f8f2` | `#1f1f1f` |
| Comment | `#6272a4` | `#6c664b` |
| Cyan | `#8be9fd` | `#036a96` |
| Green | `#50fa7b` | `#14710a` |
| Orange | `#ffb86c` | `#a34d14` |
| Pink | `#ff79c6` | `#a3144d` |
| Purple | `#bd93f9` | `#644ac9` |
| Red | `#ff5555` | `#cb3a2a` |
| Yellow | `#f1fa8c` | `#846e15` |

Note, because it is the most common transcription error in circulation: **Current
Line and Selection are the same value, `#44475a`**, and `#6272a4` is Comment,
not Current Line.

#### Nord

Sources: the official documentation at
https://www.nordtheme.com/docs/colors-and-palettes and the canonical style-guide
file at https://raw.githubusercontent.com/nordtheme/nord/develop/src/nord.css,
which carries the same sixteen values as annotated CSS custom properties. Nord
is **dark-only**: there is one palette and no official light variant — Snow
Storm is the *text* group, not a light theme. There is **no JSON or YAML
definition**; the machine-readable forms are CSS, Less, Sass and Stylus in the
same source directory. `nord8` is designated the primary accent.

| Group | Token | Value |
|---|---|---|
| Polar Night | `nord0` | `#2e3440` |
| | `nord1` | `#3b4252` |
| | `nord2` | `#434c5e` |
| | `nord3` | `#4c566a` |
| Snow Storm | `nord4` | `#d8dee9` |
| | `nord5` | `#e5e9f0` |
| | `nord6` | `#eceff4` |
| Frost | `nord7` | `#8fbcbb` |
| | `nord8` | `#88c0d0` |
| | `nord9` | `#81a1c1` |
| | `nord10` | `#5e81ac` |
| Aurora | `nord11` | `#bf616a` |
| | `nord12` | `#d08770` |
| | `nord13` | `#ebcb8b` |
| | `nord14` | `#a3be8c` |
| | `nord15` | `#b48ead` |

#### Tokyo Night

**Stated honestly: Tokyo Night has no published style-guide page.** Its
canonical definition is the colour files in the `folke/tokyonight.nvim`
repository — specifically
https://raw.githubusercontent.com/folke/tokyonight.nvim/main/lua/tokyonight/colors/storm.lua,
which holds the base table and every literal value;
https://raw.githubusercontent.com/folke/tokyonight.nvim/main/lua/tokyonight/colors/night.lua,
which deep-copies Storm and overrides **only three background keys**;
https://raw.githubusercontent.com/folke/tokyonight.nvim/main/lua/tokyonight/colors/moon.lua,
a full literal table of its own; and
https://raw.githubusercontent.com/folke/tokyonight.nvim/main/lua/tokyonight/colors/day.lua,
which is **not a literal table at all** but a function inverting the Night
palette programmatically. Day's concrete values therefore exist only in
generated output, and the values below were read from the repository's own
generated artefact,
https://raw.githubusercontent.com/folke/tokyonight.nvim/main/extras/lua/tokyonight_day.lua
— still first-party, but generated rather than hand-authored, and flagged as
such. The derived colour assembly lives in
https://raw.githubusercontent.com/folke/tokyonight.nvim/main/lua/tokyonight/colors/init.lua.

Tokyo Night ships **both**: Night, Storm and Moon are dark, and **Day is the
official light variant**. There is **no JSON or YAML palette file**; the Lua
tables are the definition, with generated exports for many targets alongside.

| Key | Storm | Night | Moon | Day (light) |
|---|---|---|---|---|
| `bg` | `#24283b` | `#1a1b26` | `#222436` | `#e1e2e7` |
| `bg_dark` | `#1f2335` | `#16161e` | `#1e2030` | `#d0d5e3` |
| `bg_dark1` | `#1b1e2d` | `#0C0E14` | `#191B29` | `#c1c9df` |
| `bg_highlight` | `#292e42` | `#292e42` | `#2f334d` | `#c4c8da` |
| `fg` | `#c0caf5` | `#c0caf5` | `#c8d3f5` | `#3760bf` |
| `fg_dark` | `#a9b1d6` | `#a9b1d6` | `#828bb8` | `#6172b0` |
| `fg_gutter` | `#3b4261` | `#3b4261` | `#3b4261` | `#a8aecb` |
| `comment` | `#565f89` | `#565f89` | `#636da6` | `#848cb5` |
| `blue` | `#7aa2f7` | `#7aa2f7` | `#82aaff` | `#2e7de9` |
| `cyan` | `#7dcfff` | `#7dcfff` | `#86e1fc` | `#007197` |
| `green` | `#9ece6a` | `#9ece6a` | `#c3e88d` | `#587539` |
| `teal` | `#1abc9c` | `#1abc9c` | `#4fd6be` | `#118c74` |
| `magenta` | `#bb9af7` | `#bb9af7` | `#c099ff` | `#9854f1` |
| `purple` | `#9d7cd8` | `#9d7cd8` | `#fca7ea` | `#7847bd` |
| `orange` | `#ff9e64` | `#ff9e64` | `#ff966c` | `#b15c00` |
| `red` | `#f7768e` | `#f7768e` | `#ff757f` | `#f52a65` |
| `red1` (error) | `#db4b4b` | `#db4b4b` | `#c53b53` | `#c64343` |
| `yellow` | `#e0af68` | `#e0af68` | `#ffc777` | `#8c6c3e` |
| `terminal_black` | `#414868` | `#414868` | `#444a73` | `#b4b5b9` |

**Night equals Storm in every key except the three background keys.** That is the
single most important fact when reproducing these, because "Tokyo Night"
colloquially means the Night variant, whose accent colours are literally
Storm's. `UNVERIFIED:` Day's `yellow` value `#8c6c3e` was read from the rainbow
array of the generated Day artefact rather than from a top-level key in a
hand-authored file; every other value in the table came from a literal table or
its direct generated equivalent.

#### Rosé Pine

Sources: the hand-authored definition at
https://raw.githubusercontent.com/rose-pine/palette/main/source/index.ts and the
generated output carrying all three variants at
https://raw.githubusercontent.com/rose-pine/palette/main/dist/css/rose-pine.css,
with the official palette page at https://rosepinetheme.com/palette/. Rosé Pine
ships **both**: Rosé Pine and Rosé Pine Moon are dark, and **Rosé Pine Dawn is
the official light variant**. It **does** publish machine-readable definitions —
JSON, YAML, TOML, CSS and a Tailwind form — at
https://github.com/rose-pine/palette/tree/main/dist/json and its siblings.

| Role | Rosé Pine (dark) | Moon (dark) | Dawn (light) |
|---|---|---|---|
| base | `#191724` | `#232136` | `#faf4ed` |
| surface | `#1f1d2e` | `#2a273f` | `#fffaf3` |
| overlay | `#26233a` | `#393552` | `#f2e9e1` |
| muted | `#6e6a86` | `#6e6a86` | `#9893a5` |
| subtle | `#908caa` | `#908caa` | `#797593` |
| text | `#e0def4` | `#e0def4` | `#575279` |
| love | `#eb6f92` | `#eb6f92` | `#b4637a` |
| gold | `#f6c177` | `#f6c177` | `#ea9d34` |
| rose | `#ebbcba` | `#ea9a97` | `#d7827e` |
| pine | `#31748f` | `#3e8fb0` | `#286983` |
| foam | `#9ccfd8` | `#9ccfd8` | `#56949f` |
| iris | `#c4a7e7` | `#c4a7e7` | `#907aa9` |
| highlight low | `#21202e` | `#2a283e` | `#f4ede8` |
| highlight med | `#403d52` | `#44415a` | `#dfdad9` |
| highlight high | `#524f67` | `#56526e` | `#cecacd` |

**One genuine discrepancy inside the official repository, recorded so a reviewer
who trips over it does not conclude this table is wrong.** The repository-root
file https://raw.githubusercontent.com/rose-pine/palette/main/palette.json gives
Dawn's `text` as `#464261`, whereas the hand-authored source and everything
generated into `dist/` give `#575279`. That root file was added in a commit
describing itself as temporary and, unlike the real definition, omits the three
highlight roles entirely — it is an incomplete side artefact. **`#575279` is the
value used above**, because it is what the authored source, all generated
output and the official palette page carry.

#### Catppuccin

Source: the single machine-readable source of truth from which all Catppuccin
ports are generated,
https://raw.githubusercontent.com/catppuccin/palette/main/palette.json, with the
rendered tables at
https://raw.githubusercontent.com/catppuccin/catppuccin/main/README.md. Values
below are from palette version 1.8.0. Catppuccin ships **both**: **Latte is the
official light flavour**, and Frappé, Macchiato and Mocha are dark in increasing
darkness. The JSON carries an explicit dark flag per flavour, plus `hex`, `rgb`,
`hsl`, `oklch` and an accent boolean per colour — **the best machine-readable
definition of the five**, and the only one `opnview` could consume mechanically
if it ever wanted to.

| Name | Mocha (dark) | Latte (light) |
|---|---|---|
| rosewater | `#f5e0dc` | `#dc8a78` |
| flamingo | `#f2cdcd` | `#dd7878` |
| pink | `#f5c2e7` | `#ea76cb` |
| mauve | `#cba6f7` | `#8839ef` |
| red | `#f38ba8` | `#d20f39` |
| maroon | `#eba0ac` | `#e64553` |
| peach | `#fab387` | `#fe640b` |
| yellow | `#f9e2af` | `#df8e1d` |
| green | `#a6e3a1` | `#40a02b` |
| teal | `#94e2d5` | `#179299` |
| sky | `#89dceb` | `#04a5e5` |
| sapphire | `#74c7ec` | `#209fb5` |
| blue | `#89b4fa` | `#1e66f5` |
| lavender | `#b4befe` | `#7287fd` |
| text | `#cdd6f4` | `#4c4f69` |
| subtext1 | `#bac2de` | `#5c5f77` |
| subtext0 | `#a6adc8` | `#6c6f85` |
| overlay2 | `#9399b2` | `#7c7f93` |
| overlay1 | `#7f849c` | `#8c8fa1` |
| overlay0 | `#6c7086` | `#9ca0b0` |
| surface2 | `#585b70` | `#acb0be` |
| surface1 | `#45475a` | `#bcc0cc` |
| surface0 | `#313244` | `#ccd0da` |
| base | `#1e1e2e` | `#eff1f5` |
| mantle | `#181825` | `#e6e9ef` |
| crust | `#11111b` | `#dce0e8` |

**Frappé** and **Macchiato** are published in the same `palette.json` under their
own keys, and in the same rendered README tables; their twenty-six values are:

*Frappé* — rosewater `#f2d5cf`, flamingo `#eebebe`, pink `#f4b8e4`, mauve
`#ca9ee6`, red `#e78284`, maroon `#ea999c`, peach `#ef9f76`, yellow `#e5c890`,
green `#a6d189`, teal `#81c8be`, sky `#99d1db`, sapphire `#85c1dc`, blue
`#8caaee`, lavender `#babbf1`, text `#c6d0f5`, subtext1 `#b5bfe2`, subtext0
`#a5adce`, overlay2 `#949cbb`, overlay1 `#838ba7`, overlay0 `#737994`, surface2
`#626880`, surface1 `#51576d`, surface0 `#414559`, base `#303446`, mantle
`#292c3c`, crust `#232634`.

*Macchiato* — rosewater `#f4dbd6`, flamingo `#f0c6c6`, pink `#f5bde6`, mauve
`#c6a0f6`, red `#ed8796`, maroon `#ee99a0`, peach `#f5a97f`, yellow `#eed49f`,
green `#a6da95`, teal `#8bd5ca`, sky `#91d7e3`, sapphire `#7dc4e4`, blue
`#8aadf4`, lavender `#b7bdf8`, text `#cad3f5`, subtext1 `#b8c0e0`, subtext0
`#a5adcb`, overlay2 `#939ab7`, overlay1 `#8087a2`, overlay0 `#6e738d`, surface2
`#5b6078`, surface1 `#494d64`, surface0 `#363a4f`, base `#24273a`, mantle
`#1e2030`, crust `#181926`.

Note the inversion in Latte: `base` is the *lightest* and `crust` the *darkest*,
while in the dark flavours `base` is dark and `crust` darker still, with the
text, overlay and surface ramps inverting accordingly. Any adoption has to map
by **role**, not by position.

### What this means for the five-palette decision

Recorded as an observation, not as a choice. Of the five named palettes, **three
ship an official light variant** (Tokyo Night's Day, Rosé Pine's Dawn,
Catppuccin's Latte), **one ships a light counterpart under a different name**
(Dracula's Alucard) and **one is dark-only** (Nord). Settled decision 5 —
"theme on first launch follows the operating system" — therefore has a concrete
consequence: **on a machine set to light, a user who has chosen Nord has no
light variant to follow the OS into.** That is a real question the mockup cycle
must answer, and it is named here rather than discovered later. Two obvious
options, neither chosen here: fall back to the industrial light palette while
keeping Nord's accents, or state plainly in the theme picker that Nord is
dark-only.

Two of the five publish a **machine-readable palette file** (Catppuccin's
`palette.json`, Rosé Pine's `dist/`), which matters because it decides whether
the values in this document are the source of truth for `opnview` or merely a
record. Given `ROADMAP.md`'s no-outbound-calls rule, **they are a record either
way**: the values ship inside the binary, and nothing is fetched at runtime.

## The maintainer's recorded preferences

**This section is the point of the cycle.** The first mockup attempt was lost
because the design brief was one line and the judgement was therefore a coin
flip. **Two of the four columns are his and two are not, and confusing them has
already cost this project a widget.** *Preference* is his own words. *Image* is
what he pointed at. **What to take from it** is this document's reading of that
image, written by an agent — a starting point, never a requirement, and never to
be quoted back as something he asked for. Where a design decision rests on that
column, it rests on a guess, and it is the guess that gives way when the built
thing turns out to be useless.

| Question | Preference | Image | What to take from it |
|---|---|---|---|
| **Charts, histograms, pie charts** | **Grafana, clearly above the others**, in quality and in granularity. It is the reference for anything that plots a value. The reason, stated in his own terms: **Grafana is the only one that does not have that over-smoothed quality, and it is very granular.** | `ui-references/screenshots/grafana-community-traffic-analysis-asn-light.png`, `ui-references/screenshots/familya-grafana-panel-editor.png`, `ui-references/screenshots/grafana-community-opnsense-suricata-signatures.png` | **Show the resolution the data actually has.** Most dashboard products round their curves and thin their series; Grafana draws what is there, and that granularity is the point rather than a side effect. Practically: no curve smoothing, no silent downsampling that hides a spike, a tooltip that lists the full stacked breakdown at one instant, and `fieldConfig.overrides`-class control over how each series renders. |
| **Flow visualisation** | **ntopng's flow view is outstanding**, and so is **Akvorado**, in particular its ASN Sankey. | `ui-references/screenshots/moodboard-domains-ntopng-flows.png`, `ui-references/screenshots/familyb-ntopng-top-flow-talkers.png`, `ui-references/screenshots/moodboard-layout-akvorado-asn-sankey.png` | ntopng: a flow rendered as **one cell** (host : port ⇄ host : port) under a dropdown-per-dimension filter bar. Akvorado: a Sankey whose ends carry the **resolved operator name**, with an honest collapsed band for the tail. |
| **Per-device view** | **Firewalla's per-device map of activities and flows** — liked a lot. | `ui-references/screenshots/familyb-firewalla-activities-flows.png`, `ui-references/screenshots/familyb-firewalla-devices-list.png` | One subject, one time window, two renderings: human-readable *activity* on one side, raw *flows* on the other. **This document once read that as needing a single toggle and one catalogue entry; the maintainer decided otherwise on 2026-09-28** — *Client volume ranking* and *Client traffic detail* stay two presets, placed side by side on a canvas when both are wanted. Composing the view is the reader's, not the widget's. |
| **World map** | **ntopng's**, over every other map captured. | `ui-references/screenshots/familyb-ntopng-geomap.png`, `ui-references/screenshots/moodboard-map-ntopng-geo.png` | A de-saturated basemap with small point marks, everything interesting in the popup, no choropleth and no arcs. The map is background; the data is foreground. |
| **Connection tree** | **Malcolm and Arkime are both very good.** | `ui-references/screenshots/moodboard-map-malcolm-conn-tree.png`, `ui-references/screenshots/moodboard-map-arkime-connections.png` | Malcolm: two mirrored trees from one root, with tree depth exposed as a control. Arkime: node size and colour driven by a named, user-chosen weight, with a docked detail panel instead of tooltips. **The single root was this document's reading, not his instruction, and it produced a widget that says only that traffic crossed the firewall — true of everything on the page. What the widget has to answer: where does a chosen subject go preferentially. A subject and a ranking are the requirement; a shape is not.** |
| **Connection tree — rejected** | **Vizceral: liked less. Too cluttered.** | `ui-references/screenshots/moodboard-map-vizceral.png` | **Avoid**: animated particles on every edge at high node count stop carrying information and become texture. Motion-as-volume works at ten nodes and fails at a hundred; do not adopt it for the segment graph. |
| **Placing, moving and resizing tiles** | **Homarr's approach** — more freedom, and less childish than the alternatives. This is the reference for the canvas mechanics. | `ui-references/screenshots/familya-homarr-board-layout-large.png`, `ui-references/screenshots/familya-homarr-layout-settings.png` | Heterogeneous widget sizes on one grid, collapsible category sections as the structural unit, and — the mechanism — **named responsive layouts, each with its own breakpoint and column count, with every item's position stored per layout**. |
| **Homarr's layout, as a shape** | **Too rounded for his taste — but close to what he wants to reach.** | same two images | **Take the structure, not the shape language.** The arrangement, the density and the grid are close to the target; the corner radius is not. |
| **Editing a card's content** | **Home Assistant's minimalism.** Syntax highlighting is enough; a full IDE embedded in a browser is not wanted. | `ui-references/screenshots/familya-homeassistant-card-yaml-editor.jpg`, `ui-references/screenshots/familya-homeassistant-raw-config-editor.jpg` | A small highlighted text pane with a live preview beside it and a link back to the visual editor. **No file tree, no minimap, no command palette, no diff viewer.** This is the calibration point for how much editor is enough. |
| **General layout** | **Cloudflare's** — and the reason matters: **angular cards, like his own personal projects.** | `ui-references/screenshots/demo-cloudflare-radar-overview.jpg`, `ui-references/screenshots/moodboard-empty-cloudflare-radar-skeleton.jpg` | A left navigation rail, a scoped header carrying the selectors, and angular cards with a one-line explanation under each title. Consistent with the industrial palette's `0.25rem` radius, and against the rounded UniFi aesthetic `ROADMAP.md` step 3 specifies. |
| **Configuration interface** | **Open. He is waiting for the next pass before judging.** | — | Nothing is decided. `docs/dashboard-format.md` proposes the two coordinated surfaces; whether that is the right shape is not yet answered, and this document does not pretend it is. |

**The split that matters.** Two of these preferences point at two different
products for two different jobs, and that is deliberate rather than
inconsistent: **Homarr for the grid, Home Assistant for the editor.** `opnview`
takes its layout interaction from one product and its editing interaction from
another. The consequence is worked through in *Conclusions for opnview*, because
it is more than a preference — it is a combination nobody in this survey has
actually built.

## Conclusions for opnview

Definite answers, not a survey. Four questions, each answered in sentences that
commit.

### What a widget references instead of a local identifier

**A typed reference object carrying a `kind`, the name of the natural key it is
expressed in, and that key's value — never a local database id, and never a
generated universal identifier.**

Concretely, `{"kind": "interface", "by": "identifier", "value": …}`,
with the full vocabulary and the per-kind key list in
`docs/dashboard-format.md`. Seven kinds exist. Three of them —
`provider` (the pair of a provider kind and a provider key, such as a resolver
implementation), `country` (an ISO 3166-1 alpha-2 code) and `operator` (an AS
number) — are **globally meaningful and travel intact**. Four of them —
`interface`, `client`, `rule` and `site` — name things that exist on one
installation and may not exist on another, and `opnview` **does not pretend
otherwise**.

The reasoning is Family A's, not this document's invention. Kibana states it
directly: a randomly generated saved-object id makes a dashboard fragile, while
a human-readable id "makes the data view easier to recreate with the same ID
across spaces, deployments, or environments" so that dashboards referencing it
keep working
(https://www.elastic.co/docs/explore-analyze/find-and-organize/data-views). Home
Assistant is the counter-example that proves it: `entity_id` travels because a
human wrote it, while `device_id`, `area_id` and user ids are opaque local
UUIDs, and a dashboard carrying them **does not error — it silently mis-targets
or silently hides content**
(https://github.com/home-assistant/home-assistant.io/blob/current/source/dashboards/views.markdown).
A silent wrong answer about a firewall is worse than a visible failure, so
`opnview` uses natural keys and accepts that some of them will not resolve.

A reference may also carry a `label` — the exporting installation's own name for
the thing — **for display only.** It is never matched on. `opnview` never infers
an interface's nature from what it is called, and that rule holds in the dashboard
format exactly as it holds in the schema.

### What happens when a reference cannot be resolved locally

**The widget is kept. It renders empty, in its full footprint, and the empty
state names the reference that failed and offers to repair it. Neither an error
screen nor a fabricated zero is acceptable.**

A zero would be a lie: it asserts "we looked and there was none" when the truth
is "we could not look". This is the same distinction the schema already draws —
`source_availability` exists precisely so an absent source is a modelled
condition rather than an absence of rows, and a geolocation cache miss is a row
whose `lookup_state` is `miss` rather than a missing row. An unresolved
reference is that idea applied to the dashboard file.

**Repair happens through two coordinated surfaces that edit the same model, and
neither is the "real" one.**

1. **A JSON/YAML editor that highlights the offending lines in place.** The file
   as text, with the exact lines whose reference failed marked, each carrying an
   inline "no data" notice naming the reference and why it did not resolve. The
   highlight clears when the reference resolves.

2. **A no-code selector whose dropdowns are populated from what this
   installation actually has** — the interfaces the firewall discovered, the
   clients seen in leases and flows, the rules in the running ruleset, the
   providers in the registry. The unresolved value is shown as the current
   selection, marked as not found, beside the choices that do exist.

**How much editor is enough is now settled, and it is not much.** The
maintainer's stated preference is Home Assistant's minimalism: a small
syntax-highlighted pane with a live preview beside it and a link back to the
visual editor. **No file tree, no minimap, no command palette, no diff viewer,
no embedded IDE.** Grafana's "Edit as code" drawer is the right *shape* — code
and rendering on one screen with an explicit apply step
(https://grafana.com/docs/grafana/latest/dashboards/build-dashboards/modify-dashboard-settings/)
— but it is heavier than wanted; Home Assistant's per-card YAML pane is the
right *weight*
(https://github.com/home-assistant/frontend/blob/dev/src/panels/lovelace/hui-editor.ts).

The repair flow itself copies Kibana, which is the only product in Family A
whose import treats a dangling reference as a first-class, user-resolvable
condition: it reports `missing_references` with the affected objects, prompts the
user to point each at something that exists, and refuses to commit until they
do
(https://www.elastic.co/docs/api/doc/kibana/v9/operation/operation-post-saved-objects-import,
https://www.elastic.co/docs/api/doc/kibana/operation/operation-post-saved-objects-resolve-import-errors).
`opnview` takes the reporting and the remapping and **deliberately declines the
refusal to commit**: the dashboard imports, broken parts visibly broken, because
a user who cannot see the dashboard cannot repair it.

Three things repair does **not** do, each stated because the temptation is real:
it does not rewrite references automatically by fuzzy-matching labels; it does
not discard the authored value before the user chooses a replacement; and it
does not block the import.

### How the format is versioned

**A single integer `format_version` at the top of the file, currently `1`,
versioning the file format and nothing else** — not `opnview`'s release number,
not the schema version in `schema_version`, not the widget catalogue. Each build
declares the version it writes and the minimum it can read.

**An older file meeting a newer `opnview`** is migrated forward on load, through
an ordered ladder of one-version steps, **one-way**, **in memory rather than on
disk**, with a **floor** below which a file is refused rather than guessed at.
That is Grafana's `DashboardMigrator` shape
(https://github.com/grafana/grafana/blob/main/public/app/features/dashboard/state/DashboardMigrator.ts)
with Kibana's version floor
(https://www.elastic.co/docs/api/doc/kibana/v9/operation/operation-post-saved-objects-import).

**A newer file meeting an older `opnview`** is refused, cleanly and completely,
with a message naming the file's version, the highest this build understands and
the fact that an upgrade is the fix. That is the deliberate opposite of the
tolerant posture taken everywhere else, and the reason is that an unknown
*parameter key* is inert data in a widget that otherwise works, whereas an
unknown *format version* says the document's structure may have changed in ways
this build cannot see.

**The integer will be turned rarely, and the evidence says it can be.** Four of
the eight Family A products carry no format version at all — Home Assistant,
Dashy, Homepage and Datadog — and all four have evolved for years on tolerant
readers plus deprecation in place. Home Assistant is the sharpest case: its
canonical config interface has no `version` key, and the only versioned artefact
is the storage envelope, which has never been migrated
(https://github.com/home-assistant/frontend/blob/dev/src/data/lovelace/config/types.ts,
https://github.com/home-assistant/core/blob/dev/homeassistant/components/lovelace/dashboard.py).
Kibana is the sole sophisticated exception, versioning **per saved-object type**
through `modelVersions` explicitly decoupled from the product version
(https://www.elastic.co/docs/extend/kibana/saved-objects/migrations).
`opnview` keeps the integer because an eventual structural change is cheaper to
handle with one than without — not because it expects to use it often.

### What an import does with a malformed or dangling file

**Two kinds of validity, treated deliberately differently.**

**Structural validity is fail-closed.** A document that is not parseable, that
lacks `format_version` or `canvases`, that has a canvas with no id or title, a
widget with no id, type or placement, a duplicate id within its scope, or a
non-integer placement, is **refused whole** — nothing imported, with a message
naming the offending canvas index, widget index and field. Datadog's positional
error is the model
(https://github.com/DataDog/terraform-provider-datadog/issues/1769), improved by
naming the field as well as the index. A partial import of a structurally broken
file is never performed.

**Referential validity is fail-open.** Every unresolved reference — an unknown
widget type, an `interface`, `client` or `rule` reference matching nothing locally,
an unrecognised parameter key, a parameter value outside the known vocabulary —
is **collected and reported, and the import proceeds**. The result is a
dashboard the user can see, with the broken parts visibly broken and repairable.
Grafana's per-panel behaviour is the precedent: an unknown panel plugin renders
an error tile in its own slot while every other panel keeps working
(https://github.com/grafana/grafana/blob/main/public/app/features/panel/components/PanelPluginError.tsx),
as is Home Assistant's per-card error element
(https://github.com/home-assistant/frontend/blob/dev/src/panels/lovelace/create-element/create-element-base.ts).

One import report is shown afterwards, listing every collected condition grouped
by canvas and widget, each linking to the widget it affects. Netdata's
dashboard-template chooser, which annotates each template with how many of its
charts the current data can actually fill, is the friendliest version of that
idea and is worth copying for the "what will this dashboard actually show me?"
question before an import rather than after
(https://learn.netdata.cloud/docs/dashboards-and-charts/tabs/dashboards).

### The design `opnview` is actually building, and the risk in it

**Eight things taken from seven products, and none of them copied whole:**

- **Homarr's layout structure and grid interaction** — heterogeneous tiles on a
  real grid, named responsive layouts with per-breakpoint column counts, items
  positioned per layout. The maintainer's reference for adding, moving and
  resizing. Its rounding is explicitly excepted.
- **Cloudflare Radar's composition, and its angularity** — a left rail, a scoped
  header carrying the selectors, angular cards each with a one-line explanation.
- **The industrial palette's shape language and tokens** — a `0.25rem` radius,
  system fonts only, high contrast in both themes, and a fixed bar plus
  collapsible sidebar plus centred main column.
- **Home Assistant's minimal editor** — a small highlighted text pane with a
  live preview, and nothing else.
- **Grafana's granular, unsmoothed charting** — the resolution the data actually
  has, no rounded curves, no silent downsampling, and per-series rendering
  control.
- **ntopng's flow view and world map** — a flow as one cell under a
  dropdown-per-dimension filter bar; a de-saturated basemap with point marks and
  the detail in a popup.
- **Firewalla's per-device activity map** — one subject, one window, activity
  and flows behind a single toggle.
- **Malcolm's or Arkime's connection tree**, and **Akvorado's ASN Sankey** —
  mirrored trees with a depth control, a named weight driving node size, and a
  Sankey whose ends carry resolved operator names.

**Rejected, and recorded as such: Vizceral's animated particle graph, as too
cluttered.**

### Grafana's query model, on one local database

The maintainer's charting preference has a structural consequence that is worth
working out here rather than discovering in step 5, because it decides the shape
of the HTTP API.

**A Grafana panel is a visualization over `targets`, an array of queries** — one
panel, several series, each series its own query, each query naming its own data
source as a `{type, uid}` reference
(https://grafana.com/docs/grafana/latest/dashboards/build-dashboards/view-dashboard-json-model/).
That is exactly the shape `docs/widget-catalogue.md`'s *Custom chart* needs: the
maintainer's example of plotting temperature and CPU against interface
throughput on one chart is three series from three different places, and a
widget that hard-codes which places would not be able to express it.

**The difference is that `opnview` has one data source, not many, and it is a
local SQLite file.** Three things follow, and none of them is a problem.

- **The `{type, uid}` datasource reference disappears entirely.** Grafana needs
  it because a dashboard can target Prometheus, Loki and Elasticsearch at once;
  it is also, per *Portability* above, the single largest thing that breaks when
  a Grafana dashboard moves between installations, and the whole `__inputs` and
  `${DS_*}` machinery exists to paper over it. `opnview` has nothing to name, so
  **a whole class of portability failure simply does not exist for it** — a
  series names *what* it plots, never *where from*. That is a real, unearned
  advantage of being a single binary over one database, and it is worth
  recognising rather than accidentally reintroducing.
- **What replaces the datasource reference is the `source` field of a series** —
  `firewall_metric`, `interface_throughput`, `flow_volume`, `security_event` and
  so on: a closed vocabulary of *what kind of thing* is being plotted, resolved
  by `opnview` to a table and a projection it owns. The user never writes SQL,
  and the dashboard file never contains any, which also means **a dashboard file
  is never executable input**: it is pasteable into a forum post without anyone
  having to think about it.
- **One query per series, joined on the time bucket at the presentation layer.**
  The catalogue says this explicitly for *Custom chart*, and the reason is that
  a telemetry sample table and a volume aggregate have no join key in common
  beyond time. That is only safe if **both families bucket on the same
  boundaries**, which is why gap G9's roll-up design is specified to align with
  the existing volume aggregates' period boundaries rather than inventing its
  own. Getting that wrong would produce charts that are subtly, unfalsifiably
  wrong about simultaneity, which is worse than charts that refuse to draw.

The consequence for step 5's API is in the amendment below: **one endpoint per
widget type taking that widget's declared parameters**, which for a multi-series
chart means the endpoint accepts a list of series and returns a list of series,
each carrying its own availability state so a single missing source degrades one
line rather than the whole chart.

**And here is the finding that matters most, stated here rather than buried in a
product subsection: no product in this survey does what `opnview` has decided to
do.** Homarr has the free grid and **no code representation of a card at all** —
its configuration is entirely graphical, there is no file, no JSON view and no
YAML view, and it has therefore **never had to solve the consistency problem
between a graphical editor and a text one**
(https://homarr.dev/blog/2024/09/23/version-1.0/). Home Assistant has both
surfaces over one model and is the only one of the two that has actually faced
keeping them in step — including where it went wrong, which is worth naming: a
YAML-mode dashboard is read-only from the UI by construction
(https://github.com/home-assistant/core/blob/dev/homeassistant/components/lovelace/dashboard.py),
a UI write silently discards YAML comments, and concurrent edits are handled by
a toast rather than a lock
(https://github.com/home-assistant/frontend/blob/dev/src/panels/lovelace/hui-editor.ts).
But its grid is far more constrained than Homarr's.

**`opnview` is combining a Homarr-class free grid with a Home-Assistant-class
code representation of every widget, plus a no-code selector, all over one
model. That combination is unproven prior art.** The nearest thing to it is
Grafana's "Edit as code" drawer beside a live dashboard — and Grafana pays for
it with a `schemaVersion` ladder forty-two rungs long, a v2 schema that is a
one-way trapdoor
(https://grafana.com/whats-new/2025-05-05-dashboard-v2-schema-and-dynamic-dashboards/),
and a provisioning model in which file and database silently fight
(https://grafana.com/docs/grafana/latest/administration/provisioning/). **The
risk of the combination lands on this project, not on a reference that can be
copied.** That is not an argument against the decision; it is the honest price
of it, and it is recorded so nobody is surprised later.

Two things follow practically. First, **the model has to be designed before
either surface is**, because both are renderers over it and the failure mode of
getting that backwards is Grafana's provisioning trap. Second, **the format's
tolerant-reader posture is doing more work here than in any of the references**,
because a free grid plus arbitrary per-widget parameters is a larger surface to
keep compatible than either product faces alone.
