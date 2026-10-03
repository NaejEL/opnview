# SPEC — Step 4C: the MaxMind GeoLite2 databases and the `geo_asn` provider

Status: APPROVED — the four decisions below were taken by the maintainer on
3 October 2026.
Cycle kind: standard

## Why now

Step 4 ends with the second and last outbound call the project allows. The
licence key has been entered on the settings surface since 4B and has never been
used; `geo_asn` has a schema, a purge, seed rows, screen queries and six widget
presets reading it, and not one row written by the product. Until the databases
are acquired, every destination is unplaced and every operator unknown.

## What MaxMind requires, verified on 3 October 2026

From `dev.maxmind.com/geoip/updating-databases` and the GeoLite EULA:

- **A direct download authenticates with the account ID AND the licence key**,
  by HTTP Basic authentication. The product stored the key only.
- The permalink is
  `https://download.maxmind.com/geoip/databases/<edition>/download?suffix=<suffix>`;
  the binary databases are gzip archives. The two editions read here are
  `GeoLite2-City` and `GeoLite2-ASN`.
- **The download is redirected** to an R2 presigned URL on
  `mm-prod-geoip-databases.a2649acb697e2c09b632799562c076f2.r2.cloudflarestorage.com`,
  and the client must follow it.
- A `HEAD` request does not count against the download limit, and its
  `Last-Modified` header carries the build date. GeoLite accounts are limited
  to 30 downloads a day; a request over the limit is answered `429`.
- The EULA requires old versions to be destroyed within 30 days of a new
  release, and requires attribution of the use to MaxMind.

## Decisions taken

1. **The account ID is a `setting` row**, `maxmind_account_id`, entered on the
   settings surface beside the licence key. It identifies an account and grants
   nothing on its own, like the firewall URL; the licence key stays an
   `encrypted_credential`. It is a positive whole number.
2. **The reader is `github.com/oschwald/maxminddb-golang/v2` v2.7.0**, pure Go,
   so `CGO_ENABLED` stays 0. Its transitive set was read before it was added:
   its only runtime requirement is `golang.org/x/sys` v0.48.0, the version the
   module graph already holds; `testify`, `x/tools`, `x/mod`, `x/sync` and
   `yaml.in/yaml/v3` enter the module graph for its own tests and tool and are
   not linked into the binary. The full `go list -m all` is reported with the
   cycle.
3. **`internal/maxmind` is the second and last package allowed to build an HTTP
   request.** The source-policy test changes from "only `internal/opnsense`" to
   "exactly `internal/opnsense` and `internal/maxmind`". Its client refuses any
   host but `download.maxmind.com` and the R2 host above, redirects included,
   and sends the credentials to `download.maxmind.com` only: Go drops the
   `Authorization` header on a redirect to another host, and a test holds it.
4. **The refresh is a `HEAD` every 24 hours, and a `GET` only when the build
   changed.** At start, then every `refresh_interval_geoip_seconds` (default
   86 400). A save of the key or of the account ID on the settings surface
   wakes the refresh at once instead of waiting a day.

## What changes

### 1. Acquisition

For each of the two editions: `HEAD` the permalink; when no database is on disk,
or `Last-Modified` is newer than the build of the one on disk, `GET` it, read
the `.mmdb` out of the gzip tar archive, open it, and only once it opens and its
metadata names the expected edition, move it over the previous file in
`<data-dir>/geoip/`. The previous file is gone the moment it is replaced, which
meets the 30-day rule by construction. A failed download leaves the database in
use untouched.

Without a key or without an account ID nothing is requested at all.

### 2. The provider's availability

The `geo_asn` provider `maxmind_geolite2` gets its `source_availability` row,
which describes the **dataset**, not one lookup: `reachable` when both databases
are on disk and open, with the City build date in the detail; `unavailable`
otherwise, with the reason as the probe — no licence key, no account ID, the
download refused (401/403), the download limit reached (429), or the download
failed. The provider is active exactly when the dataset is reachable.

### 3. Lookups

A task resolves the addresses the stored flows carry that have no `geo_asn` row,
or a row resolved against an older build than the one on disk, in bounded
batches. Only a globally routable address is looked up; a private, loopback,
link-local or otherwise non-public address is never sent to the reader. A found
address is `resolved` with country, coordinates, ASN and operator and the build
date; an address absent from the City database is a `miss`. With no dataset,
nothing is written: an unreachable dataset produces neither resolved rows nor
misses, as the data model says.

### 4. The settings surface

The geolocation card gains the account ID field and a state naming where the
dataset stands: no key, no account ID, not yet downloaded, downloaded (with its
build date), refused, limit reached, failed. A save of either value wakes the
refresh.

### 5. Attribution

`README.md` carries MaxMind's attribution, as the EULA requires.

## Acceptance criteria

| # | Criterion |
|---|---|
| AC1 | Without a licence key or an account ID, no request leaves for MaxMind, and the dataset state says which is missing. |
| AC2 | A download sends the account ID and the key by Basic authentication to `download.maxmind.com`, follows the redirect, and sends no credential to the redirected host. |
| AC3 | Any host but the two is refused, redirects included, and fails the test. |
| AC4 | A `HEAD` whose `Last-Modified` is not newer than the database on disk downloads nothing. |
| AC5 | A newer build replaces the file only after it opens and names the expected edition; a corrupt or truncated archive leaves the previous database in use. |
| AC6 | `401`/`403` and `429` are reported as their own states, distinct from a transport failure. |
| AC7 | The availability row of `maxmind_geolite2` describes the dataset, and the provider is active exactly when the dataset is reachable. |
| AC8 | Public addresses from `flow` are resolved, with the build date and the provider; an absent one is a `miss`; a private or otherwise non-public address is never looked up. |
| AC9 | Rows resolved against an older build are looked up again after a new build. |
| AC10 | The account ID is a setting row, validated as a positive whole number; the key stays encrypted. |
| AC11 | A save of the key or the account ID wakes the refresh. |
| AC12 | Exactly two packages build an HTTP request, and the only absolute URLs are the OPNsense registry citations and the MaxMind permalinks. |
| AC13 | The dependency set is reported in full, and `go list -m all` matches the audit. |

## Out of scope

- The map and every other widget: step 7. Until then the dataset state on the
  settings surface is where the product says the map will have nothing to show.
- Any geolocation source other than MaxMind.
- Checksums published beside the archives: not used, because the database is
  verified by opening it before it replaces the previous one.

## Risk

- **The download limit.** A misconfigured refresh could spend the 30 daily
  downloads. A `GET` happens only after a `HEAD` reports a newer build, and a
  `429` stops the round.
- **The redirect host changes.** MaxMind names one R2 host today. If it moves,
  downloads fail with the refused-host state rather than following an
  unexpected host, and the allow-list is updated deliberately.
- **This is the first call to a third party.** Everything the firewall client
  guarantees about outbound traffic is re-established here by its own test,
  not inherited.
