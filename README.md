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

Build a development binary:

```sh
make build
./build/ncp version --json
```

On the target NixOS machine, place the release `ncp` binary on your `PATH` and
run:

```sh
ncp install
# Manually add the exact import printed by NixCP to configuration.nix or flake.
ncp install --confirm-import
ncp service nginx install
ncp php install 8.4
ncp shell init bash   # print only; source it manually from your shell startup file
```

See [installation and module integration](docs/installation-and-module.md) for
traditional and flake handoff details. NixCP never edits `/etc/nixos`, your
flake checkout, or shell startup files.

## Supported surface

Run `ncp help` for the complete contract. The supported PHP/nixpkgs catalog is
currently PHP **8.3** and **8.4**, with `curl`, `intl`, `mbstring`, `opcache`,
`pdo_mysql`, and `redis` when available in the selected nixpkgs package set.
NixCP does not download PECL extensions or unsupported PHP releases.

All scriptable commands support `--json`; it emits exactly one JSON object on
stdout and implies `--no-input`. Persisted changes are idempotent; a no-op
reports `changed:false` and does not rebuild.

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
