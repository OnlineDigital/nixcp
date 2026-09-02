# Changelog

This project follows [Semantic Versioning](https://semver.org/). Schema version
and generated-module marker compatibility are part of the release contract.

## [Unreleased]

### Added

- Laravel runtime controls: `ncp enable schedule` manages a marked,
  per-minute crontab entry for `ncp php artisan schedule:run`; `ncp enable`
  and `ncp disable` manage user-systemd units in
  `~/.config/systemd/user/` for `queue`, `horizon`, `vite`, `reverb`, and
  `octane`, and `pulse`. Units use the invoking Laravel project's working
  directory, are enabled and started immediately, and retain every supplied
  tool argument; Pulse runs `artisan pulse:check`. `ncp restart` now restarts
  those user units and the
  `php`, `mariadb`, `valkey`, and `nginx` system targets. Reverb/Octane do not
  alter Nginx configuration.
- Pass-through shortcuts: `ncp c <script> [args...]` runs `ncp composer run
  <script> [args...]`, and `ncp pint [flags...]` runs the project's
  `php ./vendor/bin/pint [flags...]`.
- Human `ncp artisan` and `ncp php` now directly proxy child terminal streams,
  preserving normal output, errors, TTY interaction, and the child's exit
  code without a redundant NixCP wrapper after ordinary child failures. JSON
  mode remains captured and emits its structured process diagnostics.
- Interactive panel (`ncp tui`): a bubbletea-based tabbed interface with five
  tabs — Status (overview + drift), Sites (list, link form, health probe,
  unlink), PHP (installed versions, install/uninstall/use-global, curated
  extension install), Services (desired vs actual with install/start/stop/
  restart), and Activity (session log). The panel implements zero business
  logic: every mutation executes the real CLI command in-process with
  `--json --no-input`, going through the same validate → render → locked
  transaction pipeline, so YAML desired state stays the single source of
  truth. Reads (snapshot, service status, site health) go through the same
  adapters the CLI uses. Confirm overlays guard destructive actions
  (unlink, php uninstall, service stop), `ctrl+c` cancels the in-flight
  action (transaction rollback unwinds partial state), and the panel refuses
  non-TTY stdio with `tui_requires_tty`. Bare `ncp` in an interactive
  terminal opens the panel; piped invocations keep the byte-stable version
  banner.
- `ncp skill`: prints the complete command reference — every command path
  (62 commands) with synopsis, flags, and examples, plus the global-flags
  footer — as one text block sized for pasting into tool prompts or docs.
  `ncp skill --json` returns the same catalog as a single JSON envelope.
- Command shortcuts with full argv pass-through: `ncp a <args…>` runs
  `ncp artisan <args…>`, `ncp am [flags…]` runs `ncp artisan migrate [flags…]`,
  `ncp tinker [args…]` runs `ncp artisan tinker [args…]`, and `ncp ci [flags…]`
  runs `ncp composer install [flags…]`. Any arguments or flags appended after a
  shortcut are forwarded to the wrapped tool verbatim; NixCP's own global flags
  are still honored and stripped from the child argv.
- `ncp artisan` now forwards its raw argv to artisan untouched
  (`DisableFlagParsing`), so artisan options such as `--seed` or `--force` no
  longer collide with NixCP's flag parser; the tinker REPL keeps its TTY
  passthrough and exit-code propagation.

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
