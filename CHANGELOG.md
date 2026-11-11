# Changelog

All notable changes to `babelqueue-registry` (`bqschema`) are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the
project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.5.0] — 2026-11-11

Validator parity with the SDKs (ADR-0024, conformance 1.10.0
`payload_schema_keywords`, 66/66).

### Added

- **`maxLength`** (counted in Unicode code points, like `minLength`; a fractional value truncates to
  an int).
- **Union `type`** (`"type": ["string","null"]`): a value is valid when it matches any listed type.
  `integer` now accepts every number with no fractional part (`1.0`, `1e2`).
- **Unsupported-keyword warnings.** `schema.Parse` records one `{pointer, keyword}` warning per
  occurrence of a keyword outside the supported subset (`Schema.Warnings()`); `bqschema validate`
  prints them to **stderr**, once per URN. Verdicts and exit codes are unchanged. Annotation keywords
  and `x-*` (incl. `x-gdpr-sensitive`) never warn.
- Conformance `payload_schema_keywords` / `payload_schema` / `payload_schema_unicode` tests, run from a
  vendored extract in `internal/schema/testdata/conformance/`.

### Changed

- **Heads-up — validation is stricter for schemas already in your registry.** In v0.4.x `maxLength`
  and an array-form `type` (`"type": ["string","null"]`) were silently ignored; they are now
  enforced. Data that passed `bqschema validate` under 0.4.x may fail (exit `1`), and `compat` may
  report `type changed` on schemas that previously looked unconstrained. Re-run `validate` against
  your fixtures before upgrading.
- **`const` / `enum` compare numbers by mathematical value** (`1` = `1.0` = `1e0`, recursively inside
  arrays and objects; key order irrelevant); a boolean never equals a number.
- **`compat` understands union `type`**: removing a type from a union is breaking, adding one is not
  (a single type is the one-element case, so existing single-type results are unchanged).
- **`compat` flags a tightened `maxLength`** (lowered, or added where none existed) as breaking, now
  that it is enforced. **Heads-up:** a pipeline that lowered `maxLength` and passed before will fail.

## [0.4.1] — 2026-10-03

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
