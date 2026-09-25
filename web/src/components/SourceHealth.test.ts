// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte'
import { afterEach, describe, expect, it, vi } from 'vitest'

import SourceHealth from './SourceHealth.svelte'
import type { Project, QuarantineEntry, Source } from '../lib/types'

afterEach(() => {
  cleanup()
})

const NOW = '2026-09-25T10:00:00Z'

const sources: Source[] = [
  {
    name: 'opencode',
    source_version: '1.4.2',
    capture_mode: 'metadata',
    connected: true,
    last_heartbeat_at: '2026-09-25T09:59:30Z',
    run_count: 12,
    quarantine_count: 0,
    created_at: '2026-09-01T00:00:00Z',
    updated_at: NOW,
  },
  {
    name: 'codex',
    source_version: '',
    capture_mode: 'off',
    connected: false,
    run_count: 0,
    quarantine_count: 1,
    created_at: '2026-09-01T00:00:00Z',
    updated_at: '2026-09-20T00:00:00Z',
  },
]

const projects: Project[] = [
  {
    id: 'traceboard',
    name: 'traceboard',
    path: '/home/dev/traceboard',
    run_count: 12,
    created_at: NOW,
    updated_at: NOW,
  },
  { id: 'bare', run_count: 1, created_at: NOW, updated_at: NOW },
]

const quarantine: QuarantineEntry[] = [
  {
    id: 1,
    source: 'codex',
    source_event_id: 'src-1',
    payload: '{"prompt": "[redacted]"}',
    reason: 'unknown event type',
    created_at: '2026-09-24T00:00:00Z',
  },
  {
    id: 2,
    payload: '{}',
    reason: 'missing run id',
    created_at: '2026-09-24T01:00:00Z',
  },
]

function renderHealth(overrides: Record<string, unknown> = {}) {
  const props = {
    sources,
    projects,
    quarantine,
    heartbeatSeconds: 30,
    now: NOW,
    savingSource: null,
    onSetCaptureMode: vi.fn(),
    ...overrides,
  }
  return { ...render(SourceHealth, props), props }
}

describe('SourceHealth', () => {
  it('says when no source is configured and how to fix it', () => {
    renderHealth({ sources: [], projects: [], quarantine: [] })

    expect(screen.getByText(/No sources are configured yet/)).toBeTruthy()
  })

  it('reports connection state as text and a shape', () => {
    renderHealth()

    expect(screen.getByText('Connected')).toBeTruthy()
    expect(screen.getByText('Disconnected')).toBeTruthy()
  })

  it('measures heartbeat age against now', () => {
    renderHealth()

    expect(screen.getByText(/2026-09-25 09:59:30 UTC \(\+30.0 s\)/)).toBeTruthy()
    expect(screen.getByText('never reported')).toBeTruthy()
  })

  it('derives the staleness threshold from the reported heartbeat interval', () => {
    renderHealth({ heartbeatSeconds: 30 })

    expect(screen.getAllByText('90s').length).toBe(2)
  })

  it('labels a source version the source never sent', () => {
    renderHealth()

    expect(screen.getByText('not reported')).toBeTruthy()
  })

  it('changes a capture mode through the page handler', () => {
    const onSetCaptureMode = vi.fn()
    renderHealth({ onSetCaptureMode })

    const select = screen.getByLabelText('Capture mode for opencode') as HTMLSelectElement
    fireEvent.change(select, { target: { value: 'detailed' } })

    expect(onSetCaptureMode).toHaveBeenCalledWith('opencode', 'detailed')
  })

  it('locks a selector while that source is saving', () => {
    renderHealth({ savingSource: 'opencode' })

    expect(
      (screen.getByLabelText('Capture mode for opencode') as HTMLSelectElement).disabled,
    ).toBe(true)
    expect((screen.getByLabelText('Capture mode for codex') as HTMLSelectElement).disabled).toBe(false)
  })

  it('groups quarantined events by source and keeps unattributed ones visible', () => {
    renderHealth()

    expect(screen.getByText(/1 quarantined event/)).toBeTruthy()
    expect(screen.getByText('unknown event type')).toBeTruthy()
    expect(screen.getByText(/\[redacted\]/)).toBeTruthy()
    // The entry with no source is not silently folded into another source.
    expect(screen.queryByText('missing run id')).toBeNull()
  })

  it('lists projects and labels the ones missing a name or path', () => {
    renderHealth()

    expect(screen.getByText('traceboard')).toBeTruthy()
    expect(screen.getByText('/home/dev/traceboard')).toBeTruthy()
    expect(screen.getByText('bare')).toBeTruthy()
    expect(screen.getByText('path not reported')).toBeTruthy()
  })

  it('says plainly when no project has been seen', () => {
    renderHealth({ projects: [] })

    expect(screen.getByText('no projects seen yet')).toBeTruthy()
  })
})
