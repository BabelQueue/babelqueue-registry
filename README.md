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
| `command` | `check` | Subcommand: `check`, `validate`, `compat`, or `export-asyncapi`. |
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
- **Deferred** (later phases): GDPR crypto-field masking, and an OpenTelemetry-derived
  event-flow map.

## License

MIT
