# Roadmap — opnview

One page. Rules are ADRs (`docs/adr/`); this page says only where things
stand and what comes next. Dates are the maintainer's validations on his
firewall; nothing else counts as done.

## Validated on the maintainer's firewall

| What | When | Where |
|---|---|---|
| OPNsense API survey (step 1) | September 2026 | `docs/opnsense-api-survey.md` |
| Data model and vocabulary (step 2) | September 2026 | `docs/data-model.md` |
| Canvas mockup (step 3) | October 2026 | `docs/mockups/canvas-mockup.html` |
| Collection and storage (step 4) | 3 October 2026 | `main` |
| Correlation, classification, site attribution (step 5A) | 11 October 2026 | `main` at `a1e2707` |

## Running unvalidated

The branch `live-next` runs in the maintainer's `opnview-serve` container: the
step-7 front (canvases, renderers, SSE), the plugin host, the broker and the
learned mirror, 42 plugins. The maintainer has not validated it against the
mockup. The branch `wip/backend-plugins` holds everything up to the reset of 18
October 2026, tagged `archive/2026-10-18-before-reset`.

## Next, in order

1. **The core audit** (ADR 0017): the maintainer accepts or amends the
   package-by-package verdicts. Nothing is cut before.
2. **Cut the Go to the core** (ADR 0002): keep, move or delete each package as
   decided, one ADR-sized change at a time, each validated live.
3. **Validate the front against the mockup** (ADR 0016) on live data, and fix
   what differs.
4. **Step 6, alerts**: the Suricata plugin's alert widgets, in JavaScript only.
5. **Step 8, install**: the one command on a Debian host or Proxmox LXC
   (`docs/architecture.md`, *Deployment*), update by re-running, backup.
6. **Proposed ADRs** to accept or drop: version variants (0014), streams
   (0015), the scheduler's defaults (0005), the catalogue's bounds (0011).
