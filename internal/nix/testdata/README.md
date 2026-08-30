# Renderer fixture contract

Golden fixture names are reserved for schema/marker version 1. The automated
renderer tests cover deterministic output, HTTP-only behavior, service state,
PHP, extensions, sites, FPM, and escaping. Fixture files belong here when a
reviewed renderer output is promoted as a byte-for-byte compatibility contract.

## Golden files

`golden/*.nix` are byte-for-byte compatibility contracts for schema v1: each
fixed, deterministic scenario (`empty`, `services`, `php`, `sites`,
`escaping`) must render to exactly those bytes. The golden test also renders
each scenario twice and requires identical output. Site paths use the fixed
`/home` directory because `Render` validates path existence; no `t.TempDir()`
paths leak into fixtures. Any byte change in generated Nix output (whitespace,
attribute reordering) fails the tests. Regenerate after a deliberate renderer
change with `UPDATE_GOLDEN=1 go test ./internal/nix/` and review the diffs
before committing.

Do not add user secrets, host paths, or executable Nix snippets to fixtures.
