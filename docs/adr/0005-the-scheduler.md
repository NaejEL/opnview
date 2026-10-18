# 0005 — The scheduler: nearest deadline first, a real-time reserve

Status: Accepted; the default values are Proposed
Decided by: the maintainer, 11 October 2026 (architecture target, section "scheduler"); defaults proposed by the orchestrator

## Context

The firewall is a small machine and the API is its CPU. A live throughput widget
needs data every few seconds; a lease list every few minutes; logs in the
background. Twenty slow collectors must never block the live ones, and nothing
may be starved.

## Decision

- Every request renews its author's **interest** in a datum at its maximum age.
  A datum's **deadline** is its last reading plus the shortest maximum age asked.
  The scheduler serves the nearest deadline first. A datum nobody asks for is no
  longer read: an endpoint's cadence follows its most demanding reader.
- **Two lanes.** Requests whose maximum age is at or under the real-time
  threshold are the real-time lane; the rest is the background lane. A share of
  the call budget is **reserved** for the real-time lane: guaranteed, not capped,
  so the background lane may use it when nothing real-time is due. A background
  datum left waiting gains priority with age; it always passes.
- **Freshness floor.** Every endpoint has a minimum interval: its observed median
  latency, the floor its manifest declares (`traffic/top` captures about 2 s per
  interface and never goes under), or an operator setting. A more demanding
  request is served at the floor, and `age_seconds` says so.
- **Exponential backoff.** A datum in `server_error`, `transport_failure` or
  timeout backs off exponentially; failures across several endpoints back off
  the whole queue; the first success ends it.
- **A displayed widget keeps its datum fresh.** Querying a dataset of the mirror
  renews the request while somebody is looking; when nobody is, the datum ages
  and its freshness says so.
- **Measured, not assumed.** The finest freshness of each endpoint is measured on
  the maintainer's firewall (at most two CPU points), then adjustable.

### Proposed defaults, to be confirmed on the live firewall

| Setting | Default |
|---|---|
| real-time threshold | max age ≤ 5 s |
| real-time reserve | 70 % of the call budget |
| calls in flight | 2 |
| calls per second | 5 |

## Consequences

- The scheduler is core Go (0002) and depends on nothing that names a source.
- The push listeners (syslog, NetFlow) are inbound and bypass the scheduler.
