'use strict';
// Run: node --test ".claude/hooks/*.test.cjs"
const { test } = require('node:test');
const assert = require('node:assert');
const { spawnSync } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

const HOOK = path.join(__dirname, 'gofmt.cjs');
const HAS_GOFMT = !spawnSync('gofmt', ['-h'], { encoding: 'utf8' }).error;

const UNFORMATTED = 'package x\nfunc  F( ){ }\n';
const FORMATTED = 'package x\n\nfunc F() {}\n';

// Each test gets its own throwaway project folder.
function withProject(fn) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'gofmt-hook-'));
  try {
    return fn(dir);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

function run(project, input, env = {}) {
  const r = spawnSync(process.execPath, [HOOK], {
    input: typeof input === 'string' ? input : JSON.stringify(input),
    env: { ...process.env, CLAUDE_PROJECT_DIR: project, ...env },
    encoding: 'utf8',
  });
  return { code: r.status, err: r.stderr };
}

const edit = (project, file) => ({ tool_name: 'Edit', tool_input: { file_path: file }, cwd: project });

// PATH with no gofmt in it. On Windows the variable may be spelled "Path".
function envWithoutGo(emptyDir) {
  const key = Object.keys(process.env).find((k) => k.toLowerCase() === 'path') || 'PATH';
  return { [key]: emptyDir };
}

test('formats an edited .go file', { skip: !HAS_GOFMT }, () => {
  withProject((dir) => {
    const f = path.join(dir, 'a.go');
    fs.writeFileSync(f, UNFORMATTED);
    const r = run(dir, edit(dir, f));
    assert.strictEqual(r.code, 0);
    assert.strictEqual(fs.readFileSync(f, 'utf8'), FORMATTED);
  });
});

test('resolves a relative path against cwd', { skip: !HAS_GOFMT }, () => {
  withProject((dir) => {
    fs.mkdirSync(path.join(dir, 'pkg'));
    fs.writeFileSync(path.join(dir, 'pkg', 'a.go'), UNFORMATTED);
    const r = run(dir, edit(dir, path.join('pkg', 'a.go')));
    assert.strictEqual(r.code, 0);
    assert.strictEqual(fs.readFileSync(path.join(dir, 'pkg', 'a.go'), 'utf8'), FORMATTED);
  });
});

test('leaves non-Go files alone', () => {
  withProject((dir) => {
    const f = path.join(dir, 'notes.txt');
    fs.writeFileSync(f, UNFORMATTED);
    assert.strictEqual(run(dir, edit(dir, f)).code, 0);
    assert.strictEqual(fs.readFileSync(f, 'utf8'), UNFORMATTED);
  });
});

test('leaves Go files outside the project alone', { skip: !HAS_GOFMT }, () => {
  withProject((project) =>
    withProject((other) => {
      const f = path.join(other, 'a.go');
      fs.writeFileSync(f, UNFORMATTED);
      assert.strictEqual(run(project, edit(project, f)).code, 0);
      assert.strictEqual(fs.readFileSync(f, 'utf8'), UNFORMATTED);
    }),
  );
});

test('stays silent when gofmt is not installed', () => {
  withProject((dir) =>
    withProject((empty) => {
      const f = path.join(dir, 'a.go');
      fs.writeFileSync(f, UNFORMATTED);
      const r = run(dir, edit(dir, f), envWithoutGo(empty));
      assert.strictEqual(r.code, 0);
      assert.strictEqual(r.err, '');
      assert.strictEqual(fs.readFileSync(f, 'utf8'), UNFORMATTED);
    }),
  );
});

test('reports a syntax error without absolute paths', { skip: !HAS_GOFMT }, () => {
  withProject((dir) => {
    const f = path.join(dir, 'broken.go');
    fs.writeFileSync(f, 'package x\nfunc (\n');
    const r = run(dir, edit(dir, f));
    assert.strictEqual(r.code, 2);
    assert.match(r.err, /broken\.go:/);
    assert.ok(!r.err.includes(dir), 'stderr must not contain the folder path');
  });
});

test('ignores a missing file, empty input and bad JSON', () => {
  withProject((dir) => {
    assert.strictEqual(run(dir, edit(dir, path.join(dir, 'gone.go'))).code, 0);
    assert.strictEqual(run(dir, {}).code, 0);
    assert.strictEqual(run(dir, 'not json').code, 0);
  });
});
