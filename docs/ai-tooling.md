# AI tooling

I used Claude Code in bypass mode, in a separate profile and with hook protections
(`.claude/hooks/guard.cjs`, the gitleaks pre-commit hook, `scripts/check-ai-sessions.cjs`).
Sources, pinned commits and licenses of everything installed are in `.claude/THIRD_PARTY.md`.

## Install procedure

1. Download into `.quarantine/` (git-ignored), never directly into `.claude/`. Each repo was fetched at a full
   commit SHA (`git fetch --depth 1 origin <sha>`).
2. Scan: `npx ecc-agentshield@1.6.0 scan` (local, sends nothing out), then my own checks.
3. Copy only the clean, needed files into `.claude/`; delete `.quarantine/`.
4. Skill discovery check: layout (`.claude/skills/<name>/SKILL.md`) and frontmatter of all 7 skills match the Claude Code docs and pass `claude plugin validate .claude/skills`, so no skill file was changed; a session opened before the folders existed will not list them until it is restarted (or `/reload-skills`).

The two design skills (see below) followed the same procedure with two differences: the quarantine was a
temporary folder outside the repository (not `.quarantine/`), and every file was checked out with
`core.autocrlf=false` so the copies are LF and byte-identical to the pinned git blobs.

## What is installed and why

| Tool | Why |
|---|---|
| `golang-error-handling`, `golang-concurrency`, `golang-database`, `golang-testing` (samber/cc-skills-golang) | The backend is Go with a Postgres-backed queue (`FOR UPDATE SKIP LOCKED`), a worker pool and transactions. These cover exactly those areas. Taken only from github.com/samber; a malicious copy under the same name is circulating. `allowed-tools` lines were removed because they pre-approved `go` and `git` commands. |
| `vue`, `vitest` (antfu/skills) | Frontend is Vue 3 + TypeScript with Vitest. |
| `verification-before-completion` (obra/superpowers) | Enforces "run tests/build before claiming done", matching the project's verification rule. |
| `silent-failure-hunter` (anthropics/claude-plugins-official) | A queue/worker/LLM pipeline is where swallowed errors hurt. Restricted to `Read, Grep, Glob`. |
| `repo-publication-auditor` (aitmpl) | The repo is public; this audits history, author emails and leaked paths before publishing. Compared against the pinned upstream commit because the CLI downloads from `main`. |
| `frontend-design` (anthropics/skills, `41bbe19`) | Design direction for the Vue frontend: typography, palette and layout choices that are specific to the brief instead of template defaults. `SKILL.md` and `LICENSE.txt` only (Apache-2.0). |
| `ui-ux-pro-max` (nextlevelbuilder/ui-ux-pro-max-skill, `6ae86f6`) | A local, searchable database (styles, palettes, font pairings, UX rules, chart types, Vue stack rules) for the dashboard and approval screens. Only `.claude/skills/ui-ux-pro-max/` plus the root `LICENSE` (MIT); `scripts/tests/` not taken. Search results are recommendations, not instructions. |
| `gopls-lsp` plugin + `gopls` v0.24.0 | Go code intelligence. gopls is pinned; the plugin only declares `command: gopls`. |

## What is not installed and why

- `security-guidance` plugin: it installs a package whose version is not pinned and sends diffs to Opus at extra
  cost. The built-in `/security-review` is used instead.
- aitmpl Go/Vue/Postgres agents and hooks: copies of other material, or not working.
- Postgres MCP servers: a known read-only bypass vulnerability. `psql` is used instead.
- Snyk agent-scan: uploads content to an external service.
- From the `ui-ux-pro-max-skill` repo: the other skills (`banner-design`, `brand`, `design`, `design-system` and
  the rest), `cli/` and the `npx uipro-cli` installer (it would write into the project and fetch from the network),
  and `scripts/tests/` of the skill (test code and fixtures, not needed to use it).

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

## Scan results: design skills (`frontend-design`, `ui-ux-pro-max`)

Scope: the exact 50 files that were installed (2 + 48), plus the two upstream repos at the pinned commits.

- `ecc-agentshield@1.6.0 scan`: on the installed files it reports no findings, but it scans zero files in them
  (it reads `CLAUDE.md`, `settings.json`, agents, hooks and MCP definitions, not skill folders), so this result
  says little. Scanning the whole upstream `ui-ux-pro-max-skill` checkout gave grade D (51 findings) from files
  that were not installed: the repo's own `CLAUDE.md`, `stack/CLAUDE.md`, `stack/.claude/settings.json`,
  `stack/.claude/agents/design-review.md` and `stack/.mcp.json` (auto-approved MCP servers started with `npx -y`).
  The `anthropics/skills` checkout: grade A, no findings. The `--opus`, `--injection` and `--deep` modes were not
  used (they send content to an LLM).
- `gitleaks dir` on both skill folders and the license: no leaks.
- Own checks over the 50 files: invisible Unicode (zero-width, bidi, tag characters): none; symlinks and binaries:
  none; CR characters: none; injection phrases ("ignore previous", "you are now", ...): none; shell patterns
  (pipe-to-shell, privilege escalation, recursive delete, eval) in CSV/JSON/Markdown: none; URLs in the data:
  documentation and font links (plus `example.com`, `localhost:8080` in sample code), none in the scripts except
  `fonts.google.com` and `github.com` inside `validate_data.py` string checks.
- What was read in full: `SKILL.md` (both), `LICENSE.txt`, both `references/*.md`, `search.py`, `reasoning_contract.py`,
  the first 420 lines of `core.py` and the whole persistence section of `design_system.py`. The rest of `core.py`,
  `design_system.py`, `validate_data.py` and the CSV/JSON data (about 3.6 MB) were not read line by line; they were
  checked with the programmatic checks above and by grep.
- Scripts: only the Python standard library (`csv`, `re`, `json`, `difflib`, `pathlib`, `tempfile`, `argparse`, ...;
  `urllib.parse` only splits URLs in the validator). No `socket`, `subprocess`, `urllib.request`, `os.system`,
  `eval`, `exec` or dynamic imports. The only environment read is `COLORTERM`. The only write path is
  `--persist`, which creates `design-system/<slug>/MASTER.md` (and `pages/<page>.md`) under `--output-dir`; the
  project and page names pass through `safe_slug` (`[a-z0-9_-]` only, no path traversal) and an existing file is
  not overwritten without `--force`.
- Runtime: no Python on the host, so `search.py` runs in `python:3.14.10-slim-trixie` with `--network none` and the
  repo mounted read-only at `/work`; with the read-only mount `--persist` fails (checked). The command is in `CLAUDE.md`.
