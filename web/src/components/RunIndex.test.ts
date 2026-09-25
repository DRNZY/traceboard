// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte'
import { afterEach, describe, expect, it, vi } from 'vitest'

import RunIndex from './RunIndex.svelte'
import type { Run, RunStatus } from '../lib/types'

afterEach(() => {
  cleanup()
})

function makeRun(overrides: Partial<Run> & { id: string }): Run {
  return {
    source: 'opencode',
    status: 'completed' as RunStatus,
    event_count: 4,
    error_count: 0,
    next_sequence: 5,
    capture_modes: ['metadata'],
    models: [],
    open_alerts: 0,
    subagent_runs: 0,
    created_at: '2026-09-25T10:00:00Z',
    updated_at: '2026-09-25T10:00:01Z',
    ...overrides,
  }
}

const runs: Run[] = [
  makeRun({
    id: 'run-failed',
    source: 'claude-code',
    project_id: 'traceboard',
    project_name: 'traceboard',
    title: 'fix the cursor pagination bug',
    status: 'failed',
    started_at: '2026-09-25T10:00:00Z',
    ended_at: '2026-09-25T10:00:42Z',
    duration_ms: 42_000,
    event_count: 91,
    error_count: 2,
    open_alerts: 1,
    capture_modes: ['detailed'],
  }),
  makeRun({
    id: 'run-bare',
    source: 'codex',
    status: 'unknown',
    capture_modes: ['off'],
  }),
]

function renderIndex(overrides: Record<string, unknown> = {}) {
  const props = {
    runs,
    filter: {},
    filterOptions: { sources: ['claude-code', 'codex', 'opencode'], projects: [] },
    selectedRunId: null,
    loading: false,
    loadingMore: false,
    nextCursor: null,
    error: null,
    onFilterChange: vi.fn(),
    onSelectRun: vi.fn(),
    onLoadMore: vi.fn(),
    ...overrides,
  }
  return { props, ...render(RunIndex, props) }
}

describe('RunIndex filtering', () => {
  it('reports the trimmed search query without re-requesting on every keystroke', async () => {
    const { props } = renderIndex()

    await fireEvent.input(screen.getByLabelText('Search runs'), { target: { value: '  parser  ' } })

    expect(props.onFilterChange).toHaveBeenCalledWith({ query: 'parser' })
  })

  it('reports each filter field separately so a change cannot clear the others', async () => {
    const { props } = renderIndex({ filter: { query: 'parser', source: 'codex' } })

    await fireEvent.change(screen.getByLabelText('Status'), { target: { value: 'failed' } })
    expect(props.onFilterChange).toHaveBeenLastCalledWith({ query: 'parser', source: 'codex', status: 'failed' })

    await fireEvent.change(screen.getByLabelText('Alert state'), { target: { value: 'open' } })
    expect(props.onFilterChange).toHaveBeenLastCalledWith({
      query: 'parser',
      source: 'codex',
      status: 'failed',
      alert_state: 'open',
    })
  })

  it('clears the search box through its own control', async () => {
    const { props } = renderIndex({ filter: { query: 'parser' } })

    await fireEvent.click(screen.getByRole('button', { name: 'Clear search' }))

    expect(props.onFilterChange).toHaveBeenCalledWith({})
  })

  it('offers a reset that drops every filter at once', async () => {
    const { props } = renderIndex({
      filter: { query: 'parser', source: 'codex', status: 'failed', alert_state: 'open' },
    })

    await fireEvent.click(screen.getByRole('button', { name: 'Reset filters' }))

    expect(props.onFilterChange).toHaveBeenCalledWith({})
  })
})

describe('RunIndex list', () => {
  it('selects a run with a keyboard-reachable button', async () => {
    const { props } = renderIndex()

    const row = screen.getByRole('button', { name: /fix the cursor pagination bug/ })
    await fireEvent.click(row)

    expect(props.onSelectRun).toHaveBeenCalledWith('run-failed')
  })

  it('marks the selected run for assistive technology and for sighted readers', () => {
    renderIndex({ selectedRunId: 'run-failed' })

    const row = screen.getByRole('button', { name: /fix the cursor pagination bug/ })
    expect(row.getAttribute('aria-current')).toBe('true')
    expect(row.className).toContain('run-row--selected')
  })

  it('shows status as a word and a shape, never colour alone', () => {
    renderIndex()

    const row = screen.getByRole('button', { name: /fix the cursor pagination bug/ })
    const status = row.querySelector('.status')
    expect(status?.textContent).toContain('Failed')
    expect(status?.querySelector('.status__shape')?.textContent?.trim()).toBe('✕')

    const bare = screen.getByRole('button', { name: /codex/ })
    expect(bare.querySelector('.status')?.textContent).toContain('Unknown')
  })

  it('labels missing fields instead of leaving blanks', () => {
    renderIndex()

    const bare = screen.getByRole('button', { name: /codex/ })
    expect(bare.textContent).toContain('no project')
    expect(bare.textContent).toContain('no prompt captured')
    expect(bare.textContent).toContain('not reported')
  })

  it('reports duration, event count, capture mode, and alert state', () => {
    renderIndex()

    const row = screen.getByRole('button', { name: /fix the cursor pagination bug/ })
    expect(row.textContent).toContain('42.0 s')
    expect(row.textContent).toContain('91 events')
    expect(row.textContent).toContain('detailed')
    expect(row.textContent).toContain('1 open alert')
  })

  it('shows an empty state that names the active filters', () => {
    renderIndex({ runs: [], filter: { query: 'nothing' } })

    expect(screen.getByText(/no runs match/i)).toBeTruthy()
  })

  it('surfaces a load failure', () => {
    renderIndex({ runs: [], error: 'Traceboard could not complete that request' })

    expect(screen.getByRole('alert').textContent).toContain('could not complete')
  })
})

describe('RunIndex pagination', () => {
  it('offers the next page only when the server sent a cursor', async () => {
    const { props, rerender } = renderIndex({ nextCursor: 'cursor-2' })

    await fireEvent.click(screen.getByRole('button', { name: 'Load more runs' }))
    expect(props.onLoadMore).toHaveBeenCalledTimes(1)

    await rerender({ nextCursor: null })
    expect(screen.queryByRole('button', { name: 'Load more runs' })).toBeNull()
  })

  it('disables the control while a page is in flight', () => {
    renderIndex({ nextCursor: 'cursor-2', loadingMore: true })

    const button = screen.getByRole('button', { name: 'Loading more runs' })
    expect((button as HTMLButtonElement).disabled).toBe(true)
  })
})
