// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte'
import { afterEach, describe, expect, it, vi } from 'vitest'

import EventTimeline from './EventTimeline.svelte'
import type { EventRecord, EventStatus } from '../lib/types'

afterEach(() => {
  cleanup()
})

function makeEvent(overrides: Partial<EventRecord> & { event_id: string; sequence: number }): EventRecord {
  return {
    run_id: 'run-1',
    source: 'opencode',
    source_event_id: `src-${overrides.event_id}`,
    source_version: '1.2.3',
    type: 'run.started',
    status: 'completed' as EventStatus,
    occurred_at: '2026-09-25T10:00:00Z',
    received_at: '2026-09-25T10:00:01Z',
    step_id: 'step-1',
    parent_step_id: null,
    capture: { mode: 'metadata' },
    attributes: {},
    searchable: true,
    raw: null,
    ...overrides,
  }
}

const runEvents: EventRecord[] = [
  makeEvent({ event_id: 'evt-1', sequence: 1, step_id: 'step-1', type: 'run.started' }),
  makeEvent({
    event_id: 'evt-2',
    sequence: 2,
    step_id: 'step-2',
    parent_step_id: 'step-1',
    type: 'tool.completed',
    attributes: { tool: 'edit', path: 'web/src/lib/api.ts' },
  }),
  // A deliberate ingest gap: sequence 5 follows 3.
  makeEvent({
    event_id: 'evt-5',
    sequence: 5,
    step_id: 'step-2',
    parent_step_id: 'step-1',
    type: 'tool.failed',
    status: 'failed',
    attributes: { tool: 'bash' },
  }),
]

function renderTimeline(overrides: Record<string, unknown> = {}) {
  const props = {
    runEvents,
    expandedEventId: null,
    appliedSequence: 3,
    hasMore: false,
    loadingMore: false,
    onToggleEvent: vi.fn(),
    onLoadMore: vi.fn(),
    ...overrides,
  }
  return { ...render(EventTimeline, props), props }
}

describe('EventTimeline', () => {
  it('renders an empty run honestly', () => {
    renderTimeline({ runEvents: [] })

    expect(screen.getByText('No events recorded for this run yet.')).toBeTruthy()
    expect(screen.getByText('0 events loaded')).toBeTruthy()
  })

  it('lists events in the order the server sent them', () => {
    renderTimeline()

    const rows = screen.getAllByRole('button', { name: /#/ })
    expect(rows.map((row) => row.textContent ?? '')).toEqual([
      expect.stringContaining('#1'),
      expect.stringContaining('#2'),
      expect.stringContaining('#5'),
    ])
  })

  it('reports a missing sequence range instead of implying continuity', () => {
    renderTimeline()

    expect(screen.getByText(/Ingest events 3 to 4 are not in this range\./)).toBeTruthy()
  })

  it('does not report a gap when the client is still recovering a range', () => {
    renderTimeline({ appliedSequence: null })

    expect(screen.queryByText(/not in this range/)).toBeNull()
  })

  it('indents a child step under its parent', () => {
    renderTimeline()

    const groups = screen.getAllByText(/step-\d/)
    expect(groups.length).toBeGreaterThan(0)
    const child = screen.getByText(/edit \/ step-2/)
    expect(child.closest('.timeline__group')?.getAttribute('style')).toContain('16px')
  })

  it('keeps every event closed until it is expanded', () => {
    renderTimeline()

    const first = screen.getAllByRole('button', { name: /#/ })[0]
    expect(first?.getAttribute('aria-expanded')).toBe('false')
    expect(screen.queryByTestId('event-detail')).toBeNull()
  })

  it('expands exactly the selected event and reports the toggle', () => {
    const onToggleEvent = vi.fn()
    renderTimeline({ onToggleEvent })

    const rows = screen.getAllByRole('button', { name: /#/ })
    fireEvent.click(rows[1] as HTMLElement)

    expect(onToggleEvent).toHaveBeenCalledWith('evt-2')
  })

  it('labels status by shape so state is not carried by colour alone', () => {
    renderTimeline({ expandedEventId: 'evt-5' })

    // The failed event exposes a distinct shape plus a text label in its detail.
    const shapes = document.querySelectorAll('.status__shape')
    expect(shapes.length).toBeGreaterThan(0)
    const shapesUsed = new Set(
      Array.from(shapes).map((shape) => shape.textContent ?? ''),
    )
    expect(shapesUsed.size).toBeGreaterThan(1)
    expect(screen.getAllByText('Tool failed').length).toBeGreaterThan(0)
    expect(screen.getAllByText('Failed').length).toBeGreaterThan(0)
  })

  it('offers earlier events only while more remain', () => {
    const onLoadMore = vi.fn()
    const { unmount } = renderTimeline({ hasMore: true, onLoadMore })
    const loadMore = screen.getByRole('button', { name: 'Load earlier events' })
    fireEvent.click(loadMore)
    expect(onLoadMore).toHaveBeenCalled()
    unmount()

    renderTimeline()
    expect(screen.queryByRole('button', { name: 'Load earlier events' })).toBeNull()
  })

  it('disables paging while a load is in flight', () => {
    renderTimeline({ hasMore: true, loadingMore: true })

    expect(screen.getByRole('button', { name: 'Loading events' })).toBeTruthy()
  })
})
