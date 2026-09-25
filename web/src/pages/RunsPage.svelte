<script lang="ts">
  import type { ApiClient } from '../lib/api'
  import type {
    Alert,
    EventRecord,
    Run,
    RunDetail,
    RunFilter,
    Step,
  } from '../lib/types'
  import { ApiError } from '../lib/api'
  import { live } from '../lib/live.svelte'
  import type { ServerMessage } from '../lib/types'
  import RunIndex from '../components/RunIndex.svelte'
  import RunInspector from '../components/RunInspector.svelte'
  import AlertBanner from '../components/AlertBanner.svelte'

  interface Props {
    client: ApiClient
    initialRunId?: string | null
  }

  let { client, initialRunId = null }: Props = $props()

  const runPageSize = 50
  const eventPageSize = 500

  let runs = $state<Run[]>([])
  let nextCursor = $state<string | null>(null)
  let filter = $state<RunFilter>({})
  let loading = $state(false)
  let loadingMore = $state(false)
  let indexError = $state<string | null>(null)
  let sources = $state<Array<{ name: string }>>([])
  let projects = $state<Array<{ id: string; name?: string | null }>>([])

  let selectedRunId = $state<string | null>(null)
  let detail = $state<RunDetail | null>(null)
  let runEvents = $state<EventRecord[]>([])
  let steps = $state<Step[]>([])
  let alerts = $state<Alert[]>([])
  let expandedEventId = $state<string | null>(null)
  let replayIndex = $state(0)
  let appliedSequence = $state<number | null>(0)
  let eventsIncomplete = $state(false)
  let hasMoreEvents = $state(false)
  let loadingMoreEvents = $state(false)
  let acknowledgingAlertId = $state<string | null>(null)
  let notice = $state<string | null>(null)

  function describe(caught: unknown, fallback: string): string {
    if (caught instanceof ApiError) return caught.message
    return fallback
  }

  async function loadIndex(): Promise<void> {
    loading = true
    indexError = null
    try {
      const page = await client.listRuns(filter)
      runs = page.runs
      nextCursor = page.next_cursor
    } catch (caught) {
      indexError = describe(caught, 'The run index could not be read')
    } finally {
      loading = false
    }
  }

  async function loadMore(): Promise<void> {
    if (!nextCursor || loadingMore) return
    loadingMore = true
    try {
      const page = await client.listRuns(filter, nextCursor)
      runs = [...runs, ...page.runs]
      nextCursor = page.next_cursor
    } catch (caught) {
      indexError = describe(caught, 'The next page of runs could not be read')
    } finally {
      loadingMore = false
    }
  }

  async function loadFilterOptions(): Promise<void> {
    try {
      const [sourceList, projectList] = await Promise.all([client.listSources(), client.listProjects()])
      sources = sourceList.map((source) => ({ name: source.name }))
      projects = projectList.map((project) => ({ id: project.id, name: project.name }))
    } catch {
      // The index stays usable when the supporting lists cannot be read.
    }
  }

  async function openRun(runId: string): Promise<void> {
    selectedRunId = runId
    detail = null
    runEvents = []
    steps = []
    alerts = []
    expandedEventId = null
    appliedSequence = 0
    try {
      const [runDetail, eventPage, runAlerts] = await Promise.all([
        client.getRun(runId),
        client.listEvents(runId, 0, eventPageSize),
        client.listAlerts({ run_id: runId, limit: 50 }),
      ])
      detail = runDetail
      steps = runDetail.steps ?? []
      alerts = runAlerts
      runEvents = eventPage.events
      appliedSequence = eventPage.next_sequence
      hasMoreEvents = eventPage.has_more
      eventsIncomplete = eventPage.next_sequence < eventPage.run_sequence
      replayIndex = Math.max(0, eventPage.events.length - 1)
      live.watch(runId, eventPage.next_sequence)
    } catch (caught) {
      detail = null
      indexError = describe(caught, 'That run could not be opened')
    }
  }

  async function loadMoreEvents(): Promise<void> {
    if (!selectedRunId || loadingMoreEvents) return
    loadingMoreEvents = true
    try {
      const page = await client.listEvents(selectedRunId, appliedSequence ?? 0, eventPageSize)
      runEvents = [...runEvents, ...page.events]
      appliedSequence = page.next_sequence
      hasMoreEvents = page.has_more
      eventsIncomplete = page.next_sequence < page.run_sequence
    } catch (caught) {
      notice = describe(caught, 'Earlier events could not be read')
    } finally {
      loadingMoreEvents = false
    }
  }

  async function acknowledgeAlert(alertId: string): Promise<void> {
    acknowledgingAlertId = alertId
    try {
      const updated = await client.acknowledgeAlert(alertId)
      alerts = alerts.map((alert) => (alert.id === updated.id ? updated : alert))
    } catch (caught) {
      notice = describe(caught, 'The alert could not be acknowledged')
    } finally {
      acknowledgingAlertId = null
    }
  }

  async function exportRun(runId: string): Promise<void> {
    try {
      const result = await client.exportRun(runId, 'json', true)
      notice = `Exported ${result.path} (${result.bytes.toLocaleString('en-US')} bytes)`
    } catch (caught) {
      notice = describe(caught, 'The run could not be exported')
    }
  }

  async function deleteRun(runId: string): Promise<void> {
    try {
      const result = await client.deleteRun(runId)
      live.forget(runId)
      notice = `Deleted ${result.events} events and ${result.search_entries} search entries.`
      detail = null
      runEvents = []
      alerts = []
      selectedRunId = null
      await loadIndex()
    } catch (caught) {
      notice = describe(caught, 'The run could not be deleted')
    }
  }

  function onFilterChange(next: RunFilter): void {
    filter = next
    void loadIndex()
  }

  function onServerMessage(message: ServerMessage): void {
    if (message.kind === 'event.appended' && message.run_id === selectedRunId) {
      // The page refetches the range the feed named; nothing is assumed.
      void recover(selectedRunId, appliedSequence ?? 0)
      return
    }
    if (message.kind === 'run.deleted') {
      if (message.run_id === selectedRunId) {
        selectedRunId = null
        detail = null
        runEvents = []
      }
      void loadIndex()
      return
    }
    if (message.kind === 'run.updated' || message.kind === 'source.updated') {
      void loadIndex()
    }
  }

  /** Refetches exactly the range a gap notice names, then acknowledges it. */
  async function recover(runId: string | null, afterSequence: number): Promise<void> {
    if (runId === null) {
      await Promise.all([loadIndex(), loadFilterOptions()])
      live.recovered()
      live.clearNotice({ runId: null, afterSequence })
      return
    }
    if (runId !== selectedRunId) {
      live.clearNotice({ runId, afterSequence })
      return
    }
    appliedSequence = null
    try {
      const page = await client.listEvents(runId, afterSequence, eventPageSize)
      const known = new Set(runEvents.map((event) => event.event_id))
      runEvents = [...runEvents, ...page.events.filter((event) => !known.has(event.event_id))]
      appliedSequence = page.next_sequence
      eventsIncomplete = page.next_sequence < page.run_sequence
      live.acknowledge(runId, page.next_sequence)
    } catch (caught) {
      notice = describe(caught, 'The missing events could not be refetched')
    } finally {
      live.clearNotice({ runId, afterSequence })
      live.recovered()
    }
  }

  const unsubscribe = live.subscribe(onServerMessage)

  // A gap notice is a standing claim that a range is missing, so the page
  // refetches exactly the range it names and clears only that notice.
  $effect(() => {
    const pending = [...live.notices]
    for (const pendingNotice of pending) {
      void recover(pendingNotice.runId, pendingNotice.afterSequence)
    }
  })

  $effect(() => {
    void loadIndex()
    void loadFilterOptions()
  })

  $effect(() => {
    if (initialRunId) void openRun(initialRunId)
  })

  $effect(() => () => unsubscribe())
</script>

<div class="page">
  {#if notice}
    <p class="page__notice" role="status">{notice}</p>
  {/if}

  <div class="page__workspace">
    <div class="page__index">
      <RunIndex
        {runs}
        {filter}
        filterOptions={{ sources: sources.map((source) => source.name), projects }}
        {selectedRunId}
        {loading}
        {loadingMore}
        {nextCursor}
        error={indexError}
        onFilterChange={onFilterChange}
          onSelectRun={openRun}
          onLoadMore={loadMore}
        />
    </div>

    <div class="page__inspector">
      <RunInspector
        run={detail?.run ?? null}
        {steps}
        {runEvents}
        {alerts}
        {expandedEventId}
        {replayIndex}
        {appliedSequence}
        {eventsIncomplete}
        hasMoreEvents={hasMoreEvents}
        loadingMore={loadingMoreEvents}
        onToggleEvent={(eventId) => (expandedEventId = expandedEventId === eventId ? null : eventId)}
        onReplayIndexChange={(index) => (replayIndex = index)}
        onLoadMoreEvents={loadMoreEvents}
        onSelectRun={openRun}
        onExport={exportRun}
        onDelete={deleteRun}
        onAcknowledge={acknowledgeAlert}
        {acknowledgingAlertId}
      />
    </div>
  </div>

  {#each alerts.filter((alert) => alert.state === 'open').slice(0, 3) as alert (alert.id)}
    <AlertBanner
      {alert}
      onAcknowledge={acknowledgeAlert}
      onSelectRun={openRun}
      busy={acknowledgingAlertId === alert.id}
    />
  {/each}
</div>

<style>
  .page {
    display: grid;
    gap: var(--step-4);
    align-content: start;
  }

  .page__workspace {
    display: grid;
    grid-template-columns: minmax(320px, 26rem) minmax(0, 1fr);
    gap: var(--step-6);
  }

  .page__inspector {
    padding-left: var(--step-6);
    border-left: 1px solid var(--rule-strong);
  }

  .page__notice {
    margin: 0;
    padding: var(--step-2) var(--step-3);
    border-left: 4px solid var(--accent);
    color: var(--accent);
  }

  @media (max-width: 72rem) {
    .page__workspace {
      grid-template-columns: 1fr;
    }

    .page__inspector {
      padding-left: 0;
      padding-top: var(--step-6);
      border-left: 0;
      border-top: 1px solid var(--rule-strong);
    }
  }
</style>
