# opnview — development and test image.
#
# Base image:   debian:trixie-slim (Debian 13), pinned by tag and not by
#               digest, so Debian security updates arrive automatically.
#               Consequence: the PostgreSQL client and gcc versions may move
#               under the tag; only their major versions are relied upon. The
#               PostgreSQL client is the major of the pinned database
#               (PostgreSQL 17, docker-compose.yml), so psql and pg_dump match
#               the server they talk to.
# Go toolchain: go1.27.0, pinned by version and by the SHA-256 literal below.
#               A checksum mismatch fails the build. It is never masked,
#               relaxed or removed to make a build pass; the pin is changed
#               deliberately, together with its digest.
# ShellCheck:   v0.11.0, the static analyser of the shell scripts (ci/ and
#               sql/), pinned by version and by the SHA-256 literal below on
#               the same terms as the Go toolchain: a mismatch fails the build.
#
# Network: building this image fetches the Debian package index, the pinned
# Go tarball and the pinned ShellCheck tarball. All three are build-time
# dependencies of the development image, not application outbound calls.
# They are therefore unrelated to the project rule that the program calls the
# firewall and nothing else (its plugins' own downloads are theirs). The
# running container makes no outbound call. The database image the compose
# files pull is likewise an installation-time download.
#
# Out of scope for this image (see
# specs/SPEC-containerised-dev-environment.md):
#   - Production. Docker provides a Debian userland on a Linux kernel and
#     nothing more. The systemd unit, service start ordering, unprivileged-LXC
#     uid mapping, ct/install.sh and update-by-re-running without data loss are
#     not exercised here; they remain step 8 and need a real LXC.
#   - A production or release image: no multi-stage build producing a shipping
#     artifact, no published image, no registry.
#   - Any Go dependency. The PostgreSQL driver is pure Go and the product
#     builds with CGO_ENABLED=0; gcc is installed because the race detector of
#     the pre-deployment command needs cgo, and for nothing else.

FROM debian:trixie-slim

# The pinned Go version is written once, here.
ARG GO_VERSION=go1.27.0
# Published SHA-256 of https://dl.google.com/go/go1.27.0.linux-amd64.tar.gz
ARG GO_SHA256=675c26c449cbb18fc24b74650de1eabbae6e16f64326fd85a283fb3b58280685

# The pinned ShellCheck release, written once, here.
ARG SHELLCHECK_VERSION=v0.11.0
# SHA-256 of https://github.com/koalaman/shellcheck/releases/download/v0.11.0/shellcheck-v0.11.0.linux.x86_64.tar.xz
# as the release page lists it, and as computed on the downloaded file on 10 October 2026.
ARG SHELLCHECK_SHA256=8c3be12b05d5c177a04c29e3c78ce89ac86f1595681cab149b65b97c4e227198

# The container runs as a non-root user, closer to the unprivileged LXC that is
# the production target, and exercising the file-mode and umask behaviour that
# Windows masks. Override at build time to match the host account owning the
# bind-mounted working tree.
ARG UID=1000
ARG GID=1000

# postgresql-client-17: psql and pg_dump of the pinned major, for the schema
#                  checks and the backup round trip of the pre-deployment command.
# ca-certificates: system trust store, and the tarball download below.
# gcc + libc6-dev: the race detector needs cgo; libc6-dev carries the headers
#                  and the C runtime gcc links against.
# tzdata:          required to exercise normalisation of the OPNsense filter-log
#                  timestamps, which carry neither year nor timezone.
# curl:            fetches the pinned Go and ShellCheck tarballs in the next layers.
# xz-utils:        unpacks the ShellCheck tarball, which is published as .tar.xz.
RUN set -eu; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        gcc \
        libc6-dev \
        postgresql-client-17 \
        tzdata \
        xz-utils; \
    rm -rf /var/lib/apt/lists/*

RUN set -eu; \
    tarball="${GO_VERSION}.linux-amd64.tar.gz"; \
    curl -fsSL -o /tmp/go.tar.gz "https://dl.google.com/go/${tarball}"; \
    printf '%s  %s\n' "${GO_SHA256}" /tmp/go.tar.gz | sha256sum -c -; \
    tar -C /usr/local -xzf /tmp/go.tar.gz; \
    rm -f /tmp/go.tar.gz; \
    /usr/local/go/bin/go version; \
    : 'A login shell sources /etc/profile, which resets PATH and would drop'; \
    : 'the ENV PATH set below. Restore it there too, so an interactive or a'; \
    : 'devcontainer terminal finds go and gofmt like any other invocation.'; \
    printf '%s\n' 'export PATH=/usr/local/go/bin:/home/dev/go/bin:$PATH' \
        > /etc/profile.d/go-toolchain.sh

# ShellCheck, so the shell checks of sql/schema-checks.sh (PN-AC28) and the -S
# error check of ci/pre-deployment.sh (AC23 of specs/SPEC-step-5b-widget-api.md)
# run in this container rather than on the host.
RUN set -eu; \
    tarball="shellcheck-${SHELLCHECK_VERSION}.linux.x86_64.tar.xz"; \
    curl -fsSL -o /tmp/shellcheck.tar.xz \
        "https://github.com/koalaman/shellcheck/releases/download/${SHELLCHECK_VERSION}/${tarball}"; \
    printf '%s  %s\n' "${SHELLCHECK_SHA256}" /tmp/shellcheck.tar.xz | sha256sum -c -; \
    tar -C /tmp -xJf /tmp/shellcheck.tar.xz; \
    install -m 0755 "/tmp/shellcheck-${SHELLCHECK_VERSION}/shellcheck" /usr/local/bin/shellcheck; \
    rm -rf /tmp/shellcheck.tar.xz "/tmp/shellcheck-${SHELLCHECK_VERSION}"; \
    shellcheck --version

# -o allows reusing a UID or GID already present in the base image, so any host
# account maps cleanly without the build failing on a collision.
RUN set -eu; \
    groupadd -o -g "${GID}" dev; \
    useradd -o -u "${UID}" -g "${GID}" -m -s /bin/bash dev; \
    mkdir -p /home/dev/go/pkg/mod /home/dev/go/bin /home/dev/.cache/go-build /data /workspace; \
    chown -R "${UID}:${GID}" /home/dev /data /workspace

# The cache and data mount points are created and owned above so that a fresh
# named volume, which Docker initialises from the image content at that path,
# inherits that ownership and stays writable by the non-root user.
ENV PATH=/usr/local/go/bin:/home/dev/go/bin:$PATH
# A future go.mod declaring a higher go directive must fail loudly rather than
# silently download another toolchain over the network at test time.
ENV GOTOOLCHAIN=local
ENV GOMODCACHE=/home/dev/go/pkg/mod
ENV GOCACHE=/home/dev/.cache/go-build

USER dev
WORKDIR /workspace
CMD ["bash"]
