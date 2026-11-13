'use strict';
// Scans ai-sessions/ (exported Claude Code sessions) for API keys, e-mail
// addresses, phone numbers and home-directory paths before the repo goes public.
// Findings are reported MASKED (file:line, type, partial value); the raw value
// and the surrounding line are never printed.
//
// Usage: node scripts/check-ai-sessions.cjs [dir]      (default: ai-sessions)
// Exit code: 0 = clean, 1 = findings, 2 = usage/IO error.

const fs = require('fs');
const path = require('path');

const ROOT = path.resolve(__dirname, '..');
const dir = path.resolve(process.argv[2] || path.join(ROOT, 'ai-sessions'));

// Deliberately allowed. Both values appear in every commit of this public repo (the
// commit author address is the GitHub noreply address; the home directory shows up in
// tool output) and history will not be rewritten, so reporting them only adds noise.
// Anything else that looks like an e-mail address or a home path is still reported.
// Matching is case-insensitive and applies to the captured user segment, so every
// spelling the home-path rule recognises is covered: C:\Users\X, C:\\Users\\X (JSON
// escaped), C:/Users/X, /c/Users/X and /mnt/c/Users/X.
const ALLOWED_EMAILS = new Set(['79401693+eypcnmrt@users.noreply.github.com']);
const ALLOWED_HOME_USERS = new Set(['eyup']);

const SAFE_USERS = new Set(['public', 'default', 'all users', 'user', 'username', 'you', 'name', 'yourname', 'x']);

const maskSecret = (v) => `${v.slice(0, 4)}…[${v.length} chars]`;
const maskEmail = (v) => {
  const [local, domain] = v.split('@');
  const parts = domain.split('.');
  return `${local[0]}***@${parts[0][0]}***.${parts[parts.length - 1]}`;
};
const maskPhone = (v) => `[${v.replace(/\D/g, '').length} digits, ends ${v.replace(/\D/g, '').slice(-2)}]`;

// Each rule: type, regex (global), value picker, masker, optional filters (skip: drop a match
// in context, ok: drop a picked value).
const RULES = [
  { type: 'api-key', re: /sk-ant-[A-Za-z0-9_-]{20,}/g, mask: maskSecret },
  { type: 'api-key', re: /\bsk-(?:proj-)?[A-Za-z0-9_-]{20,}/g, mask: maskSecret },
  { type: 'api-key', re: /\b(?:AKIA|ASIA)[0-9A-Z]{16}\b/g, mask: maskSecret },
  { type: 'api-key', re: /\b(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{30,})/g, mask: maskSecret },
  { type: 'api-key', re: /\bxox[baprs]-[A-Za-z0-9-]{10,}/g, mask: maskSecret },
  { type: 'api-key', re: /\bAIza[0-9A-Za-z_-]{35}\b/g, mask: maskSecret },
  { type: 'api-key', re: /\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}/g, mask: maskSecret },
  { type: 'api-key', re: /-----BEGIN [A-Z ]*PRIVATE KEY-----/g, mask: () => '[private key block]' },
  {
    type: 'api-key',
    re: /(?:api[_-]?key|secret|token|passw(?:or)?d)["']?\s*[:=]\s*["']?([A-Za-z0-9/+_-]{16,})/gi,
    pick: (m) => m[1],
    mask: maskSecret,
  },
  {
    type: 'email',
    re: /[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}/g,
    // A cut-off copy of an allowed address (a diff or terminal line that was truncated) is not a new leak.
    ok: (v) => ![...ALLOWED_EMAILS].some((a) => a === v.toLowerCase() || (v.includes('@') && a.startsWith(v.toLowerCase()))),
    mask: maskEmail,
  },
  // international (+CC ...) and Turkish national formats, separators required to avoid bare digit runs
  {
    type: 'phone',
    re: /(?<![\w+])\+\d{1,3}[\s.-]?\(?\d{2,4}\)?[\s.-]?\d{3}[\s.-]?\d{2,4}(?:[\s.-]?\d{2,4})?(?!\d)/g,
    ok: (v) => /\d/.test(v) && v.replace(/\D/g, '').length >= 10 && v.replace(/\D/g, '').length <= 15,
    mask: maskPhone,
  },
  { type: 'phone', re: /(?<!\d)0\s?\(?5\d{2}\)?[\s.-]?\d{3}[\s.-]?\d{2}[\s.-]?\d{2}(?!\d)/g, mask: maskPhone },
  { type: 'phone', re: /(?<!\d)5\d{2}[\s.-]\d{3}[\s.-]\d{2}[\s.-]\d{2}(?!\d)/g, mask: maskPhone },
  {
    type: 'home-path',
    // C:\Users\name, C:/Users/name, /c/Users/name, /Users/name, /home/name
    re: /(?:[A-Za-z]:[\\/]+|\/[a-z]\/|\/mnt\/[a-z]\/|\/)(?:Users|home)[\\/]+([^\\/\s"'`<>*:|]+)/g,
    // trailing sentence punctuation is not part of a user name ("C:\Users\X, ...")
    pick: (m) => m[1].replace(/[.,;)\]}]+$/, ''),
    // the user segment is already masked in the text (…/Users/E***): this is the scanner's own output
    skip: (m, line) => line[m.index + m[0].length] === '*',
    ok: (v) => v !== '' && !SAFE_USERS.has(v.toLowerCase()) && !ALLOWED_HOME_USERS.has(v.toLowerCase()),
    mask: (v) => `…/Users/${v[0]}***`,
  },
];

function walk(d, out = []) {
  for (const e of fs.readdirSync(d, { withFileTypes: true })) {
    const p = path.join(d, e.name);
    if (e.isDirectory()) walk(p, out);
    else out.push(p);
  }
  return out;
}

if (!fs.existsSync(dir) || !fs.statSync(dir).isDirectory()) {
  console.error(`check-ai-sessions: directory not found: ${path.relative(ROOT, dir) || dir}`);
  process.exit(2);
}

const findings = [];
for (const file of walk(dir)) {
  const buf = fs.readFileSync(file);
  if (buf.includes(0)) continue; // binary
  const lines = buf.toString('utf8').split(/\r?\n/);
  lines.forEach((line, idx) => {
    const spans = []; // report each stretch of text once even if several rules match it
    for (const rule of RULES) {
      for (const m of line.matchAll(rule.re)) {
        const value = rule.pick ? rule.pick(m) : m[0];
        if (rule.skip && rule.skip(m, line)) continue;
        if (rule.ok && !rule.ok(value)) continue;
        const start = m.index;
        const end = m.index + m[0].length;
        if (spans.some(([s, e]) => start < e && end > s)) continue;
        spans.push([start, end]);
        findings.push({
          file: path.relative(ROOT, file).replace(/\\/g, '/'),
          line: idx + 1,
          type: rule.type,
          masked: rule.mask(value),
        });
      }
    }
  });
}

const rel = path.relative(ROOT, dir).replace(/\\/g, '/') || dir;
if (findings.length === 0) {
  console.log(`check-ai-sessions: ${rel} is clean (no keys, e-mails, phone numbers or home paths found)`);
  process.exit(0);
}

const counts = {};
for (const f of findings) {
  console.log(`${f.file}:${f.line}  [${f.type}]  ${f.masked}`);
  counts[f.type] = (counts[f.type] || 0) + 1;
}
console.log(
  `\ncheck-ai-sessions: ${findings.length} finding(s) in ${rel}: ` +
    Object.entries(counts).map(([k, v]) => `${k}=${v}`).join(', ')
);
process.exit(1);
