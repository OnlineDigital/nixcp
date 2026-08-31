# Installation and generated NixOS module

`ncp install` is an unprivileged bootstrap. It checks for NixOS, systemd,
`x86_64-linux`, the required Nix tools and lock support before it creates
`~/.nixcp`. It never edits `/etc/nixos`, a flake checkout, `.bashrc`, `.zshrc`,
or Fish configuration. The generated module and state files are private
(`0600`); directories are private (`0700`).

## Manual import handoff

For a traditional configuration, add the exact `imports = [ "…" ];` line
printed by `ncp install` to `/etc/nixos/configuration.nix`, then run:

```text
ncp install --confirm-import
```

For flakes, pass the target during bootstrap, for example
`ncp install --flake .#hostname`. Add the printed import to the NixOS module in
the flake yourself. An absolute import outside the flake source requires an
impure evaluation (`--impure`). The preferred pure alternative is to arrange
for the generated path to be tracked by the flake; NixCP does not make that
repository change. Confirmation runs `nixos-rebuild build`, never `switch`, so
it does not claim activation or mutate the host system.

## Generated-module rules

The module is derived from a validated snapshot and has a generated header,
versioned marker, platform assertion, and marker file. It does **not** read YAML
or custom snippets during Nix evaluation. The renderer reads a validated custom
snippet once and embeds it as Nix string data.

All dynamic strings and attribute keys use Nix double-quoted string encoding:
backslash, quote, newline, carriage return and tab are escaped; invalid UTF-8
and NUL cannot form Nix syntax. `${...}` has its dollar escaped so it remains literal text inside the Nix
string and is never interpreted as injected Nix expression. Templates are
fixed built-in location bodies; a custom snippet is placed only in the owned
Nginx location boundary, never parsed as Nix source. The generated module is
HTTP-only (`listen` port 80) and intentionally contains no TLS, SSL, ACME,
certificate, email, PECL, or other excluded functionality.

## Security model and tenancy

NixCP must be run directly by its configured normal user; it refuses root and
sudo environments. Managed state is private and symlinks, unexpected entries,
and unsafe ownership or modes are rejected before any privileged action. Project
paths must be existing readable directories and may not sit beneath a
non-sticky world-writable ancestor. NixCP never changes project permissions:
if Nginx cannot read or traverse a project, the user must deliberately correct
that project path's permissions.

NixCP v1 is for applications controlled by one trusted user. Separate FPM pools
and Unix sockets prevent accidental site cross-wiring, but they are **not** a
hostile multi-tenant hosting isolation boundary. MariaDB and Valkey remain bound
to loopback/socket access; only Nginx HTTP port 80 is public. TLS is permanently
outside this product's scope and custom Nginx snippets cannot add listener,
server, include, TLS, or certificate directives.

## Candidate evaluation

Candidate preparation writes `candidate-module.nix` and a wrapper that imports
that exact staged path. Its traditional build argv uses
`-I nixos-config=<candidate-wrapper>` rather than the stable
`~/.nixcp/generated` import. A flake target is not silently built as a
candidate, because it would otherwise evaluate the old host import; faithful
flake candidate publication/rollback is a transaction concern. This prevents a
false positive that validates an old generated module.
