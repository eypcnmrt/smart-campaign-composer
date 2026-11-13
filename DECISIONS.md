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
