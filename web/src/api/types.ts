// Types of the API contract. They mirror the Go structs one to one (backend/internal/api and campaign); field names are the
// JSON names, which is why they are snake_case.

/** Which client the API uses for generation: the real model or the demo mock. */
export type LlmMode = 'mock' | 'anthropic'

/** GET /api/config: everything the UI would otherwise hard-code. */
export interface Config {
  llm_mode: LlmMode
  model: string
  limits: {
    sms: number
    email_subject: number
    email_body: number
    brief: number
  }
  /** Whitelisted personalization variables, without braces. */
  variables: string[]
  channels: string[]
}

/** The body of every error response. `details` depends on `code`. */
export interface ErrorEnvelope {
  error: {
    code: string
    message: string
    details: unknown
  }
}
