<script lang="ts">
  import { untrack } from 'svelte'

  import type { Run, RunFilter, RunStatus } from '../lib/types'
  import {
    formatCount,
    formatDuration,
    formatStatus,
    formatTimestamp,
  } from '../lib/format'

  interface FilterOptions {
    sources: string[]
    projects: Array<{ id: string; name?: string | null }>
  }

  interface Props {
    runs: Run[]
    filter: RunFilter
    filterOptions: FilterOptions
    selectedRunId: string | null
    loading: boolean
    loadingMore: boolean
    nextCursor: string | null
    error: string | null
    onFilterChange: (filter: RunFilter) => void
    onSelectRun: (runId: string) => void
    onLoadMore: () => void
  }

  let {
    runs,
    filter,
    filterOptions,
    selectedRunId,
    loading,
    loadingMore,
    nextCursor,
    error,
    onFilterChange,
    onSelectRun,
    onLoadMore,
  }: Props = $props()

  // The controls merge against the newest value the user chose rather than the
  // prop as it stood when the component last rendered, so two quick edits never
  // drop one another. Seeding is untracked on purpose: the prop stays the
  // authority, and the effect below is what re-syncs after the page answers.
  let current = $state<RunFilter>(untrack(() => ({ ...filter })))
  $effect.pre(() => {
    current = { ...filter }
  })

  const STATUSES: RunStatus[] = ['started', 'completed', 'failed', 'cancelled', 'incomplete', 'unknown']
  const CAPTURE_MODES = ['off', 'metadata', 'detailed'] as const

  function commit(next: RunFilter): void {
    current = next
    onFilterChange(next)
  }

  function merge(changes: Partial<RunFilter>): void {
    for (const [key, value] of Object.entries(changes)) {
      if (value === undefined) delete (current as Record<string, unknown>)[key]
    }
    commit({ ...current, ...changes })
  }

  function setQuery(value: string): void {
    const query = value.trim()
    if (query) {
      commit({ ...current, query })
      return
    }
    const { query: _dropped, ...rest } = current
    commit(rest)
  }

  function activeFilters(): string[] {
    const names: string[] = []
    if (current.query) names.push(`"${current.query}"`)
    if (current.source) names.push(`source ${current.source}`)
    if (current.project_id) names.push(`project ${current.project_id}`)
    if (current.status) names.push(`status ${current.status}`)
    if (current.capture_mode) names.push(`capture ${current.capture_mode}`)
    if (current.alert_state) names.push(`alerts ${current.alert_state}`)
    return names
  }

  function runTitle(run: Run): string {
    const title = run.title?.trim()
    return title && title.length > 0 ? title : 'no prompt captured'
  }
</script>

<section class="index" aria-labelledby="run-index-heading">
  <header class="index__header">
    <div>
      <p class="label" id="run-index-heading">Run index</p>
      <p class="index__count">
        {formatCount(runs.length)} run{runs.length === 1 ? '' : 's'} shown
      </p>
    </div>
    <button class="button" type="button" onclick={() => onFilterChange({})}>Reset filters</button>
  </header>

  <div class="index__filters">
    <div class="field">
      <label class="label" for="run-search">Search runs</label>
      <div class="search">
        <input
          id="run-search"
          class="field__input"
          type="search"
          placeholder="prompt, tool, model, path, error"
          value={current.query ?? ''}
          oninput={(event) => setQuery(event.currentTarget.value)}
        />
        <button
          class="button button--inline"
          type="button"
          aria-label="Clear search"
          disabled={!current.query}
          onclick={() => setQuery('')}
        >
          Clear
        </button>
      </div>
    </div>

    <div class="index__grid">
      <div class="field">
        <label class="label" for="filter-source">Source</label>
        <select
          id="filter-source"
          class="field__select"
          value={current.source ?? ''}
          onchange={(event) =>
            merge({ source: event.currentTarget.value || undefined })}
        >
          <option value="">Any source</option>
          {#each filterOptions.sources as source (source)}
            <option value={source}>{source}</option>
          {/each}
        </select>
      </div>

      <div class="field">
        <label class="label" for="filter-project">Project</label>
        <select
          id="filter-project"
          class="field__select"
          value={current.project_id ?? ''}
          onchange={(event) =>
            merge({ project_id: event.currentTarget.value || undefined })}
        >
          <option value="">Any project</option>
          {#each filterOptions.projects as project (project.id)}
            <option value={project.id}>{project.name ?? project.id}</option>
          {/each}
        </select>
      </div>

      <div class="field">
        <label class="label" for="filter-status">Status</label>
        <select
          id="filter-status"
          class="field__select"
          value={current.status ?? ''}
          onchange={(event) =>
            merge({ status: (event.currentTarget.value || undefined) as RunStatus | undefined })}
        >
          <option value="">Any status</option>
          {#each STATUSES as status (status)}
            <option value={status}>{formatStatus(status).text}</option>
          {/each}
        </select>
      </div>

      <div class="field">
        <label class="label" for="filter-capture">Capture mode</label>
        <select
          id="filter-capture"
          class="field__select"
          value={current.capture_mode ?? ''}
          onchange={(event) =>
            merge({
              capture_mode: (event.currentTarget.value || undefined) as
                | 'off'
                | 'metadata'
                | 'detailed'
                | undefined,
            })}
        >
          <option value="">Any capture mode</option>
          {#each CAPTURE_MODES as mode (mode)}
            <option value={mode}>{mode}</option>
          {/each}
        </select>
      </div>

      <div class="field">
        <label class="label" for="filter-alert">Alert state</label>
        <select
          id="filter-alert"
          class="field__select"
          value={current.alert_state ?? ''}
          onchange={(event) =>
            merge({
              alert_state: (event.currentTarget.value || undefined) as 'open' | 'none' | undefined,
            })}
        >
          <option value="">Any alert state</option>
          <option value="open">Open alert</option>
          <option value="none">No open alert</option>
        </select>
      </div>
    </div>
  </div>

  {#if error}
    <p class="index__error" role="alert">{error}</p>
  {/if}

  {#if runs.length === 0}
    <p class="index__empty">
      {#if activeFilters().length > 0}
        No runs match {activeFilters().join(', ')}. Reset the filters to see everything captured so far.
      {:else if loading}
        Reading the local store…
      {:else}
        No runs recorded yet. Point an agent at <code>traceboard configure</code> and run it.
      {/if}
    </p>
  {:else}
    <ul class="index__list">
      {#each runs as run (run.id)}
        {@const status = formatStatus(run.status)}
        {@const alertText =
          run.open_alerts === 1 ? '1 open alert' : `${formatCount(run.open_alerts)} open alerts`}
        <li>
          <button
            class="run-row"
            class:run-row--selected={run.id === selectedRunId}
            type="button"
            aria-current={run.id === selectedRunId ? 'true' : undefined}
            onclick={() => onSelectRun(run.id)}
          >
            <span class="run-row__head">
              <span class="status status--{status.tone}">
                <span class="status__shape" aria-hidden="true">{status.shape}</span>
                {status.text}
              </span>
              <span class="run-row__source">{run.source}</span>
            </span>

            <span class="run-row__title">{runTitle(run)}</span>

            <span class="run-row__facts">
              <span>{run.project_name ?? 'no project'}</span>
              <span aria-hidden="true">·</span>
              <span>{formatCount(run.event_count)} events</span>
              <span aria-hidden="true">·</span>
              <span>{formatDuration(run.duration_ms)}</span>
              <span aria-hidden="true">·</span>
              <span>{(run.capture_modes ?? []).join(' + ') || 'not reported'}</span>
            </span>

            <span class="run-row__facts">
              <span>{formatTimestamp(run.started_at ?? run.last_event_at)}</span>
              {#if run.open_alerts > 0}
                <span aria-hidden="true">·</span>
                <span class="run-row__alerts">{alertText}</span>
              {/if}
              {#if run.error_count > 0}
                <span aria-hidden="true">·</span>
                <span>{formatCount(run.error_count)} errors</span>
              {/if}
            </span>
          </button>
        </li>
      {/each}
    </ul>

    {#if nextCursor}
      <div class="index__more">
        <button class="button" type="button" onclick={onLoadMore} disabled={loadingMore}>
          {loadingMore ? 'Loading more runs' : 'Load more runs'}
        </button>
      </div>
    {/if}
  {/if}
</section>

<style>
  .index {
    display: grid;
    gap: var(--step-4);
    align-content: start;
  }

  .index__header {
    display: flex;
    align-items: flex-end;
    justify-content: space-between;
    gap: var(--step-3);
    padding-bottom: var(--step-2);
    border-bottom: 1px solid var(--rule-strong);
  }

  .index__count {
    margin: var(--step-1) 0 0;
    font-family: var(--display);
    font-size: 26px;
    line-height: 1;
    text-transform: uppercase;
  }

  .index__filters {
    display: grid;
    gap: var(--step-3);
  }

  .search {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    gap: var(--step-1);
  }

  .index__grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(130px, 1fr));
    gap: var(--step-2);
  }

  .index__error {
    margin: 0;
    padding: var(--step-2) var(--step-3);
    border: 1px solid var(--accent);
    color: var(--accent);
  }

  .index__empty {
    margin: 0;
    padding: var(--step-6) 0;
    color: var(--bone-dim);
  }

  .index__list {
    display: grid;
    gap: 0;
    margin: 0;
    padding: 0;
    list-style: none;
    border-top: 1px solid var(--rule);
  }

  .run-row {
    display: grid;
    gap: var(--step-1);
    width: 100%;
    padding: var(--step-3) var(--step-2) var(--step-3) var(--step-2);
    background: transparent;
    border: 0;
    border-bottom: 1px solid var(--rule);
    border-left: 4px solid transparent;
    text-align: left;
    color: inherit;
  }

  .run-row:hover {
    background: var(--ink-raised);
    border-left-color: var(--rule-strong);
  }

  .run-row--selected {
    background: var(--ink-raised);
    border-left-color: var(--accent);
  }

  .run-row__head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--step-2);
  }

  .run-row__source {
    font-family: var(--mono);
    font-size: 11px;
    color: var(--bone-dim);
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }

  .run-row__title {
    font-family: var(--display);
    font-size: 16px;
    line-height: 1.2;
  }

  .run-row__facts {
    display: flex;
    flex-wrap: wrap;
    gap: var(--step-1);
    font-family: var(--mono);
    font-size: 11px;
    color: var(--bone-dim);
  }

  .run-row__alerts {
    color: var(--accent);
  }

  .index__more {
    padding-top: var(--step-3);
  }
</style>
