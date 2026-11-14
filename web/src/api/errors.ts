import { formatNumber } from '@/domain/format'
import { ApiError, isRecord } from './client'
import type { FieldProblem } from './types'

// Turns anything that can go wrong into Turkish text for the screen. Two rules:
//  * the text depends on the error's `code` (and, for field problems, on `code` and `params`), never on the server's English
//    `message`: that sentence is for logs and may change. A code this file does not know shows the server's message.
//  * the screen decides how to show it; this file only says what happened and what to do.

/** What a screen needs to show an error. */
export interface ErrorDescription {
  /** Short heading: what happened. */
  title: string
  /** What to do about it, one or two sentences. */
  body: string
  /** Seconds to wait before trying again (a countdown), or null. */
  retryAfterSeconds: number | null
  /** Whether doing the same thing again can work. False for errors that need a changed request or a reload. */
  retryable: boolean
  /** Field problems, translated, for the forms that can show them next to their fields. */
  fields: FieldMessage[]
  /** English lines for the collapsible "Teknik ayrıntılar" panel; empty when there is nothing technical to show. */
  technical: string[]
  /** The id to quote to whoever runs the server (internal errors only). */
  requestId: string | null
  /** When creating a campaign failed after its draft was saved: the draft's id. */
  campaignId: number | null
}

export interface FieldMessage {
  path: string
  /** Turkish name of the field: "SMS metni". */
  label: string
  /** The sentence, without the name: "161 karakter; sınır 160." */
  text: string
}

export interface ProblemContext {
  /** Whitelisted variable names without braces (from /api/config), for the "unknown variable" sentence. */
  variables: readonly string[]
}

const FIELD_LABELS: Record<string, string> = {
  brief: 'Brief',
  sms: 'SMS metni',
  email_subject: 'E-posta konusu',
  email_body: 'E-posta gövdesi',
  channels: 'Kanallar',
  version: 'Sürüm',
  limit: 'Liste uzunluğu',
}

export function fieldLabel(path: string): string {
  // channels[1] is the channel list too.
  const name = path.replace(/\[\d+\]$/, '')
  return FIELD_LABELS[name] ?? path
}

function num(value: unknown): string {
  return typeof value === 'number' ? formatNumber(value) : '?'
}

function str(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

function variableList(variables: readonly string[]): string {
  return variables.map((v) => `{{${v}}}`).join(' ve ')
}

/**
 * The Turkish sentence for one field problem, chosen by its code and params. `char` is the 1-based character number.
 * An unknown code returns the server's own message, so a new server-side check is never silent.
 */
export function describeProblem(problem: FieldProblem, ctx: ProblemContext): string {
  const p = problem.params ?? {}
  const at = p.char !== undefined ? ` (${num(p.char)}. karakter)` : ''
  switch (problem.code) {
    case 'empty':
      return problem.path.startsWith('channels') ? 'En az bir kanal seçilmeli.' : 'Boş bırakılamaz.'
    case 'invalid_utf8':
      return 'Metinde bozuk bir karakter (�) var; yeniden yazın.'
    case 'too_long':
      return `${num(p.length)} karakter; sınır ${num(p.limit)}.`
    case 'unknown_variable':
      return `Bilinmeyen değişken {{${str(p.name)}}}${at}. Kullanılabilecekler: ${variableList(ctx.variables)}.`
    case 'unbalanced_brace':
      return p.brace === '}'
        ? `Tek başına } kullanılamaz${at}. Değişken, adından sonra }} ile kapanmalı.`
        : `Tek başına { kullanılamaz${at}. Değişkeni {{first_name}} biçiminde yazın; başka süslü parantez kullanmayın.`
    case 'unclosed_variable':
      return `{{ açılmış ama }} ile kapatılmamış${at}.`
    case 'malformed_variable':
      return `Değişken hatalı yazılmış${at}. Biçim: {{first_name}}.`
    case 'unknown_channel':
      return `Bilinmeyen kanal: ${str(p.value)}.`
    case 'duplicate_channel':
      return `Kanal iki kez seçilmiş: ${str(p.value)}.`
    case 'required':
      return 'Zorunlu alan.'
    case 'not_a_string':
      return 'Metin olmalı.'
    case 'unknown_property':
      return 'Tanınmayan alan.'
    case 'invalid_param':
      return p.min !== undefined && p.max !== undefined ? `${num(p.min)} ile ${num(p.max)} arasında bir sayı olmalı.` : 'Geçersiz değer.'
    default:
      return problem.message
  }
}

/** Reads the details of validation_failed, a list with one entry per field. Entries that do not have a path and a code are skipped. */
export function fieldProblems(details: unknown): FieldProblem[] {
  if (!Array.isArray(details)) return []
  const out: FieldProblem[] = []
  for (const d of details) {
    if (!isRecord(d) || typeof d.path !== 'string') continue
    out.push({
      path: d.path,
      code: typeof d.code === 'string' ? d.code : '',
      message: typeof d.message === 'string' ? d.message : '',
      params: isRecord(d.params) ? d.params : undefined,
    })
  }
  return out
}

const DEFAULT_CONTEXT: ProblemContext = { variables: ['first_name', 'city'] }

interface Text {
  title: string
  body: string
  retryable?: boolean
}

const BY_CODE: Record<string, Text> = {
  network_error: {
    title: 'Sunucuya ulaşılamıyor',
    body: 'Bağlantınızı ve sunucunun çalıştığını kontrol edip tekrar deneyin.',
    retryable: true,
  },
  timeout: {
    title: 'Sunucu zamanında yanıt vermedi',
    body: 'İstek beklenenden uzun sürdü. Bağlantınızı kontrol edip tekrar deneyin.',
    retryable: true,
  },
  http_error: {
    title: 'Sunucudan beklenmeyen bir yanıt geldi',
    body: 'Sunucu ya da aradaki bağlantı bir sorun yaşıyor olabilir. Biraz sonra tekrar deneyin.',
    retryable: true,
  },
  bad_response: {
    title: 'Sunucunun yanıtı anlaşılamadı',
    body: 'Sunucu beklenen biçimde yanıt vermedi. Sayfayı yenileyip tekrar deneyin.',
    retryable: true,
  },
  stream_incomplete: {
    title: 'İçerik akışı yarıda kesildi',
    body: 'Bağlantı, içerik tamamlanmadan koptu. Tekrar deneyin.',
    retryable: true,
  },
  invalid_json: { title: 'İstek anlaşılamadı', body: 'İstek geçerli değil. Sayfayı yenileyip tekrar deneyin.' },
  body_too_large: { title: 'Metin çok uzun', body: 'Gönderilen metin sunucunun kabul ettiği boyutu aşıyor. Kısaltıp tekrar deneyin.' },
  validation_failed: { title: 'Bazı alanlar geçersiz', body: 'Aşağıdaki alanları düzeltip tekrar deneyin.' },
  acknowledgement_required: {
    title: 'Uyarıları onaylamanız gerekiyor',
    body: 'Kampanyada uyarılar var. Okuyup onay kutusunu işaretledikten sonra tekrar onaylayın.',
  },
  no_recipients: {
    title: 'Gönderilecek kimse yok',
    body: 'Seçili kanallarda mesaj almayı kabul etmiş kimse yok. Başka bir kanal seçin ya da brief’i değiştirin.',
  },
  not_found: { title: 'Kampanya bulunamadı', body: 'Bu adreste bir kampanya yok. Kampanyalar listesinden seçin.' },
  invalid_state: {
    title: 'Kampanya şu an bu işleme uygun değil',
    body: 'Kampanyanın durumu değişmiş olabilir. Sayfayı yenileyip güncel durumuna bakın.',
  },
  state_changed: {
    title: 'Kampanya başka bir yerden değişti',
    body: 'Siz işlem yaparken kampanya değişti. Sayfayı yenileyip tekrar deneyin.',
  },
  version_mismatch: {
    title: 'Kampanya değişti, lütfen tekrar gözden geçirin',
    body: 'Onayladığınız sürüm artık güncel değil. Güncel hâlini inceleyip yeniden onaylayın.',
  },
  content_exists: {
    title: 'Bu kampanyanın içeriği zaten var',
    body: 'İçeriği yeniden üretmek mevcut metinlerin yerine yenilerini yazar. Bunu isterseniz onaylayın.',
  },
  generation_in_progress: {
    title: 'İçerik zaten üretiliyor',
    body: 'Bu kampanya için bir üretim sürüyor. Bitmesini bekleyin; sayfayı yenileyerek sonucu görebilirsiniz.',
    retryable: true,
  },
  llm_refused: {
    title: 'Model bu brief üzerinde çalışmayı reddetti',
    body: 'Brief’i değiştirip yeniden deneyin.',
  },
  llm_rate_limited: {
    title: 'Model hizmeti şu an istek sınırında',
    body: 'Çok sayıda istek yapıldı. Biraz bekleyip tekrar deneyin.',
    retryable: true,
  },
  llm_output_truncated: {
    title: 'Modelin yanıtı yarıda kesildi',
    body: 'Daha kısa bir brief ile tekrar deneyin.',
    retryable: true,
  },
  llm_error: {
    title: 'Model hizmeti isteği işleyemedi',
    body: 'Sorun sizden kaynaklanmıyor. Biraz sonra tekrar deneyin; sürerse sistemi yönetenlere bildirin.',
    retryable: true,
  },
  llm_unavailable: {
    title: 'Model hizmetine şu an ulaşılamıyor',
    body: 'Biraz sonra tekrar deneyin.',
    retryable: true,
  },
  llm_busy: {
    title: 'Şu an çok fazla üretim çalışıyor',
    body: 'Birkaç saniye sonra tekrar deneyin.',
    retryable: true,
  },
  llm_timeout: {
    title: 'Model zamanında yanıt vermedi',
    body: 'Tekrar deneyin.',
    retryable: true,
  },
  server_shutting_down: {
    title: 'Sunucu yeniden başlıyor',
    body: 'Birkaç saniye sonra tekrar deneyin.',
    retryable: true,
  },
  client_too_slow: {
    title: 'Akış, okunamadığı için kapatıldı',
    body: 'Bağlantınız yavaş görünüyor. Tekrar deneyin.',
    retryable: true,
  },
  client_closed: { title: 'İstek iptal edildi', body: 'Tekrar deneyebilirsiniz.', retryable: true },
  approval_unconfirmed: {
    title: 'Onayın uygulanıp uygulanmadığı belli değil',
    body: 'Tekrar onaylamayın. Sayfayı yenileyip kampanyanın durumuna bakın.',
  },
  method_not_allowed: { title: 'Bu işlem desteklenmiyor', body: 'Sayfayı yenileyip tekrar deneyin.' },
  not_ready: {
    title: 'Sunucu henüz hazır değil',
    body: 'Veritabanı yanıt vermiyor. Biraz sonra tekrar deneyin.',
    retryable: true,
  },
  internal: {
    title: 'Sunucuda bir sorun oluştu',
    body: 'Tekrar deneyin. Sürerse aşağıdaki kodu sistemi yönetenlere iletin.',
    retryable: true,
  },
}

const STAGE_TEXT: Record<string, string> = { segment: 'kitle kuralını', content: 'içeriği' }

/** The text, and the English problems for the technical panel. */
function llmInvalidOutput(details: unknown): [Text, string[]] {
  const d = isRecord(details) ? details : {}
  const attempts = typeof d.attempts === 'number' ? d.attempts : null
  const what = typeof d.stage === 'string' ? (STAGE_TEXT[d.stage] ?? 'yanıtı') : 'yanıtı'
  const tries = attempts === null ? 'birkaç denemede' : `${formatNumber(attempts)} denemede`
  const problems = Array.isArray(d.problems) ? d.problems.filter((p): p is string => typeof p === 'string') : []
  return [
    {
      title: 'Model geçerli bir çıktı üretemedi',
      body: `Model ${what} ${tries} geçerli biçimde üretemedi. Tekrar deneyin; olmazsa brief’i daha açık yazın.`,
      retryable: true,
    },
    problems,
  ]
}

function detailNumber(details: unknown, key: string): number | null {
  return isRecord(details) && typeof details[key] === 'number' ? details[key] : null
}

/**
 * Describes any thrown value. An ApiError is translated by its code; anything else (a bug, not an API answer) gets a generic
 * text and its message as technical detail, so it is never swallowed.
 */
const OUT_OF_DATE = new Set(['network_error', 'timeout', 'http_error', 'bad_response', 'stream_incomplete', 'invalid_state', 'state_changed'])

/**
 * Whether a failed action may have left the screen different from the server: the answer never came (the action may have
 * been applied anyway) or the server says the campaign is in another state. The screen then reads the campaign again instead
 * of showing what it believed before.
 */
export function mayBeOutOfDate(err: unknown): boolean {
  return err instanceof ApiError && OUT_OF_DATE.has(err.code)
}

export function describeError(err: unknown, ctx: ProblemContext = DEFAULT_CONTEXT): ErrorDescription {
  if (!(err instanceof ApiError)) {
    return {
      title: 'Beklenmeyen bir hata oluştu',
      body: 'Sayfayı yenileyip tekrar deneyin.',
      retryAfterSeconds: null,
      retryable: true,
      fields: [],
      technical: [err instanceof Error ? err.message : String(err)],
      requestId: null,
      campaignId: null,
    }
  }

  let text: Text
  let technical: string[]
  if (err.code === 'llm_invalid_output') {
    ;[text, technical] = llmInvalidOutput(err.details)
  } else if (BY_CODE[err.code] !== undefined) {
    text = BY_CODE[err.code] as Text
    technical = []
  } else {
    text = { title: 'İşlem tamamlanamadı', body: 'Sunucu bu hatayı bildirdi; aşağıdaki ayrıntıya bakın.', retryable: false }
    technical = [`${err.code}: ${err.message}`]
  }

  const fields =
    err.code === 'validation_failed'
      ? fieldProblems(err.details).map((p) => ({ path: p.path, label: fieldLabel(p.path), text: describeProblem(p, ctx) }))
      : []

  return {
    title: text.title,
    body: text.body,
    retryAfterSeconds: err.retryAfterSeconds,
    retryable: text.retryable ?? false,
    fields,
    technical,
    requestId: err.code === 'internal' && isRecord(err.details) && typeof err.details.request_id === 'string' ? err.details.request_id : null,
    campaignId: detailNumber(err.details, 'campaign_id'),
  }
}
