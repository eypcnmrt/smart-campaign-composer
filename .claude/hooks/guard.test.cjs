'use strict';
// Run: node --test .claude/hooks/
const { test } = require('node:test');
const assert = require('node:assert');
const { spawnSync } = require('node:child_process');
const path = require('node:path');

const GUARD = path.join(__dirname, 'guard.cjs');
const PROJECT = path.resolve(__dirname, '..', '..');

function run(tool_name, tool_input) {
  const r = spawnSync('node', [GUARD], {
    input: JSON.stringify({ tool_name, tool_input, cwd: PROJECT }),
    env: { ...process.env, CLAUDE_PROJECT_DIR: PROJECT },
    encoding: 'utf8',
  });
  return { code: r.status, err: r.stderr };
}

const sh = (command) => run('Bash', { command });

const BLOCKED = [
  ['Read', { file_path: path.join(PROJECT, '.env') }],
  ['Read', { file_path: '.env.local' }],
  ['Read', { file_path: 'sub/.env.production' }],
  ['Read', { file_path: '.env::$DATA' }],
  ['Grep', { pattern: 'KEY', path: '.env' }],
  ['Grep', { pattern: 'KEY', glob: '.env*' }],
  ['Grep', { pattern: 'KEY', glob: '**/*.env' }],
  ['Bash', { command: 'cat .env' }],
  ['Bash', { command: 'cat ./.env.local' }],
  ['Bash', { command: 'type "C:\\proj\\.env"' }],
  ['Bash', { command: 'cat .en""v' }],
  ['Bash', { command: 'cat .e*' }],
  ['Bash', { command: 'cat .env{,.bak}' }],
  ['Bash', { command: 'grep KEY .env' }],
  ['Bash', { command: 'source .env && echo hi' }],
  ['Bash', { command: 'echo hi; head -n1 .env' }],
  ['Bash', { command: 'bash -c "cat .env"' }],
  ['Bash', { command: 'echo $(cat .env)' }],
  ['PowerShell', { command: 'Get-Content .env' }],
  ['PowerShell', { command: 'gc .\\.env.production' }],
  ['PowerShell', { command: 'Select-String KEY -Path .env' }],
  // environment dumps
  ['Bash', { command: 'env' }],
  ['Bash', { command: 'env | sort' }],
  ['Bash', { command: 'printenv' }],
  ['Bash', { command: 'printenv ANTHROPIC_API_KEY' }],
  ['Bash', { command: 'cd x && set' }],
  ['Bash', { command: 'set | grep KEY' }],
  ['Bash', { command: 'export -p' }],
  ['Bash', { command: 'declare -x' }],
  ['Bash', { command: 'echo $ANTHROPIC_API_KEY' }],
  ['Bash', { command: 'echo "${GITHUB_TOKEN}"' }],
  ['Bash', { command: 'cat /proc/self/environ' }],
  ['Bash', { command: 'docker compose config' }],
  ['Bash', { command: 'docker-compose -f a.yml config' }],
  ['Bash', { command: 'docker inspect api' }],
  ['Bash', { command: 'docker exec api env' }],
  ['Bash', { command: 'node -e "console.log(process.env)"' }],
  ['Bash', { command: 'python -c "import os; print(os.environ)"' }],
  ['PowerShell', { command: 'Get-ChildItem env:' }],
  ['PowerShell', { command: 'gci Env:\\' }],
  ['PowerShell', { command: 'dir env:ANTHROPIC_API_KEY' }],
  ['PowerShell', { command: '$env:ANTHROPIC_API_KEY' }],
  ['PowerShell', { command: 'Write-Host $env:MY_SECRET' }],
  ['PowerShell', { command: '[Environment]::GetEnvironmentVariables()' }],
  ['PowerShell', { command: 'cmd /c set' }],
  ['PowerShell', { command: 'powershell -Command "Get-ChildItem env:"' }],
  [
    'PowerShell',
    { command: 'powershell -EncodedCommand ' + Buffer.from('gci env:', 'utf16le').toString('base64') },
  ],
  // force push and hook bypass
  ['Bash', { command: 'git push --force' }],
  ['Bash', { command: 'git push -f origin main' }],
  ['Bash', { command: 'git push -fu origin main' }],
  ['Bash', { command: 'git push --force-with-lease' }],
  ['Bash', { command: 'git push origin +main' }],
  ['Bash', { command: 'git -C . push --force' }],
  ['Bash', { command: 'git commit -m x --no-verify' }],
  ['Bash', { command: 'git commit -nm x' }],
  ['Bash', { command: 'git -c core.hooksPath=/dev/null commit -m x' }],
  ['Bash', { command: 'git config core.hooksPath /dev/null' }],
  // deletion outside the project
  ['Bash', { command: 'rm -rf /' }],
  ['Bash', { command: 'rm -rf ..' }],
  ['Bash', { command: 'rm -rf ../other' }],
  ['Bash', { command: 'rm -rf ~/x' }],
  ['Bash', { command: 'rm -rf $HOME/x' }],
  ['Bash', { command: 'rm -rf /c/Users' }],
  ['Bash', { command: 'rm -rf .' }],
  ['Bash', { command: 'cd .. && rm -rf proj' }],
  ['Bash', { command: 'ls | xargs rm' }],
  ['Bash', { command: 'find / -name x -delete' }],
  ['Bash', { command: 'find .. -type f -exec rm {} \\;' }],
  ['Bash', { command: 'bash -c "rm -rf /tmp/x"' }],
  ['Bash', { command: 'node -e "require(\'fs\').rmSync(\'/x\',{recursive:true})"' }],
  ['PowerShell', { command: 'Remove-Item -Recurse -Force C:\\Windows\\Temp\\x' }],
  ['PowerShell', { command: 'Remove-Item -Path ..\\x -Recurse' }],
  ['PowerShell', { command: 'Remove-Item -LiteralPath:C:\\x' }],
  ['PowerShell', { command: 'ri $env:TEMP\\x' }],
  ['PowerShell', { command: 'Get-ChildItem | Remove-Item' }],
  ['PowerShell', { command: 'cmd /c rmdir /s /q C:\\x' }],
  ['PowerShell', { command: 'del C:\\x\\y.txt' }],
];

const ALLOWED = [
  ['Read', { file_path: path.join(PROJECT, '.env.example') }],
  ['Read', { file_path: 'docs/PLAN.md' }],
  ['Grep', { pattern: 'foo', path: 'src', glob: '*.{ts,tsx}' }],
  ['Grep', { pattern: 'foo', glob: '**/*.go' }],
  ['Bash', { command: 'cat .env.example' }],
  ['Bash', { command: 'git status --short' }],
  ['Bash', { command: 'git commit -m "add feature"' }],
  ['Bash', { command: 'git push -u origin main' }],
  ['Bash', { command: 'git push --follow-tags' }],
  ['Bash', { command: 'git log --format=%H -n 5' }],
  ['Bash', { command: 'env FOO=1 node app.js' }],
  ['Bash', { command: 'set -euo pipefail' }],
  ['Bash', { command: 'node -e "console.log(process.env.NODE_ENV)"' }],
  ['Bash', { command: 'echo $HOME' }],
  ['Bash', { command: 'docker compose up -d' }],
  ['Bash', { command: 'docker compose -f config/compose.yml up' }],
  ['Bash', { command: 'docker run --env-file x.list alpine true' }],
  ['Bash', { command: 'rm -rf node_modules' }],
  ['Bash', { command: 'rm -f dist/out.txt build/a.o' }],
  ['Bash', { command: 'rm -rf ./tmp-test-dir' }],
  ['Bash', { command: 'cd docs && rm old.md' }],
  ['Bash', { command: 'find . -name "*.o" -delete' }],
  ['Bash', { command: 'find src -type f -exec rm {} \\;' }],
  ['Bash', { command: 'grep -rn "process.env" src' }],
  ['Bash', { command: 'node scripts/check-ai-sessions.js' }],
  ['PowerShell', { command: 'Get-ChildItem -Recurse' }],
  ['PowerShell', { command: 'Remove-Item -Recurse -Force .\\dist' }],
  ['PowerShell', { command: 'Remove-Item -Path dist\\a.txt' }],
  ['PowerShell', { command: 'Write-Host $env:Path' }],
  ['PowerShell', { command: 'Get-Content docs\\PLAN.md' }],
];

// A drive-letter path (C:\x) is only an absolute path on Windows. On Linux (the test container) it is
// an ordinary relative name, so those cases cannot be blocked there and are only run on Windows.
const windowsDrivePath = (input) => /(^|[\s:=])[A-Za-z]:\\/.test(String(input.command ?? ''));
const skipHere = (input) => process.platform !== 'win32' && windowsDrivePath(input) && 'Windows drive paths are only resolved on Windows';

BLOCKED.forEach(([tool, input]) => {
  test(`blocks ${tool}: ${JSON.stringify(input)}`, { skip: skipHere(input) }, () => {
    const r = run(tool, input);
    assert.strictEqual(r.code, 2, `expected block, got ${r.code}`);
    assert.match(r.err, /^Blocked by guard: [^\n]+\n$/, 'message must be a single line');
    // the matched value must never be echoed
    for (const v of Object.values(input)) {
      // short inputs such as "env" are substrings of the static message by chance
      if (String(v).length > 8) assert.ok(!r.err.includes(String(v)), 'message must not echo the input');
    }
  });
});

ALLOWED.forEach(([tool, input]) => {
  test(`allows ${tool}: ${JSON.stringify(input)}`, () => {
    const r = run(tool, input);
    assert.strictEqual(r.code, 0, `expected allow, got ${r.code}: ${r.err}`);
  });
});

test('invalid JSON fails closed', () => {
  const r = spawnSync('node', [GUARD], { input: 'not json', encoding: 'utf8' });
  assert.strictEqual(r.status, 2);
});

test('unrelated tools pass', () => {
  assert.strictEqual(run('Edit', { file_path: 'x' }).code, 0);
});
