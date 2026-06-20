# babelqueue-registry

> File-based, broker-free schema governance for BabelQueue message payloads.

BabelQueue's wire envelope is frozen, and a message's `data` block is by contract "pure
JSON the caller validates." [`urn-naming.md §6`](https://babelqueue.com) already
**recommends** that teams keep a checked-in per-URN registry with a JSON Schema for each
message's `data` — but ships no tooling. **`bqschema` is that tooling.**

It does three jobs, all as a CLI you run in CI — **no Kafka, no service, no database**.
Schemas live in your git repo as files:

- **`validate`** — does this message's `data` match the schema registered for its URN?
- **`compat`** — is this schema change **backward-compatible**, or does it break consumers?
  This enforces [`versioning-policy.md §3`](https://babelqueue.com): an additive optional
  field is safe; removing / renaming / retyping a field or making an optional field
  required is breaking — so you must **mint a new URN** (`…:created.v2`) instead.
- **`export-asyncapi`** — generate an AsyncAPI 3.0 event catalog from the registry, so the
  same git-tracked schemas double as discoverable, tool-agnostic documentation.

It also **serves** a Confluent-compatible read-mostly REST API (`serve`) and **governs PII** —
declaring, auditing, and masking GDPR-sensitive fields (`gdpr`).

Unlike Confluent Schema Registry, schemas aren't coupled to a broker — so there's no
cold-start circular dependency, and it works identically across Redis, RabbitMQ, SQS,
Kafka, Pulsar, and the rest.

## Install

```sh
go install github.com/babelqueue/babelqueue-registry/cmd/bqschema@latest
```

## The registry

A `registry.json` manifest maps each URN to a draft-07 JSON Schema file for its `data`:

```json
{
  "schemas": [
    { "urn": "urn:babel:orders:created", "schema": "schemas/orders-created.json", "owner": "orders" }
  ]
}
```

```jsonc
// schemas/orders-created.json — JSON Schema for the "data" block
{
  "type": "object",
  "required": ["order_id", "amount"],
  "properties": {
    "order_id": { "type": "integer", "minimum": 1 },
    "amount":   { "type": "number",  "minimum": 0 },
    "currency": { "enum": ["USD", "EUR", "TRY"] }
  },
  "additionalProperties": false
}
```

## Usage

```sh
# Validate a message's data against its URN's registered schema
bqschema validate --registry examples/registry.json examples/messages/order-created.json

# Fail CI when a schema change would break consumers (then: mint a new URN)
bqschema compat examples/schemas/orders-created.json examples/schemas/orders-created.v2-breaking.json

# Sanity-check the registry itself (every schema parses)
bqschema check --registry examples/registry.json

# Generate an AsyncAPI 3.0 event catalog from the registry
bqschema export-asyncapi --registry examples/registry.json -o asyncapi.json

# Serve a Confluent-compatible (read-mostly) REST API over the registry
bqschema serve --registry examples/registry.json --addr :8081

# Audit GDPR-sensitive (PII) fields across the registry
bqschema gdpr --registry examples/registry.json                       # inventory
bqschema gdpr --registry examples/registry.json --require             # CI gate: fail on un-annotated PII
bqschema gdpr --registry examples/registry.json --mask examples/messages/user-registered.json
```

### In CI

```yaml
- run: go install github.com/babelqueue/babelqueue-registry/cmd/bqschema@latest
- run: bqschema check --registry registry.json
- run: bqschema compat schemas/orders-created.json schemas/orders-created.json   # old vs PR's version
```

## REST surface (Confluent-compatible)

`bqschema serve` exposes a **subset of the [Confluent Schema Registry REST API](https://docs.confluent.io/platform/current/schema-registry/develop/api.html)**
over the **same git-tracked registry** — so existing Schema-Registry tooling can introspect a
BabelQueue registry **without a broker, a database, or a write path**. It is **read-mostly**: it
serves what's in the files and never writes back (the registry is managed in git, not over REST).

```sh
bqschema serve --registry registry.json --addr :8081
# flags: --addr (default :8081), --compatibility (default BACKWARD, the level /config reports)
```

Responses use the `application/vnd.schemaregistry.v1+json` content type, and errors use Confluent's
`{ "error_code": <int>, "message": <string> }` body.

### Subject ↔ URN mapping

- **One URN = one Confluent "subject"**, named by the **URN verbatim** (e.g. the subject for
  `urn:babel:orders:created` *is* `urn:babel:orders:created`). When putting a subject in a URL,
  percent-encode it as usual for a path segment.
- The file registry holds **exactly one schema per URN**, so **every subject has exactly one
  version — version `1`**, and `latest` resolves to it.
- **Schema ids** are assigned **deterministically** (1-based, in lexical URN order) when the
  server loads the registry, so an id is stable for a given registry file and
  `GET /schemas/ids/{id}` is well-defined.
- There are **no per-subject compatibility overrides**: `/config` and `/config/{subject}` both
  report the single registry-wide level (`--compatibility`, default `BACKWARD`).

### Endpoints implemented

| Method & path | Returns |
| ------------- | ------- |
| `GET /subjects` | the subject names (the registry's URNs) |
| `GET /subjects/{subject}/versions` | `[1]` (the only version) |
| `GET /subjects/{subject}/versions/{version}` (and `/latest`) | `{ subject, version, id, schema }` — `schema` is the JSON Schema as a string |
| `GET /schemas/ids/{id}` | `{ schema }` for the stable id |
| `POST /compatibility/subjects/{subject}/versions/{version}` | `{ "is_compatible": bool }` — the posted candidate (Confluent's `{"schema":"…"}` body) checked against the registered schema via the **same `compat` engine** the CLI uses |
| `GET /config` | `{ "compatibilityLevel": "<level>" }` (registry-wide) |
| `GET /config/{subject}` | the same level for a known subject |

Compatibility direction is **`BACKWARD`** (Confluent's default): the posted candidate is treated as
the **new** schema and must still accept data valid under the **registered** schema — exactly the
`compat` rule in [the table below](#what-compat-considers-breaking).

### Out of scope (deferred / by design)

- **Registration / writes** — `POST /subjects/{subject}/versions`, `PUT /config`, deletes. The
  registry is **git-managed**: you add a schema by committing a file, not by writing over REST. The
  server never mutates the file store, keeping it git-native.
- **Multiple versions per subject** — the file store is one-schema-per-URN by design; versioning a
  message means [**minting a new URN**](#what-compat-considers-breaking), not stacking versions
  under one subject.
- **Schema *types* other than JSON Schema** (no Avro/Protobuf), schema references, mode endpoints,
  and `?fetchMaxId` / `?normalize`-style query options.

## GDPR sensitive fields

A registry is the natural place to **declare** which message fields carry personal data, so PII
sensitivity is **auditable** in CI and **maskable** for safe logging — long before any encryption
runs. `bqschema` recognises a single JSON-Schema extension keyword and a `gdpr` subcommand over it.

> **Scope:** the registry **declares and audits** sensitivity. Field-level runtime
> **encryption / tokenisation is an SDK concern**, intentionally out of scope here — see
> [SDK enforcement](#sdk-enforcement) below.

### The `x-gdpr-sensitive` keyword

Mark any property in a `data` schema with `x-gdpr-sensitive`:

```jsonc
{
  "type": "object",
  "required": ["user_id", "email"],
  "properties": {
    "user_id": { "type": "integer" },
    "email":   { "type": "string", "x-gdpr-sensitive": "email" },   // optional category
    "phone":   { "type": "string", "x-gdpr-sensitive": true },      // or just true
    "profile": { "type": "object", "properties": {
      "full_name": { "type": "string", "x-gdpr-sensitive": true }
    }},
    "addresses": { "type": "array", "items": { "type": "object", "properties": {
      "line": { "type": "string", "x-gdpr-sensitive": true }
    }}}
  }
}
```

- It is an **extension keyword**: it accepts either `true` (a boolean) or a non-empty **string
  category** (e.g. `"email"`, `"national_id"`) for documentation. `false` and `""` leave a field
  unmarked.
- It is **ignored by validation** — like every unknown keyword, it never makes a value valid or
  invalid. Adding it to an existing schema is **not** a breaking change (`compat` ignores it too),
  so you can annotate PII without minting a new URN.
- It works at any depth: nested objects (`profile.full_name`) and array items (`addresses[].line`).

### `bqschema gdpr`

```sh
bqschema gdpr --registry registry.json [--require [--pattern <re>]] [--mask <message.json> [--urn <urn>]]
```

| Mode | What it does |
| ---- | ------------ |
| *(default)* **inventory** | For each URN, lists the sensitive field paths (incl. nested + array items) and prints a coverage summary `N URN(s), M sensitive field(s)`. Always exits `0`. |
| `--require` | **CI gate.** Fails if any property whose **name** matches a PII pattern (`email`, `ssn`, `phone`, `tckn`/national-id, `iban`, `address`, …) is **not** marked `x-gdpr-sensitive` — catching PII someone forgot to annotate. Override the pattern with `--pattern <regexp>`. Exits `1` on a finding, `0` when clean. (Object/array *containers* whose name matches are not themselves flagged — annotation lives on their leaves.) |
| `--mask <message.json>` | Prints a copy of the message with the sensitive fields **masked**, for safe logging or fixtures. A full **envelope** masks its `data` against the URN in `job`/`urn`; a **bare data object** masks itself against `--urn`. Exits `0`; exits `1` only when the URN has no registered schema. |

Masking is leaf-aware: a sensitive **string** keeps its first character then `***`
(`"alice@example.com"` → `"a***"`) so it stays distinguishable in a log; any other sensitive value
(number, boolean, object, array) becomes `"***"`. Masking is **one-way** — it is for safe logging,
**not** encryption. The same logic is a reusable library function (`internal/gdpr.Mask`).

```sh
# Inventory
$ bqschema gdpr --registry examples/registry.json
  urn:babel:orders:created: no sensitive fields
  urn:babel:users:registered: 5 sensitive field(s)
    - addresses[].line
    - email (email)
    - phone
    - profile.full_name
    - profile.tckn (national_id)
2 URN(s), 5 sensitive field(s).
```

### AsyncAPI

`x-gdpr-sensitive` is **carried through** into the generated AsyncAPI 3.0 catalog (the `data`
schema is embedded verbatim as each message's payload), so the event catalog documents which
fields are PII — usable by downstream tooling.

### SDK enforcement

The registry is the **declaration + audit** layer; it never touches message bytes at runtime. An
**SDK** is where runtime crypto/masking hooks in: a producer/consumer reads the same
`x-gdpr-sensitive` annotation (from the schema, or from the AsyncAPI catalog) to decide which `data`
fields to **encrypt/tokenise on publish and decrypt on consume**. That keeps the wire envelope
frozen (the ciphertext is still pure JSON in `data`) while the registry remains the single,
git-tracked source of truth for *what* is sensitive. `bqschema gdpr --mask` is the registry-side,
non-reversible equivalent used only for safe logging and fixtures.

## GitHub Action

A packaged composite Action installs the CLI and runs it as a merge gate, so you don't
have to wire up `go install` yourself. By default it runs `check` (registry
self-validation); point `command` at any other subcommand and use `args` as an escape
hatch for positional arguments (e.g. `compat`'s two schema files).

```yaml
# .github/workflows/schema-gate.yml
name: Schema gate
on: [pull_request]

permissions:
  contents: read

jobs:
  registry:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5

      # Validate the registry itself (every schema parses).
      - uses: BabelQueue/babelqueue-registry@v0.2.0
        with:
          registry: registry.json

  compat:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5

      # Backward-compatibility gate: fails the PR on a breaking schema change.
      # `compat` takes two positional schema files: <old> <new>.
      - uses: BabelQueue/babelqueue-registry@v0.2.0
        with:
          command: compat
          args: schemas/orders-created.json schemas/orders-created.json
```

### Action inputs

| Input | Default | Description |
| ----- | ------- | ----------- |
| `version` | `latest` | `bqschema` version to install (a release tag like `v0.1.0`, or `latest`). |
| `command` | `check` | Subcommand: `check`, `validate`, `compat`, `export-asyncapi`, or `gdpr`. |
| `registry` | `registry.json` | Manifest path (passed to `check`/`validate`/`export-asyncapi`). |
| `dir` | `.` | Working directory the CLI runs in. |
| `args` | `""` | Extra args appended after the subcommand (e.g. `compat`'s `<old> <new>` files). |

A non-zero exit (`1` = breaking change / validation failure, `2` = usage/IO error) fails
the step and blocks the merge.

## What `compat` considers breaking

| Change | Verdict |
| ------ | ------- |
| Add an **optional** field | compatible |
| Make an optional field **required** | **breaking** |
| Add a new **required** field | **breaking** |
| Remove / rename / retype a field | **breaking** |
| Tighten `additionalProperties` `true → false` | **breaking** |
| Drop an `enum` value / add an enum where any value was allowed | **breaking** |
| Widen an `enum`, relax a constraint, drop a required field | compatible |

A non-empty `compat` result is the signal to **version the URN, not mutate it**
("consumers upgrade before producers").

## Exit codes

| Code | Meaning |
| ---- | ------- |
| `0`  | valid / backward-compatible |
| `1`  | validation failure / a breaking change found |
| `2`  | usage or IO error |

## Scope & limits (honest list)

- **Subset of JSON Schema draft-07**, by design: `type`, `required`, `properties`,
  `additionalProperties`, `items`, `enum`, `const`, `minLength`, `minimum`. Enough for real
  `data` shapes, mirroring php-sdk's envelope validator subset. Unknown keywords are ignored.
- **Zero dependencies** (Go stdlib only), in the spirit of BabelQueue's GR-7.
- **Optional and SDK-independent** — no BabelQueue SDK depends on this; adopt it if you want
  the governance gate. The envelope is untouched (`schema_version: 1`).
- **Confluent-compatible REST** (read-mostly) is available via `bqschema serve` — see
  [REST surface](#rest-surface-confluent-compatible). Registration/writes stay out of scope (the
  registry is git-managed).
- **GDPR sensitive-field governance** — declare PII with `x-gdpr-sensitive`, audit it (`gdpr
  --require`), and mask it for safe logging (`gdpr --mask`); see
  [GDPR sensitive fields](#gdpr-sensitive-fields). Runtime field encryption stays an **SDK**
  concern (the registry declares + audits; the SDK enforces).
- **Deferred** (later phases): an OpenTelemetry-derived event-flow map.

## License

MIT
