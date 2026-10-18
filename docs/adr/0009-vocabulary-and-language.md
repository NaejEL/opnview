# 0009 — The vocabulary is OPNsense's; everything is written in English

Status: Accepted
Decided by: the maintainer, steps 1 to 3 (September 2026)

## Context

A term of its own (`segment`) was invented in the first commit for the thing
OPNsense calls an **interface**, before any research existed, while the API
survey had carried the real vocabulary since step 1. `device` meant the network
device in one place and a client machine in another. Nobody noticed for three
steps.

The maintainer speaks French. That is irrelevant to the artifacts.

## Decision

- **opnview uses the vocabulary of OPNsense and of the products it reads** (pf,
  Suricata, Kea, Dnsmasq, ISC, Unbound, Insight, MaxMind). It never invents a
  term for something those products already name. When the right term is not
  known, it is researched in `docs/opnsense-api-survey.md`, on
  `docs.opnsense.org` or in the product's documentation, or the maintainer is
  asked. It is never guessed. Where a product genuinely has no word for
  something opnview needs, that is stated, the term is chosen deliberately, and
  the reason is recorded next to it. The term-by-term mapping is in
  `docs/data-model.md`, *Vocabulary*.
- **Everything produced in this repository is written in English**: code,
  identifiers, comments, commit messages, branch names, documents, ADRs, agent
  prompts, CI output, the UI's source strings, API payloads, the GitHub
  description, issues and pull requests. A conversation held in French still
  produces English output. A file found in another language is a defect to fix.
- English is the UI's **source** language, not its only language: every
  user-visible string comes from the string catalogue, and the user switches
  language in the interface (0016).

## Consequences

- A reviewer checks a new identifier against the survey before accepting it.
- The step-3 mockup is a single file and is exempt from the string-catalogue
  rule, being a mockup.
