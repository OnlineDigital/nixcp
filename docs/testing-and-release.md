# Testing, release, and operator runbook

## Required local validation

From a clean checkout, run the same checks as CI:

```sh
make check                         # gofmt check, vet, unit/integration tests
make race                          # state and transaction race coverage
make fuzz-smoke                    # bounded fuzz/property smoke tests
make build                         # reproducible local build in build/ncp
make static                        # staticcheck and govulncheck if installed
make test-shell                    # real bash/zsh/fish behavior when installed
git diff --check
```

`make nix-eval` is safe and evaluation-only when `nix` exists; it builds no
host configuration, switches no generation, starts no service, and touches no
database. The VM scenarios listed below are CI/release fixtures only and are
**not executed by local Make targets**.

## Disposable Docker Nix evaluation

On an x86_64 Linux host with Docker, run:

```sh
make nix-container-test
# equivalently: ./scripts/test-nix-container.sh
```

The launcher uses Docker Hub's pinned `nixos/nix:2.35.2` image and `docker run
--rm`. It mounts the checkout read-only, preserves only a Docker-managed Nix
store cache volume, runs Go tests as the invoking unprivileged UID, and
NixOS-module-evaluates every rendered golden fixture. Override the image only
when deliberately testing another compatible Nix version:

```sh
NIXCP_NIX_IMAGE=nixos/nix:2.35.2 ./scripts/test-nix-container.sh
```

This is deliberately **not** an installation or service-lifecycle test. The
`nixos/nix` image is a Nix environment, not a booted NixOS system with systemd,
`nixos-rebuild`, or NixCP's required host admission conditions. It neither
runs `ncp install` nor switches a generation, starts listeners, or creates a
database. Those scenarios remain the disposable NixOS VM release gate.

The launcher does not use `--privileged`. That flag would grant broad device
and kernel access to the container and is unnecessary for evaluation. Any
future PID-1/systemd fixture must be separate, disposable, and explicitly
document why elevated container privileges are required before using them.

## Test matrix

| Layer | Coverage |
| --- | --- |
| Go unit/property | strict YAML; domains/paths/DB names; PHP precedence; extension warnings; Nix and shell escaping; JSON envelopes; service state; no-op behavior |
| TUI model | root model tab routing/quit/refresh, overlay confirm/select/form flows (accept + cancel), two-phase action dispatch with fake backend, failure/cancel logging in Activity, views render without panic, backend wiring: snapshot/service status/site health reads and in-process CLI mutations (envelope parse, preconditions) against a fake-adapter state home; `ncp skill` command catalog determinism/tree coverage |
| Renderer golden | empty module; service running/stopped policies; multi-PHP/extensions; Laravel/WordPress/generic/custom; multi-site/FPM; MariaDB; adversarial escaping |
| Integration/fault | temporary HOME/fake adapters, aliases, lock timeout, every build/publish/switch/health/rollback branch, stale journal recovery, PHP/Artisan argv and non-destructive unlink; shortcut pass-through (`a`, `am`, `tinker`, `ci`) with appended args/flags, global-flag stripping, and child exit-code propagation |
| Real shell | bash/zsh/fish source/delegation, current-shell activation, independent sessions, PATH de-duplication and injection resistance |
| Nix evaluation | empty and golden states, PHP attributes/extensions, `x86_64-linux` assertion, candidate wrapper, no TLS/ACME options |
| NixOS VM (release gate) | bootstrap, each service lifecycle/reboot, local listeners, two PHP sites, Laravel/WordPress/custom, MariaDB grants, rollback/kill recovery, retained HTTP response |

VM definitions must use disposable NixOS configuration and fixture data only.
They must not run against a host system, live service, or real database.

## Nix pin limitation

A repository flake is intentionally not shipped in this release checkout.
There is no trustworthy nixpkgs revision or lockfile material available here to
pin, and a fabricated `flake.lock` would falsely claim reproducibility. CI
accepts an explicit `NIXPKGS_REF` (default `nixpkgs`) for evaluation and keeps
that exact ref in release provenance. Before publishing v1.0, maintainers must
commit a reviewed `flake.nix` and real `flake.lock` generated with Nix, then
record its `nixpkgs` revision in the release notes. This limitation does not
alter host configuration or claim NixOS VM validation ran locally.

## Release procedure

1. Start from an up-to-date clean `main`; run every command in **Required local
   validation** and retain output in CI artifacts.
2. Run the Nix evaluation and disposable NixOS VM matrix against a reviewed,
   pinned nixpkgs ref. Reject any failure, network listener regression, or
   candidate/rollback divergence.
3. Build with immutable metadata, for example:

   ```sh
   VERSION=v1.0.0 COMMIT=$(git rev-parse HEAD) \
     BUILD_DATE=$(git show -s --format=%cI HEAD) make release
   ```

4. Publish `dist/ncp_<version>_linux_amd64.tar.gz`, its SHA-256 file, SBOM, and
   provenance. Include generated Cobra completion files and a copy of this
   operator documentation in the archive.
5. Verify artifact checksum from a fresh download, run `ncp version --json`,
   attach the source commit/ref and nixpkgs ref to provenance, tag only after
   all required CI jobs pass, and update `CHANGELOG.md`.

## Install/use and recovery

Follow [installation-and-module.md](installation-and-module.md) for manual
traditional/flake import. For shell use, add the single source line printed by
`ncp shell init bash|zsh|fish` yourself; NixCP will not edit startup files.

A failed candidate build leaves committed state untouched. A switch or health
failure attempts to restore files and the previous system generation. If that
rollback fails, do not retry or delete journals: retain the transaction
information, inspect `ncp doctor`, and manually recover the prior generation
according to NixOS operational policy.
