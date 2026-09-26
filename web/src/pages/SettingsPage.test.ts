// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from '@testing-library/svelte'
import { afterEach, describe, expect, it, vi } from 'vitest'

import SettingsPage from './SettingsPage.svelte'
import { ApiClient } from '../lib/api'
import type { Settings, SpoolState } from '../lib/types'

afterEach(() => {
  cleanup()
})

function clientReturning(settings: Partial<Settings>): ApiClient {
  return new ApiClient(
    async () =>
      new Response(JSON.stringify(settings), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    {},
  )
}

const base: Settings = {
  version: 'test',
  commit: 'none',
  listen_address: '127.0.0.1:47821',
  retention_days: 0,
  keep_newest_per_project: 0,
  database_path: '/tmp/traceboard.db',
  config_path: '/tmp/config.json',
  sessions_valid: 1,
  ingest_token_configured: true,
  dashboard_token_configured: true,
}

const healthy: SpoolState = {
  source: 'opencode',
  events: 12,
  bytes: 4096,
  limit_bytes: 1048576,
  dropped: 0,
  at_risk: false,
}

const atRisk: SpoolState = {
  source: 'claude',
  events: 3,
  bytes: 1048576,
  limit_bytes: 1048576,
  dropped: 7,
  at_risk: true,
  at_risk_since: '2026-09-25T09:00:00Z',
}

describe('SettingsPage offline spool panel', () => {
  it('says nothing is buffered when the spool list is empty', async () => {
    render(SettingsPage, { props: { client: clientReturning({ ...base, spool: [] }) } })

    await waitFor(() => expect(screen.getByText('Offline spool')).toBeTruthy())
    expect(screen.getByText(/Nothing is buffered/)).toBeTruthy()
    expect(screen.queryByText(/at the size limit/)).toBeNull()
  })

  it('says nothing is buffered when the collector omits the spool field entirely', async () => {
    render(SettingsPage, { props: { client: clientReturning({ ...base }) } })

    await waitFor(() => expect(screen.getByText('Offline spool')).toBeTruthy())
    expect(screen.getByText(/Nothing is buffered/)).toBeTruthy()
  })

  it('lists a healthy buffered source with its event count and byte usage', async () => {
    render(SettingsPage, { props: { client: clientReturning({ ...base, spool: [healthy] }) } })

    await waitFor(() => expect(screen.getByText('opencode')).toBeTruthy())
    expect(screen.getByText(/12 events/)).toBeTruthy()
    expect(screen.getByText(/4 KiB of 1\.0 MiB/)).toBeTruthy()
    expect(screen.getByText(/drains on the next collector start/)).toBeTruthy()
    expect(screen.queryByText(/at the size limit/)).toBeNull()
  })

  it('flags a source at its size limit and reports the dropped count', async () => {
    render(SettingsPage, { props: { client: clientReturning({ ...base, spool: [healthy, atRisk] }) } })

    await waitFor(() => expect(screen.getByText('claude')).toBeTruthy())
    expect(screen.getByText(/at the size limit; 7 dropped/)).toBeTruthy()
    // The healthy source is still listed, so one bad source does not hide the rest.
    expect(screen.getByText('opencode')).toBeTruthy()
    expect(screen.getByText(/drains on the next collector start/)).toBeTruthy()
  })

  it('renders a spool state that omits the optional timestamps', async () => {
    const minimal: SpoolState = {
      source: 'codex',
      events: 1,
      bytes: 10,
      limit_bytes: 1048576,
      dropped: 0,
      at_risk: false,
    }
    render(SettingsPage, { props: { client: clientReturning({ ...base, spool: [minimal] }) } })

    await waitFor(() => expect(screen.getByText('codex')).toBeTruthy())
    expect(screen.getByText(/1 events/)).toBeTruthy()
  })

  it('still surfaces a read failure instead of claiming the spool is empty', async () => {
    const failing = new ApiClient(vi.fn(async () => new Response('nope', { status: 500 })), {})
    render(SettingsPage, { props: { client: failing } })

    await waitFor(() =>
      expect(screen.getByRole('alert').textContent ?? '').toMatch(/could not be read/i),
    )
    // The panel must not claim a healthy empty spool when the request failed.
    expect(screen.queryByText(/Nothing is buffered/)).toBeNull()
  })
})
