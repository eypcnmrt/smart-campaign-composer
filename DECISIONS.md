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

## Content

### Limits count characters of the template, variables are matched exactly
- **Decision:** SMS 160, subject 80 and body 2000 are counted in runes (`utf8.RuneCountInString`) on the
  template text, before variables are filled in. Only `{{first_name}}` and `{{city}}`, spelled exactly
  (no spaces, no other case), are accepted; any other `{` or `}` is an error with its character number.
- **Why:** "ğ" is two bytes but one character, so a byte count would reject valid Turkish text. A
  variable like `{{ first_name }}` would pass a lenient check but never be replaced and would reach the
  customer as literal text, so the validator and `Render` agree on one spelling.
- **Alternative:** trim spaces inside braces. Rejected: one more spelling to keep in sync, and the model
  can simply be told the exact form in the repair message.

### Empty variable values fall back, in a single pass
- **Decision:** a blank `first_name` renders as "Değerli müşterimiz" (docs/PLAN.md section 2, assumption 7).
  I also added a fallback for a blank `city`: "Türkiye". Substitution is one pass, so a value that looks
  like `{{city}}` is inserted literally.
- **Why:** `city` is NOT NULL in the seed schema, so its fallback should never fire; it exists so a
  `{{city}}` can never reach a customer unfilled. The plan only names the `first_name` fallback.
- **Alternative:** reuse "Değerli müşterimiz" for the city. Rejected: "Değerli müşterimiz için fırsat" reads
  fine but "Değerli müşterimiz şubesi" does not.

### SMS segment info is computed from the GSM 03.38 alphabet
- **Decision:** `AnalyzeSMS` keeps GSM-7 (160 / 153 per part) only if every character is in the default
  alphabet or its extension table (extension characters cost two septets), otherwise UCS-2 (70 / 67, emoji
  cost two units). Of the Turkish letters Ü Ö Ç ü ö are in GSM-7; ç ğ ı İ ş Ş Ğ are not, so one of them
  switches the whole text. A character is never split across two messages. It is information only; the
  limit stays 160 characters (docs/PLAN.md section 2, assumption 5).
- **Why:** this is how carriers count, and it is why a 150-character Turkish SMS can already cost three
  messages. A unit test checks the alphabet size so a typo cannot flip texts between encodings.
- **Alternative:** "any non-ASCII means UCS-2". Simpler, but wrong for ü, ö and Ç, which are extremely
  common in Turkish text. Turkish national shift tables are not assumed, because carriers rarely use them.

## Streaming

### A small partial-JSON extractor, with the final decode as the source of truth
- **Decision:** `streamjson.Extractor` is a byte-level state machine for one shape only: a flat object
  with string values. It resolves escapes (`\"`, `\n`, `\u00e7`, surrogate pairs) and holds back anything
  unfinished across chunk boundaries (half an escape, half a multi-byte character, the first half of a
  surrogate pair). Invalid UTF-8 and unpaired surrogates become U+FFFD exactly as `encoding/json` does.
  On anything else (nested value, duplicate field, bad escape) it stops with `ErrUnexpected` and the
  caller shows what it has. What it emitted is only a preview: when the stream ends, the whole text is
  decoded with `encoding/json` (`content.Parse`) and validated, and that result replaces the preview.
- **Why:** the model produces the JSON in small pieces and the UI should show the text as it is written
  (docs/PLAN.md section 4.2). Tests cut every sample at every byte and compare with `encoding/json`; a
  fuzz target checks that chunking never changes the result and that any flat string object accepted by
  `encoding/json` yields the same text.
- **Alternative:** a streaming JSON library, or regexes on the raw text. A library would still need the
  chunk-boundary handling tested; regexes break on escaped quotes. The fallback from the plan stands: if
  the extractor errors, show the raw stream and let the final validated result replace it.

## LLM client

### Official Go SDK, pinned, called through a small interface
- **Decision:** `github.com/anthropics/anthropic-sdk-go/v2` at exactly v2.9.0 (go.mod and go.sum), behind
  `llm.Client` (`Complete`, `Stream`, `Mode`, `Model`). Everything else in the backend sees only the
  interface. `FromEnv` returns the real client only if `ANTHROPIC_API_KEY` is set; otherwise the demo
  mock, and `Mode()` says which (the "Mock LLM" badge). No other credential source is consulted, so a
  machine that happens to be logged in elsewhere cannot spend money without the key being set. The model
  comes from `ANTHROPIC_MODEL`, default `claude-opus-5-5`.
- **Why:** the SDK's request and streaming types change between releases and the API surface for this
  model family moved recently (thinking, effort, fallbacks), so the version is fixed and the bindings
  were read from the SDK source rather than guessed. The interface is what lets every test run without a
  key; the real client is tested against a fake API server through the base-URL override instead.
- **Alternative:** raw HTTP. More code to own and no gain. A floating SDK version was rejected: a `go get -u`
  could silently change request shapes.

### One structured-output request per task, effort set explicitly, nothing the model rejects
- **Decision:** segment: one non-streaming request, `output_config.format` = the schema, effort `low`.
  Content: streaming, same mechanism with the content schema, effort `medium`. Thinking is left unset
  (always on and adaptive on Claude Opus 5.5), so effort is the only control, and it is always sent
  because the model's default is `medium`. No assistant prefill, forced tool choice or sampling
  parameters, which this model rejects with a 400. `max_tokens` is 16000 (segment) and 64000 (content),
  with whole-call deadlines of 90 s and 4 min.
- **Why:** thinking tokens count toward `max_tokens`, and a cut-off answer is a failed call that costs more
  than a large limit ever does. Streaming is used for the large limit so a long answer does not hit an
  HTTP timeout. The deadline covers the SDK's retries as well, so a hanging provider cannot hold a request.
- **Alternative:** a tool call with a forced `tool_choice` (rejected by the model) or JSON in plain text
  (no guarantee the output follows the shape).

### SDK retries and the repair loop are separate and never share a budget
- **Decision:** the SDK retries 408, 409, 429, 5xx and connection errors up to twice and honors
  `Retry-After`; the repair loop (package `compose`) re-asks only when the model's own output failed
  validation. Errors are typed (`ErrRefusal`, `ErrMaxTokens`, `ErrTimeout`, `ErrRateLimited` with the wait,
  `ErrUnavailable`, `ErrBadRequest`, `ErrUnexpectedStop`) and only a validation failure is repaired.
  An error event inside a stream carries status 200, so it is classified by its type.
- **Why:** mixing them would turn a rate limit into "the model keeps producing invalid JSON" and burn
  repair attempts on a problem the model cannot fix, or retry an invalid answer as if it were a network fault.
- **Alternative:** one retry loop around everything. Simpler, but it cannot tell the two failures apart.

### Refusals: server-side fallback is on by default
- **Decision:** requests carry `fallbacks: "default"` with beta `server-side-fallback-2026-07-01`, so a
  request declined by a safety classifier is re-run on the model Anthropic recommends for that category,
  inside the same call. `ANTHROPIC_FALLBACK=off` turns it off in case the beta is not enabled for an
  account. `stop_reason` is checked before any content is read. After a mid-stream fallback the text
  continues in a new block; all text blocks together are the answer (tested against the fake API with the
  cut inside a `\u` escape). If the final stop reason is still `refusal` the call fails with `RefusalError`
  (category and explanation kept) and any partial text is discarded; it is not retried or repaired.
- **Why:** classifiers can fire on harmless marketing text, and the fallback is one round trip with no
  client logic. The fallback model does not receive the first model's thinking blocks; that is fine here
  because every request is a single turn.
- **Alternative:** the SDK's client-side middleware (needed only on Bedrock, Vertex AI or Foundry, which
  this project does not use) or a manual retry on another model.

### No key means a demo mock, tests use a scripted client
- **Decision:** the demo mock is deterministic and keyword-based (cities, "son N gün", sepet, satın
  alma, a TL amount, a discount percentage); it always returns valid output and flags what it cannot
  express as `unsupported_criteria`. Tests of the repair loop use `Scripted`, which plays exact steps (a
  broken answer, a refusal, a cut-off) and records every request.
- **Why:** the demo has to look sensible for someone running `docker compose up` without a key, while the
  scenario tests need exact control over what "the model" says.
- **Alternative:** one mock that does both, which would make the demo less convincing or the tests vague.
