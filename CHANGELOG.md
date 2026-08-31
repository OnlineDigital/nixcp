# Changelog

This project follows [Semantic Versioning](https://semver.org/). Schema version
and generated-module marker compatibility are part of the release contract.

## [Unreleased]

### Added

- Stage 10 release validation, CI, reproducible build metadata, security and
  operator documentation.
- Golden renderer regression coverage, transaction fault matrix coverage, and
  property/fuzz coverage for encoders and PHP marker parsing.
- Per-site dedicated MariaDB account: every site that declares a database gets
  an account whose user equals the database name and a generated random
  alphanumeric password (persisted only in the private site YAML). The generated
  module renders a oneshot `nixcp-mariadb-accounts` unit (only while MariaDB is
  installed and desired running) that reads a private 0600
  `~/.nixcp/secrets/mariadb/accounts.sql` file via stdin and creates/updates the
  user, grants, and rotates the password idempotently. That SQL file carries a
  deterministic SHA-256 in the unit so a password rotation changes the unit text
  and re-runs `ALTER USER` on the next switch. No password is ever baked into
  the world-readable Nix store, and none appears on the client argv (MYSQL
  auth happens via the root unix socket). The post-switch health phase verifies
  each declared database as its own dedicated account using `MYSQL_PWD`.
- `sites list` reports `enabled`, `handler` (and `db`) columns; `sites show` and
  `link` print the generated database user/password for the site's `.env`.
- `php ext install` now validates the extension against the *installed* PHP
  versions and fails with `php_extension_unavailable` when none of them provides
  it (including when no PHP is installed), instead of accepting anything in the
  catalog.
- Real shell startup files: `ncp install` writes an `ncp()` wrapper plus a
  new-session bootstrap (not a placeholder comment) into
  `~/.nixcp/shell/{bash.sh,zsh.sh,fish.fish}`; a hidden `ncp php session
  --shell-emit=<shell>` captures the active/global-default version. `ncp shell
  init` prints the same full startup snippet. Fixed zsh activation to split
  `$PATH` explicitly via `${(s.:.)PATH}`.

### Release compatibility

- Supported platform: NixOS `x86_64-linux`, one unprivileged owner.
- Supported PHP catalog: 8.3 and 8.4 from the release's tested nixpkgs set.
- State schema: 2. Older binaries must refuse a newer schema; generated modules
  are always regenerated from authoritative YAML during a supported migration.
