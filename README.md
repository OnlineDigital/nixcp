# NixCP

NixCP is an **HTTP-only, CLI-only NixOS control plane** for one trusted local
user. It keeps desired state in `~/.nixcp/*.yaml`, generates a NixOS module,
and activates validated changes through a locked, rollback-capable transaction.
It manages Nginx, local MariaDB/Valkey, multiple nixpkgs PHP versions and
per-site PHP-FPM pools.

> **Status:** v1 release candidate implementation. NixCP must be run by its
> configured unprivileged owner on `x86_64-linux` NixOS. Do not run it with
> `sudo`.

## Install and first use

NixCP runs on `x86_64-linux` NixOS with systemd, as the normal user that will
own the sites. Do not run `ncp` with `sudo`.

### Install `ncp` globally for your user

Build from this checkout, then install the binary in `~/.local/bin`. This makes
`ncp` available from every shell for the current user, without a system-wide
installation or root access:

```sh
make build
mkdir -p ~/.local/bin
install -m 0755 build/ncp ~/.local/bin/ncp
```

In Fish, persist that directory in `PATH` and verify the installation:

```fish
fish_add_path -U ~/.local/bin
ncp version --json
```

`fish_add_path -U` stores the path as a Fish universal variable, so new Fish
sessions can find `ncp`. If `~/.local/bin` is already on your `PATH`, this step
is harmless. A release binary can be installed in the same way in place of
`build/ncp`.

### Bootstrap NixCP

Initialize the private state directory, copy the exact import line printed by
the command into your NixOS configuration or flake, then confirm that import:

```sh
ncp install
# Add the exact `imports = [ "…" ];` line printed above to configuration.nix or the NixOS flake module.
ncp install --confirm-import
```

`--confirm-import` evaluates the configuration with `nixos-rebuild build`; it
does not switch the system. See [installation and module integration](docs/installation-and-module.md)
for traditional and flake handoff details. NixCP never edits `/etc/nixos`, your
flake checkout, or shell startup files itself.

### Enable the Fish PHP integration

NixCP needs a Fish function so `ncp php use <version>` can update the *current*
shell's `PATH`. Generate it once and source it now:

```fish
mkdir -p ~/.config/fish/conf.d
ncp shell init fish > ~/.config/fish/conf.d/nixcp.fish
source ~/.config/fish/conf.d/nixcp.fish
```

The file is loaded automatically by future Fish sessions. After installing a
PHP version, make it the default for new shells with
`ncp php use --global 8.4`; use `ncp php use 8.3` inside a shell to switch that
shell and write the project's `.php-version` marker.

### Example: a new Laravel site

The following creates a Laravel project, installs the required runtime, and
links it to Nginx. Choose a real DNS name that resolves to this machine (or add
an equivalent local hosts entry yourself); NixCP does not manage DNS or
`/etc/hosts`.

```sh
# Install and select the PHP runtime, then enable Nginx.
ncp php install 8.4
ncp php use --global 8.4
ncp service nginx install

# Create a project with Composer running under NixCP's selected PHP.
mkdir -p ~/projects
cd ~/projects
ncp composer create-project laravel/laravel example.test
cd example.test

# Create the HTTP site. The Laravel template uses `public/` as its document root.
ncp link example.test --template laravel --php 8.4

# Common Laravel commands run with the PHP version resolved by NixCP.
ncp artisan key:generate
ncp artisan migrate

# Shortcuts: aliases that forward any extra arguments and flags verbatim.
ncp a make:model Post --migration     # ncp artisan make:model Post --migration
ncp am --seed                         # ncp artisan migrate --seed
ncp tinker                            # ncp artisan tinker
ncp ci --prefer-dist                  # ncp composer install --prefer-dist
ncp c dev                             # ncp composer run dev
ncp pint --parallel                   # ncp php ./vendor/bin/pint --parallel

# Run background Laravel processes from this project's directory.
ncp enable schedule
ncp enable queue --tries=3
ncp enable reverb --host=127.0.0.1 --port=6001
```

To create a dedicated local MariaDB database at link time, first run
`ncp service mariadb install`, then add `--mariadb example` to the `ncp link`
command. NixCP prints the generated database credentials; put those values in
the Laravel `.env` file before running migrations.

## Supported surface

Run `ncp help` for the complete contract, `ncp help php` for PHP usage, and
`ncp help php install` (or any other command path) for focused examples. The
NixOS 26.05 nixpkgs catalog currently supports PHP **8.2**, **8.3**, **8.4**,
and **8.5**. NixCP exposes the curated extensions `apcu`, `bcmath`, `curl`,
`gd`, `imagick`, `intl`, `mbstring`, `mysqli`, `pdo_mysql`, `pdo_pgsql`,
`pdo_sqlite`, `redis`, `soap`, `sockets`, `xdebug`, `xml`, and `zip` on all
four versions; `opcache` is available on PHP 8.2–8.4. NixCP does not download
PECL extensions or unsupported PHP releases.

All scriptable commands support `--json`; it emits exactly one JSON object on
stdout and implies `--no-input`. Persisted changes are idempotent; a no-op
reports `changed:false` and does not rebuild.

The `a`, `am`, `tinker`, `ci`, `c`, and `pint` commands are pass-through
shortcuts: `ncp a <args…>` runs `ncp artisan <args…>`, `ncp am [flags…]` runs
`ncp artisan migrate [flags…]`, `ncp tinker [args…]` runs
`ncp artisan tinker [args…]`, `ncp ci [flags…]` runs
`ncp composer install [flags…]`, `ncp c <script> [args…]` runs
`ncp composer run <script> [args…]`, and `ncp pint [flags…]` runs
`ncp php ./vendor/bin/pint [flags…]`. Anything appended after the shortcut —
arguments or flags — is forwarded to the wrapped tool unchanged, and NixCP's
own global flags (`--json`, `--timeout`, …) are consumed before the call, never
leaked into the child argv.

`ncp skill` prints the full command reference — every command path with its
synopsis, flags, and examples — as one text block for pasting into tool
prompts or docs. `ncp skill --json` returns the same catalog as JSON.

`ncp tui` opens the interactive panel: five tabs (Status, Sites, PHP,
Services, Activity) over the exact same use-cases as the CLI. The panel adds
no business logic — every mutation runs the real CLI pipeline
(validate → render → locked transaction) in-process, and every change lands
in the same YAML desired state. It refuses to start on non-TTY stdio
(`tui_requires_tty`), and running bare `ncp` in an interactive terminal
opens the panel while piped/script invocations keep printing the version
banner.

`ncp enable` and `ncp disable` manage per-project Laravel runtime processes.
`enable schedule` installs NixCP's managed per-minute cron entry for
`php artisan schedule:run`; `queue`, `horizon`, `vite`, `reverb`, `octane`,
and `pulse` install user systemd units in `~/.config/systemd/user/`, enable them at login,
and start them immediately. Every user-systemd target accepts and preserves
appended tool arguments; Pulse runs `ncp php artisan pulse:check`. Use
`ncp restart queue|horizon|vite|reverb|octane|pulse` for user services, or
`ncp restart php|mariadb|valkey|nginx` for the matching
system service. NixCP does not configure Nginx for Reverb or Octane.

## Security and limitations

Read [SECURITY.md](SECURITY.md) before deployment. In particular:

- NixCP serves **HTTP on port 80 only**. TLS, ACME, certificates, DNS, email,
  web UI, containers, Home Manager, and automatic source/config edits are
  deliberately unsupported and rejected where applicable.
- MariaDB and Valkey are local-only; PHP-FPM uses per-site Unix sockets.
- This is **not hostile multi-tenant hosting**. Pools reduce accidental
  cross-wiring but all sites run as the same trusted owner.
- `unlink` and service `stop` do not delete project files, databases, or data.
- Custom Nginx snippets are constrained location content, not server blocks.

## Development and release

The reproducible command matrix, CI gates, fuzz commands, artifact procedure,
rollback troubleshooting, and current Nix limitation are in
[docs/testing-and-release.md](docs/testing-and-release.md). Release notes are
maintained in [CHANGELOG.md](CHANGELOG.md).
