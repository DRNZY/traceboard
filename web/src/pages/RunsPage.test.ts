// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import RunsPage from './RunsPage.svelte'
import { live } from '../lib/live.svelte'
import { ApiClient, type ApiClientOptions } from '../lib/api'
import type {
  Alert,
  CaptureMode,
  EventPage,
  ExportResult,
  Run,
  RunDetail,
  RunPage,
  RunStatus,
  Source,
  Project,
} from '../lib/types'

afterEach(() => {
  cleanup()
  live.stop()
  live.notices = []
})

interface Call {
  method: string
  url: string
  path: string
  query: URLSearchParams
  init?: RequestInit
}

let calls: Call[] = []

/** Matches on method plus path, so a query string never changes the fixture key. */
type Route = (call: Call) => unknown | undefined

let routes: Route[] = []

function fetcher(): (input: string, init?: RequestInit) => Promise<Response> {
  return async (input, init) => {
    const parsed = new URL(input, 'http://localhost')
    const call: Call = {
      method: (init?.method ?? 'GET').toUpperCase(),
      url: input,
      path: parsed.pathname,
      query: parsed.searchParams,
      init,
    }
    calls.push(call)
    for (const route of routes) {
      const body = route(call)
      if (body !== undefined) {
        return new Response(JSON.stringify(body), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        })
      }
    }
    return new Response('not found', { status: 404 })
  }
}

const run: Run = {
  id: 'run-1',
  source: 'opencode',
  project_id: 'traceboard',
  project_name: 'traceboard',
  title: 'fix pagination',
  status: 'failed' as RunStatus,
  started_at: '2026-09-25T10:00:00Z',
  event_count: 2,
  error_count: 1,
  next_sequence: 3,
  capture_modes: ['metadata'],
  models: [],
  open_alerts: 0,
  subagent_runs: 0,
  created_at: '2026-09-25T10:00:00Z',
  updated_at: '2026-09-25T10:00:42Z',
}

const secondRun: Run = { ...run, id: 'run-2', title: 'second run', status: 'completed' }

const detail: RunDetail = { run, steps: [], subagent_runs: [], alerts: [] }

function event(sequence: number, overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    event_id: `evt-${sequence}`,
    run_id: 'run-1',
    sequence,
    source: 'opencode',
    source_event_id: `src-${sequence}`,
    source_version: '1.0.0',
    type: 'run.started',
    status: 'completed',
    occurred_at: '2026-09-25T10:00:00Z',
    received_at: '2026-09-25T10:00:00Z',
    capture: { mode: 'metadata' },
    attributes: {},
    searchable: true,
    ...overrides,
  }
}

type RunPageEvents = EventPage['events']

const pageEvents: RunPageEvents = [event(1) as never, event(2) as never]

const page: EventPage = {
  events: pageEvents,
  next_sequence: 3,
  run_sequence: 2,
  has_more: false,
  run_status: 'failed',
}

const sources: Source[] = [
  {
    name: 'opencode',
    source_version: '1.0.0',
    capture_mode: 'metadata',
    connected: true,
    run_count: 2,
    quarantine_count: 0,
    created_at: '2026-09-25T10:00:00Z',
    updated_at: '2026-09-25T10:00:00Z',
  },
]

const projects: Project[] = [
  {
    id: 'traceboard',
    name: 'traceboard',
    run_count: 2,
    created_at: '2026-09-25T10:00:00Z',
    updated_at: '2026-09-25T10:00:00Z',
  },
]

function baseRoutes(): Route[] {
  return [
    (call) =>
      call.method === 'GET' && call.path === '/api/v1/runs'
        ? { runs: [run, secondRun], next_cursor: null }
        : undefined,
    (call) => (call.method === 'GET' && call.path === '/api/v1/sources' ? sources : undefined),
    (call) => (call.method === 'GET' && call.path === '/api/v1/projects' ? projects : undefined),
    (call) => (call.method === 'GET' && call.path === '/api/v1/quarantine' ? [] : undefined),
  ]
}

function client(): ApiClient {
  return new ApiClient(fetcher())
}

function runDetailRoute(): Route {
  return (call) =>
    call.path === '/api/v1/runs/run-1' && call.method === 'GET' ? detail : undefined
}

/** Serves events strictly after the requested sequence, so a gap test is honest. */
function eventsRoute(): Route {
  return (call) => {
    if (call.path !== '/api/v1/runs/run-1/events') return undefined
    const after = Number(call.query.get('after') ?? '0')
    return {
      events: pageEvents.filter((candidate) => candidate.sequence > after),
      next_sequence: 3,
      run_sequence: after,
      has_more: false,
      run_status: 'failed',
    }
  }
}

function alertsRoute(alerts: Alert[]): Route {
  return (call) => (call.path === '/api/v1/alerts' && call.method === 'GET' ? alerts : undefined)
}

async function renderPage(props: Record<string, unknown> = {}) {
  const result = render(RunsPage, { client: client(), ...props })
  await waitFor(() => expect(screen.getByText('fix pagination')).toBeTruthy())
  return result
}

beforeEach(() => {
  calls = []
  routes = baseRoutes()
})

describe('RunsPage', () => {
  it('loads the run index with same-origin credentials', async () => {
    await renderPage()

    const listCall = calls.find((call) => call.path === '/api/v1/runs')
    expect(listCall).toBeDefined()
    expect(listCall?.init?.credentials).toBe('include')
  })

  it('lists the filter options the source and project data provide', async () => {
    await renderPage()

    expect(await screen.findByLabelText('Source')).toBeTruthy()
    expect(screen.getByRole('option', { name: 'opencode' })).toBeTruthy()
    expect(screen.getByRole('option', { name: 'traceboard' })).toBeTruthy()
  })

  it('sends filter edits to the server rather than filtering locally only', async () => {
    await renderPage()

    await fireEvent.input(screen.getByLabelText('Search runs'), { target: { value: 'pagination' } })
    await waitFor(() => {
        expect(calls.some((call) => call.query.get('q') === 'pagination')).toBe(true)
    })
  })

  it('opens a run and loads its detail, events, and alerts', async () => {
    routes.push(runDetailRoute(), eventsRoute(), alertsRoute([]))

    await renderPage()
    await fireEvent.click(screen.getAllByRole('button', { name: /fix pagination/ })[0] as HTMLElement)

    await waitFor(() => expect(screen.getByText('2 events loaded')).toBeTruthy())
    expect(
      calls.some((call) => call.path === '/api/v1/runs/run-1' && call.method === 'GET'),
    ).toBe(true)
    expect(calls.some((call) => call.path === '/api/v1/alerts' && call.query.get('run_id') === 'run-1')).toBe(
      true,
    )
  })

  it('refetches only the missing range when a gap notice arrives', async () => {
    routes.push(runDetailRoute(), eventsRoute(), alertsRoute([]))

    await renderPage()
    await fireEvent.click(screen.getAllByRole('button', { name: /fix pagination/ })[0] as HTMLElement)
    await waitFor(() => expect(screen.getByText('2 events loaded')).toBeTruthy())

    calls = []
    routes.push((call) =>
      call.path === '/api/v1/runs/run-1/events' && call.query.get('after') === '2'
        ? {
            events: [event(3) as never],
            next_sequence: 4,
            run_sequence: 3,
            has_more: false,
            run_status: 'failed',
          }
        : undefined,
    )
    live.notices = [{ runId: 'run-1', afterSequence: 2, reason: 'sequence' }]

    await waitFor(() =>
      expect(
        calls.some(
          (call) => call.path === '/api/v1/runs/run-1/events' && call.query.get('after') === '2',
        ),
      ).toBe(true),
    )
    expect(
      calls.some(
        (call) => call.path === '/api/v1/runs/run-1/events' && call.query.get('after') === '0',
      ),
    ).toBe(false)
  })

  it('refetches the whole index for a gap that names no run', async () => {
    await renderPage()
    calls = []
    live.notices = [{ runId: null, afterSequence: 0, reason: 'reconnect' }]

    await waitFor(() => expect(calls.some((call) => call.path === '/api/v1/runs')).toBe(true))
  })

  it('reports a failed request without inventing data', async () => {
    // Drop the run list route so the request 404s like a broken or unauthenticated server.
    routes = routes.filter((route) => {
      const probe = { method: 'GET', url: '', path: '/api/v1/runs', query: new URLSearchParams() }
      return route(probe) === undefined
    })

    render(RunsPage, { client: client() })

    const alerts = await screen.findAllByRole('alert')
    expect(alerts.length).toBeGreaterThan(0)
    // The failure is reported plainly, with no body text from the server response.
    expect(alerts.map((node) => node.textContent).join(' ')).not.toContain('not found')
  })

  it('exports the open run and reports the path', async () => {
    const exported: ExportResult = { path: '/tmp/traceboard/run-1.json', format: 'json', bytes: 2048 }
    routes.push(runDetailRoute(), eventsRoute(), alertsRoute([]))
    routes.push((call) => (call.path === '/api/v1/runs/run-1/export' ? exported : undefined))

    await renderPage()
    await fireEvent.click(screen.getAllByRole('button', { name: /fix pagination/ })[0] as HTMLElement)
    await waitFor(() => expect(screen.getByText('2 events loaded')).toBeTruthy())

    await fireEvent.click(screen.getByRole('button', { name: 'Export run' }))
    expect(await screen.findByText(/run-1\.json \(2,048 bytes\)/)).toBeTruthy()
  })

  it('acknowledges an alert and reloads the alert list', async () => {
    const alert: Alert = {
      id: 'alert-1',
      type: 'run.failed',
      run_id: 'run-1',
      message: 'run failed',
      created_at: '2026-09-25T10:00:43Z',
      state: 'open',
    }
    routes.push(
      (call) =>
        call.path === '/api/v1/runs/run-1' && call.method === 'GET'
          ? { ...detail, alerts: [alert] }
          : undefined,
      eventsRoute(),
      alertsRoute([alert]),
    )
    routes.push((call) =>
      call.path === '/api/v1/alerts/alert-1/acknowledge'
        ? { ...alert, state: 'acknowledged' }
        : undefined,
    )

    await renderPage()
    await fireEvent.click(screen.getAllByRole('button', { name: /fix pagination/ })[0] as HTMLElement)
    await waitFor(() => expect(screen.getByText('2 events loaded')).toBeTruthy())

    await fireEvent.click(screen.getAllByRole('button', { name: 'Acknowledge' })[0] as HTMLElement)
    await waitFor(() =>
      expect(calls.some((call) => call.url === '/api/v1/alerts/alert-1/acknowledge')).toBe(true),
    )
  })

  it('deletes a run only after an explicit click', async () => {
    routes.push(runDetailRoute(), eventsRoute(), alertsRoute([]))
    routes.push((call) =>
      call.path === '/api/v1/runs/run-1' && call.method === 'DELETE'
        ? { run_id: 'run-1', events: 2, steps: 0, alerts: 0, search_entries: 2 }
        : undefined,
    )

    await renderPage()
    await fireEvent.click(screen.getAllByRole('button', { name: /fix pagination/ })[0] as HTMLElement)
    await waitFor(() => expect(screen.getByText('2 events loaded')).toBeTruthy())

    expect(
      calls.some((call) => call.path === '/api/v1/runs/run-1' && call.method === 'DELETE'),
    ).toBe(false)

    await fireEvent.click(screen.getByRole('button', { name: 'Delete this run' }))
    await waitFor(() =>
      expect(
        calls.some((call) => call.path === '/api/v1/runs/run-1' && call.method === 'DELETE'),
      ).toBe(true),
    )
  })

  it('never offers a control that re-executes a run', async () => {
    routes.push(runDetailRoute(), eventsRoute(), alertsRoute([]))

    await renderPage()
    await fireEvent.click(screen.getAllByRole('button', { name: /fix pagination/ })[0] as HTMLElement)
    await waitFor(() => expect(screen.getByText('2 events loaded')).toBeTruthy())

    const labels = screen.getAllByRole('button').map((button) => button.textContent ?? '')
    expect(labels.some((label) => /re-?run|re-?execute|retry|resume/i.test(label))).toBe(false)
  })
})
