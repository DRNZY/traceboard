import { afterEach, describe, expect, it, vi } from 'vitest'

import { ApiClient, ApiError } from './api'
import type { RunPage } from './types'

type FetchCall = { url: string; init: RequestInit }

const runPage: RunPage = {
  runs: [
    {
      id: 'run-2',
      source: 'opencode',
      project_id: 'traceboard',
      project_name: 'traceboard',
      status: 'failed',
      event_count: 12,
      error_count: 1,
      next_sequence: 13,
      capture_modes: ['metadata'],
      models: [],
      open_alerts: 1,
      subagent_runs: 0,
      created_at: '2026-09-25T10:00:02Z',
      updated_at: '2026-09-25T10:00:09Z',
    },
    {
      id: 'run-1',
      source: 'claude-code',
      status: 'completed',
      event_count: 4,
      error_count: 0,
      next_sequence: 5,
      capture_modes: ['detailed'],
      models: ['claude-opus-5'],
      input_tokens: 1200,
      output_tokens: 340,
      open_alerts: 0,
      subagent_runs: 0,
      created_at: '2026-09-25T10:00:00Z',
      updated_at: '2026-09-25T10:00:01Z',
    },
  ],
  next_cursor: 'eyJzb3J0X2F0IjoxNzU4ODA0MDAwMDAwMDB9',
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

function harness(responses: Response[] = [jsonResponse(runPage)]) {
  const calls: FetchCall[] = []
  const fetcher = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    calls.push({ url: String(input), init })
    const next = responses.shift() ?? jsonResponse({})
    return next
  })
  return { calls, fetcher: fetcher as unknown as typeof fetch, client: new ApiClient(fetcher) }
}

afterEach(() => {
  vi.useRealTimers()
})

describe('ApiClient.request', () => {
  it('uses same-origin relative URLs and sends cookies without bearer tokens', async () => {
    const { calls, client } = harness()

    await client.listRuns({})

    expect(calls).toHaveLength(1)
    const call = calls[0]
    expect(call.url.startsWith('/api/v1/runs')).toBe(true)
    expect(call.init.credentials).toBe('include')
    expect(call.init.method).toBe('POST' in call.init ? call.init.method : 'GET')
    const headers = new Headers(call.init.headers)
    expect(headers.get('authorization')).toBeNull()
    expect(headers.get('accept')).toBe('application/json')
  })

  it('refuses to build a request against another origin', async () => {
    const { calls, client } = harness()

    await expect(client.getRun('https://example.com/steal')).rejects.toBeInstanceOf(ApiError)

    expect(calls).toHaveLength(0)
  })

  it('raises a typed unauthenticated error without echoing the response body', async () => {
    const secret = 'ingest-token-should-never-surface'
    const { calls, client } = harness([
      new Response(JSON.stringify({ error: 'invalid token ' + secret }), {
        status: 401,
        headers: { 'content-type': 'application/json' },
      }),
    ])

    const error = await client.listRuns({}).catch((thrown: unknown) => thrown)

    expect(error).toBeInstanceOf(ApiError)
    const apiError = error as ApiError
    expect(apiError.status).toBe(401)
    expect(apiError.code).toBe('unauthenticated')
    expect(apiError.unauthenticated).toBe(true)
    expect(apiError.message).toBe('Sign-in required')
    expect(apiError.message).not.toContain(secret)
    expect(JSON.stringify(apiError)).not.toContain(secret)
    expect(calls[0].init.credentials).toBe('include')
  })

  it('maps not-found responses to a typed error', async () => {
    const { client } = harness([jsonResponse({ error: 'run not found' }, 404)])

    const error = (await client.getRun('missing').catch((thrown: unknown) => thrown)) as ApiError

    expect(error).toBeInstanceOf(ApiError)
    expect(error.notFound).toBe(true)
    expect(error.status).toBe(404)
  })

  it('keeps server failures generic', async () => {
    const { client } = harness([new Response('boom /home/darnell/.config/traceboard', { status: 500 })])

    const error = (await client.getRun('run-1').catch((thrown: unknown) => thrown)) as ApiError

    expect(error).toBeInstanceOf(ApiError)
    expect(error.code).toBe('server_error')
    expect(error.message).toBe('Traceboard could not complete that request')
    expect(error.message).not.toContain('/home/darnell')
  })
})

describe('ApiClient.listRuns', () => {
  it('encodes every supported filter and passes the cursor through', async () => {
    const { calls, client } = harness()
    const filter = {
      query: 'fix parser',
      source: 'opencode',
      project_id: 'traceboard',
      status: 'failed',
      capture_mode: 'metadata',
      alert_state: 'open',
      started_after: '2026-09-01T00:00:00Z',
      started_before: '2026-09-25T23:59:59Z',
      parent_run_id: 'run-parent',
      subagent_only: true,
    }

    const page = await client.listRuns(filter, 'cursor-1', 25)

    expect(page).toEqual(runPage)
    const url = new URL(calls[0].url, 'http://127.0.0.1:47821')
    expect(url.pathname).toBe('/api/v1/runs')
    expect(url.searchParams.get('q')).toBe('fix parser')
    expect(url.searchParams.get('source')).toBe('opencode')
    expect(url.searchParams.get('project_id')).toBe('traceboard')
    expect(url.searchParams.get('status')).toBe('failed')
    expect(url.searchParams.get('capture_mode')).toBe('metadata')
    expect(url.searchParams.get('alert_state')).toBe('open')
    expect(url.searchParams.get('started_after')).toBe('2026-09-01T00:00:00Z')
    expect(url.searchParams.get('started_before')).toBe('2026-09-25T23:59:59Z')
    expect(url.searchParams.get('parent_run_id')).toBe('run-parent')
    expect(url.searchParams.get('subagent_only')).toBe('true')
    expect(url.searchParams.get('cursor')).toBe('cursor-1')
    expect(url.searchParams.get('limit')).toBe('25')
  })

  it('omits empty filters so the server applies its own defaults', async () => {
    const { calls, client } = harness()

    await client.listRuns({ query: '   ', source: '' })

    const url = new URL(calls[0].url, 'http://127.0.0.1:47821')
    expect(url.searchParams.get('q')).toBeNull()
    expect(url.searchParams.has('source')).toBe(false)
    expect(url.searchParams.has('limit')).toBe(false)
  })

  it('reports an empty cursor as no more pages', async () => {
    const { client } = harness([jsonResponse({ runs: [], next_cursor: '' })])

    const page = await client.listRuns({})

    expect(page.runs).toEqual([])
    expect(page.next_cursor).toBeNull()
  })
})

describe('ApiClient.listEvents', () => {
  it('requests a range after a sequence for gap recovery', async () => {
    const { calls, client } = harness([
      jsonResponse({
        events: [
          {
            event_id: 'evt-12',
            source_event_id: 'src-12',
            source: 'opencode',
            source_version: '1.0.0',
            run_id: 'run-1',
            sequence: 12,
            occurred_at: '2026-09-25T10:00:12Z',
            received_at: '2026-09-25T10:00:12Z',
            type: 'tool.completed',
            status: 'completed',
            capture: {},
            attributes: {},
            searchable: false,
          },
        ],
        next_sequence: 12,
        run_sequence: 13,
        has_more: false,
        run_status: 'completed',
      }),
    ])

    const page = await client.listEvents('run-1', 11, 500)

    const url = new URL(calls[0].url, 'http://127.0.0.1:47821')
    expect(url.pathname).toBe('/api/v1/runs/run-1/events')
    expect(url.searchParams.get('after')).toBe('11')
    expect(url.searchParams.get('limit')).toBe('500')
    expect(page.events).toHaveLength(1)
    expect(page.next_sequence).toBe(12)
    expect(page.run_sequence).toBe(13)
    expect(page.has_more).toBe(false)
  })

  it('defaults the cursor to zero for a first page', async () => {
    const { calls, client } = harness([
      jsonResponse({ events: [], next_sequence: 0, run_sequence: 1, has_more: false, run_status: 'started' }),
    ])

    await client.listEvents('run-1')

    const url = new URL(calls[0].url, 'http://127.0.0.1:47821')
    expect(url.searchParams.get('after')).toBe('0')
  })
})

describe('ApiClient alerts', () => {
  it('acknowledges an alert and returns the updated alert', async () => {
    const acknowledged = {
      id: 'run.failed:run-2:',
      type: 'run.failed',
      run_id: 'run-2',
      message: 'Run failed',
      created_at: '2026-09-25T10:00:09Z',
      acknowledged_at: '2026-09-25T10:05:00Z',
      state: 'acknowledged',
    }
    const { calls, client } = harness([jsonResponse(acknowledged)])

    const alert = await client.acknowledgeAlert('run.failed:run-2:')

    expect(calls[0].url).toBe('/api/v1/alerts/run.failed%3Arun-2%3A/acknowledge')
    expect(calls[0].init.method).toBe('POST')
    expect(alert.state).toBe('acknowledged')
  })

  it('lists open alerts for the run index', async () => {
    const { calls, client } = harness([jsonResponse([])])

    await client.listAlerts({ state: 'open', limit: 20 })

    const url = new URL(calls[0].url, 'http://127.0.0.1:47821')
    expect(url.pathname).toBe('/api/v1/alerts')
    expect(url.searchParams.get('state')).toBe('open')
    expect(url.searchParams.get('limit')).toBe('20')
  })
})

describe('ApiClient sources, settings, and run actions', () => {
  it('stores a capture mode for one source', async () => {
    const { calls, client } = harness([
      jsonResponse({
        name: 'claude-code',
        source_version: '2.1.0',
        capture_mode: 'detailed',
        connected: true,
        run_count: 3,
        quarantine_count: 0,
        created_at: '2026-09-25T09:00:00Z',
        updated_at: '2026-09-25T10:00:00Z',
      }),
    ])

    const source = await client.setCaptureMode('claude-code', 'detailed')

    expect(calls[0].url).toBe('/api/v1/sources/claude-code/capture-mode')
    expect(calls[0].init.method).toBe('PUT')
    expect(JSON.parse(String(calls[0].init.body))).toEqual({ capture_mode: 'detailed' })
    expect(source.capture_mode).toBe('detailed')
  })

  it('reads settings without any credential fields', async () => {
    const { calls, client } = harness([
      jsonResponse({
        version: '0.1.0',
        listen_address: '127.0.0.1:47821',
        retention_days: 30,
        keep_newest_per_project: 0,
        database_path: '/home/darnell/.local/share/traceboard/traceboard.db',
        config_path: '/home/darnell/.config/traceboard/config.json',
        sessions_valid: 1,
      }),
    ])

    const settings = await client.getSettings()

    expect(calls[0].url).toBe('/api/v1/settings')
    expect(settings.version).toBe('0.1.0')
    expect(Object.keys(settings)).not.toContain('ingest_token')
    expect(Object.keys(settings)).not.toContain('dashboard_token')
  })

  it('exports and deletes a run through the run routes', async () => {
    const { calls, client } = harness([
      jsonResponse({ path: '/tmp/run-1.json', format: 'json', bytes: 2048 }),
      jsonResponse({
        run_id: 'run-1',
        events: 12,
        steps: 3,
        alerts: 1,
        search_entries: 12,
      }),
    ])

    const exported = await client.exportRun('run-1', 'json')
    const deleted = await client.deleteRun('run-1')

    expect(calls[0].url).toBe('/api/v1/runs/run-1/export')
    expect(calls[0].init.method).toBe('POST')
    expect(exported.path).toBe('/tmp/run-1.json')
    expect(calls[1].url).toBe('/api/v1/runs/run-1')
    expect(calls[1].init.method).toBe('DELETE')
    expect(deleted.search_entries).toBe(12)
  })

  it('lists runs changed since a version for reconnect recovery', async () => {
    const { calls, client } = harness([jsonResponse({ runs: runPage.runs, next_cursor: '' })])

    await client.listChangedRuns('2026-09-25T10:00:00Z')

    const url = new URL(calls[0].url, 'http://127.0.0.1:47821')
    expect(url.pathname).toBe('/api/v1/runs/changed')
    expect(url.searchParams.get('since')).toBe('2026-09-25T10:00:00Z')
  })
})
