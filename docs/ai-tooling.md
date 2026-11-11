# AI tooling

I used Claude Code in bypass mode, in a separate profile and with hook protections
(`.claude/hooks/guard.cjs`, the gitleaks pre-commit hook, `scripts/check-ai-sessions.cjs`).
Sources, pinned commits and licenses of everything installed are in `.claude/THIRD_PARTY.md`.

## Install procedure

1. Download into `.quarantine/` (git-ignored), never directly into `.claude/`. Each repo was fetched at a full
   commit SHA (`git fetch --depth 1 origin <sha>`).
2. Scan: `npx ecc-agentshield@1.6.0 scan` (local, sends nothing out), then my own checks.
3. Copy only the clean, needed files into `.claude/`; delete `.quarantine/`.

## What is installed and why

| Tool | Why |
|---|---|
| `golang-error-handling`, `golang-concurrency`, `golang-database`, `golang-testing` (samber/cc-skills-golang) | The backend is Go with a Postgres-backed queue (`FOR UPDATE SKIP LOCKED`), a worker pool and transactions. These cover exactly those areas. Taken only from github.com/samber; a malicious copy under the same name is circulating. `allowed-tools` lines were removed because they pre-approved `go` and `git` commands. |
| `vue`, `vitest` (antfu/skills) | Frontend is Vue 3 + TypeScript with Vitest. |
| `verification-before-completion` (obra/superpowers) | Enforces "run tests/build before claiming done", matching the project's verification rule. |
| `silent-failure-hunter` (anthropics/claude-plugins-official) | A queue/worker/LLM pipeline is where swallowed errors hurt. Restricted to `Read, Grep, Glob`. |
| `repo-publication-auditor` (aitmpl) | The repo is public; this audits history, author emails and leaked paths before publishing. Compared against the pinned upstream commit because the CLI downloads from `main`. |
| `gopls-lsp` plugin + `gopls` v0.24.0 | Go code intelligence. gopls is pinned; the plugin only declares `command: gopls`. |

## What is not installed and why

- `security-guidance` plugin: it installs a package whose version is not pinned and sends diffs to Opus at extra
  cost. The built-in `/security-review` is used instead.
- aitmpl Go/Vue/Postgres agents and hooks: copies of other material, or not working.
- Postgres MCP servers: a known read-only bypass vulnerability. `psql` is used instead.
- Snyk agent-scan: uploads content to an external service.

## Scan results

Scope: the selected paths of the five downloaded sources and the aitmpl download (54 files, including `evals/` and `GENERATION.md` that were not installed).

- `ecc-agentshield@1.6.0 scan --path .quarantine` (grade D overall, but that covers whole upstream repos,
  including plugins I did not install). For the installed files it reported 27 findings:
  prompt-defense heuristics ("missing instruction-boundary/data-leakage defense", etc.) on the two agent files,
  size warnings (>5000 characters) and one "agent has no tools restriction" on `silent-failure-hunter`
  (fixed by adding `tools: Read, Grep, Glob`). No secrets, hook, MCP or permission findings, and nothing at all
  for the skills.
- Own checks (zero-width, bidi and tag Unicode; HTML comments; `curl | bash`; base64/eval/exec; network calls;
  archives and binaries; `allowed-tools`; hook/MCP in frontmatter; injection phrases):
  - invisible Unicode: none; archives/binaries: none; `curl | bash`: none; hooks/MCP definitions: none.
  - HTML comments: 32, all of the form `<!-- Source references: - https://... -->` or short code comments
    inside Vue examples; verified no non-URL content in the multi-line ones.
  - eval/exec/network matches: only `db.Exec(...)`, `fetch('/api/...')` and `localhost` URLs in code examples.
  - `allowed-tools`: only the four samber skills (removed; 0 remaining).
  - Notes: `golang-testing` mentions `go install github.com/cweill/gotests/gotests@latest` (text and an
    `openclaw` metadata block, neither executed by Claude Code); `repo-publication-auditor` has `Bash` in its tools.
- `repo-publication-auditor`: SHA-256 identical to the file at `davila7/claude-code-templates@4036c90`.
- `gopls-lsp` marketplace copy identical to `anthropics/claude-plugins-official@dceb15d` (line endings aside).
