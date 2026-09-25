// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte'
import { afterEach, describe, expect, it, vi } from 'vitest'

import AlertBanner from './AlertBanner.svelte'
import type { Alert, AlertState } from '../lib/types'

afterEach(() => {
  cleanup()
})

const openAlert: Alert = {
  id: 'alert-7',
  type: 'source.disconnected',
  source: 'codex',
  message: 'codex missed three heartbeats',
  created_at: '2026-09-25T10:00:00Z',
  state: 'open' as AlertState,
}

describe('AlertBanner', () => {
  it('announces an open alert assertively', () => {
    render(AlertBanner, { alert: openAlert })

    const banner = screen.getByRole('alert')
    expect(banner.textContent ?? '').toContain('codex missed three heartbeats')
  })

  it('labels state with text and a shape, not colour alone', () => {
    render(AlertBanner, { alert: openAlert })

    expect(screen.getByText('Open')).toBeTruthy()
    expect(document.querySelector('.status__shape')?.textContent).toBe('◆')
  })

  it('acknowledges on request and reports the alert id', () => {
    const onAcknowledge = vi.fn()
    render(AlertBanner, { alert: openAlert, onAcknowledge })

    fireEvent.click(screen.getByRole('button', { name: 'Acknowledge' }))
    expect(onAcknowledge).toHaveBeenCalledWith('alert-7')
  })

  it('disables acknowledgement while the request is in flight', () => {
    render(AlertBanner, { alert: openAlert, onAcknowledge: vi.fn(), busy: true })

    expect(
      (screen.getByRole('button', { name: 'Acknowledging' }) as HTMLButtonElement).disabled,
    ).toBe(true)
  })

  it('offers no acknowledgement for an alert already handled', () => {
    render(AlertBanner, {
      alert: { ...openAlert, state: 'acknowledged' as AlertState, acknowledged_at: '2026-09-25T10:05:00Z' },
      onAcknowledge: vi.fn(),
    })

    expect(screen.queryByRole('button', { name: 'Acknowledge' })).toBeNull()
    expect(screen.getByText('Acknowledged')).toBeTruthy()
    expect(screen.getByText(/acknowledged 2026-09-25/)).toBeTruthy()
  })

  it('shows a run link only when the alert names a run', () => {
    const onSelectRun = vi.fn()
    const { unmount } = render(AlertBanner, { alert: { ...openAlert, run_id: 'run-9' }, onSelectRun })

    fireEvent.click(screen.getByRole('button', { name: 'Open run' }))
    expect(onSelectRun).toHaveBeenCalledWith('run-9')
    unmount()

    render(AlertBanner, { alert: openAlert, onSelectRun })
    expect(screen.queryByRole('button', { name: 'Open run' })).toBeNull()
  })

  it('reports an unrecognized state verbatim rather than assuming it is open', () => {
    render(AlertBanner, {
      alert: { ...openAlert, state: 'snoozed' as AlertState },
      onAcknowledge: vi.fn(),
    })

    expect(screen.getByText('snoozed')).toBeTruthy()
    expect(document.querySelector('.status__shape')?.textContent).toBe('?')
  })
})
