import type { ContentField } from '@/api/types'
import { runeCount } from './sms'

// A port of the content validator of the Go backend (backend/internal/content/validate.go). The editor flags a problem while
// the marketer types, with the same code and params the server would answer with (backend/internal/content/testdata/
// template_cases.json is run by both), so a text that passes here is not refused on save, and the other way round.

export interface TemplateProblem {
  code: string
  /** Always has `char` (1-based, in code points) for the template problems; see describeProblem in api/errors.ts. */
  params: Record<string, number | string>
}

/** Characters Go's strings.TrimSpace removes. Not the same set as JavaScript's trim(): a BOM stays, a NEL goes. */
const GO_SPACE = new Set([0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x20, 0x85, 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000])
for (let cp = 0x2000; cp <= 0x200a; cp++) GO_SPACE.add(cp)

const REPLACEMENT = 0xfffd

function isBlank(text: string): boolean {
  for (const ch of text) if (!GO_SPACE.has(ch.codePointAt(0) ?? -1)) return false
  return true
}

/** U+FFFD, or a lone surrogate (JSON carries it as an escape the server decodes to U+FFFD). */
function isDamaged(text: string): boolean {
  for (const ch of text) {
    const cp = ch.codePointAt(0) ?? 0
    if (cp === REPLACEMENT || (cp >= 0xd800 && cp <= 0xdfff)) return true
  }
  return false
}

/** Untrusted text echoed in a problem is cut to 40 characters and an ellipsis, as the server does. */
function clip(text: string): string {
  const chars = Array.from(text)
  return chars.length > 40 ? `${chars.slice(0, 40).join('')}…` : text
}

/** Every unknown variable and every brace that is not part of a well-formed {{name}}, in the order of the text. */
export function templateProblems(text: string, variables: readonly string[]): TemplateProblem[] {
  const chars = Array.from(text)
  const problems: TemplateProblem[] = []
  const add = (at: number, code: string, params: Record<string, number | string> = {}): void => {
    problems.push({ code, params: { ...params, char: at + 1 } })
  }

  for (let i = 0; i < chars.length; ) {
    const ch = chars[i]
    if (ch === '{') {
      if (chars[i + 1] !== '{') {
        add(i, 'unbalanced_brace', { brace: '{' })
        i++
        continue
      }
      let end = -1 // where "}}" starts, counted from the first character after "{{"
      for (let j = i + 2; j + 1 < chars.length; j++) {
        if (chars[j] === '}' && chars[j + 1] === '}') {
          end = j - (i + 2)
          break
        }
      }
      if (end < 0) {
        add(i, 'unclosed_variable')
        return problems
      }
      const name = chars.slice(i + 2, i + 2 + end).join('')
      if (name.includes('{') || name.includes('}')) add(i, 'malformed_variable')
      else if (!variables.includes(name)) add(i, 'unknown_variable', { name: clip(name) })
      i += 2 + end + 2
    } else if (ch === '}') {
      add(i, 'unbalanced_brace', { brace: '}' })
      i++
    } else {
      i++
    }
  }
  return problems
}

/**
 * Everything the server checks in one field: empty, a damaged character, the limit and the template. A blank text has no
 * other problem to report. The order is the server's.
 */
export function validateField(text: string, limit: number, variables: readonly string[]): TemplateProblem[] {
  if (isBlank(text)) return [{ code: 'empty', params: {} }]
  const problems: TemplateProblem[] = []
  if (isDamaged(text)) problems.push({ code: 'invalid_utf8', params: {} })
  const length = runeCount(text)
  if (length > limit) problems.push({ code: 'too_long', params: { length, limit } })
  return [...problems, ...templateProblems(text, variables)]
}

/** The three fields with their problems, for the fields that have any. */
export function validateContent(
  content: Record<ContentField, string>,
  limits: Record<ContentField, number>,
  variables: readonly string[],
): Partial<Record<ContentField, TemplateProblem[]>> {
  const out: Partial<Record<ContentField, TemplateProblem[]>> = {}
  for (const field of ['sms', 'email_subject', 'email_body'] as const) {
    const problems = validateField(content[field], limits[field], variables)
    if (problems.length > 0) out[field] = problems
  }
  return out
}
