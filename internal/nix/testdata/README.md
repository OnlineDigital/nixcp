# Renderer fixture contract

Golden fixture names are reserved for schema/marker version 1. The automated
renderer tests cover deterministic output, HTTP-only behavior, service state,
PHP, extensions, sites, FPM, and escaping. Fixture files belong here when a
reviewed renderer output is promoted as a byte-for-byte compatibility contract.

Do not add user secrets, host paths, or executable Nix snippets to fixtures.
