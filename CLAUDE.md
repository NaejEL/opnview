# opnview — project rules

## Language — ABSOLUTE RULE

**Everything produced in this repository is written in English. No exception.**

This covers, without being limited to:

- source code, identifiers, comments, docstrings;
- commit messages, branch names, tags;
- `README.md`, `ROADMAP.md`, and every other document;
- specifications under `specs/`, and their status lines;
- agent prompts, skills, CI scripts, their log output and error messages;
- the web UI: every label, heading, tooltip, empty state and error message;
- API payloads, field names, enum values;
- the GitHub repository description, topics, issues and pull requests.

The maintainer speaks French. **That is irrelevant to the artifacts.** A
conversation held in French still produces English output. If any file in this
repository is found written in another language, translating it is a defect to
fix, not a preference to debate.

## Project

`opnview` gives inter-segment visibility on a VLAN-segmented network behind an
OPNsense firewall. Single Go binary, local SQLite, frontend embedded in the
binary and served by it.

It reads five sources through the OPNsense REST API: filter logs, the Suricata
`eve.json` event log (optional — primary source of site names and of IDS
alerts), NetFlow/Insight, DHCP leases, and resolver DNS lookups (fallback
source of site names).

See `ROADMAP.md` for the seven-step plan and the cross-cutting engineering
rules (no hardcoded configuration, no secrets in the repo, read-only against
the firewall, exactly two outbound calls, observation-point limit stated in
the UI).
