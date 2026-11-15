# Claude Code sessions

This folder holds the Claude Code sessions that built the project, one text file per session, in the order they happened. The
text of the sessions is mostly Turkish (the owner's instructions) with English code, commands and tool output.

Planning and prompt drafting were also discussed in a separate chat outside these sessions. It is not included because it also contains unrelated personal information. Every instruction that reached the coding sessions is in these exports, as typed.

## The exports

| File | Topic | Added to the repository | Model in the header |
|---|---|---|---|
| [`01-ortam-kurulumu.txt`](01-ortam-kurulumu.txt) | checking and installing the tools on the Windows machine (Node.js, Go, make, Docker Desktop; publisher and checksum shown before each install) | 2026-11-11 | Sonnet 5.5 |
| [`02-gereksinim-analizi.txt`](02-gereksinim-analizi.txt) | analysing the case requirements and writing the plan (`docs/PLAN.md`) | 2026-11-11 | Opus 5.5 |
| [`03-guvenlik-temeli.txt`](03-guvenlik-temeli.txt) | the security foundation: guard hook, gitleaks pre-commit, session scanner | 2026-11-11 | Sonnet 5.5 |
| [`04-arac-kurulumu.txt`](04-arac-kurulumu.txt) | installing the Go, Vue and review skills and agents (quarantine, scan, pin) | 2026-11-13 | Sonnet 5.5 |
| [`05-segment-dsl.txt`](05-segment-dsl.txt) | the segment rule language: validation, parameterized SQL, JSON schema | 2026-11-13 | Opus 5.5 |
| [`06-llm-katmani.txt`](06-llm-katmani.txt) | the LLM layer: client, streaming extractor, repair loop, prompts | 2026-11-14 | Opus 5.5 |
| [`07-kuyruk-worker.txt`](07-kuyruk-worker.txt) | database, seed, mock provider, queue and worker | 2026-11-14 | Sonnet 5.5 |
| [`08-kampanya-api.txt`](08-kampanya-api.txt) | the campaign state machine, approval and the HTTP API | 2026-11-14 | Opus 5.5 |
| [`09-tasarim-skilleri.txt`](09-tasarim-skilleri.txt) | installing the two design skills | 2026-11-14 | Sonnet 5.5 |
| [`10-frontend.txt`](10-frontend.txt) | the web app, from the design direction to the delivery dashboard | 2026-11-14 | Opus 5.5 |
| [`11-dogrulama-utf8.txt`](11-dogrulama-utf8.txt) | verification and the UTF-8 problem found with a Windows shell | 2026-11-14 | Sonnet 5.5 |
| [`12-gece-frontend.txt`](12-gece-frontend.txt) | the unattended overnight round on the web app and the browser tests | 2026-11-14 | Sonnet 5.5 |
| [`13-acik-konular.txt`](13-acik-konular.txt) | the closing session: open issues, the demo mock, the screenshots | 2026-11-14 | Sonnet 5.5 |
| [`14-devir-testi.txt`](14-devir-testi.txt) | the flaky worker hand-over test (101 requests in one second at the hand-over: root cause and fix), a flaky late-reply test, and a stray `backend;C` folder made by Git Bash | 2026-11-15 | Sonnet 5.5 |
| [`15-gercek-api-eval.txt`](15-gercek-api-eval.txt) | the first use of the real API: smoke test and the golden-set eval | 2026-11-15 | Opus 5.5 |
| [`16-eval-kapanis.txt`](16-eval-kapanis.txt) | closing the eval: repair reasons, both models on the final prompt | 2026-11-15 | Sonnet 5.5 |
| [`17-sonnet-gecis.txt`](17-sonnet-gecis.txt) | the move to Sonnet as the default model | 2026-11-15 | Sonnet 5.5 |
| [`18-dolgu-kosul.txt`](18-dolgu-kosul.txt) | the filler condition: a rule without a condition means every user | 2026-11-15 | Sonnet 5.5 |
| [`19-olmayan-kriter.txt`](19-olmayan-kriter.txt) | the criterion the rule cannot express must not replace one it can | 2026-11-15 | Sonnet 5.5 |

The date is the day the file was added to the repository (`git log`), and the model is the one named in the header of the export,
that is the model the session started with; the project settings use a plan-then-execute model setting, so a session can have used
the other one for part of its work. The decisions that came out of these sessions are in [`../DECISIONS.md`](../DECISIONS.md).

## How the exports were made

Each export is the output of Claude Code's `/export` command, taken at the end of the session. An export holds the whole
conversation: after the context of a long session was compacted automatically, the export still contains the full conversation
from the start, not only the summary that the model continued from. The files are not edited by hand except for the masking below.

## How they were checked, and what is masked

Before an export is committed, `node scripts/check-ai-sessions.cjs` scans the folder (the script and its tests are in the
repository; commits are scanned by gitleaks as well). It looks for:

| Kind | What it matches | How it is reported or masked |
|---|---|---|
| API keys | `sk-ant-…` and other vendor key shapes, key-looking values after `api_key`, `token`, `secret`, `password`, private key blocks, JWTs | the first four characters and the length, as `sk-a…[N chars]` |
| E-mail addresses | anything shaped like an address (a local part, an at sign, a domain with a dot) | `a***@e***.com` |
| Phone numbers | international (`+CC …`) and Turkish national formats | `+9000000****` in the files; the report says only the number of digits |
| Home paths | `C:\Users\NAME`, `/c/Users/NAME`, `/home/NAME`, … | the report masks the name as `…/Users/E***` |

The scanner prints only a masked value and its place (`file:line`), never the line, so running it does not copy a secret into
another transcript. It exits with 1 when it finds something. Two values are allowed on purpose: the GitHub no-reply address that
is the author of every commit of the repository, and the home-folder name of the machine, which appears in tool output of
almost every session and in every path; reporting them would only bury a real finding.

What was masked in the committed files: the fake seed data that looked like contact data (the `+90000…` numbers and `example.com`
addresses of exports 08 and 09, which are fake but trip the phone and e-mail rules) and a name of an environment variable that the
key rule took for a value (`ANTH…[17 chars]` in the same exports). All the other exports were clean when scanned. No key-shaped
string was found in any export: the key sits in the dotenv file only, and the guard hook stops a session from reading that file.
Exports 15 to 19 were written after the real key was in use, and the tracked files were also checked by count (`sk-ant-` followed
by 20 characters: 0 matches).

A scan is a lexical check, not a proof: a secret in an unusual shape would pass, which is why the key never entered a session in the
first place.
