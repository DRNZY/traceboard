<script lang="ts">
  import { defaultClient, type ApiClient } from './lib/api'
  import { live } from './lib/live.svelte'
  import type { ConnectionState } from './lib/types'
  import RunsPage from './pages/RunsPage.svelte'
  import SourcesPage from './pages/SourcesPage.svelte'
  import SettingsPage from './pages/SettingsPage.svelte'

  interface Props {
    client?: ApiClient
  }

  let { client = defaultClient }: Props = $props()

  type View = 'runs' | 'sources' | 'settings'

  const SHAPES: Record<ConnectionState, string> = {
    idle: '·',
    connecting: '›',
    live: '◆',
    recovering: '◐',
    offline: '✕',
  }

  const LABELS: Record<ConnectionState, string> = {
    idle: 'Idle',
    connecting: 'Connecting',
    live: 'Live',
    recovering: 'Recovering',
    offline: 'Offline',
  }

  let route = $state(parseRoute())
  let connection = $state<ConnectionState>(live.connection)

  function parseRoute(): { view: View; runId: string | null } {
    if (typeof window === 'undefined') return { view: 'runs', runId: null }
    const hash = window.location.hash.replace(/^#\/?/, '')
    const [view, runId] = hash.split('/')
    if (view === 'sources') return { view: 'sources', runId: null }
    if (view === 'settings') return { view: 'settings', runId: null }
    return { view: 'runs', runId: runId || null }
  }

  const currentLabel = $derived(LABELS[connection] ?? String(connection))
  const currentShape = $derived(SHAPES[connection] ?? '?')
  const missingRanges = $derived(live.notices.length)

  $effect(() => {
    const onHashChange = () => {
      route = parseRoute()
    }
    window.addEventListener('hashchange', onHashChange)
    return () => window.removeEventListener('hashchange', onHashChange)
  })

  $effect(() => {
    live.start()
    const unsubscribe = live.subscribe(() => {
      connection = live.connection
    })
    connection = live.connection
    return () => {
      unsubscribe()
    }
  })
</script>

<svelte:head>
  <title>Traceboard</title>
  <meta name="description" content="Local-first observability for coding-agent runs." />
</svelte:head>

<div class="shell">
  <header class="shell__masthead">
    <a class="shell__brand" href="#/runs">
      <span class="display shell__wordmark">Traceboard</span>
      <span class="label shell__tagline">Local agent observability</span>
    </a>

    <nav class="shell__nav" aria-label="Sections">
      <a class="shell__link" href="#/runs" aria-current={route.view === 'runs' ? 'page' : undefined}>
        Runs
      </a>
      <a
        class="shell__link"
        href="#/sources"
        aria-current={route.view === 'sources' ? 'page' : undefined}
      >
        Sources
      </a>
      <a
        class="shell__link"
        href="#/settings"
        aria-current={route.view === 'settings' ? 'page' : undefined}
      >
        Settings
      </a>
    </nav>

    <div class="shell__status">
      <span class="status shell__connection">
        <span class="status__shape" aria-hidden="true">{currentShape}</span>
        {currentLabel}
      </span>
      {#if missingRanges > 0}
        <span class="shell__gap" role="status">
          {missingRanges} event range{missingRanges === 1 ? '' : 's'} missing
        </span>
      {/if}
    </div>
  </header>

  <main class="shell__main">
    {#if route.view === 'runs'}
      <RunsPage {client} initialRunId={route.runId} />
    {:else if route.view === 'sources'}
      <SourcesPage {client} />
    {:else}
      <SettingsPage {client} />
    {/if}
  </main>

  <footer class="shell__footer">
    <p class="label">Loopback only · no external requests · redaction runs before every write</p>
  </footer>
</div>

<style>
  .shell {
    display: grid;
    grid-template-rows: auto 1fr auto;
    min-height: 100dvh;
  }

  .shell__masthead {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto auto;
    gap: var(--step-6);
    align-items: center;
    padding: var(--step-3) var(--step-6);
    border-bottom: 1px solid var(--rule-strong);
  }

  .shell__brand {
    display: flex;
    gap: var(--step-3);
    align-items: baseline;
    text-decoration: none;
  }

  .shell__wordmark {
    font-size: 20px;
    line-height: 1;
  }

  .shell__tagline {
    color: var(--bone-dim);
  }

  .shell__nav {
    display: flex;
    gap: var(--step-1);
  }

  .shell__link {
    padding: var(--step-1) var(--step-3);
    border: 1px solid transparent;
    font-family: var(--mono);
    font-size: 12px;
    letter-spacing: 0.1em;
    text-transform: uppercase;
    text-decoration: none;
    color: var(--bone-dim);
  }

  .shell__link[aria-current='page'] {
    border-color: var(--rule-strong);
    color: var(--bone);
  }

  .shell__link:hover {
    border-color: var(--rule-strong);
    color: var(--bone);
  }

  .shell__status {
    display: flex;
    gap: var(--step-3);
    align-items: baseline;
  }

  .shell__gap {
    padding: var(--step-1) var(--step-2);
    border: 1px solid var(--accent);
    color: var(--accent);
    font-family: var(--mono);
    font-size: 11px;
  }

  .shell__main {
    padding: var(--step-6);
  }

  .shell__footer {
    padding: var(--step-3) var(--step-6);
    border-top: 1px solid var(--rule);
  }

  .shell__footer p {
    margin: 0;
    color: var(--bone-dim);
  }

  @media (max-width: 56rem) {
    .shell__masthead {
      grid-template-columns: 1fr;
      gap: var(--step-3);
    }

    .shell__main {
      padding: var(--step-4);
    }
  }
</style>
