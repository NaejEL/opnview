# SPEC — Polling sized from measured usage, not from a constant

Status: REWRITTEN AFTER THE LOSS — the original of this specification (20 532
bytes, written on 2 October 2026 at 15:55) was destroyed with the working copy
that night and has no surviving copy. This text was written on 3 October 2026
from the code and test names rebuilt from that evening's compiled build. It
describes what was built, not what the original said, and where the two
differed nothing here can tell.
Cycle kind: standard

## Why now

Every paged read `opnview` makes is governed by **two** figures: how often it
polls, and how many records one request asks for. What a poll can lose depends
on the pair. A filter-log poll every ten seconds that asks for 500 records keeps
up with a source producing fewer than fifty records a second and silently drops
the rest, and the API offers no way to recover them (survey, *Data source 1 —
Filter logs*).

Before this cycle the interval was a `setting` row and the page size was a
constant in `internal/collect`. Half of the pair was out of the operator's
reach, and neither half was informed by anything the installation had actually
seen. The defaults are the survey's starting points; a busy installation and a
quiet one are both entitled to a different pair, and the only honest basis for
one is the rate the installation's own sources produce.

`opnview` already stores every record it reads, with the instant the source
gives it and, for most tables, the instant it was stored. The rate is therefore
measurable from the local database alone, with no call to the firewall.

## What changes

### 1. The page size becomes a setting, beside the interval

Three rows, `page_size_firewall_log`, `page_size_security_event` and
`page_size_dhcp_lease`, override the page each paged read asks for; a missing
row means the documented default. The per-pair sampler and the resolver take no
page size: the first reads a snapshot with no paging, the second answers with
its whole ring buffer up to a limit the firewall fixes.

A page size must be a positive whole number. One **above** what the firewall
serves is accepted, stored and reported as above the ceiling; refusing it would
hide the measurement that shows why it was typed.

### 2. The configuration in force is live

The scheduler reads each interval from a live holder (`config.Live`) on every
turn rather than once at start-up, and the collector takes its page sizes
through `Configure`. Start-up and a save on the collection surface load the
configuration the same way and hand it to both, so a saved pair reaches the
running service **without a restart**, and a running service and a restarted
one cannot disagree about what is in force.

### 3. The record rate is measured per kind

`store.MeasureRecordRate` measures one kind's table over a window: the record
count, the span the records cover, the mean rate on the source clock and — where
the table has one — on the ingested clock, the peak rate in 60-second buckets,
and the collection gaps detected in the window, per reason, with the seconds
they cover. The window is the last 24 hours, or the retention horizon if that is
shorter, since the purge has removed whatever lies beyond it.

Each figure the measurement cannot give is a **named state**, never a nought: a
window with no record has no span and no rate; a table with no ingested clock
(`dhcp_lease`) has no ingested rate rather than the source rate copied over.

The measurement is one SQL statement per kind, built from a registry of kinds
in `internal/sizing` whose table and column names live in code and never come
from input. The same statement, for one bucket width, is the *Record rate per
kind* section of `sql/queries/diagnostics.sql`, and a test holds the two
identical.

**The rate is a lower bound.** It counts the records `opnview` stored; a record
lost in a collection gap is counted nowhere. This is documented in
`docs/data-model.md`, under *Record rate*, and printed on no screen — the same
rule as the observation-point limit.

### 4. A pair is suggested from the measurement

`sizing.Suggest` derives a pair for one kind from its measurement, the pair in
force, and whether `opnview` collects the kind at all. It reads nothing and
writes nothing.

- A kind `opnview` does not collect gets no suggestion: no implementation of it
  is both not turned off and reachable, so no row of it is being written.
- A measurement that does not hold enough gets none either, and says so: no
  peak, fewer than two records, or a span shorter than one bucket.
- Otherwise **the page moves before the interval.** At the interval in force —
  raised to the floor of 10 seconds if it is below it — the page the peak
  requires is computed with a headroom factor of 4. Within the firewall's
  ceiling, that page at that interval is the suggestion. Beyond it, the page is
  set to the ceiling and the interval shortened until a page of that size keeps
  up, down to the floor; a rate that still overflows is reported as **not
  coverable**, the one outcome that says the source writes faster than
  `opnview` reads.
- A suggestion never lengthens the interval: it only ever asks for a bigger
  page or a shorter interval.
- For the resolver, whose page is its ring buffer and not a setting, only the
  interval is suggested.

The headroom factor and the 10-second floor are `opnview`'s own figures, chosen
and stated as such. The page ceilings are properties of the API, each citing the
survey section that records it: 1000 for the filter log, 9999 for the alert
query, 1000 for the resolver's ring buffer, and none for the lease searches.

Every suggestion passes a final validation, so no path through the derivation
can hand out a pair that breaks one of these rules.

### 5. The collection surface

A fourth surface, `/collection`, signed-in like settings, reachable from the
bar. One card per kind the provider table actually holds, read at run time; a
kind with no implementation on this installation produces no card. Each card
carries three groups under separate headings:

- **in force** — what the firewall reports about the kind's implementations,
  what the operator selected, the page size, the interval, the ceiling and
  whether the page is within it;
- **measurement** — every figure of the measurement and the gaps by reason;
- **suggestion** — the suggested page and interval when there is a pair, and
  always the outcome as a state, so an outcome with no pair says why.

The page suggested and the page in force are two separately labelled rows under
two headings, so a proposal can never be read as a setting.

Each card is its own form with its own save. A refused figure is reported in its
card, against the field that caused it, with what was typed put back. A save is
confirmed in the card that was saved. A **fill** control puts the suggestion into
the fields and stores nothing — the save beside it is the second deliberate act —
and it is disabled where the outcome carries no pair.

Drawing the surface, measuring and filling read the local database only: they
write nothing and contact nothing. A save writes the two rows of one pair.

Every label and state is a catalogue entry. A measured figure is data, shown in
one of three forms only — a whole number, a rate to three decimals, an instant
in RFC 3339, UTC — and the catalogue test's exception for figures covers those
three forms and nothing else.

## Acceptance criteria

Each criterion is held by the test named beside it.

| # | Criterion | Test |
|---|---|---|
| AC1 | The three page sizes default to their documented starting points. | `config` `TestThePageSizesDefaultToTheirDocumentedStartingPoints` |
| AC2 | A setting row overrides every page size. | `config` `TestASettingRowOverridesEveryPageSize` |
| AC3 | A page size that is not a positive count is refused when loaded. | `config` `TestAPageSizeThatIsNotAPositiveCountIsRefused` |
| AC4 | A saved interval changes the next scheduled pass without a restart. | `collect` `TestASavedIntervalChangesTheNextScheduledPassWithoutARestart` |
| AC5 | A saved page size reaches a running collector. | `collect` `TestASavedPageSizeReachesARunningCollector` |
| AC6 | The rate is measured per kind over both clocks. | `store` `TestTheRecordRateIsMeasuredPerKindOverBothClocks` |
| AC7 | The window is bounded by the retention horizon. | `store` `TestTheWindowIsBoundedByTheRetentionHorizon`; `sizing` `TestTheWindowIsTwentyFourHoursBoundedByRetention` |
| AC8 | A window with no record is a state, not a rate of nought. | `store` `TestAWindowWithNoRecordIsAStateAndNotARateOfNought` |
| AC9 | The measurement SQL is the diagnostic in the repository. | `sizing` `TestTheMeasurementSQLIsTheDiagnosticInTheRepository` |
| AC10 | The registry of kinds agrees with the schema's provider registry. | `sizing` `TestTheRegistryAgreesWithTheSchema` |
| AC11 | Every ceiling cites a survey section that exists. | `sizing` `TestEveryCeilingCitesASectionOfTheSurveyThatExists` |
| AC12 | The derivation moves the page before the interval and never past the ceiling. | `sizing` `TestTheDerivationMovesThePageBeforeTheIntervalAndNeverPastTheCeiling` |
| AC13 | No suggestion exceeds the ceiling at any measured peak. | `sizing` `TestNoSuggestionEverExceedsTheCeilingAtAnyMeasuredPeak` |
| AC14 | A degenerate measurement is never rendered as a pair. | `sizing` `TestADegenerateMeasurementIsNeverRenderedAsAPair` |
| AC15 | The route names itself and labels each card. | `web` `TestTheCollectionRouteNamesItselfAndLabelsEachCard` |
| AC16 | A kind with no provider row produces no card. | `web` `TestAKindWithNoProviderRowProducesNoCard` |
| AC17 | The surface reports every figure it measures. | `web` `TestTheCollectionSurfaceReportsEveryFigureItMeasures` |
| AC18 | A source that is not collected is its own state and gets no suggestion. | `web` `TestASourceThatIsNotCollectedIsItsOwnStateAndGetsNoSuggestion` |
| AC19 | The fill control is not offered with nothing to fill with. | `web` `TestTheFillControlIsNotOfferedWithNothingToFillWith` |
| AC20 | The fill control writes nothing and fills the fields. | `web` `TestTheFillControlWritesNothingAndFillsTheFields` |
| AC21 | A saved pair is stored, and a non-number is refused. | `web` `TestASavedPairIsStoredAndANonNumberIsRefused` |
| AC22 | A saved pair reaches the running service without a restart. | `web` `TestASavedPairReachesTheRunningServiceWithoutARestart` |
| AC23 | A save is confirmed in the card it was saved in. | `web` `TestASaveIsConfirmedInTheCardItWasSavedIn` |
| AC24 | A refused figure is reported against the field that caused it. | `web` `TestARefusedFigureIsReportedAgainstTheFieldThatCausedIt` |
| AC25 | An interval no duration represents is refused. | `web` `TestAnIntervalNoDurationRepresentsIsRefused` |
| AC26 | A page size above the ceiling is stored and reported. | `web` `TestAPageSizeAboveTheCeilingIsStoredAndReported` |
| AC27 | Drawing the surface and measuring write nothing. | `web` `TestDrawingTheSurfaceAndMeasuringWriteNothing` |
| AC28 | The measurement contacts nothing. | `web` `TestTheMeasurementContactsNothing` |
| AC29 | Only the three figure forms the pages produce pass the figure exception. | `web` `TestOnlyTheThreeFigureFormsThePagesProducePassTheFigureException` |
| AC30 | The lower-bound caveat is documented and never printed. | `web` `TestTheLowerBoundCaveatIsDocumentedAndNeverPrinted` |

The test names are those of the compiled build of 2 October. Their bodies were
lost with this document and are rewritten; the numbering of the criteria is this
rewrite's own.

## Out of scope

- **Applying a suggestion automatically.** A suggestion is shown; it becomes the
  pair in force only when the operator fills the fields and saves. Nothing in
  the product changes a polling figure on its own.
- **Sizing the per-pair sampler.** It reads a snapshot with no paging and has no
  record of its own to measure.
- **Correcting the lower bound.** The measured rate counts what was stored; no
  estimate of what was missed is added to it.
- **Measuring against the firewall.** Every figure comes from the local
  database; the surface makes no outbound call.

## Risk

- **A page size the firewall does not honour.** The filter log answers a request
  above its ceiling with the ceiling rather than an error, so a page typed above
  it would silently not take effect. It is stored, and the surface shows it as
  above the ceiling, beside the ceiling itself.
- **A rate measured on a source nobody reads.** A kind whose implementations are
  all turned off or unreachable writes no rows; a measurement of it is a
  measurement of the past. It gets no suggestion, and the card says why.
- **A saved pair that reaches the database and not the service.** The save loads
  the configuration again, through the path start-up uses, and hands it to the
  live holder and the collector; two tests hold that end to end.
