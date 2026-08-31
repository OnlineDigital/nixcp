#!/usr/bin/env sh
# Runs inside nixos/nix. The source checkout is deliberately mounted read-only.
set -eu

workspace=${WORKSPACE:-/workspace}

fail() {
  printf '%s\n' "nix-container integration: $*" >&2
  exit 1
}

[ -d "$workspace" ] || fail "workspace does not exist: $workspace"
[ -f "$workspace/go.mod" ] || fail "workspace is not a NixCP checkout: $workspace"
command -v nix >/dev/null 2>&1 || fail "nix is not available"
command -v nix-instantiate >/dev/null 2>&1 || fail "nix-instantiate is not available"

printf '%s\n' '==> Nix environment'
nix --version
nix-instantiate --eval --strict -E '(import <nixpkgs> {}).stdenv.hostPlatform.system' \
  | grep -qx '"x86_64-linux"' \
  || fail 'the nixpkgs channel is not x86_64-linux'

printf '%s\n' '==> Go unit, command, transaction, and shell integration tests'
# The image intentionally contains Nix rather than Go. Nix supplies the tested
# Go toolchain without modifying the read-only checkout. Go's command tests
# deliberately reject root, so drop to the invoking host UID/GID for them.
test_user=${NIXCP_TEST_USER:-nixcp}
test_home=/home/$test_user
test_uid=${NIXCP_TEST_UID:?NIXCP_TEST_UID is required}
test_gid=${NIXCP_TEST_GID:?NIXCP_TEST_GID is required}
mkdir -p "$test_home" /tmp/nixcp-go-cache /tmp/nixcp-go-mod-cache
chown "$test_uid:$test_gid" "$test_home" /tmp/nixcp-go-cache /tmp/nixcp-go-mod-cache
nix --extra-experimental-features 'nix-command flakes' \
  shell nixpkgs#go_1_25 nixpkgs#util-linux -c sh -eu -c '
    cd "$1"
    exec setpriv --reuid="$3" --regid="$4" --clear-groups env HOME="$2" USER="$5" GOCACHE=/tmp/nixcp-go-cache GOMODCACHE=/tmp/nixcp-go-mod-cache CGO_ENABLED=0 go test ./...
  ' sh "$workspace" "$test_home" "$test_uid" "$test_gid" "$test_user"

printf '%s\n' '==> Evaluate every rendered Nix module fixture as NixOS'
# nixos/nix is a Nix environment, not a booted NixOS/systemd host. eval-config
# still type-checks the generated modules against the current NixOS module
# system, without building or switching a host configuration.
for fixture in "$workspace"/internal/nix/testdata/golden/*.nix; do
  [ -f "$fixture" ] || continue
  eval_file=$(mktemp)
  trap 'rm -f "$eval_file"' EXIT HUP INT TERM
  cat >"$eval_file" <<EOF
let
  evalConfig = import <nixpkgs/nixos/lib/eval-config.nix>;
  system = evalConfig {
    system = "x86_64-linux";
    modules = [
      ({ pkgs, ... }: {
        system.stateVersion = "25.11";
        boot.loader.grub.devices = [ "nodev" ];
        fileSystems."/" = { device = "/dev/null"; fsType = "ext4"; };
      })
      $fixture
    ];
  };
in builtins.deepSeq system.config.environment.etc."nixcp/module-marker".text true
EOF
  printf '    %s\n' "$(basename "$fixture")"
  nix-instantiate --eval --strict "$eval_file" >/dev/null
  rm -f "$eval_file"
  trap - EXIT HUP INT TERM
done

printf '%s\n' '==> Nix container integration checks passed'
