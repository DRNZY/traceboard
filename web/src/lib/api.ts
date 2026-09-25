import type {
  Alert,
  AlertFilter,
  CaptureMode,
  DeleteResult,
  EventPage,
  ExportFormat,
  ExportResult,
  Project,
  QuarantineEntry,
  RunDetail,
  RunFilter,
  RunPage,
  Settings,
  Source,
} from './types'

const UNAUTHENTICATED_MESSAGE = 'Sign-in required'
const GENERIC_FAILURE_MESSAGE = 'Traceboard could not complete that request'

/**
 * A failed local request. Server response bodies are never copied into the
 * message: a body could carry a path, a token, or captured content, and none of
 * that belongs in the interface. The status and a fixed code are enough.
 */
export class ApiError extends Error {
  readonly status: number
  readonly code: string

  constructor(status: number, code: string, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }

  get unauthenticated(): boolean {
    return this.code === 'unauthenticated'
  }

  get notFound(): boolean {
    return this.status === 404
  }
}

/**
 * The narrow fetch shape the client depends on. Declaring it explicitly lets a
 * test supply a stub without pretending to implement the whole Fetch API.
 */
export type Fetcher = (input: string, init?: RequestInit) => Promise<Response>

export interface ApiClientOptions {
  basePath?: string
  fetch?: Fetcher
}

function trimOrUndefined(value: string | undefined): string | undefined {
  const trimmed = value?.trim()
  return trimmed ? trimmed : undefined
}

/**
 * The only client the dashboard talks to. Every request is same-origin and
 * cookie-authenticated: the browser is never given a bearer token, and a
 * path that would leave the origin is refused before any request is made.
 */
const defaultFetch: Fetcher = (input, init) => globalThis.fetch(input, init)

export class ApiClient {
  readonly #fetch: Fetcher
  readonly #basePath: string

  constructor(fetchImpl: Fetcher = defaultFetch, options: ApiClientOptions = {}) {
    this.#fetch = fetchImpl
    this.#basePath = options.basePath ?? '/api/v1'
  }

  /**
   * Builds a same-origin API path. A segment that carries a scheme, a host, or
   * a leading slash is rejected here rather than being escaped into a URL that
   * would leave the collector.
   */
  /**
   * Encodes one caller-supplied identifier. A value that carries a scheme, a
   * host, or a backslash is refused outright, so no identifier can be turned
   * into a request that leaves the local collector.
   */
  #segment(value: string): string {
    if (value.includes('://') || value.includes('//') || value.includes('\\')) {
      throw new ApiError(0, 'invalid_target', 'That request would leave the local collector')
    }
    return encodeURIComponent(value)
  }

  async #request<T>(path: string, init: RequestInit = {}): Promise<T> {
    const target = `${this.#basePath}${path}`
    if (!target.startsWith('/') || target.startsWith('//')) {
      throw new ApiError(0, 'invalid_target', 'That request would leave the local collector')
    }

    let response: Response
    try {
      response = await this.#fetch(target, {
        method: init.method ?? 'GET',
        ...init,
        credentials: 'include',
        headers: { Accept: 'application/json', ...(init.headers ?? {}) },
      })
    } catch {
      throw new ApiError(0, 'unreachable', 'The local collector did not respond')
    }

    if (response.status === 401 || response.status === 403) {
      throw new ApiError(response.status, 'unauthenticated', UNAUTHENTICATED_MESSAGE)
    }
    if (response.status === 404) {
      throw new ApiError(404, 'not_found', 'That record is not in this collector')
    }
    if (response.status >= 500) {
      throw new ApiError(response.status, 'server_error', GENERIC_FAILURE_MESSAGE)
    }
    if (!response.ok) {
      throw new ApiError(response.status, 'request_failed', GENERIC_FAILURE_MESSAGE)
    }
    if (response.status === 204) return undefined as T
    const text = await response.text()
    if (text.trim() === '') return undefined as T
    return JSON.parse(text) as T
  }
  async listRuns(filter: RunFilter = {}, cursor?: string, limit?: number): Promise<RunPage> {
    const parameters = new URLSearchParams()
    const query = trimOrUndefined(filter.query)
    if (query) parameters.set('q', query)
    if (filter.source) parameters.set('source', filter.source)
    if (filter.project_id) parameters.set('project_id', filter.project_id)
    if (filter.status) parameters.set('status', filter.status)
    if (filter.capture_mode) parameters.set('capture_mode', filter.capture_mode)
    if (filter.alert_state) parameters.set('alert_state', filter.alert_state)
    if (filter.started_after) parameters.set('started_after', filter.started_after)
    if (filter.started_before) parameters.set('started_before', filter.started_before)
    if (filter.parent_run_id) parameters.set('parent_run_id', filter.parent_run_id)
    if (filter.subagent_only) parameters.set('subagent_only', 'true')
    if (cursor) parameters.set('cursor', cursor)
    if (limit) parameters.set('limit', String(limit))
    const suffix = parameters.toString()
    const page = await this.#request<RunPage>(`/runs${suffix ? `?${suffix}` : ''}`)
    return { runs: page.runs ?? [], next_cursor: page.next_cursor || null }
  }

  /** Runs committed after a reconnect point, so a client can resync the index. */
  async listChangedRuns(since: string): Promise<RunPage> {
    const page = await this.#request<RunPage>(`/runs/changed?since=${encodeURIComponent(since)}`)
    return { runs: page.runs ?? [], next_cursor: page.next_cursor || null }
  }

  async getRun(runId: string): Promise<RunDetail> {
    return this.#request<RunDetail>(`/runs/${this.#segment(runId)}`)
  }

  async listEvents(runId: string, after = 0, limit?: number): Promise<EventPage> {
    const parameters = new URLSearchParams({ after: String(after) })
    if (limit) parameters.set('limit', String(limit))
    return this.#request<EventPage>(`/runs/${this.#segment(runId)}/events?${parameters.toString()}`)
  }

  async listAlerts(filter: AlertFilter = {}): Promise<Alert[]> {
    const parameters = new URLSearchParams()
    if (filter.state) parameters.set('state', filter.state)
    if (filter.run_id) parameters.set('run_id', filter.run_id)
    if (filter.limit) parameters.set('limit', String(filter.limit))
    const suffix = parameters.toString()
    return this.#request<Alert[]>(`/alerts${suffix ? `?${suffix}` : ''}`)
  }

  async acknowledgeAlert(alertId: string): Promise<Alert> {
    return this.#request<Alert>(`/alerts/${this.#segment(alertId)}/acknowledge`, { method: 'POST' })
  }

  async listSources(): Promise<Source[]> {
    return this.#request<Source[]>('/sources')
  }

  async setCaptureMode(source: string, mode: CaptureMode): Promise<Source> {
    return this.#request<Source>(`/sources/${this.#segment(source)}/capture-mode`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ capture_mode: mode }),
    })
  }

  async listProjects(): Promise<Project[]> {
    return this.#request<Project[]>('/projects')
  }

  async listQuarantine(): Promise<QuarantineEntry[]> {
    return this.#request<QuarantineEntry[]>('/quarantine')
  }

  async getSettings(): Promise<Settings> {
    return this.#request<Settings>('/settings')
  }

  async exportRun(
    runId: string,
    format: ExportFormat = 'json',
    force = false,
  ): Promise<ExportResult> {
    // The format travels in the body so the path stays a plain run resource.
    return this.#request<ExportResult>(`/runs/${this.#segment(runId)}/export`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ format, force }),
    })
  }

  async deleteRun(runId: string, confirmation?: string): Promise<DeleteResult> {
    const suffix = confirmation ? `?confirm=${encodeURIComponent(confirmation)}` : ''
    return this.#request<DeleteResult>(`/runs/${this.#segment(runId)}${suffix}`, { method: 'DELETE' })
  }
}

export const defaultClient = new ApiClient()
