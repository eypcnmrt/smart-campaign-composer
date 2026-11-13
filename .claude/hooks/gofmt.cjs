'use strict';
// PostToolUse hook for Edit, Write and MultiEdit: runs `gofmt -w` on the edited
// .go file so formatting never needs a manual pass.
//
// Best effort by design (unlike guard.cjs, which fails closed): if the call is
// not about a .go file inside the project, gofmt is not installed, or anything
// unexpected happens, the hook stays silent and exits 0. Only a gofmt syntax
// error is surfaced (exit 2, gofmt's message on stderr) so the model sees it.
// gofmt runs from the file's folder with the bare file name, so its messages
// never contain absolute paths.

const fs = require('fs');
const path = require('path');
const { spawnSync } = require('child_process');

const PROJECT = path.resolve(process.env.CLAUDE_PROJECT_DIR || path.join(__dirname, '..', '..'));

function inside(root, file) {
  let a = file;
  let b = root;
  if (process.platform === 'win32') {
    a = a.toLowerCase();
    b = b.toLowerCase();
  }
  const rel = path.relative(b, a);
  return rel !== '' && !rel.startsWith('..') && !path.isAbsolute(rel);
}

function run(input) {
  const file = input && input.tool_input && input.tool_input.file_path;
  if (typeof file !== 'string' || !file.endsWith('.go')) return 0;

  const abs = path.resolve(input.cwd || process.cwd(), file);
  if (!inside(PROJECT, abs) || !fs.existsSync(abs)) return 0;

  const r = spawnSync('gofmt', ['-w', path.basename(abs)], { cwd: path.dirname(abs), encoding: 'utf8' });
  if (r.error) return 0; // gofmt missing or not runnable: skip silently
  if (r.status !== 0) {
    process.stderr.write(`gofmt: ${(r.stderr || '').trim()}\n`);
    return 2;
  }
  return 0;
}

let raw = '';
process.stdin.setEncoding('utf8');
process.stdin.on('data', (d) => (raw += d));
process.stdin.on('end', () => {
  let code = 0;
  try {
    code = run(JSON.parse(raw));
  } catch {
    code = 0;
  }
  process.exit(code);
});
