// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte'
import { afterEach, describe, expect, it, vi } from 'vitest'

import RunInspector from './RunInspector.svelte'
import type {
  Alert,
  AlertState,
  EventRecord,
  EventStatus,
  Run,
  RunStatus,
  Step,
} from '../lib/types'

afterEach(() => {
  cleanup()
})

const run: Run = {
  id: 'run-1',
  source: 'opencode',
  project_id: 'traceboard',
  project_name: 'traceboard',
  title: 'fix pagination',
  status: 'failed' as RunStatus,
  started_at: '2026-09-25T10:00:00Z',
  ended_at: '2026-09-25T10:00:42Z',
  duration_ms: 42_000,
  event_count: 2,
  error_count: 1,
  next_sequence: 3,
  capture_modes: ['metadata'],
  models: [],
  input_tokens: 900,
  output_tokens: 120,
  open_alerts: 1,
  subagent_runs: 0,
  created_at: '2026-09-25T10:00:00Z',
  updated_at: '2026-09-25T10:00:42Z',
}

function makeEvent(overrides: Partial<EventRecord> & { event_id: string; sequence: number }): EventRecord {
  return {
    run_id: 'run-1',
    source: 'opencode',
    source_event_id: `src-${overrides.event_id}`,
    source_version: '1.0.0',
    type: 'run.started',
    status: 'completed' as EventStatus,
    occurred_at: '2026-09-25T10:00:00Z',
    received_at: '2026-09-25T10:00:00Z',
    capture: { mode: 'metadata' },
    attributes: {},
    searchable: true,
    ...overrides,
  }
}

const runEvents: EventRecord[] = [
  makeEvent({ event_id: 'evt-1', sequence: 1, step_id: 'step-1' }),
  makeEvent({
    event_id: 'evt-2',
    sequence: 2,
    step_id: 'step-2',
    parent_step_id: 'step-1',
    type: 'run.failed',
    status: 'failed',
  }),
]

const steps: Step[] = [
  {
    run_id: 'run-1',
    id: 'step-1',
    type: 'session',
    status: 'completed',
    event_count: 1,
  },
]

const openAlert: Alert = {
  id: 'alert-1',
  type: 'run.failed',
  run_id: 'run-1',
  source: 'opencode',
  message: 'run fix pagination reported a failure',
  created_at: '2026-09-25T10:00:43Z',
  state: 'open' as AlertState,
}

function renderInspector(overrides: Record<string, unknown> = {}) {
  const props = {
    run,
    steps,
    runEvents,
    alerts: [openAlert],
    expandedEventId: null,
    replayIndex: 1,
    appliedSequence: 2,
    eventsIncomplete: false,
    hasMoreEvents: false,
    loadingMore: false,
    onToggleEvent: vi.fn(),
    onReplayIndexChange: vi.fn(),
    onLoadMoreEvents: vi.fn(),
    onSelectRun: vi.fn(),
    onExport: vi.fn(),
    onAcknowledge: vi.fn(),
    acknowledgingAlertId: null,
    ...overrides,
  }
  return { ...render(RunInspector, props), props }
}

describe('RunInspector', () => {
  it('states the run identity and the fields the run actually has', () => {
    renderInspector()

    expect(screen.getByText('fix pagination')).toBeTruthy()
    expect(screen.getByText(/run-1/)).toBeTruthy()
    expect(screen.getByText('42.0 s')).toBeTruthy()
    expect(screen.getByText('in 900 / out 120')).toBeTruthy()
  })

  it('labels missing optional facts instead of showing blanks', () => {
    renderInspector({
      run: {
        ...run,
        title: undefined,
        project_name: undefined,
        ended_at: undefined,
        duration_ms: undefined,
        models: [],
        input_tokens: undefined,
        output_tokens: undefined,
      },
      alerts: [],
    })

    expect(screen.getByText('Untitled run')).toBeTruthy()
    expect(screen.getByText('none reported')).toBeTruthy()
    expect(screen.getAllByText(/not reported/).length).toBeGreaterThan(0)
  })

  it('surfaces an open alert with an acknowledge action', () => {
    const onAcknowledge = vi.fn()
    renderInspector({ alerts: [openAlert], onAcknowledge })

    expect(screen.getByText('run fix pagination reported a failure')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Acknowledge' }))
    expect(onAcknowledge).toHaveBeenCalledWith('alert-1')
  })

  it('hides an acknowledged alert from the top of the inspector', () => {
    renderInspector({ alerts: [{ ...openAlert, state: 'acknowledged' as AlertState }] })

    expect(screen.queryByRole('button', { name: 'Acknowledge' })).toBeNull()
    expect(screen.getByText(/1 resolved or acknowledged alert/)).toBeTruthy()
  })

  it('links a subagent run to its parent without offering a re-run', () => {
    const onSelectRun = vi.fn()
    renderInspector({
      run: { ...run, parent_run_id: 'run-parent' },
      onSelectRun,
    })

    fireEvent.click(screen.getByRole('button', { name: /Open parent run/ }))
    expect(onSelectRun).toHaveBeenCalledWith('run-parent')
    expect(screen.queryByRole('button', { name: /re-run|execute|retry/i })).toBeNull()
  })

  it('lists steps and nests child steps under the run', () => {
    renderInspector()

    expect(screen.getAllByText('step-1').length).toBeGreaterThan(0)
    expect(screen.getByText(/session · completed · 1 events/)).toBeTruthy()
    expect(screen.getByText('step-2')).toBeTruthy()
    // step-2 is listed once as a child, not duplicated in a second section.
    expect(screen.queryByText('Nested steps')).toBeNull()
  })

  it('refuses to fold a run forward while a range is missing', () => {
    renderInspector({ appliedSequence: null })

    expect(screen.getByText(/This run is missing events/)).toBeTruthy()
  })

  it('passes replay and export actions up to the page', () => {
    const onExport = vi.fn()
    const onReplayIndexChange = vi.fn()
    renderInspector({ onExport, onReplayIndexChange, replayIndex: 0 })

    fireEvent.click(screen.getByRole('button', { name: 'Export run' }))
    expect(onExport).toHaveBeenCalledWith('run-1')

    fireEvent.click(screen.getByRole('button', { name: 'Next event' }))
    expect(onReplayIndexChange).toHaveBeenCalledWith(1)
  })

  it('disables replay stepping at the ends of the recorded range', () => {
    renderInspector({ replayIndex: 0 })

    expect((screen.getByRole('button', { name: 'Previous event' }) as HTMLButtonElement).disabled).toBe(
      true,
    )
    expect(
      (screen.getByRole('button', { name: 'Next event' }) as HTMLButtonElement).disabled,
    ).toBe(false)
  })

  it('omits the step list when the run has no steps', () => {
    renderInspector({ steps: [] })

    expect(screen.queryByText('Steps')).toBeNull()
  })
})
