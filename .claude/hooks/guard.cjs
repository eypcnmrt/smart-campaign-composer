'use strict';
// PreToolUse guard for Read, Grep, Bash and PowerShell calls.
// Hooks run even in bypass-permissions mode, so this is the real protection layer.
//
// Contract: reads the hook JSON from stdin; exit 0 allows, exit 2 blocks and
// stderr (one static line) is shown to the model. The matched value is NEVER
// printed. Any internal error blocks (fail closed).
//
// This is a best-effort lexical check, not a sandbox: it covers direct and
// common indirect forms (quotes, wrappers, substitutions, encoded commands),
// not arbitrary programs that read files or delete paths on their own.

const fs = require('fs');
const os = require('os');
const path = require('path');

const PROJECT_REAL = realish(
  path.resolve(process.env.CLAUDE_PROJECT_DIR || path.join(__dirname, '..', '..'))
);

const R = {
  envFile: 'reading dotenv files is blocked (secrets must not enter the session transcript)',
  envDump: 'dumping environment variables is blocked (secrets must not enter the session transcript)',
  force: 'git force push is blocked',
  hooks: 'bypassing git hooks is blocked',
  delOutside: 'deleting outside the project folder is blocked',
  unverifiable: 'command could not be verified as safe (deleting or nesting too opaque), run it manually if intended',
  failure: 'guard could not evaluate this call, blocked to be safe',
};

function deny(reason) {
  process.stderr.write(`Blocked by guard: ${reason}\n`);
  process.exit(2);
}

// ---------- path helpers ----------

function realish(p) {
  let cur = p;
  const rest = [];
  for (;;) {
    try {
      const r = fs.realpathSync.native(cur);
      return rest.length ? path.join(r, ...rest.reverse()) : r;
    } catch {
      const parent = path.dirname(cur);
      if (parent === cur) return p;
      rest.push(path.basename(cur));
      cur = parent;
    }
  }
}

function fromPosixDrive(p) {
  // Git Bash / Cygwin style /c/Users/x -> c:/Users/x
  return process.platform === 'win32' ? p.replace(/^\/(?:cygdrive\/)?([a-zA-Z])(?=\/|$)/, '$1:') : p;
}

function expandTilde(p) {
  return p === '~' || p.startsWith('~/') || p.startsWith('~\\') ? path.join(os.homedir(), p.slice(1)) : p;
}

// allowRoot: the project folder itself counts as inside (used for find roots only;
// deleting the project folder itself is never allowed).
function insideProject(abs, allowRoot) {
  let a = realish(abs);
  let b = PROJECT_REAL;
  if (process.platform === 'win32') {
    a = a.toLowerCase();
    b = b.toLowerCase();
  }
  const rel = path.relative(b, a);
  if (rel === '') return Boolean(allowRoot);
  return !rel.startsWith('..') && !path.isAbsolute(rel);
}

// ---------- dotenv file detection ----------

function isEnvFileName(base) {
  const b = base.replace(/::\$data$/i, '').replace(/[. ]+$/, '').toLowerCase();
  if (b === '.env.example') return false;
  return /^\.env(\..*)?$/.test(b);
}

function isEnvPath(p) {
  return typeof p === 'string' && isEnvFileName(p.split(/[\\/]/).pop());
}

function expandBraces(p) {
  const m = /\{([^{}]*)\}/.exec(p);
  if (!m) return [p];
  return m[1]
    .split(',')
    .flatMap((alt) => expandBraces(p.slice(0, m.index) + alt + p.slice(m.index + m[0].length)));
}

function globToRegex(g) {
  let re = '';
  for (let i = 0; i < g.length; i++) {
    const c = g[i];
    if (c === '*') re += '[^/\\\\]*';
    else if (c === '?') re += '[^/\\\\]';
    else if (c === '[') {
      const j = g.indexOf(']', i + 1);
      if (j > i) {
        re += '[' + g.slice(i + 1, j).replace(/^!/, '^').replace(/\\/g, '\\\\') + ']';
        i = j;
      } else re += '\\[';
    } else re += c.replace(/[.+^${}()|\\]/g, '\\$&');
  }
  return new RegExp('^' + re + '$', 'i');
}

const ENV_SAMPLES = ['.env', '.env.local', '.env.production', '.env.development'];

// requireDot: shell tokens must start with "." to count; Grep globs may name "env" anywhere.
function globTargetsEnv(glob, requireDot) {
  const base = glob.split(/[\\/]/).pop();
  if (!/[*?[{]/.test(base) && !/env/i.test(base)) return false;
  for (const alt of expandBraces(base)) {
    if (requireDot ? !alt.startsWith('.') : !/env/i.test(alt) && !alt.startsWith('.')) continue;
    let re;
    try {
      re = globToRegex(alt);
    } catch {
      return true;
    }
    if (ENV_SAMPLES.some((s) => re.test(s))) return true;
  }
  return false;
}

const ENV_REF =
  /(?:^|[\s'"`=\/\\:<>(),;|&@])(\.env(?:\.[A-Za-z0-9_.\-]*)?)(?=$|[\s'"`;|&)<>,:*?\]}$])/gi;

function commandRefsEnvFile(cmd) {
  for (const variant of [cmd, cmd.replace(/["'`]/g, '')]) {
    for (const m of variant.matchAll(ENV_REF)) {
      if (m[1].toLowerCase().replace(/\.+$/, '') !== '.env.example') return true;
    }
    for (const tok of variant.split(/\s+/)) {
      if (/[*?[{]/.test(tok) && globTargetsEnv(tok, true)) return true;
    }
  }
  return false;
}

// ---------- environment dumping ----------

const ENV_DUMP_RES = [
  /\[\s*(?:system\.)?environment\s*\]\s*::\s*getenvironmentvariables/i,
  /getenvironmentvariable\s*\([^)]*(?:key|token|secret|passw|credential|auth)/i,
  /\$\{?[A-Z0-9_]*(?:API_?KEY|SECRET|TOKEN|PASSWORD|PASSWD|CREDENTIAL|PRIVATE_?KEY)[A-Z0-9_]*\}?/,
  /\$\{?env:[A-Za-z0-9_]*(?:key|secret|token|passw|credential|auth)/i,
  /%[A-Za-z0-9_]*(?:key|secret|token|passw|credential)[A-Za-z0-9_]*%/i,
  /\/proc\/[^\s]*\/environ/i,
  /\breg(?:\.exe)?\s+query\s+[^\n]*\\environment/i,
];

const INLINE_CODE_RUNNERS = new Set(['node', 'nodejs', 'deno', 'bun', 'python', 'python3', 'py', 'ruby', 'perl', 'php']);
const INLINE_FLAG = /^(?:-[a-z]*[ecp]|--eval|--print|--command)$/i;
const INLINE_ENV_RES = [
  /process\.env\b(?!\s*\.\s*[A-Za-z_]|\s*\[)/,
  /process\.env\s*(?:\.\s*|\[\s*["'])[A-Za-z0-9_]*(?:key|secret|token|passw|credential)/i,
  /os\.environ\b(?!\s*\[|\.get\b)/,
  /os\.(?:environ|getenv)[^\n]*(?:key|secret|token|passw|credential)/i,
];
const INLINE_DELETE_RE =
  /rmSync|rmdirSync|unlinkSync|rimraf|\bfs\.rm\b|\.rm\(|rmtree|os\.(?:remove|unlink|rmdir)|removedirs|Remove-Item/;

// ---------- tokenising ----------

function tokenize(s) {
  const out = [];
  let cur = '';
  let q = null;
  let has = false;
  for (const ch of s) {
    if (q) {
      if (ch === q) q = null;
      else cur += ch;
      continue;
    }
    if (ch === '"' || ch === "'") {
      q = ch;
      has = true;
      continue;
    }
    if (/\s/.test(ch)) {
      if (cur || has) out.push(cur);
      cur = '';
      has = false;
      continue;
    }
    cur += ch;
  }
  if (cur || has) out.push(cur);
  return out;
}

function splitSegments(cmd) {
  const segs = [];
  let cur = '';
  let q = null;
  for (const ch of cmd) {
    if (q) {
      cur += ch;
      if (ch === q) q = null;
      continue;
    }
    if (ch === '"' || ch === "'") {
      q = ch;
      cur += ch;
      continue;
    }
    if (';&|\n\r(){}`'.includes(ch)) {
      segs.push(cur);
      cur = '';
      continue;
    }
    cur += ch;
  }
  segs.push(cur);
  return segs.map((s) => s.trim()).filter(Boolean);
}

// Remove heredoc bodies so apostrophes in prose cannot hide later commands.
// Bodies fed to a shell interpreter are returned for analysis.
function stripHeredocs(cmd) {
  const lines = cmd.split(/\r?\n/);
  const kept = [];
  const shellBodies = [];
  for (let i = 0; i < lines.length; i++) {
    kept.push(lines[i]);
    const m = /<<-?\s*(['"]?)([A-Za-z_][\w-]*)\1/.exec(lines[i]);
    if (!m) continue;
    const body = [];
    let j = i + 1;
    while (j < lines.length && lines[j].trim() !== m[2]) body.push(lines[j++]);
    if (/\b(?:ba|z|da|k)?sh\b|\bpwsh\b|\bpowershell\b/i.test(lines[i])) shellBodies.push(body.join('\n'));
    i = j;
  }
  return { text: kept.join('\n'), shellBodies };
}

// ---------- git ----------

function gitCheck(tokens) {
  let i = 1;
  while (i < tokens.length && tokens[i].startsWith('-')) {
    const o = tokens[i];
    if (o === '-c') {
      if (/^core\.hookspath/i.test(tokens[i + 1] || '')) return R.hooks;
      i += 2;
    } else if (['-C', '--git-dir', '--work-tree', '--namespace', '--exec-path', '--config-env'].includes(o)) i += 2;
    else i += 1;
  }
  const sub = (tokens[i] || '').toLowerCase();
  const rest = tokens.slice(i + 1);
  if (rest.includes('--no-verify')) return R.hooks;
  if (sub === 'config' && rest.some((t) => /^core\.hookspath$/i.test(t))) return R.hooks;
  if (sub === 'commit') {
    for (const t of rest) {
      if (/^-[A-Za-z]+$/.test(t)) {
        for (const ch of t.slice(1)) {
          if ('mFCctS'.includes(ch)) break;
          if (ch === 'n') return R.hooks;
        }
      }
    }
  }
  if (sub === 'push') {
    for (const t of rest) {
      if (/^--force(?:-with-lease|-if-includes)?(?:=.*)?$/.test(t) || t === '--mirror') return R.force;
      if (/^-[A-Za-z0-9]+$/.test(t)) {
        for (const ch of t.slice(1)) {
          if (ch === 'o') break;
          if (ch === 'f') return R.force;
        }
      }
      if (/^\+\S/.test(t)) return R.force;
    }
  }
  return null;
}

// ---------- deletion ----------

const DEL_UNIX = new Set(['rm', 'unlink', 'shred', 'rimraf', 'trash', 'trash-put']);
const DEL_WIN = new Set(['rmdir', 'del', 'erase', 'rd', 'ri', 'remove-item']);
const SLASH_FLAG = /^\/[A-Za-z](?::\S*)?$/;
const OPAQUE = /[$%`]/;

function deleteTargets(tokens, win) {
  const out = [];
  let afterDashes = false;
  for (let i = 1; i < tokens.length; i++) {
    const t = tokens[i];
    if (!afterDashes) {
      if (t === '--') {
        afterDashes = true;
        continue;
      }
      const m = /^-(?:path|literalpath)[:=](.+)$/i.exec(t);
      if (m) {
        out.push(...m[1].split(','));
        continue;
      }
      if (t.startsWith('-')) continue;
      if (win && SLASH_FLAG.test(t)) continue;
    }
    if (t) out.push(...(win ? t.split(',') : [t]));
  }
  return out;
}

function checkTarget(t, state, allowRoot) {
  if (OPAQUE.test(t)) return R.unverifiable;
  let p = fromPosixDrive(expandTilde(t));
  if (path.isAbsolute(p)) p = path.resolve(p);
  else if (!state.cwd) return R.unverifiable;
  else p = path.resolve(state.cwd, p);
  return insideProject(p, allowRoot) ? null : R.delOutside;
}

// ---------- command analysis ----------

const PREFIX_WORDS = new Set([
  'sudo', 'command', 'builtin', 'exec', 'time', 'nohup', 'xargs', 'nice', 'stdbuf',
  'then', 'do', 'else', 'elif', 'if', 'while', 'until', '!', 'call', 'start',
]);

function analyze(cmd, cwd, depth) {
  if (depth > 4) return R.unverifiable;
  if (commandRefsEnvFile(cmd)) return R.envFile;
  if (ENV_DUMP_RES.some((re) => re.test(cmd))) return R.envDump;

  for (const m of cmd.matchAll(/\$\(([^()]*)\)|`([^`]*)`/g)) {
    const r = analyze(m[1] !== undefined ? m[1] : m[2], cwd, depth + 1);
    if (r) return r;
  }

  const { text, shellBodies } = stripHeredocs(cmd);
  for (const body of shellBodies) {
    const r = analyze(body, cwd, depth + 1);
    if (r) return r;
  }

  const state = { cwd, findRoots: null };
  for (const seg of splitSegments(text)) {
    const r = analyzeSegment(seg, state, depth);
    if (r) return r;
  }
  return null;
}

function analyzeSegment(seg, state, depth) {
  const tokens = tokenize(seg);
  while (tokens.length) {
    if (/^[A-Za-z_]\w*=/.test(tokens[0])) {
      tokens.shift();
    } else if (PREFIX_WORDS.has(tokens[0].toLowerCase())) {
      tokens.shift();
      while (tokens.length && tokens[0].startsWith('-') && tokens[0] !== '-') tokens.shift();
    } else break;
  }
  if (!tokens.length) return null;

  const w = path
    .basename(tokens[0].replace(/\\/g, '/'))
    .toLowerCase()
    .replace(/\.(?:exe|cmd|bat|ps1)$/, '');
  const args = tokens.slice(1);

  // PowerShell provider drive "env:" (Get-ChildItem env:, ls Env:\X, ...)
  if (args.some((t) => /^env:/i.test(t))) return R.envDump;

  if (w === 'printenv') return R.envDump;
  if (w === 'env') {
    let i = 0;
    while (i < args.length) {
      const t = args[i];
      if (['-u', '-C', '-S'].includes(t)) i += 2;
      else if (t.startsWith('-') || /^[A-Za-z_]\w*=/.test(t)) i += 1;
      else break;
    }
    if (i >= args.length) return R.envDump;
  }
  if (w === 'set' && args.length === 0) return R.envDump;
  if (w === 'export' && (args.length === 0 || args[0] === '-p')) return R.envDump;
  if ((w === 'declare' || w === 'typeset') && args.every((t) => /^-[xp]+$/.test(t))) return R.envDump;

  if (['docker', 'docker-compose', 'podman', 'podman-compose'].includes(w)) {
    const t = args.map((x) => x.toLowerCase());
    const compose = w.endsWith('compose') || t.includes('compose');
    if (compose && t.includes('config')) return R.envDump;
    if (t.includes('inspect')) return R.envDump;
    if (
      (t.includes('exec') || t.includes('run')) &&
      t.some((x) => /(?:^|[\s;|&(])(?:env|printenv)(?:$|[\s;|&)])/.test(x))
    ) {
      return R.envDump;
    }
  }

  if (w === 'git') {
    const r = gitCheck(tokens);
    if (r) return r;
  }

  // wrappers that run another command line
  if (['bash', 'sh', 'zsh', 'dash', 'ksh', 'cmd', 'powershell', 'pwsh'].includes(w)) {
    for (let i = 0; i < args.length; i++) {
      const f = args[i].toLowerCase();
      if (/^-(?:e|ec|enc|encodedcommand)$/.test(f) && ['powershell', 'pwsh'].includes(w)) {
        const decoded = Buffer.from(args[i + 1] || '', 'base64').toString('utf16le');
        return analyze(decoded, state.cwd, depth + 1);
      }
      if (/^-[a-z]*c$/.test(f) || /^\/[ckr]$/.test(f) || f === '-command' || f === '-commandwithargs') {
        return analyze(args.slice(i + 1).join(' '), state.cwd, depth + 1);
      }
    }
  }
  if (['eval', 'iex', 'invoke-expression'].includes(w)) return analyze(args.join(' '), state.cwd, depth + 1);

  // inline interpreter code
  if (INLINE_CODE_RUNNERS.has(w) && args.some((t) => INLINE_FLAG.test(t))) {
    if (INLINE_ENV_RES.some((re) => re.test(seg))) return R.envDump;
    if (INLINE_DELETE_RE.test(seg)) return R.unverifiable;
  }

  // working directory tracking
  if (['cd', 'chdir', 'pushd', 'push-location', 'set-location', 'sl'].includes(w)) {
    const a = args.filter((t) => !(t.startsWith('-') && t !== '-') && !/^\/d$/i.test(t));
    if (a.length === 0) state.cwd = os.homedir();
    else if (a[0] === '-' || OPAQUE.test(a[0]) || (!path.isAbsolute(fromPosixDrive(expandTilde(a[0]))) && !state.cwd)) state.cwd = null;
    else state.cwd = path.resolve(state.cwd || '/', fromPosixDrive(expandTilde(a[0])));
    return null;
  }
  if (w === 'popd' || w === 'pop-location') {
    state.cwd = null;
    return null;
  }

  // deletion
  if (DEL_UNIX.has(w) || DEL_WIN.has(w)) {
    const targets = deleteTargets(tokens, DEL_WIN.has(w));
    if (targets.length === 0) return R.unverifiable;
    for (const t of targets) {
      const r = checkTarget(t, state);
      if (r) return r;
    }
    return null;
  }
  if (w === 'find') {
    const roots = [];
    for (const t of args) {
      if (t.startsWith('-') || t === '(' || t === '\\(' || t === '!') break;
      roots.push(t);
    }
    state.findRoots = roots.length ? roots : ['.'];
  }
  if (
    state.findRoots &&
    (/(?:^|\s)-delete(?:\s|$)/.test(seg) || /-exec(?:dir)?\s+\S*\b(?:rm|rmdir|unlink)\b/.test(seg))
  ) {
    for (const t of state.findRoots) {
      const r = checkTarget(t, state, true);
      if (r) return r;
    }
  }
  return null;
}

// ---------- entry point ----------

function evaluate(input) {
  const tool = input.tool_name;
  const ti = input.tool_input || {};
  const cwd = input.cwd || process.cwd();

  if (tool === 'Read') {
    return isEnvPath(ti.file_path) ? R.envFile : null;
  }
  if (tool === 'Grep') {
    if (isEnvPath(ti.path)) return R.envFile;
    if (typeof ti.glob === 'string') {
      for (const g of ti.glob.split(/\s+/).filter(Boolean)) {
        if (globTargetsEnv(g, false) && g.replace(/^.*[\\/]/, '').toLowerCase() !== '.env.example') return R.envFile;
      }
    }
    return null;
  }
  if (tool === 'Bash' || tool === 'PowerShell') {
    return typeof ti.command === 'string' ? analyze(ti.command, cwd, 0) : null;
  }
  return null;
}

let raw = '';
process.stdin.setEncoding('utf8');
process.stdin.on('data', (d) => (raw += d));
process.stdin.on('end', () => {
  let reason;
  try {
    reason = evaluate(JSON.parse(raw));
  } catch {
    reason = R.failure;
  }
  if (reason) deny(reason);
  process.exit(0);
});
