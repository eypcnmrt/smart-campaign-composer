# Smart Campaign Composer

Natural-language marketing brief -> segment rule + SMS/email content (Claude) -> human approval -> queued delivery to a mock provider with live monitoring. Plan: `docs/PLAN.md`.

## Stack
- Backend: Go (chi, pgx, anthropic-sdk-go), one binary with api / worker / seed commands; separate mock-provider.
- Frontend: Vue 3 + Vite + TypeScript. Database: PostgreSQL (queue = `deliveries` table, `FOR UPDATE SKIP LOCKED`).
- Runtime: Docker Compose. LLM access goes through the `LLMClient` interface (Anthropic or mock).

## Commands
- `docker compose up` - run the whole stack (planned)
- `make test` - Go unit/integration + frontend tests in Docker, needs no API key (planned)
- `node --test .claude/hooks/guard.test.cjs` - tests for the protection hook
- `node scripts/check-ai-sessions.cjs` - scan `ai-sessions/` for keys, e-mails, phones, home paths (masked report)
- `gitleaks git --staged --redact` - what the pre-commit hook runs

## Security rules (public repo, sessions run in bypass mode)
- Never read, print or write dotenv files other than `.env.example`; never dump environment variables
  (`env`, `printenv`, `set`, `Get-ChildItem env:`, `docker compose config`). `.claude/hooks/guard.cjs` enforces this.
- Never use `git push --force`, `--no-verify` or change `core.hooksPath`; never delete outside the project folder.
- The API key lives only in `.env` (git-ignored); `.env.example` keeps `ANTHROPIC_API_KEY=` empty.
- Git identity is repo-local (GitHub noreply address). Do not change global git config.
- Commits are scanned by gitleaks (`.githooks/pre-commit`). If it blocks, fix the cause; do not bypass.
- Before pushing: run `node scripts/check-ai-sessions.cjs` and grep the repo for `sk-ant-`, e-mails and phone numbers.
- LLM output is untrusted: it is validated against a schema and whitelists, and compiled to parameterized SQL only.

## Conventions
- All repo files and code comments are in English (exceptions: `docs/PLAN.md` and raw exports in `ai-sessions/`).
- Text files use LF line endings (`.gitattributes`); scripts run inside Linux containers.
- Keep changes minimal; verify with the relevant test, `go vet` or `vue-tsc` before finishing.
