#!/usr/bin/env sh
# Start a disposable Nix integration environment for this checkout.
# Docker's --rm removes the container when the test command exits.
set -eu

image=${NIXCP_NIX_IMAGE:-nixos/nix:2.35.2}
repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)

case "$(uname -s)" in
  Linux) ;;
  *)
    printf '%s\n' 'nix-container integration requires a Linux Docker daemon.' >&2
    exit 2
    ;;
esac

case "$(uname -m)" in
  x86_64|amd64) ;;
  *)
    printf '%s\n' "nix-container integration requires an x86_64 host; found $(uname -m)." >&2
    exit 2
    ;;
esac

command -v docker >/dev/null 2>&1 || {
  printf '%s\n' 'Docker is required; install Docker and ensure the daemon is running.' >&2
  exit 127
}
docker info >/dev/null 2>&1 || {
  printf '%s\n' 'Docker is installed but its daemon is unavailable to the current user.' >&2
  exit 1
}

# Persist only Nix's cache between runs. Project sources remain read-only, and
# the named volume holds no NixCP state or host configuration.
# The tests intentionally exercise NixCP's unprivileged-user admission. Keep
# the container entry process root only long enough to populate Nix's cache;
# the integration script drops to this invoking user's numeric identity for Go.
exec docker run --rm --init \
  --mount "type=bind,src=$repo_root,dst=/workspace,readonly" \
  --mount 'type=volume,src=nixcp-nix-store,dst=/nix' \
  --mount 'type=bind,src=/etc/passwd,dst=/etc/passwd,readonly' \
  --mount 'type=bind,src=/etc/group,dst=/etc/group,readonly' \
  --tmpfs /home:mode=1777 \
  --workdir /workspace \
  --env WORKSPACE=/workspace \
  --env NIXCP_TEST_USER="$(id -un)" \
  --env NIXCP_TEST_UID="$(id -u)" \
  --env NIXCP_TEST_GID="$(id -g)" \
  --entrypoint sh \
  "$image" /workspace/scripts/integration/nix-container.sh
