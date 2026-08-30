# Changelog

This project follows [Semantic Versioning](https://semver.org/). Schema version
and generated-module marker compatibility are part of the release contract.

## [Unreleased]

### Added

- Stage 10 release validation, CI, reproducible build metadata, security and
  operator documentation.
- Golden renderer regression coverage, transaction fault matrix coverage, and
  property/fuzz coverage for encoders and PHP marker parsing.

### Release compatibility

- Supported platform: NixOS `x86_64-linux`, one unprivileged owner.
- Supported PHP catalog: 8.3 and 8.4 from the release's tested nixpkgs set.
- State schema: 1. Older binaries must refuse a newer schema; generated modules
  are always regenerated from authoritative YAML during a supported migration.
