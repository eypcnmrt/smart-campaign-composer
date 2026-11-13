'use strict';
// Tests for scripts/check-ai-sessions.cjs: run the scanner on a temporary directory and
// look at its exit code and (masked) report.
const test = require('node:test');
const assert = require('node:assert');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawnSync } = require('child_process');

const SCRIPT = path.join(__dirname, 'check-ai-sessions.cjs');
const BS = '\\';

function scan(text) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'scan-'));
  try {
    fs.writeFileSync(path.join(dir, 'session.txt'), text);
    const r = spawnSync(process.execPath, [SCRIPT, dir], { encoding: 'utf8' });
    return { code: r.status, out: r.stdout + r.stderr };
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

const homePath = (user) => `C:${BS}Users${BS}${user}`;

test('a path of an unknown user is a finding and is reported masked', () => {
  const r = scan(`opened ${homePath('someone')} today\n`);
  assert.strictEqual(r.code, 1);
  assert.match(r.out, /home-path/);
  assert.ok(!r.out.includes('someone'), 'the raw user name must not be printed');
});

test('the scanner masked output is not a finding', () => {
  // what the scanner prints for a path, quoted inside a session text
  const r = scan('the remaining finding is the scanner output (…/Users/E***) itself\n');
  assert.strictEqual(r.code, 0, r.out);
});

test('the allowed user is ignored, even when sentence punctuation sticks to it', () => {
  for (const tail of ['', ',', '.', ').', ';']) {
    const r = scan(`see ${homePath('EYUP')}${tail} for details\n`);
    assert.strictEqual(r.code, 0, `tail ${JSON.stringify(tail)}: ${r.out}`);
  }
});

test('placeholder users followed by punctuation are ignored', () => {
  const r = scan(`example ${homePath('X')}, and ${homePath('name')}.\n`);
  assert.strictEqual(r.code, 0, r.out);
});

test('punctuation does not hide a real user name', () => {
  const r = scan(`example ${homePath('someone')}, and more\n`);
  assert.strictEqual(r.code, 1);
});

test('the allowed noreply address and its cut-off copy are ignored', () => {
  const full = '79401693+eypcnmrt@users.noreply.github.com';
  assert.strictEqual(scan(`author ${full}\n`).code, 0);
  assert.strictEqual(scan(`author ${full.slice(0, -17)}\n`).code, 0);
});

test('other addresses are findings, also when they share the start of the allowed one', () => {
  assert.strictEqual(scan('mail person@example.org\n').code, 1);
  assert.strictEqual(scan('mail 79401693+other@users.noreply.github.com\n').code, 1);
});

test('keys and phone numbers are findings', () => {
  assert.strictEqual(scan(`key sk-ant-${'a'.repeat(30)}\n`).code, 1);
  assert.strictEqual(scan('call +90 555 123 45 67\n').code, 1);
});

test('a clean text exits 0', () => {
  assert.strictEqual(scan('nothing to see here\n').code, 0);
});
