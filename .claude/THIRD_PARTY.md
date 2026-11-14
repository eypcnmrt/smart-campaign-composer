# Third-party Claude Code tooling

Everything below was downloaded into a git-ignored quarantine folder, scanned, and only then copied into
`.claude/`. Every source is pinned to a full commit SHA. Full license texts are in `.claude/licenses/`.

| Component | Installed at | Source | Pinned commit | License | Copyright |
|---|---|---|---|---|---|
| Skills `golang-error-handling`, `golang-concurrency`, `golang-database`, `golang-testing` | `.claude/skills/<name>/` | https://github.com/samber/cc-skills-golang | `d68d80aec298e1b74b37c2a472c002b5217248b3` | MIT (`licenses/samber-cc-skills-golang.LICENSE`) | Copyright (c) 2026 Samuel Berthe |
| Skills `vue`, `vitest` | `.claude/skills/<name>/` | https://github.com/antfu/skills | `2b26163650610e2bd32261bef68b4093312cc6ff` | MIT (`licenses/antfu-skills.LICENSE`) | Copyright (c) 2025-PRESENT Anthony Fu |
| Skill `verification-before-completion` | `.claude/skills/verification-before-completion/` | https://github.com/obra/superpowers | `22f0b463e7a5e579904dd6211a520321035526f7` | MIT (`licenses/obra-superpowers.LICENSE`) | Copyright (c) 2025 Jesse Vincent |
| Agent `silent-failure-hunter` | `.claude/agents/silent-failure-hunter.md` | https://github.com/anthropics/claude-plugins-official, `plugins/pr-review-toolkit/agents/silent-failure-hunter.md` | `dceb15d50832e6adf380bedc26dde03d251324b9` | Apache-2.0 (`licenses/claude-plugins-official-pr-review-toolkit.LICENSE`; the license file carries no copyright line) | Anthropic |
| Agent `repo-publication-auditor` | `.claude/agents/repo-publication-auditor.md` | https://github.com/davila7/claude-code-templates, `cli-tool/components/agents/security/repo-publication-auditor.md` (fetched with `claude-code-templates@1.33.1`) | `4036c908aa8884757551948cde3009ed898f6667` | MIT (`licenses/davila7-claude-code-templates.LICENSE`) | Copyright (c) 2025 Daniel (San) Avila |
| Skill `frontend-design` | `.claude/skills/frontend-design/` (`SKILL.md`, `LICENSE.txt`) | https://github.com/anthropics/skills, `skills/frontend-design` | `41bbe19d1a1a7eaab5e7bb9050a417e5c6cffc8f` | Apache-2.0 (`LICENSE.txt` in the skill folder) | Anthropic |
| Skill `ui-ux-pro-max` | `.claude/skills/ui-ux-pro-max/` (`SKILL.md`, `LICENSE`, `references/`, `scripts/` without `tests/`, `data/`) | https://github.com/nextlevelbuilder/ui-ux-pro-max-skill, `.claude/skills/ui-ux-pro-max/` plus the repo-root `LICENSE` | `6ae86f61f520d6ca229d2f40e070410638c908c5` | MIT (`LICENSE` in the skill folder) | Copyright (c) 2024 Next Level Builder |
| Plugin `gopls-lsp@claude-plugins-official` (project scope, enabled in `.claude/settings.json`) | Claude Code plugin cache | https://github.com/anthropics/claude-plugins-official, `plugins/gopls-lsp` | content identical to `dceb15d50832e6adf380bedc26dde03d251324b9` | Apache-2.0 | Anthropic |
| `gopls` v0.24.0 (not vendored; installed with `go install golang.org/x/tools/gopls@v0.24.0`) | `$GOPATH/bin` | https://github.com/golang/tools | tag `gopls/v0.24.0` | BSD-3-Clause | The Go Authors |

## Local modifications

- `golang-*` skills (samber): the `allowed-tools:` line was deleted from each `SKILL.md`, so `go` and `git`
  commands are no longer pre-approved. `evals/` directories were not copied.
- `vue`, `vitest` (antfu): only `SKILL.md` and `references/` were copied (no `GENERATION.md`).
- `silent-failure-hunter`: `tools: Read, Grep, Glob` was added to the frontmatter (read-only agent).
  Apache-2.0 section 4(b): this file was changed from the original.
- `repo-publication-auditor`: byte-identical to the pinned upstream file (SHA-256
  `16de9af0ad111b372add8dbf4114f551b0d47edcb1ecc05641ec06f42005d3e0`). Its upstream frontmatter grants
  `tools: Read, Grep, Glob, Bash`.
- `frontend-design`: byte-identical to the pinned upstream files (`SKILL.md`, `LICENSE.txt`); nothing else from that repo.
- `ui-ux-pro-max`: byte-identical to the pinned upstream folder except that `scripts/tests/` was not copied;
  the root `LICENSE` of the repo was added to the folder. Upstream `SKILL.md` is unmodified (it refers to
  `${CLAUDE_PLUGIN_ROOT}`, which does not exist for a project skill; the working command is in `CLAUDE.md`).
  The scripts run in a pinned `python:3.14.10-slim-trixie` container (no Python on the host). Line endings: LF.
- `gopls-lsp`: the marketplace copy was compared with the pinned commit and matches (line endings aside).
  The marketplace itself tracks the default branch, so the plugin version is not pinned by Claude Code.

## Not installed

See `docs/ai-tooling.md` (including the other skills, `cli/` and the `uipro-cli` installer of the ui-ux-pro-max repo).
