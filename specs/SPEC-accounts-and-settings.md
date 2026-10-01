# SPEC — Step 4B: accounts, sign-in, and the settings surface credentials enter through

Status: APPROVED
Cycle kind: interface

## Why now

`ROADMAP.md` step 4: *"First-run setup: creating the first account, then the
firewall URL, the API key/secret, the MaxMind key and the theme. **This is where
credentials enter the product, so nothing before it can collect from a real
firewall.**"*

Today the only way to see real data is to hand an API key to an agent and run a
throwaway script. `cmd/opnview/main.go` builds `opnsense.NewClient` with **empty
credentials** and says so in its own package comment; `internal/config` reads the
`setting` table and its own defaults and holds no credential; there is no account
table, no session table, no HTTP tree anywhere in the repository. After this cycle
the maintainer types the key into the product, and step 4's validation — real
collection against a real OPNsense — becomes reachable for the first time.

The security design is **already decided** in `ROADMAP.md`, *Rules that apply to
every step*, under *`opnview` has its own accounts*, and was re-recorded as
decision 3 of `specs/SPEC-collection-and-storage-core.md`. This spec restates it
as criteria and does not revisit it.

## Decisions taken before this spec was written

Nine questions were open when this cycle was planned. All nine are settled, and
they are recorded here so none is re-litigated mid-cycle.

1. **4B builds the routing and session skeleton step 5 extends** — not a
   disposable server. It serves three things and no more: setup, sign-in/sign-out,
   settings. Step 5 adds widget endpoints by adding files, not by rewriting
   `main.go`. Cost accepted deliberately: steps 5 and 7 inherit this routing and
   session shape.
2. **The setup surface demands a one-time token printed on the console** at first
   start. The product ships into an LXC reachable from the network, and a setup
   surface that is public by construction is a race anybody on the LAN can win.
   Reading one line of the container's log is a step the maintainer already takes.
3. **Settings live in the existing `setting` table; the two ciphertexts do not.**
   A ciphertext needs a nonce, an algorithm label and a key identifier beside it,
   and `setting.value` is a single `TEXT NOT NULL`. The firewall URL and the theme
   are `setting` rows, because that is where `internal/config` already looks and a
   second location would be a second truth.
4. **The theme is one `setting` row for the installation.** No per-account
   preference: there is no such concept in the schema and one account cannot
   justify inventing one.
5. **The account table can hold several rows; this cycle creates exactly one, and
   there are no roles.** Nothing in `ROADMAP.md`, the schema or the survey mentions
   a second user, a role, a permission or an invitation. A constraint restricting
   the table to one row would be honest today and a migration tomorrow, and there
   are no migrations.
6. **The session cookie carries no `Secure` flag**, `SameSite=Lax`, and the reason
   is written into the README's limitations: `Secure` makes sign-in impossible over
   plain HTTP and there is no TLS story before step 8. **A CSRF token is built
   now**, because setup and settings are mutating forms and step 5's mutating
   endpoints would otherwise inherit nothing.
7. **A minimal string catalogue ships in 4B**, English only, which step 7 grows.
   `ROADMAP.md` step 7 says no user-visible string is a literal and exempts only
   the step-3 mockup; shipping real interface with literals would break that rule
   the day it was written.
8. **Sessions expire on a sliding idle window with an absolute cap.** No lockout
   and no delay after repeated failures: nothing in the requirement asks for one,
   and AC7's indistinguishable failures are what the requirement does ask for.
9. **A forgotten password is recoverable by possession of the key file.** Whoever
   holds that file already holds the decryptable secrets, so requiring it grants an
   attacker nothing they did not have, and it returns the installation without
   deleting the database.

## What changes

### The seam

**The server owns routing, sessions and the static-asset story; a feature owns its
handlers and registers them.** That is the seam step 5 crosses.

Packages, named by what they hold. The internal structure is the Builder's.

| Package | Holds |
|---|---|
| `internal/web` | the HTTP server, routing, session and CSRF middleware, the setup / sign-in / settings handlers, the embedded templates and assets, and the string catalogue |
| `internal/auth` | password hashing and verification, account creation, session issue / validate / revoke, the setup token |
| `internal/secret` | the key file beside the database, its creation with restrictive permissions, and authenticated encryption of the two stored credentials |
| `internal/store` | `schema.sql` gains the account, session and encrypted-credential tables, and typed read/write paths for them |
| `internal/config` | surfaces the firewall URL and the decrypted credentials to the collectors, and notices a change without a restart |
| `cmd/opnview` | starts the HTTP server alongside the scheduler and stops both on the existing signal path |

### What is a seam here, and what is not

The modularity rule is a product property. It is also a rule against building a
registry for a problem nobody has.

- **A password hasher is not a seam.** One algorithm is mandated. What
  `ROADMAP.md` asks for is *parameter agility* — the parameters stored beside the
  hash so they can be raised later — and that is a column layout, not a registry.
- **A secret cipher is not a seam**, with the same mitigation: an algorithm label
  and a key identifier beside each ciphertext, so a future scheme is a second
  label rather than a second copy of the product.
- **A session store is not a seam.** There is one store, and the storage rule
  forbids provisioning another.
- **What is a seam, and already exists, is the credential source the OPNsense
  client reads.** 4A hardcodes `opnsense.Credentials{}` at one call site. Replacing
  it with something the settings surface updates while the process runs is what
  makes a typo recoverable without a restart.

### Verifying what the user typed

A credential check against the firewall **is** the first of the two permitted
outbound calls — same destination, same registry, same read-only guarantee — not a
third. It reuses one endpoint already in `opnsense.Registry()` and the existing
`Outcome` vocabulary, which already separates 401/403 from 404 from unreachable.

**The MaxMind key is not verified in this cycle.** Verifying it means downloading,
downloading is 4C, and a verification download here would be a call made before it
is needed.

### Documentation

`README.md`'s *Limitations, stated up front* gains the key-file limit in the words
`ROADMAP.md` uses, and the cookie's missing `Secure` flag. `docs/data-model.md`
documents the new tables in its existing per-entity form. **No new document.**

## Acceptance criteria

- [ ] **AC1** — `docker compose run --rm checks` and `schema-checks` both exit 0,
      with **no existing assertion removed or relaxed**, including
      `TestDiscoveryAndAllFiveCollectorsReachTheFirewallAndNothingElse`.
- [ ] **AC2** — the schema stays **one file**, applied idempotently. A second apply
      changes no row count in any table, and seeds no account, no session and no
      credential row.
- [ ] **AC3** — on a data directory with no database, the service creates it,
      applies the schema, **prints a one-time setup token to the console**, and
      starts. A test asserts the account table is empty and the service ran anyway.
- [ ] **AC4** — **the setup surface demands the token.** Setup without it, or with
      a wrong one, is refused and creates nothing. The token is single-use: a test
      completes setup and asserts a replay of the same token is refused. The token
      is never written into the database and never appears in a response body.
- [ ] **AC5** — **collection does not wait for a sign-in.** With credentials stored
      and no session in existence, a scheduled pass reaches the firewall fake.
      Signing out does not stop it: a test signs out and the next pass still runs.
- [ ] **AC6** — creating the account stores an **Argon2id** hash with a per-account
      salt and the algorithm parameters **in the record**. A test proves: the stored
      value contains the password nowhere; verification reads the parameters from
      the record, so a record written with one parameter set still verifies after
      the product's defaults are raised; and no code path returns a hash, a salt or
      a parameter set in an HTTP response.
- [ ] **AC7** — **the setup surface closes permanently.** Once one account exists, a
      request to setup cannot create a second account or reset the first — asserted
      with a valid token, with a spent one, and **again after a restart against the
      same data directory**.
- [ ] **AC8** — signing in with the right password yields a session; with the wrong
      one it does not, and the two responses are **indistinguishable** in what they
      disclose about whether the login exists. Signing out invalidates the session
      and a replay of it is refused.
- [ ] **AC9** — a session expires on a **sliding idle window** and on an **absolute
      cap**, both asserted, and an expired session is refused rather than renewed.
- [ ] **AC10** — every route is either **explicitly public** (setup, sign-in, static
      assets) or refuses an unauthenticated request. A test **enumerates the
      registered routes** and asserts each one falls in exactly one of those two
      sets, so a route added later without a decision fails the test rather than
      shipping open.
- [ ] **AC11** — **every mutating form carries a CSRF token**, and a submission
      without one, or with one belonging to another session, is refused. Asserted on
      setup and on settings.
- [ ] **AC12** — the **key file** is created beside the database on first start with
      permissions denying group and other, asserted on the file inside the
      container. It is never written into the database and never appears in a
      response.
- [ ] **AC13** — the OPNsense secret and the MaxMind key are stored under
      **authenticated encryption**, with the nonce, the algorithm label and a key
      identifier beside the ciphertext. A test proves: the stored bytes do not
      contain the plaintext; a **tampered ciphertext fails to decrypt** and is
      reported as a failure rather than silently yielding anything; a round trip
      returns exactly what was stored.
- [ ] **AC14** — **a lost or replaced key file is a named state, not silence.** With
      the database intact and the key file gone, the service starts, reports that the
      stored credentials cannot be decrypted **as a distinct state**, and offers
      re-entry. It does not report "no firewall configured" — a test asserts the two
      are distinguishable.
- [ ] **AC15** — **a forgotten password is recoverable by possession of the key
      file**, and by nothing else. A test proves the reset path succeeds when the
      key file is presented and is refused without it, and that it cannot be reached
      from an unauthenticated HTTP request that does not present it.
- [ ] **AC16** — **changing a credential takes effect without a restart.** A test
      stores a URL and key/secret through the settings path, asserts the next pass
      uses them, changes the secret, and asserts the pass after that uses the new
      one **in the same process**. Overwriting leaves **no prior plaintext anywhere**
      and **no prior ciphertext in any row**.
      **Amended 2026-10-01**, because the original sentence said *"no prior ciphertext
      readable in the database"* and that is broader than what is true. A credential is
      one row per name replaced with `ON CONFLICT DO UPDATE`, so the prior ciphertext is
      overwritten in place and no row holds it — which is what the tests establish, by
      reading through SQL. The **write-ahead log is a different matter**: while the
      service runs, `<database>-wal` holds both the prior and the current ciphertext, and
      `secure_delete` says nothing about the WAL in either setting. The residue is
      AES-GCM under the same key file, so it discloses nothing to anyone without that
      file and only a **superseded** secret to anyone holding both. Stated rather than
      dressed up, beside the pragma and in the README's limitations.
- [ ] **AC17** — **verification is the permitted firewall call and nothing else.** It
      calls one endpoint already in `opnsense.Registry()`, read-only, **cited against
      `docs/opnsense-api-survey.md` in a comment beside the call**. A test asserts no
      outbound destination other than the configured firewall is contacted during
      setup, sign-in or settings, and that a 401/403 reads as a credential failure, a
      404 as a module absence and an unreachable host as unreachable — **none of the
      three worked around or reported as success**.
- [ ] **AC18** — **4B makes no MaxMind call.** The key is stored and not verified; a
      test asserts no outbound request to any MaxMind host occurs in this cycle's
      paths.
- [ ] **AC19** — **zero hardcoded configuration.** No URL, host name, address, CIDR,
      interface name or port of any real network appears as a default, a placeholder,
      an example, a template `value=`, a fixture or a comment. Test credentials are
      generated in the test. A grep-shaped assertion over the new packages and
      templates enforces it.
- [ ] **AC20** — **no secret in the repository.** No key, secret, password or key
      file is committed; the data directory and the key file sit outside the
      bind-mounted tree in the compose environment, as the database already does.
- [ ] **AC21** — **no user-visible string is a literal.** Every one comes from the
      minimal English catalogue this cycle ships. A test asserts no served template
      or handler emits a user-visible literal, and that every catalogue entry is
      reachable and every reached key exists.
- [ ] **AC22** — **no explanatory copy in the interface.** No paragraph explaining
      the product, no recitation of the observation-point limit, no recitation of the
      key-file limitation, no tutorial text. A field is named by its label. A test
      asserts the characteristic phrases of both limitations are absent from the
      embedded assets and the catalogue.
- [ ] **AC23** — **nothing is loaded from off-host.** No font, script, stylesheet,
      favicon or analytics from an external origin. A test asserts no served asset
      contains an absolute off-host URL.
- [ ] **AC24** — the three surfaces are written against the binding documents and
      cite them: the industrial palette at `0.25rem` radius, system fonts, and the
      operating system's colour-scheme preference on first launch with an override
      that persists as a `setting` row — the rule as `ROADMAP.md` step 3 and
      `docs/ui-references.md` state it, not paraphrased into a new list here.
- [ ] **AC25** — `README.md`'s *Limitations, stated up front* carries the key-file
      limit in `ROADMAP.md`'s words and the cookie's missing `Secure` flag;
      `docs/data-model.md` documents the new tables in its existing per-entity form,
      with any `opnview`-own term recorded in its *Vocabulary* section. **No new
      document is created.**
- [ ] **AC26** — the product still starts and stops cleanly: `SIGTERM` stops the
      HTTP server and the scheduler, and the existing `PRAGMA integrity_check` path
      still reports `ok`.
- [ ] **AC27** — **step 4's validation becomes performable**: a documented sequence
      — start, read the token, create the account, sign in, enter the URL and
      key/secret, see the verification outcome — is exercisable end to end against
      the existing firewall fake, with **no credential supplied by any means other
      than the settings surface**.

### For maintainer review — judgement, not criteria

- Whether the three surfaces read as the same product as the step-3 mockup.
- Whether settings are usable **without** a sentence of explanation. If a field
  needs one, the field is wrong — a finding for the next cycle, not a licence to
  add copy.
- Whether the verification outcome tells him which of *wrong URL*, *wrong key*,
  *module absent* or *host unreachable* he has.

## Out of scope

- **MaxMind acquisition, refresh and the `geo_asn` provider.** That is 4C.
- **The widget HTTP API.** Step 5. 4B adds routing, not widget endpoints.
- **The canvas frontend**, canvases, tabs, widgets, the dashboard format,
  import/export. Step 7.
- **The theme-as-a-file and widget-as-a-directory registries, and the language
  picker.** Step 7. This cycle ships one catalogue in one language.
- **A second account, roles, permissions, multi-factor, password reset by email.**
- **TLS termination, reverse proxy, systemd, the installer.** Step 8.
- **The plugin engine, the connector protocol, per-kind package directories.**
- Migrating anything in the existing schema.

## Risk

**The setup surface staying reachable after the first account exists.** It is the
classic first-run hole, and the token narrows the window without closing this one:
if the state that ends setup is computed loosely — a flag in memory, a check only
on the `GET`, a check a restart resets — anyone on the LAN owns the installation.
AC7 asserts it across a restart and with a spent token, and that is the criterion
to get right before any other.

Second: **the credentials reaching the database but not the running collectors**,
because `main.go` builds the client once at start-up (AC16).

Third: **Argon2id forces a new direct dependency.** The standard library has none.
The global build rule applies — read the actual declared transitive set and report
it **in full before adding it**, exactly as `modernc.org/sqlite` was handled in 4A.
AES-GCM is in the standard library, so the AEAD adds nothing. `UNVERIFIED:` the
current dependency set of `golang.org/x/crypto`; the Builder audits it.

Fourth: **file permissions are the one thing the Windows host cannot prove.** The
mode assertion is meaningful only inside the container, and it proves nothing about
the unprivileged-LXC uid mapping, which is step 8.
