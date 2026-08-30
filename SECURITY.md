# Security policy and deployment model

## Supported security boundary

NixCP v1 is designed for one trusted local NixOS user and their applications.
It is not a multi-tenant hosting product: PHP-FPM pools have separate sockets,
but run under the same owner and do not isolate mutually hostile customers.

Run `ncp` as its configured normal user only. It refuses EUID 0 and sudo
contexts. Persistent mutations acquire a private lock, validate the entire
snapshot, stage and build the candidate module, publish atomically, switch,
health-check, and restore state/generation when a later phase fails.

## Deliberate constraints

- Only Nginx HTTP/80 is public. TLS/SSL, ACME, certificates, HTTPS, DNS, and
  email are permanently outside this product.
- MariaDB and Redis bind locally; FPM uses Unix sockets rather than TCP.
- State, generated modules, shell artifacts, journals, and backups are private
  (`0600` files in `0700` directories). Symlinks, unsafe owner/mode, and unsafe
  managed entries are rejected before privileged work.
- YAML has strict schema and duplicate-key handling. Nix, shell, and Nginx
  have separate escaping/validation boundaries. Commands use explicit argv;
  user input is never passed to `sh -c`.
- NixCP does not create database passwords, read or modify `.env`, or store
  credentials in YAML, argv, generated modules, or diagnostic output.

## Reporting vulnerabilities

Do not file public issues for a suspected security vulnerability. Contact the
repository maintainers privately through the GitHub security advisory flow and
include the NixCP version/commit, NixOS/nixpkgs revision, reproduction steps,
and impact. Do not attach secrets, private project content, or generated state.

## Operational response

If `ncp` reports rollback failure, stop issuing mutations. Preserve
`~/.nixcp/transactions/` (permissions permitting), inspect `ncp doctor`, and
follow the reported recovery instruction before retrying. Do not delete journal
or backup directories to silence the error.
