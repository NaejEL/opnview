# 0006 — Zero hardcoded configuration, own accounts, encrypted secrets

Status: Accepted
Decided by: the maintainer, steps 1 to 4 (September and early October 2026)

## Context

opnview runs unattended and collects without a human signed in, on an
installation that is not ours. Nothing about the network may be assumed, and
nothing secret may travel with the code or the database.

## Decision

- **Zero hardcoded configuration.** No interface name, VLAN name, addressing
  plan or interface count in the code, the templates or the defaults. Everything
  is discovered at runtime through the API.
- **No secret in the repository.** The OPNsense URL, API key and secret, and the
  MaxMind licence key are entered in the interface, never in a file to edit
  before starting. There is no environment variable, no configuration file and
  no `configure` command for them.
- **opnview has its own accounts.** On first start it prints a one-time setup
  token; the first account is created with it; everything else, the firewall's
  URL and key included, is configured from the interface once signed in.
- **Passwords** are hashed with Argon2id, a per-user salt, and the parameters
  stored beside the hash so they can be raised later. Never read back.
- **The OPNsense secret and the MaxMind key are not passwords.** They are sent on
  every call, so they are encrypted at rest with authenticated encryption and
  decrypted to be used.
- **The encryption key is a file beside the database**, created on first start
  with restrictive permissions (0600). This protects a database that is copied,
  backed up, sent or committed by mistake, because the key does not travel with
  it. It does not protect against someone who already has the machine. No scheme
  can while the service starts and collects without a human. The README's
  limitations section says so in those words.
- A plugin's settings and secrets are declared in its manifest, entered on a
  generic settings page, read through `host.settings`; a secret is never
  returned by the API.

## Consequences

- A backup is `pg_dump` plus the key file. Without the key the history restores
  but not the stored credentials.
- A credential changed in the interface takes effect on the next collection pass
  without a restart.
