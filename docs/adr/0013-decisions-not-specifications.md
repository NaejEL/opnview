# 0013 — Decisions, not specifications; no self-approved work

Status: Accepted
Decided by: the maintainer, 18 October 2026 (the reset)

## Context

The work was run as a factory: a planner wrote a specification, a builder
implemented it, a verifier approved it, and the orchestrator, from 17 October,
approved its own specifications "under the maintainer's instruction to deliver
end to end". Nineteen specifications, 10 900 lines, one of them 5 500 lines
long, with acceptance criteria that bred the test machinery of 0012. The
maintainer's own architecture documents and his rule of 0002 were recorded and
then contradicted by the specifications' decisions. On 18 October the maintainer
found none of his choices respected and nearly deleted the project.

## Decision

- **There are no specifications.** `specs/` is deleted; its decisions in force
  are these ADRs; its history is in git under the tag
  `archive/2026-10-18-before-reset`.
- **A decision is one page in `docs/adr/`**: context, decision, consequences,
  who decided and when. A decision the maintainer did not take is marked
  Proposed and binds nobody until he accepts it. **An agent never approves its
  own ADR, and never records a decision as the maintainer's.**
- **A change is**: an accepted ADR, the smallest build that honours it, the one
  test command green once, and the change run on the maintainer's firewall. A
  change is validated by the maintainer looking at it on live data, not by a
  verifier reading fixtures. Only validated work reaches `main`; work in
  progress is committed and pushed on a `wip/` branch as it goes.
- **No new process artefact** (a planner, a verifier, a lot, a phase, an
  acceptance-criteria list, a budget tool) without an ADR the maintainer
  accepted.
- `ROADMAP.md` is one page: what is validated, what runs unvalidated, what comes
  next. It carries no rule; rules are ADRs.
- `docs/` holds references a plugin author or the maintainer needs: the API
  survey, the data model and vocabulary, the plugin contract, the UI references
  and the mockup, the widget catalogue, the dashboard format, the architecture
  page. A document that describes the code to itself is not kept.

## Consequences

- The agent definitions under `.claude/agents/` and the `factory-run` skill are
  the maintainer's tooling and were left untouched by the reset; they are not
  to be invoked for this repository until an ADR says how agents work here.
- The French architecture pages the maintainer wrote on 11 October 2026 are the
  source of 0001 to 0005, 0011, 0014 and 0015; `docs/architecture.md` is their
  English rendering in the repository.
