# 0015 — A stream is opened once and shared

Status: Proposed
Proposed by the orchestrator from the maintainer's architecture target of 11 October 2026

## Context

Some OPNsense endpoints answer `text/event-stream` (`stream_log`,
`cpu_usage/stream`) and cut after about 60 s. Several plugins may want the same
stream, and a stream's samples are a time series.

## Decision

- A `text/event-stream` response is recognised at the header, before the body is
  read. The catalogue notes its shape, its observed cadence and when the server
  cuts.
- **One subscription, N readers**: opened at the first subscriber, closed at the
  last, unless it feeds a table. Reconnection with backoff; an interruption is a
  gap, never zeros. The first event of `cpu_usage/stream`, an average since
  boot, is discarded.
- The fields read go into a hypertable of schema `opnsense`: short-retention
  detail, minute and hour aggregates (min, max, mean, last, count), refreshed on
  opnview's clock.
- A plugin may declare that a stream replaces a polled endpoint. While the
  stream runs, its request on the endpoint is suspended; it resumes if the
  stream falls. The equivalence is declared, never inferred.

## Consequences

- The front's own event stream (`/api/events`, SSE to the browser) is a
  different thing: it carries opnview's events to the canvases.
