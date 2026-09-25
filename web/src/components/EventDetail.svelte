<script lang="ts" module>
  import type { EventRecord } from '../lib/types'
  import {
    formatElapsed,
    formatEventStatus,
    formatEventType,
    formatStatus,
    formatTimestamp,
  } from '../lib/format'

  const CAPTURE_EXPLANATIONS: Record<string, string> = {
    detailed: 'detailed: prompt and tool bodies retained after redaction',
    metadata: 'metadata: lifecycle, timing, and names only; prompt and tool bodies withheld',
    off: 'off: this source reports connection health only',
  }

  /** The human sentence for one event's capture record. */
  function describeCapture(event: EventRecord): string {
    const mode = typeof event.capture?.mode === 'string' ? event.capture.mode : ''
    const known = CAPTURE_EXPLANATIONS[mode]
    return known ?? `${mode || 'unspecified'} (mode not recognized by this build)`
  }

  /** Names the part of the event that hit an ingestion ceiling, if any. */
  function truncationNote(event: EventRecord): string | null {
    const parts: string[] = []
    if (event.capture?.content_truncated === true) parts.push('content')
    if (event.capture?.raw_truncated === true) parts.push('raw payload')
    if (event.capture?.attributes_truncated === true) parts.push('attributes')
    if (parts.length === 0) return null
    return `Truncated at the ingestion limit: ${parts.join(', ')}.`
  }

  function redactionNote(event: EventRecord): string | null {
    const count = event.capture?.redacted_fields
    if (typeof count !== 'number' || count <= 0) return null
    return `${count} field${count === 1 ? '' : 's'} redacted before storage.`
  }
</script>

<script lang="ts">
  interface Props {
    event: EventRecord
  }

  let { event }: Props = $props()

  const text = $derived.by(() => {
    const content = event.content
    if (!content) return null
    for (const key of ['text', 'prompt', 'output', 'input', 'error', 'response']) {
      const value = content[key]
      if (typeof value === 'string' && value.length > 0) return value
    }
    return null
  })

  const withheld = $derived(event.capture?.content_withheld === true)
  const withheldFields = $derived(
    Array.isArray(event.capture?.content_fields) ? (event.capture.content_fields as string[]) : [],
  )
  const rawWithheld = $derived(event.capture?.raw_withheld === true)
  const capture = $derived(describeCapture(event))
  const truncated = $derived(truncationNote(event))
  const redacted = $derived(redactionNote(event))

  /** Rendered as text, never as markup: captured payloads are untrusted. */
  function render(value: unknown): string {
    if (value === null || value === undefined) return 'not reported'
    if (typeof value === 'string') return value
    try {
      return JSON.stringify(value, null, 2)
    } catch {
      return 'unreadable value'
    }
  }
</script>

<article class="detail" data-testid="event-detail">
  <header class="detail__head">
    <h4 class="detail__type">{formatEventType(event.type, event.source)}</h4>
    <p class="detail__identity">
      <span class="detail__id">#{event.sequence}</span>
      {#if event.source_sequence !== null && event.source_sequence !== undefined}
        <span class="detail__source-seq">#{event.source_sequence}</span>
      {/if}
      <span class="detail__source">{event.source}</span>
      <span class="detail__version">{event.source_version || 'not reported'}</span>
      <span class="detail__source-event">{event.source_event_id}</span>
    </p>
  </header>

  <dl class="detail__grid">
    <div class="detail__row">
      <dt class="label">Status</dt>
      <dd>
        <span class="status status--{formatStatus(event.status).tone}">
          <span class="status__shape" aria-hidden="true">{formatStatus(event.status).shape}</span>
          {formatEventStatus(event.status)}
        </span>
      </dd>
    </div>
    <div class="detail__row">
      <dt class="label">Occurred</dt>
      <dd class="mono">{formatTimestamp(event.occurred_at)}</dd>
    </div>
    <div class="detail__row">
      <dt class="label">Received</dt>
      <dd class="mono">
        {formatTimestamp(event.received_at)}
        <span class="detail__elapsed">{formatElapsed(event.occurred_at, event.received_at)}</span>
      </dd>
    </div>
    <div class="detail__row">
      <dt class="label">Step</dt>
      <dd class="mono">
        {#if event.step_id}
          {event.step_id}
          {#if event.parent_step_id}
            <span class="detail__parent">child of {event.parent_step_id}</span>
          {/if}
        {:else}
          run level
        {/if}
      </dd>
    </div>
    <div class="detail__row">
      <dt class="label">Search</dt>
      <dd class="mono">
        {event.searchable ? 'indexed for search' : 'not indexed for search'}
      </dd>
    </div>
  </dl>

  <section class="detail__section">
    <h5 class="label">Capture</h5>
    <p class="mono detail__capture">{capture}</p>
    {#if truncated}
      <p class="mono detail__note detail__note--warn">{truncated}</p>
    {/if}
    {#if redacted}
      <p class="mono detail__note">{redacted}</p>
    {/if}
    {#if withheld}
      <p class="mono detail__note">
        Prompt and tool body content was withheld. The event was still recorded, and the fields that
        were dropped are named: {withheldFields.join(', ') || 'none listed'}.
      </p>
    {:else if !text}
      <p class="mono detail__note">The source reported no content for this event.</p>
    {/if}
    <ul class="detail__capture-list">
      {#each Object.entries(event.capture ?? {}) as [key, value] (key)}
        <li class="mono">
          <span class="detail__key">{key}</span>
          <span class="detail__value">{render(value)}</span>
        </li>
      {/each}
    </ul>
  </section>

  {#if text}
    <section class="detail__section">
      <h5 class="label">Captured body</h5>
      <pre class="detail__body mono">{text}</pre>
    </section>
  {/if}

  <section class="detail__section">
    <h5 class="label">Attributes</h5>
    {#if Object.keys(event.attributes ?? {}).length === 0}
      <p class="mono detail__note">The source reported no attributes for this event.</p>
    {:else}
      <ul class="detail__attributes">
        {#each Object.entries(event.attributes) as [key, value] (key)}
          <li class="mono">
            <span class="detail__key">{key}</span>
            <span class="detail__value">{render(value)}</span>
          </li>
        {/each}
      </ul>
    {/if}
  </section>

  <section class="detail__section">
    <h5 class="label">Raw source payload</h5>
    {#if rawWithheld}
      <p class="mono detail__note">
        Raw source payload withheld: the active capture mode drops the raw body before storage.
      </p>
    {:else if event.raw === null || event.raw === undefined}
      <p class="mono detail__note">no raw payload retained: the source sent none.</p>
    {:else}
      <pre class="detail__body detail__body--raw mono">{render(event.raw)}</pre>
    {/if}
  </section>
</article>

<style>
  .detail {
    display: grid;
    gap: var(--step-4);
    padding: var(--step-4) 0 var(--step-6) var(--step-4);
    border-left: 2px solid var(--rule);
  }

  .detail__head {
    display: grid;
    gap: var(--step-1);
  }

  .detail__type {
    margin: 0;
    font-family: var(--display);
    font-size: 16px;
    letter-spacing: 0.01em;
    text-transform: uppercase;
  }

  .detail__identity {
    display: flex;
    flex-wrap: wrap;
    gap: var(--step-2);
    margin: 0;
    font-family: var(--mono);
    font-size: 11px;
    color: var(--bone-dim);
  }

  .detail__id {
    color: var(--bone);
    font-weight: 700;
  }

  .detail__grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
    gap: var(--step-3);
    margin: 0;
  }

  .detail__row dt {
    margin-bottom: var(--step-1);
  }

  .detail__row dd {
    margin: 0;
  }

  .detail__elapsed {
    margin-left: var(--step-2);
    color: var(--bone-dim);
  }

  .detail__parent {
    display: block;
    color: var(--bone-dim);
    font-size: 11px;
  }

  .detail__section {
    display: grid;
    gap: var(--step-1);
  }

  .detail__capture {
    margin: 0;
    color: var(--bone);
  }

  .detail__note {
    margin: 0;
    color: var(--bone-dim);
  }

  .detail__note--warn {
    color: var(--accent);
  }

  .detail__capture-list,
  .detail__attributes {
    display: grid;
    gap: 0;
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .detail__capture-list li,
  .detail__attributes li {
    display: grid;
    grid-template-columns: 12rem minmax(0, 1fr);
    gap: var(--step-3);
    padding: var(--step-1) 0;
    border-bottom: 1px solid var(--rule);
  }

  .detail__key {
    color: var(--bone-dim);
  }

  .detail__value {
    white-space: pre-wrap;
    word-break: break-word;
  }

  .detail__body {
    max-height: 24rem;
    margin: 0;
    padding: var(--step-3);
    background: var(--ink-raised);
    border: 1px solid var(--rule);
    overflow: auto;
    white-space: pre-wrap;
    word-break: break-word;
  }

  .detail__body--raw {
    border-style: dashed;
  }
</style>
