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
```

### In CI

```yaml
- run: go install github.com/babelqueue/babelqueue-registry/cmd/bqschema@latest
- run: bqschema check --registry registry.json
- run: bqschema compat schemas/orders-created.json schemas/orders-created.json   # old vs PR's version
```

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
- **Deferred** (later phases): a Confluent-compatible REST API, GDPR crypto-field masking,
  and an OpenTelemetry-derived event-flow map.

## License

MIT
