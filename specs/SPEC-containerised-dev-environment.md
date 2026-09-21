# SPEC — Containerised development and test environment (Debian trixie)

Status: APPROVED

## Context

The production target is an unprivileged Proxmox LXC running Debian. A Windows
binary is never an artifact of this project. Windows also masks precisely the
bug classes `opnview` will hit: file mode and umask on the SQLite file and its
`-wal` / `-shm` companions, filesystem case sensitivity, `SIGTERM` handling for
the future systemd unit, and normalising the OPNsense filter-log timestamps,
which carry neither year nor timezone (`docs/opnsense-api-survey.md`).

The repository contains no Go code yet, which is the right moment to adopt a
containerised Debian toolchain: no line of this project is ever built or tested
on the host OS.

Two pre-existing inconsistencies must be removed by this cycle, not worked
around:

- `ci/factory.sh` and `ci/factory.ps1` carry a tool allowlist that explicitly
  permits host `go` and `gofmt` execution. This cycle must **remove**
  capability, not only add it.
- `.claude/agents/factory-verifier.md` instructs the Verifier to run
  `go` / `gofmt` / `go test` directly and to raise a **critical** issue when
  `go` is missing from the machine. Left as is, it produces a false critical on
  every future cycle.

Reference facts, verified on this machine and to be treated as the baseline of
this cycle: Docker engine 28.3.2, `linux/x86_64`, compose v2.38.2, WSL2 with a
Debian distribution; `debian:trixie-slim` is Debian 13.7 carrying `sqlite3`
3.46.1-7+deb13u2 and `gcc` 14.2.0; the host Go toolchain is go1.27.0.

## Decisions taken with the maintainer

1. **A minimal Go module is created this cycle** (Branch B). `go build ./...`
   and `go test ./...` must genuinely run, so a defect in the image surfaces
   now rather than at step 4. This is a deliberate amendment to the roadmap's
   "first module at step 4" position.
2. **Module path: `github.com/NaejEL/opnview`**, matching the configured git
   remote. Not a maintainer preference — read from `git remote -v`.
3. **The container runs as a non-root user**, UID and GID supplied as build
   arguments defaulting to 1000. Closer to the unprivileged LXC, and it
   exercises the permission and umask behaviour that motivates the cycle.
4. **The factory allowlist grants exactly one command**: the checks entry
   point. Not `docker *`, not `docker compose *`. Minimal surface.
5. **`.claude/agents/factory-verifier.md` is amended in this cycle**, so the
   Verifier stops demanding a host Go toolchain.
6. **`debian:trixie-slim` is pinned by tag**, not by digest, so Debian security
   updates arrive automatically. Consequence: `sqlite3` and `gcc` versions may
   move under the tag, so acceptance criteria assert major versions only.
7. **A `.devcontainer/` definition is included**, so VS Code "Reopen in
   Container" runs the editor's Go tooling inside Debian too. Without it the
   editor would still hunt for a Go toolchain on the host.
8. **Two compose files, not one with profiles.** The file created here is the
   development environment and keeps the name `docker-compose.yml`. Step 8 will
   add a separate deployment compose file for non-Proxmox users.
9. **`README.md` is not modified.** It is end-user-facing and belongs to step 8.
10. **No data-directory environment variable is surfaced** to the future
    application. Inventing its name here would pre-empt step 4.

## Scope

### Files to create

1. **`Dockerfile`** (repository root) — development and test image.
   - Base `debian:trixie-slim`, pinned by tag.
   - An explicitly pinned Go toolchain, the version written **once** as a build
     argument and stated in a header comment. The pin is **go1.27.0**, matching
     the host. The tarball download is integrity-checked against a pinned
     SHA-256 literal; an unverified download is a failure of this cycle, never
     a shortcut, and a checksum mismatch must never be masked to make the build
     pass.
   - `GOTOOLCHAIN=local`, so a future `go.mod` declaring a higher `go`
     directive fails loudly instead of silently fetching another toolchain over
     the network at test time.
   - Packages: `sqlite3` (CLI), `ca-certificates`, `gcc`, `tzdata`. `gcc` keeps
     the cgo SQLite driver an open option; `tzdata` is required to exercise
     filter-log timestamp normalisation.
   - The image must **not** set `CGO_ENABLED=0`, which would foreclose the
     driver choice in the other direction. This cycle makes no driver choice
     and adds no Go dependency.
   - A non-root user created from `UID` / `GID` build arguments defaulting to
     1000, owning the cache and data mount points so a fresh named volume is
     writable.
   - `GOMODCACHE` and `GOCACHE` pointed at directories backed by named volumes,
     never at the bind mount.
   - A header comment recording the pinned Go version, the base image, and the
     scope limits under *Out of scope*.

2. **`docker-compose.yml`** (repository root) — development environment.
   - Builds from the `Dockerfile` above, passing the UID/GID build arguments.
   - A `dev` service for arbitrary commands, repository bind-mounted read-write
     at a fixed in-container path, `working_dir` set to it.
   - A `checks` service whose command is **baked into the compose file**, so
     the host invocation carries no quoting and is byte-for-byte identical in
     PowerShell and in bash.
   - Three **named volumes**: Go module cache, Go build cache, SQLite data
     directory — all mounted at paths **outside** the bind mount. A compose
     comment states the reason: SQLite in WAL mode on a Windows bind mount has
     pathological locking behaviour, and a database file on the bind mount
     would also pollute the working tree.
   - No published ports, no environment file, no secret, no OPNsense URL, no
     network configuration of any kind.

3. **`.dockerignore`** — the build context needs essentially nothing, since
   sources arrive through the bind mount rather than through `COPY`. Excludes
   at minimum `.git/`, `factory-logs/`, `data/`, `*.db`, `*.db-wal`,
   `*.db-shm`, `*.mmdb`, `.env`, `config.local.*`.

4. **`ci/checks.sh`** — the checks entry point run inside the container,
   executing in order and reporting each exit code: `gofmt -l .`,
   `go vet ./...`, `go build ./...`, `go test ./...`. LF-terminated in the
   working tree; adding `.gitattributes` to guarantee that is in scope.

5. **A minimal Go module**: `go.mod` declaring module path
   `github.com/NaejEL/opnview` and a `go` directive matching the pinned
   toolchain, plus a trivial package and a trivial `*_test.go` — enough that
   `go build ./...` and `go test ./...` are meaningful rather than vacuous. A
   `go test ./...` matching no package is not evidence the toolchain works. No
   third-party dependency, no SQLite driver.

6. **`.devcontainer/devcontainer.json`** reusing the compose `dev` service, so
   VS Code "Reopen in Container" gives the editor the same Debian toolchain.

### Files to modify

7. **`ci/factory.sh`** and **`ci/factory.ps1`** — the allowlist must no longer
   permit `Bash(go *)` or `Bash(gofmt *)`, and must permit exactly the checks
   entry point invocation. Both scripts fail with a clear, actionable message
   when the Docker CLI or the compose plugin is unavailable, in the style of
   the existing `claude`-not-found branch. Both expose the checks run as a mode
   reachable without launching a factory cycle. The two files stay behavioural
   twins and their allowlist strings stay identical.

8. **`.vscode/tasks.json`** — add a container-checks task and a
   container-shell task. For the new tasks the POSIX `command` and the
   `windows.command` override are the same string, or the override is omitted
   because it is unnecessary. The three pre-existing tasks are unchanged.

9. **`.claude/agents/factory-verifier.md`** — amend so the Verifier runs the
   four project commands **through the container**, and so a missing host Go
   toolchain is never a defect. The Verifier must never record a host-Go result
   as the project result.

10. **`ROADMAP.md`** — insert a `##` section titled exactly
    `Development and test environment` **before** the `## Steps` heading, and
    change step 4's `Prerequisite: **install Go**.` to point at it. The step
    table stays at **eight** rows, unchanged numbering, titles, deliverables
    and statuses. No ninth step, no renumbering.

## Project commands

The four project commands are **not** N/A for this cycle. Each runs inside the
container, through the single canonical invocation, and each must exit `0`,
with `gofmt -l .` producing empty output. The Verifier records the four exit
codes and the `gofmt` output, and runs them **in the container, never on the
host**.

## Acceptance criteria

Every criterion is checked by executing a command, except where it says
"inspection". All `docker` commands run from the repository root.

- [ ] AC1 — `docker compose build` exits `0`, and `docker compose build
      --no-cache` also exits `0`.
- [ ] AC2 — `docker compose run --rm dev cat /etc/os-release` reports
      `ID=debian` and `VERSION_ID="13"`; `docker compose run --rm dev uname -s`
      prints `Linux`.
- [ ] AC3 — `docker compose run --rm dev go version` prints exactly the version
      pinned in the `Dockerfile` (`go1.27.0`), and that version string appears
      literally in the `Dockerfile` (inspection). A mismatch fails.
- [ ] AC4 — `docker compose run --rm dev go env GOTOOLCHAIN` prints `local`.
- [ ] AC5 — Inside the container, `sqlite3 --version` exits `0` and prints a
      3.x version, `gcc --version` exits `0`, and `gofmt --help` runs and
      prints its usage.
- [ ] AC6 — The image can compile C: a one-line C program compiled inside the
      container with `gcc` produces a binary that runs and exits `0`. Neither
      the `Dockerfile` nor `docker-compose.yml` sets `CGO_ENABLED=0`
      (inspection), and no Go dependency or SQLite driver appears anywhere in
      the diff — `go.sum` is absent or empty.
- [ ] AC7 — `tzdata` is usable: `ls /usr/share/zoneinfo/UTC` exits `0` inside
      the container, and comparing `TZ=UTC date +%z` with a non-UTC `TZ` shows
      different offsets. The non-UTC zone is supplied on the command line by
      the person running the check; no timezone is hardcoded in any repository
      file.
- [ ] AC8 — `ca-certificates` is effective: inside the container the CA bundle
      referenced by the system trust store exists and is non-empty. No network
      call is made to prove this.
- [ ] AC9 — SQLite in WAL mode works on the data volume: creating a database
      under the data-volume mount point with `PRAGMA journal_mode=WAL` and
      inserting a row inside a held transaction shows `-wal` and `-shm` files
      alongside it, and the row is still readable in a second
      `docker compose run --rm` invocation. The mount point is read from
      `docker compose config`, not typed from memory.
- [ ] AC10 — Nothing written in AC9 lands on the bind mount: host
      `git status --porcelain` lists no `*.db`, `*.db-wal` or `*.db-shm` path,
      and no untracked file beyond what this cycle legitimately adds.
- [ ] AC11 — `docker compose config --volumes` lists three named volumes (Go
      module cache, Go build cache, data). `docker compose run --rm dev go env
      GOMODCACHE GOCACHE` returns two paths, each a mount point of one of those
      volumes and neither under the bind-mounted working directory.
- [ ] AC12 — The Go caches persist across containers: a file written under
      `GOMODCACHE` is still present in a subsequent `docker compose run --rm
      dev` invocation.
- [ ] AC13 — The container runs as a non-root user (`id -u` is not `0`), and
      that user can write to the bind mount, to `GOMODCACHE`, to `GOCACHE` and
      to the data volume: four `test -w` checks, all exiting `0`, in a single
      invocation.
- [ ] AC14 — The UID/GID build arguments are effective: building with a
      non-default UID produces a container whose `id -u` matches it.
- [ ] AC15 — A single canonical invocation runs the four project commands, and
      the **exact same string** works from PowerShell and from bash. The
      Verifier runs it in both shells and records both exit codes; they must
      match, and both must be `0`.
- [ ] AC16 — All four project commands pass in the container: `gofmt -l .`
      exits `0` with empty output, `go vet ./...` exits `0`, `go build ./...`
      exits `0`, `go test ./...` exits `0` and reports at least one package
      with at least one test actually run — a result matching no package fails
      this criterion.
- [ ] AC17 — `go.mod` declares module path `github.com/NaejEL/opnview` and a
      `go` directive consistent with the pinned toolchain (inspection).
- [ ] AC18 — The canonical invocation requires no knowledge of the Windows
      `PATH`: no repository file invokes `go`, `gofmt` or `sqlite3` outside a
      container context (`grep` over `ci/`, `.vscode/tasks.json`,
      `docker-compose.yml`, `.devcontainer/` — inspection), and the invocation
      succeeds in a shell where those binaries are not on `PATH`.
- [ ] AC19 — `ci/factory.sh` and `ci/factory.ps1` no longer grant `Bash(go *)`
      or `Bash(gofmt *)`, and grant exactly the checks entry point — not
      `docker *`, not `docker compose *`. The two allowlist strings are
      identical between the two files (a diff of the extracted strings is
      empty).
- [ ] AC20 — Both factory scripts exit non-zero with a message naming Docker
      when the Docker CLI or the compose plugin is absent. Verified by running
      each with a command lookup in which `docker` is not resolvable; the
      message is actionable, not a stack trace.
- [ ] AC21 — `.claude/agents/factory-verifier.md` no longer instructs the
      Verifier to run `go` / `gofmt` / `go test` on the host, no longer treats
      a missing host Go toolchain as a critical issue, and directs the four
      commands through the container (inspection).
- [ ] AC22 — `bash -n ci/factory.sh` and `bash -n ci/checks.sh` exit `0`;
      `shellcheck` on both reports no error-level finding; `ci/factory.ps1`
      parses without error.
- [ ] AC23 — Every `*.sh` file in the repository is LF-terminated in the
      working tree (a command counting CR bytes returns `0`), and the checks
      script executes inside the container without an interpreter error.
- [ ] AC24 — `.vscode/tasks.json` is valid JSON (parsed by a command, not by
      eye) and contains a container-checks task and a container-shell task
      whose `command` and `windows.command` are the same string, or whose
      `windows.command` is absent. The three pre-existing tasks are unchanged.
- [ ] AC25 — `.devcontainer/devcontainer.json` is valid JSON, references the
      compose `dev` service rather than duplicating its definition, and adds no
      host Go requirement.
- [ ] AC26 — No secret anywhere: `docker compose config` output, the
      `Dockerfile`, `.dockerignore`, `docker-compose.yml` and
      `.devcontainer/devcontainer.json` contain no API key, secret, token,
      password, MaxMind licence key, OPNsense URL or hostname; there is no
      `.env` file and no `env_file:` directive; `docker image history` shows no
      layer embedding a credential.
- [ ] AC27 — No hardcoded network configuration: no interface name, VLAN name,
      segment name, CIDR, IP address, subnet or assumed segment count in any
      file this cycle creates or modifies. No custom compose network with a
      fixed subnet, no port published to a literal host address.
- [ ] AC28 — The only network access in this cycle is the image build fetching
      the Debian package index and the pinned Go tarball. The `Dockerfile`
      header comment states explicitly that this is a build-time dependency of
      the development image and not an application outbound call, so it is not
      read as a third call against the two-outbound-call rule. The running
      container makes no outbound call.
- [ ] AC29 — The Go tarball download is integrity-checked: the `Dockerfile`
      contains a pinned SHA-256 literal and the build fails on mismatch.
      Verified by inspection **and** by a build with a deliberately corrupted
      digest exiting non-zero — performed in a scratch copy outside the
      repository; the repository must not be modified for this check.
- [ ] AC30 — `ROADMAP.md` contains a `##` section titled exactly
      `Development and test environment`, positioned before the `## Steps`
      heading, and step 4 no longer reads `Prerequisite: **install Go**.` but
      refers to that section.
- [ ] AC31 — The `ROADMAP.md` step table still has exactly eight data rows,
      numbered 1 to 8, with the same titles, deliverables and statuses as
      before this cycle — `git diff ROADMAP.md` shows no change inside the
      table.
- [ ] AC32 — The new `ROADMAP.md` section states plainly, in words that cannot
      be read as covering production, that Docker provides the Debian userland
      and the Linux kernel only, and does **not** exercise: the systemd unit,
      service start ordering, unprivileged-LXC uid mapping, `ct/install.sh`, or
      update by re-running without data loss — all of which remain step 8 and
      require a real LXC. All five items are named.
- [ ] AC33 — The new `ROADMAP.md` section gives the canonical invocation
      verbatim, states that it is identical on PowerShell and bash, and states
      that no Go toolchain is installed on the host.
- [ ] AC34 — Everything added or modified is in English: no French word in any
      file this cycle touches.
- [ ] AC35 — `git status --porcelain` after the cycle lists only: the added
      `Dockerfile`, `docker-compose.yml`, `.dockerignore`, `ci/checks.sh`,
      `go.mod`, the minimal package and its test, `.devcontainer/devcontainer.json`,
      this spec file, optionally `.gitattributes`; and the modified
      `ci/factory.sh`, `ci/factory.ps1`, `.vscode/tasks.json`,
      `.claude/agents/factory-verifier.md`, `ROADMAP.md`. No `factory-logs/`
      entry, no database file, no image tarball, no scratch file.

## Out of scope

- **Production.** This cycle does not cover the systemd unit, service start
  ordering, unprivileged-LXC uid mapping, `ct/install.sh`, or
  update-by-re-running without data loss. Docker gives a Debian userland on a
  Linux kernel and nothing more; those items stay in step 8 and need a real LXC
  to be validated. Nobody may read this cycle as evidence that deployment
  works.
- **A production or release image.** No multi-stage build producing a shipping
  artifact, no published image, no registry.
- **The step-8 deployment compose file** for non-Proxmox users.
- **The SQLite driver choice** and any Go dependency. `gcc` is present so the
  choice stays open; making it is step 4.
- **The data model, schema and migrations** — step 2.
- **Any OPNsense API call, credential, URL or collector** — step 4.
- **CI on a hosted runner** — not requested.
- **`README.md`** — untouched; it is end-user-facing and belongs to step 8.
- **Reformatting the pre-existing factory logic** beyond the allowlist, the
  Docker guard and the new checks mode.

## Risks

- **Toolchain pin drift.** go1.27.0 is pinned by version and SHA-256. If the
  published tarball is ever re-uploaded, the build breaks loudly — correct, and
  preferable to a silent version change. The pin is updated deliberately, never
  by relaxing the checksum.
- **`debian:trixie-slim` is a moving tag**, by decision 6. Point releases change
  the `sqlite3` and `gcc` versions under a fixed tag, so the observed
  3.46.1-7+deb13u2 and 14.2.0 are not guaranteed for a future rebuild. The
  acceptance criteria assert major versions only.
- **File ownership across the WSL2 bind mount.** Docker Desktop's uid mapping
  may prevent the non-root user writing to the working tree. AC13 detects it.
  If it proves unworkable, the fix is a documented UID build argument, not a
  silent fallback to root.
- **Named volumes initialised as root.** A non-root user cannot write to a
  fresh named volume unless the image pre-creates and owns the mount point.
  AC9 and AC13 catch a wrong answer.
- **CRLF.** The repository has no `.gitattributes`; a `ci/*.sh` checked out
  with CRLF fails inside the container with a confusing error. AC23 guards it.
- **`docker compose run` semantics.** `--rm`, orphan containers and the
  implicit build-on-first-run can make the "single identical invocation" behave
  differently on a cold and a warm cache. The Verifier tests both.
- **False confidence.** The largest risk is cultural: a green container run
  says nothing about the LXC. AC32 forces the disclaimer into `ROADMAP.md` for
  exactly that reason.
- **Amending the Verifier's own definition** changes the rules the Verifier of
  this very cycle runs under. The Verifier must judge the amended file as a
  deliverable, not adopt it mid-cycle.
