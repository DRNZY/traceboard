// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import App from './App.svelte'
import { live } from './lib/live.svelte'
import { ApiClient } from './lib/api'

afterEach(() => {
  cleanup()
  live.stop()
  live.notices = []
  live.connection = 'idle'
  window.location.hash = ''
  vi.unstubAllGlobals()
})

beforeEach(() => {
  window.location.hash = '#/runs'
  // The shell opens a socket on mount; a stub keeps the test off the network.
  vi.stubGlobal(
    'WebSocket',
    class {
      static readonly CONNECTING = 0
      readonly readyState = 0
      onopen: (() => void) | null = null
      onmessage: ((event: { data: string }) => void) | null = null
      onerror: (() => void) | null = null
      onclose: (() => void) | null = null
      close(): void {}
    },
  )
})

function client(): ApiClient {
  const fetcher = async (input: string) => {
    const path = new URL(input, 'http://localhost').pathname
    if (path === '/api/v1/sources') return new Response('[]', { status: 200 })
    if (path === '/api/v1/projects') return new Response('[]', { status: 200 })
    if (path === '/api/v1/quarantine') return new Response('[]', { status: 200 })
    if (path === '/api/v1/runs') {
      return new Response(JSON.stringify({ runs: [], next_cursor: null }), { status: 200 })
    }
    if (path === '/api/v1/settings') {
      return new Response(
        JSON.stringify({
          version: '0.1.0',
          commit: 'abc1234',
          listen_address: '127.0.0.1:7345',
          retention_days: 30,
          keep_newest_per_project: 5,
          database_path: '/home/dev/.traceboard/traceboard.db',
          config_path: '/home/dev/.traceboard/config.json',
          sessions_valid: 1,
          ingest_token_configured: true,
          dashboard_token_configured: true,
        }),
        { status: 200 },
      )
    }
    return new Response('not found', { status: 404 })
  }
  return new ApiClient(fetcher)
}

describe('App shell', () => {
  it('shows the run index for the default route', async () => {
    render(App, { client: client() })
    await waitFor(() => expect(screen.getByText(/No runs recorded yet/)).toBeTruthy())
  })

  it('navigates between runs, sources, and settings by hash', async () => {
    render(App, { client: client() })
    await waitFor(() => expect(screen.getByText(/No runs recorded yet/)).toBeTruthy())

    window.location.hash = '#/settings'
    fireEvent(window, new HashChangeEvent('hashchange'))
    expect(await screen.findByText('Listen address')).toBeTruthy()

    window.location.hash = '#/sources'
    fireEvent(window, new HashChangeEvent('hashchange'))
    expect(await screen.findByText(/No sources are configured yet/)).toBeTruthy()
  })

  it('marks the current page for assistive technology', async () => {
    render(App, { client: client() })
    await waitFor(() => expect(screen.getByText(/No runs recorded yet/)).toBeTruthy())

    expect(screen.getByRole('link', { name: 'Runs' }).getAttribute('aria-current')).toBe('page')
    expect(screen.getByRole('link', { name: 'Sources' }).getAttribute('aria-current')).toBeNull()
  })

  it('reports the live connection state as text, not colour alone', async () => {
    render(App, { client: client() })
    await waitFor(() => expect(screen.getByText(/No runs recorded yet/)).toBeTruthy())

    // Driven from the feed, so the label follows the real state machine.
    live.connection = 'offline'
    await waitFor(() => expect(screen.getByText('Offline')).toBeTruthy())
    expect(document.querySelector('.shell__connection .status__shape')?.textContent).toBe('✕')
  })

  it('says a range is missing while the feed is recovering', async () => {
    // On the settings route no page drains notices, so the shell state is visible.
    window.location.hash = '#/settings'
    render(App, { client: client() })
    await waitFor(() => expect(screen.getByText('Listen address')).toBeTruthy())

    live.notices = [{ runId: 'run-1', afterSequence: 4, reason: 'sequence' }]
    await waitFor(() => expect(screen.getByText(/1 event range missing/)).toBeTruthy())

    live.notices = [
      { runId: 'run-1', afterSequence: 4, reason: 'sequence' },
      { runId: 'run-2', afterSequence: 9, reason: 'reconnect' },
    ]
    await waitFor(() => expect(screen.getByText(/2 event ranges missing/)).toBeTruthy())
  })

  it('opens the run named in the URL', async () => {
    window.location.hash = '#/runs/run-42'
    render(App, { client: client() })

    // The route is parsed even when the run itself cannot be fetched yet.
    await waitFor(() => expect(screen.getByText(/Select a run to read its timeline/)).toBeTruthy())
  })

  it('navigates with real links so a modified click still opens a tab', async () => {
    render(App, { client: client() })
    await waitFor(() => expect(screen.getByText(/No runs recorded yet/)).toBeTruthy())

    // The anchor keeps a real href, so the browser handles cmd/ctrl-click itself.
    const settings = screen.getByRole('link', { name: 'Settings' })
    expect(settings.getAttribute('href')).toBe('#/settings')
    expect(settings.tagName).toBe('A')
  })
})
