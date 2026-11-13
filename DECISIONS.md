# Decisions

Short records: what was decided, why, and what was considered instead. Product assumptions
(segment scope, limits, consent, delivery semantics) are in `docs/PLAN.md` section 2 and move
here when the README is written.

## LLM layer

### Segment JSON schema is generated from the vocabulary and checked both ways
- **Decision:** `segment.Schema()` builds the structured-output schema from the same field,
  operator and enum tables the validator uses (`defaultFields`, `eventTypes`, `eventOps`).
  `schema_test.go` reads the schema back as plain JSON and compares it with the vocabulary in
  both directions (everything in the vocabulary is in the schema and the other way round), then
  feeds every example the schema can express through `Decode` and `Validate`. The comparison is
  itself tested by editing the vocabulary and requiring a report.
- **Why:** a schema and a validator that disagree either force the model into output the repair
  loop can never fix, or let it pick values the validator rejects. A generated schema cannot
  forget a city; the test also covers the structure (variants, refs), which is written by hand.
- **Alternative:** a hand-written JSON string. Simpler to read, but it drifts silently when a city
  or operator is added.

### The schema carries no limits and no recursion
- **Decision:** no `minimum`, `maximum`, `minLength`, `maxItems` and similar keywords, and no
  recursive `$ref`. `within_days` 1-365, list lengths and the condition count are checked by the
  Go validator and only mentioned in descriptions. A test allows only the keywords structured
  outputs support and rejects reference cycles.
- **Why:** structured outputs do not support recursion or numeric/string constraints, and the Go
  SDK is not documented to strip them for us (as the Python and TypeScript SDKs do), so one
  unsupported keyword would be a 400 at runtime. `any_of` cannot nest because the group refers to
  single conditions only (docs/PLAN.md section 4.1).
- **Alternative:** put the limits in the schema and rely on the API ignoring them. Rejected: it is
  undocumented for Go and would hide the validator as the real authority.
