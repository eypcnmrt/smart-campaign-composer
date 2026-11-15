# Smart Campaign Composer

Natural-language marketing brief -> segment rule + SMS/email content (Claude) -> human approval -> queued delivery to a mock provider with live monitoring. Plan: `docs/PLAN.md`.

## Stack
- Backend: Go (chi, pgx, anthropic-sdk-go), one binary with api / worker / seed commands; separate mock-provider.
- Frontend: Vue 3 + Vite + TypeScript. Database: PostgreSQL (queue = `deliveries` table, `FOR UPDATE SKIP LOCKED`).
- Runtime: Docker Compose. LLM access goes through the `LLMClient` interface (Anthropic or mock).

## Commands
- `docker compose up` - db, migrate, seed, mock-provider, worker and the api (127.0.0.1:8080; the default `LLM_MODE=auto` uses the real model when a key is set in the dotenv file, which costs money). **Every compose up in a session or script of this repo gives `LLM_MODE=mock` explicitly** (`$env:LLM_MODE='mock'; docker compose up -d` in PowerShell, `LLM_MODE=mock docker compose up -d` in Git Bash); only a smoke test chosen on purpose uses `LLM_MODE=anthropic` and the web app (http://127.0.0.1:3000: nginx, unprivileged, read-only, serves the build and forwards `/api` with the event stream unbuffered; security headers come from `web/nginx/security-headers.conf`); every setting has a default, no dotenv file needed
- `make test` - gofmt, `go vet`, `go test -race` incl. PostgreSQL integration tests (`-tags=integration`, fail instead of skip without a database) the hook/scanner tests and the web checks (`npm ci`, `npm audit --audit-level=high`, vue-tsc, vitest, via `scripts/test-web.sh`; the npm cache is the external volume `scc-npm-cache`), all in Docker, no API key
- `make soak` - 5,000 messages through the real containers with a chaos provider; checks no double send, 100/s cap, Retry-After (about 3-4 minutes: the default backoff starts at 15 s)
- `make eval LLM_MODE=anthropic` - the golden set (`eval/golden.json`, 14 Turkish briefs) against the REAL model, scored by deterministic checks (no model as judge); costs money, so it refuses to start without `LLM_MODE=anthropic` and a key in the dotenv file, and stops at `EVAL_MAX_USD` (default 1.00). `EVAL_MODEL=` picks another model, `EVAL_ARGS="-only id1,id2"` some briefs, `EVAL_ARGS="-mock"` is a free dry run (the only form a session may run without being told to spend). Results: `eval/results/`. Not part of `make test` or CI. Prices: `backend/internal/llm/pricing.go`
- `make e2e` - browser tests (Playwright, `e2e/`) against the real stack with the mock model, in the compose project `scc-e2e` (own network and database, nothing published, removed afterwards; not part of `make test`; about 90 s). `SCREENSHOTS=1` rewrites `docs/screenshots/`, `GREP="outage"` selects tests, `KEEP=1` leaves the project up. npm for `e2e/` runs only through `sh scripts/e2e-npm.sh <args>` (container, no lifecycle scripts); the Playwright image tag and the `@playwright/test` version must move together. The mock provider's control switches (the outage, and `POST /control/throttle-next/N`: the next N messages get a 429) exist only with `MOCK_CONTROL=1`. The compose mock provider fails a little by default (5 % 500, 5 % 429, 20-200 ms) so that retries show on the dashboard; the e2e overlay, the soak and the Go tests set their own deterministic values.
- Do not run `docker compose config` (the guard blocks it); verify with `docker compose up -d` (with `LLM_MODE=mock`, see above) and `docker compose ps -a`
- A subset of the tests with the database: `docker compose -f docker-compose.test.yml run --rm backend sh -c "cd backend && go test -race -tags=integration -count=1 -run NAME ./internal/PKG/"` (then `docker compose -f docker-compose.test.yml down -v`)
- Packages added in `backend/internal/`: `migrate`, `db`, `testdb`, `seed`, `mockprovider`, `queue`, `retry`, `ratelimit`, `worker` (queue dispatcher, circuit breaker, channel lock, heartbeat), `soak`, `campaign` (state machine, store, approval, stats), `api` (chi handlers, SSE stream), `metrics` (Prometheus); SQL in `backend/migrations/`
- `make fmt` / `make vet` / `make test-unit` - Go formatting, `go vet`, `go test ./...` (local, no Docker, no API key)
- `make fuzz` - fuzz the segment compiler, `FUZZTIME=30s` per target; `-race` runs later in Docker (needs a C compiler on Windows)
- Web (`web/`): npm, vite, vitest and vue-tsc run only in a pinned node container, never on the host (third-party package code stays off
  this machine). `sh scripts/web.sh <command>` mounts only `web/`, keeps `node_modules` and the npm cache in named volumes and turns
  lifecycle scripts off: `sh scripts/web.sh npm ci`, `sh scripts/web.sh npm run typecheck`, `sh scripts/web.sh npx vitest run`,
  `sh scripts/web.sh npm install --save-exact pkg@x.y.z` (writes package.json and the lock file), `sh scripts/web.sh dev` (dev server on
  127.0.0.1:5173, proxies `/api` to the host's :8080, so run `docker compose up -d` first, with `LLM_MODE=mock`). Only read-only queries such as `npm view` run on the host.
  Pin exact versions that are at least two weeks old and keep `npm audit` clean; there are no dotenv files for Vite (settings come from `vite.config.ts` and `/api/config`).
- Windows Application Control may block `vet.exe` (and now and then a fresh test binary): then use `make test-unit GOTESTFLAGS=-vet=off`
  and run vet in a container (from PowerShell): `docker run --rm -v "${PWD}/backend:/src:ro" -w /src -e GOCACHE=/tmp/gocache golang:1.27 go vet ./...`
- Go commands run in a container, not on Windows (Application Control blocks new binaries). From PowerShell, with module and
  build caches in named volumes (replace the last words, e.g. `gofmt -l .`, `go vet ./...`, `go test -race ./...`,
  `go test -run '^$' -fuzz '^FuzzExtractor$' -fuzztime 20s ./internal/streamjson/`; this also gives `-race` a C compiler):
  `docker run --rm -v "${PWD}:/src" -w /src/backend -v scc-gomod:/go/pkg/mod -v scc-gocache:/root/.cache/go-build -e GOFLAGS=-buildvcs=false golang:1.27 go test ./...`
- Every `docker run -v` line here is for PowerShell. In Git Bash the path conversion rewrites the `-v` argument and Docker then creates a junk
  folder such as `backend;C` in the repo root; there, `export MSYS_NO_PATHCONV=1` and use `$(pwd -W)` as `scripts/web.sh` does.
- `node --test ".claude/hooks/*.test.cjs"` - tests for the protection hook and the gofmt hook
- `ui-ux-pro-max` search script (no Python on this machine; it runs in a pinned official image, no network, repo mounted read-only at /work).
  From PowerShell, in the repo root (the skill's own `${CLAUDE_PLUGIN_ROOT}/...` path does not exist for a project skill, use this instead):
  `docker run --rm --network none -v "${PWD}:/work:ro" -w /work python:3.14.10-slim-trixie python -I -B .claude/skills/ui-ux-pro-max/scripts/search.py "marketing campaign dashboard" --design-system`
  (other modes: `"<query>" --domain ux|chart|color|typography|...`, `"<query>" --stack vue`). Only `--persist` writes, and only to `design-system/<slug>/`;
  to allow it, mount the repo writable (`:rw` instead of `:ro`) and add `--output-dir /work`. The upstream `SKILL.md` is kept unmodified.
- Layout: Go module `smartcampaign` in `backend/` (run Go commands as `go -C backend ...`); the Vue 3 + Vite + TypeScript app is in `web/` (no UI library, no Pinia; tokens in `web/src/styles/tokens.css`, design notes in `design-system/MASTER.md`)
- Packages in `backend/internal/`: `segment` (rule DSL, validation, SQL, JSON schema), `content` (limits, variables, render, SMS info),
  `streamjson` (partial JSON for streamed text), `llm` (`Client`, Anthropic + mocks, `FromEnv` with `LLM_MODE`), `compose` (prompts and repair loop).
  Tests never need an API key: the Anthropic client is tested against a fake API server, the repair loop with `llm.Scripted`.
- Tool quirk: `Write`/`Edit` turn a typed backslash-u-0-0-e-7 into the character "ç" itself (lone surrogates and a doubled backslash are left alone).
  To put a literal JSON escape in a file, type a placeholder such as `¦u00e7` and replace the `¦` with a backslash with `node` afterwards.
- `.claude/hooks/gofmt.cjs` runs `gofmt -w` after every edit of a `.go` file (silent if gofmt is missing)
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
