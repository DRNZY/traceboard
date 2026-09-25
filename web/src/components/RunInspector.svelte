<script lang="ts">
  import type { Alert, EventRecord, Run, Step } from '../lib/types'
  import {
    formatCount,
    formatDuration,
    formatStatus,
    formatTokens,
    formatTimestamp,
  } from '../lib/format'
  import EventTimeline from './EventTimeline.svelte'
  import ReplayScrubber from './ReplayScrubber.svelte'

  interface Props {
    run: Run | null
    steps: Step[]
    runEvents: EventRecord[]
    alerts: Alert[]
    expandedEventId: string | null
    replayIndex: number
    /** null while the client is still refetching a range. */
    appliedSequence: number | null
    eventsIncomplete: boolean
    hasMoreEvents: boolean
    loadingMore: boolean
    onToggleEvent: (eventId: string) => void
    onReplayIndexChange: (index: number) => void
    onLoadMoreEvents: () => void
    onSelectRun: (runId: string) => void
    onExport: (runId: string) => void
    onDelete?: (runId: string) => void
    onAcknowledge: (alertId: string) => void
    acknowledgingAlertId: string | null
  }

  let {
    run,
    steps,
    runEvents,
    alerts,
    expandedEventId,
    replayIndex,
    appliedSequence,
    eventsIncomplete,
    hasMoreEvents,
    loadingMore,
    onToggleEvent,
    onReplayIndexChange,
    onLoadMoreEvents,
    onSelectRun,
    onExport,
    onDelete,
    onAcknowledge,
    acknowledgingAlertId,
  }: Props = $props()

  const openAlerts = $derived(alerts.filter((alert) => alert.state === 'open'))
  const settledAlerts = $derived(alerts.length - openAlerts.length)

  /** Nested steps are shown once, inside the single step list. */
  const rootSteps = $derived(steps.filter((step) => !step.parent_step_id))
  const childSteps = $derived(steps.filter((step) => Boolean(step.parent_step_id)))

  const status = $derived(formatStatus(run?.status ?? 'unknown'))
  const title = $derived(run?.title?.trim() ? (run.title as string) : 'Untitled run')
  const lastIndex = $derived(Math.max(0, runEvents.length - 1))
</script>

<section class="inspector" aria-labelledby="inspector-heading">
  {#if !run}
    <div class="inspector__placeholder">
      <p class="label" id="inspector-heading">Run inspector</p>
      <h2 class="display inspector__placeholder-title">
        Select a run to read its timeline.
      </h2>
      <p class="inspector__placeholder-note">
        The index lists every captured run with its source, status, and duration. Open one to read
        its ordered prompts, model calls, tool calls, file changes, and failure point.
      </p>
    </div>
  {:else}
    <header class="inspector__header">
      <div>
        <p class="label" id="inspector-heading">Run inspector</p>
        <h2 class="display inspector__title">{title}</h2>
        <p class="inspector__id">
          run {run.id} · {run.source} · {run.project_name ?? 'no project reported'}
        </p>
      </div>
      <div class="inspector__status">
        <span class="status status--{status.tone}">
          <span class="status__shape" aria-hidden="true">{status.shape}</span>
          {status.text}
        </span>
        {#if run.parent_run_id}
          <button class="button" type="button" onclick={() => onSelectRun(run.parent_run_id as string)}>
            Open parent run
          </button>
        {/if}
      </div>
    </header>

    {#if openAlerts.length > 0}
      <section class="inspector__alerts" aria-labelledby="inspector-alerts-heading">
        <h3 id="inspector-alerts-heading" class="label">Open alerts</h3>
        <ul class="inspector__alert-list">
          {#each openAlerts as alert (alert.id)}
            <li class="inspector__alert">
              <p class="mono">{alert.message}</p>
              <button
                class="button"
                type="button"
                disabled={acknowledgingAlertId === alert.id}
                onclick={() => onAcknowledge(alert.id)}
              >
                {acknowledgingAlertId === alert.id ? 'Acknowledging' : 'Acknowledge'}
              </button>
            </li>
          {/each}
        </ul>
      </section>
    {:else if settledAlerts > 0}
      <p class="inspector__settled mono">
        {settledAlerts} resolved or acknowledged alert{settledAlerts === 1 ? '' : 's'} on this run.
      </p>
    {/if}

    <dl class="inspector__facts">
      <div class="inspector__fact">
        <dt class="label">Started</dt>
        <dd class="mono">{formatTimestamp(run.started_at)}</dd>
      </div>
      <div class="inspector__fact">
        <dt class="label">Ended</dt>
        <dd class="mono">{formatTimestamp(run.ended_at)}</dd>
      </div>
      <div class="inspector__fact">
        <dt class="label">Duration</dt>
        <dd class="mono">{formatDuration(run.duration_ms)}</dd>
      </div>
      <div class="inspector__fact">
        <dt class="label">Events</dt>
        <dd class="mono">{formatCount(run.event_count)}</dd>
      </div>
      <div class="inspector__fact">
        <dt class="label">Models</dt>
        <dd class="mono">{run.models?.length ? run.models.join(', ') : 'none reported'}</dd>
      </div>
      <div class="inspector__fact">
        <dt class="label">Tokens</dt>
        <dd class="mono">
          {#if run.input_tokens === null || run.input_tokens === undefined}
            not reported
          {:else}
            in {formatTokens(run.input_tokens)} / out {formatTokens(run.output_tokens)}
          {/if}
        </dd>
      </div>
      <div class="inspector__fact">
        <dt class="label">Capture mode</dt>
        <dd class="mono">{(run.capture_modes ?? []).join(' + ') || 'not reported'}</dd>
      </div>
      <div class="inspector__fact">
        <dt class="label">Child runs</dt>
        <dd class="mono">{formatCount(run.subagent_runs)}</dd>
      </div>
    </dl>

    {#if eventsIncomplete || appliedSequence === null}
      <p class="inspector__incomplete" role="status">
        This run is missing events. The timeline below is not complete, and no summary is claimed for
        the missing range.
      </p>
    {/if}

    {#if steps.length > 0}
      <section class="inspector__steps" aria-labelledby="inspector-steps-heading">
        <h3 id="inspector-steps-heading" class="label">Steps</h3>
        <ul class="inspector__step-list">
          {#each [...rootSteps, ...childSteps] as step (step.id)}
            {@const stepStatus = formatStatus(step.status)}
            <li class="inspector__step" class:inspector__step--child={Boolean(step.parent_step_id)}>
              <span class="status status--{stepStatus.tone}">
                <span class="status__shape" aria-hidden="true">{stepStatus.shape}</span>
                {stepStatus.text}
              </span>
              <span class="mono inspector__step-id">{step.id}</span>
              <span class="mono inspector__step-meta">
                {step.type} · {stepStatus.text.toLowerCase()} · {formatCount(step.event_count)} events
              </span>
              {#if step.parent_step_id}
                <span class="mono inspector__step-parent">child of {step.parent_step_id}</span>
              {/if}
            </li>
          {/each}
        </ul>
      </section>
    {/if}

    <EventTimeline
      {runEvents}
      {expandedEventId}
      {appliedSequence}
      hasMore={hasMoreEvents}
      loadingMore={loadingMore}
      {onToggleEvent}
      onLoadMore={onLoadMoreEvents}
    />

    <ReplayScrubber
      {runEvents}
      selectedIndex={replayIndex}
      incomplete={eventsIncomplete}
      onIndexChange={onReplayIndexChange}
    />

    <div class="inspector__actions">
      <button class="button" type="button" onclick={() => onExport(run.id)}>Export run</button>
      {#if onDelete}
        <button class="button button--danger" type="button" onclick={() => onDelete(run.id)}>
          Delete this run
        </button>
      {/if}
    </div>
  {/if}
</section>

<style>
  .inspector {
    display: grid;
    gap: var(--step-6);
    align-content: start;
  }

  .inspector__header {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    gap: var(--step-4);
    align-items: start;
    padding-bottom: var(--step-3);
    border-bottom: 1px solid var(--rule-strong);
  }

  .inspector__title {
    margin: var(--step-1) 0 0;
    font-size: clamp(22px, 3vw, 34px);
    line-height: 1.05;
  }

  .inspector__id {
    margin: var(--step-2) 0 0;
    color: var(--bone-dim);
    font-size: 11px;
    word-break: break-all;
  }

  .inspector__status {
    display: flex;
    flex-wrap: wrap;
    gap: var(--step-2);
    align-items: center;
    justify-content: flex-end;
  }

  .inspector__alerts {
    display: grid;
    gap: var(--step-2);
  }

  .inspector__alert-list {
    display: grid;
    gap: 0;
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .inspector__alert {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    gap: var(--step-3);
    align-items: center;
    padding: var(--step-2) 0;
    border-bottom: 1px solid var(--rule);
  }

  .inspector__alert p {
    margin: 0;
  }

  .inspector__settled {
    margin: 0;
    color: var(--bone-dim);
  }

  .inspector__facts {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
    gap: var(--step-3);
    margin: 0;
  }

  .inspector__fact dt {
    margin-bottom: var(--step-1);
  }

  .inspector__fact dd {
    margin: 0;
    word-break: break-word;
  }

  .inspector__incomplete {
    margin: 0;
    padding: var(--step-2) var(--step-3);
    border: 1px solid var(--accent);
    color: var(--accent);
  }

  .inspector__steps {
    display: grid;
    gap: var(--step-2);
  }

  .inspector__step-list {
    display: grid;
    gap: 0;
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .inspector__step {
    display: grid;
    grid-template-columns: 8rem minmax(0, 1fr) minmax(0, 1.5fr);
    gap: var(--step-3);
    align-items: baseline;
    padding: var(--step-2) 0;
    border-bottom: 1px solid var(--rule);
  }

  .inspector__step--child {
    padding-left: var(--step-4);
    border-left: 2px solid var(--rule);
  }

  .inspector__step-id,
  .inspector__step-parent {
    font-size: 11px;
    word-break: break-all;
  }

  .inspector__step-parent {
    grid-column: 1 / -1;
    color: var(--bone-dim);
  }

  .inspector__actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--step-2);
    padding-top: var(--step-3);
    border-top: 1px solid var(--rule);
  }

  .inspector__placeholder {
    display: grid;
    gap: var(--step-3);
    align-content: center;
    min-height: 40vh;
  }

  .inspector__placeholder-title {
    margin: 0;
    font-size: clamp(22px, 3.5vw, 38px);
    line-height: 1.05;
    max-width: 20ch;
  }

  .inspector__placeholder-note {
    max-width: 56ch;
    margin: 0;
    color: var(--bone-dim);
  }

  @media (max-width: 48rem) {
    .inspector__header {
      grid-template-columns: 1fr;
    }

    .inspector__status {
      justify-content: flex-start;
    }

    .inspector__step {
      grid-template-columns: minmax(0, 1fr);
      gap: var(--step-1);
    }
  }
</style>
