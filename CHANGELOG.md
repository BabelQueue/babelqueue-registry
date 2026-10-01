# Changelog

All notable changes to `babelqueue-registry` (`bqschema`) are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the
project adheres to [Semantic Versioning](https://semver.org/).

## [0.4.1] — Unreleased

### Fixed

- **Behaviour fix — `compat` now flags every removed field as breaking.** Previously a
  property dropped from `properties` was only reported when the new schema had
  `"additionalProperties": false`; with an open schema (`additionalProperties` `true` or
  absent) the removal slipped through as compatible. Per `versioning-policy.md` §3 a
  removal is breaking regardless of `additionalProperties`, and it is now reported as
  `<path>: property removed` (nested objects and array items included, e.g.
  `customer.email`, `lines[].qty`). It exits with the existing "incompatible" code `1`.
  **Heads-up:** pipelines that removed fields from open schemas and passed before will now
  fail `bqschema compat` — mint a new URN (`…v2`) instead of mutating the existing one.
- **`minLength` counts Unicode code points, not UTF-8 bytes** (ADR-0024, GR-5). `"ğü"` no
  longer satisfies `minLength: 3`, matching every SDK's payload validator and the
  conformance `payload_schema_unicode` cases.

### Changed

- Minimum Go version raised to **1.24** (`go.mod`); CI tests Go 1.24 and 1.25.
- Added Dependabot for Go modules and GitHub Actions.
