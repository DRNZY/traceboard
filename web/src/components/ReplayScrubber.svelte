<script lang="ts" module>
  import type { EventRecord } from '../lib/types'

  export interface ReconstructedRunState {
    /** The position this state was built from, or -1 before the first event. */
    index: number
    /** Everything the run has recorded in total, whether applied or not. */
    eventCount: number
    appliedEvents: EventRecord[]
    currentEvent: EventRecord | null
    currentStepId: string | null
    parentStepId: string | null
    stepCount: number
    filesChanged: string[]
    toolsInvoked: string[]
    modelsUsed: string[]
    tokens: { input: number | null; output: number | null }
    firstFailure: EventRecord | null
  }

  function text(value: unknown): string | null {
    return typeof value === 'string' && value.length > 0 ? value : null
  }

  function count(value: unknown): number | null {
    if (typeof value === 'number' && Number.isFinite(value)) return value
    if (typeof value === 'string' && value.trim() !== '' && !Number.isNaN(Number(value))) {
      return Number(value)
    }
    return null
  }

  /**
   * Folds a prefix of the recorded events into the state the run had reached at
   * that point. This is a pure read of captured data: it never re-executes a
   * prompt, tool, or command, and it never looks past the selected index.
   */
  export function reconstructRunState(
    runEvents: EventRecord[],
    index: number,
  ): ReconstructedRunState {
    const appliedEvents = index < 0 ? [] : runEvents.slice(0, index + 1)
    const safeIndex = runEvents.length === 0 ? -1 : Math.min(index, runEvents.length - 1)
    const filesChanged: string[] = []
    const toolsInvoked: string[] = []
    const modelsUsed: string[] = []
    const steps = new Set<string>()
    let input: number | null = null
    let output: number | null = null
    let firstFailure: EventRecord | null = null

    const push = (list: string[], value: string | null) => {
      if (value && !list.includes(value)) list.push(value)
    }

    for (const event of appliedEvents) {
      const attributes = event.attributes ?? {}
      if (event.step_id) steps.add(event.step_id)
      if (event.type === 'file.changed') {
        push(filesChanged, text(attributes.path) ?? text(attributes.file))
      }
      if (event.type.startsWith('tool.') || event.type.startsWith('command.')) {
        push(toolsInvoked, text(attributes.tool) ?? text(attributes.tool_name) ?? text(attributes.command))
      }
      if (event.type.startsWith('model.')) {
        push(modelsUsed, text(attributes.model))
      }
      const usage = (attributes['gen_ai.usage'] ?? {}) as Record<string, unknown>
      const eventInput = count(attributes.input_tokens) ?? count(usage.input_tokens)
      const eventOutput = count(attributes.output_tokens) ?? count(usage.output_tokens)
      if (eventInput !== null) input = (input ?? 0) + eventInput
      if (eventOutput !== null) output = (output ?? 0) + eventOutput
      if (!firstFailure && event.status === 'failed') firstFailure = event
    }

    const currentEvent = appliedEvents[appliedEvents.length - 1] ?? null
    return {
      index: safeIndex,
      eventCount: runEvents.length,
      appliedEvents,
      currentEvent,
      currentStepId: currentEvent?.step_id ?? null,
      parentStepId: currentEvent?.parent_step_id ?? null,
      stepCount: steps.size,
      filesChanged,
      toolsInvoked,
      modelsUsed,
      tokens: { input, output },
      firstFailure,
    }
  }
</script>

<script lang="ts">
  import { formatEventType, formatOffsetTime, formatStatus, formatTokens } from '../lib/format'

  interface Props {
    runEvents: EventRecord[]
    selectedIndex: number
    /** True when the server reported that events are still missing. */
    incomplete: boolean
    onIndexChange: (index: number) => void
  }

  let { runEvents, selectedIndex, incomplete, onIndexChange }: Props = $props()

  const last = $derived(Math.max(0, runEvents.length - 1))
  const clamped = $derived(Math.max(0, Math.min(selectedIndex, last)))
  const state = $derived(reconstructRunState(runEvents, clamped))
  const atEnd = $derived(clamped >= last || runEvents.length === 0)
  const atStart = $derived(clamped <= 0)

  /** A fractional slider value is floored: an event is never half applied. */
  function commit(value: number): void {
    onIndexChange(Math.max(0, Math.min(Math.floor(value), last)))
  }
</script>

<section class="replay" aria-labelledby="replay-heading">
  <header class="replay__header">
    <h3 id="replay-heading" class="label">Replay</h3>
    <p class="replay__note" role="note">
      Replay reconstructs recorded data only. No action is run, and nothing is sent to an agent.
    </p>
  </header>

  {#if incomplete}
    <p class="replay__incomplete" role="status">
      This run is missing events, so the reconstructed state below is incomplete.
    </p>
  {/if}

  <div class="replay__controls">
    <button
      class="button"
      type="button"
      aria-label="Previous event"
      disabled={atStart}
      onclick={() => commit(clamped - 1)}
    >
      Previous
    </button>

    <label class="visually-hidden" for="replay-position">Replay position</label>
    <input
      id="replay-position"
      class="replay__slider"
      type="range"
      min="0"
      max={last}
      step="1"
      value={clamped}
      oninput={(event) => commit(Number(event.currentTarget.value))}
    />
    <span class="replay__position" aria-live="off">
      {runEvents.length === 0 ? '0 / 0' : `${clamped + 1} / ${runEvents.length}`}
    </span>

    <button
      class="button"
      type="button"
      aria-label="Next event"
      disabled={atEnd}
      onclick={() => commit(clamped + 1)}
    >
      Next
    </button>
  </div>

  {#if state.currentEvent}
    {@const status = formatStatus(state.currentEvent.status)}
    <dl class="replay__state" data-testid="replay-state">
      <div class="replay__cell">
        <dt class="label">At event</dt>
        <dd class="mono">
          #{state.currentEvent.sequence} · {formatOffsetTime(state.currentEvent.occurred_at)}
        </dd>
      </div>
      <div class="replay__cell">
        <dt class="label">Event</dt>
        <dd class="mono">{formatEventType(state.currentEvent.type, state.currentEvent.source)}</dd>
      </div>
      <div class="replay__cell">
        <dt class="label">Status</dt>
        <dd class="status status--{status.tone}">
          <span class="status__shape" aria-hidden="true">{status.shape}</span>
          {status.text}
        </dd>
      </div>
      <div class="replay__cell">
        <dt class="label">Events applied</dt>
        <dd class="mono">{state.appliedEvents.length} of {state.eventCount}</dd>
      </div>
      <div class="replay__cell">
        <dt class="label">Steps seen</dt>
        <dd class="mono">{formatTokens(state.stepCount)}</dd>
      </div>
      <div class="replay__cell">
        <dt class="label">Models</dt>
        <dd class="mono">{state.modelsUsed.join(', ') || 'none recorded'}</dd>
      </div>
      <div class="replay__cell">
        <dt class="label">Tools invoked</dt>
        <dd class="mono">{state.toolsInvoked.join(', ') || 'none recorded'}</dd>
      </div>
      <div class="replay__cell">
        <dt class="label">Files changed</dt>
        <dd class="mono">{state.filesChanged.join(', ') || 'none recorded'}</dd>
      </div>
      <div class="replay__cell">
        <dt class="label">Tokens in / out</dt>
        <dd class="mono">
          {formatTokens(state.tokens.input)} / {formatTokens(state.tokens.output)}
        </dd>
      </div>
      <div class="replay__cell">
        <dt class="label">First reported failure</dt>
        <dd class="mono">
          {#if state.firstFailure}
            #{state.firstFailure.sequence} {formatEventType(state.firstFailure.type, state.firstFailure.source)}
          {:else}
            none in the applied range
          {/if}
        </dd>
      </div>
    </dl>
  {:else}
    <p class="replay__empty">No event has been recorded for this run yet.</p>
  {/if}
</section>

<style>
  .replay {
    display: grid;
    gap: var(--step-3);
    padding-top: var(--step-4);
    border-top: 1px solid var(--rule-strong);
  }

  .replay__header {
    display: grid;
    gap: var(--step-1);
  }

  .replay__note {
    margin: 0;
    color: var(--bone-dim);
    font-size: 11px;
  }

  .replay__incomplete {
    margin: 0;
    padding: var(--step-2) var(--step-3);
    border: 1px solid var(--accent);
    color: var(--accent);
  }

  .replay__controls {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr) auto;
    gap: var(--step-2);
    align-items: center;
  }

  .replay__slider {
    width: 100%;
    accent-color: var(--accent);
  }

  .replay__position {
    min-width: 6rem;
    font-family: var(--mono);
    font-size: 12px;
    text-align: right;
  }

  .replay__state {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
    gap: var(--step-3);
    margin: 0;
  }

  .replay__cell dt {
    margin-bottom: var(--step-1);
  }

  .replay__cell dd {
    margin: 0;
    word-break: break-word;
  }

  .replay__empty {
    margin: 0;
    color: var(--bone-dim);
  }
</style>
