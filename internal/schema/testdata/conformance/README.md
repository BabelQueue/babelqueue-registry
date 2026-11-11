# Vendored conformance extract

`manifest.json` is an extract of the cross-SDK conformance `manifest.json`
(`conformance/` at release **1.10.0**): only the sections the registry's validator
is held to — `payload_schema`, `payload_schema_unicode` and `payload_schema_keywords`
(ADR-0024). The full suite is not vendored here because the registry has no envelope
codec. Refresh by re-extracting those three keys from the new release's `manifest.json`.
