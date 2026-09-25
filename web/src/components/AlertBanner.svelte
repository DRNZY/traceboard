<script lang="ts">
  import type { Alert, AlertState } from '../lib/types'
  import { formatTimestamp } from '../lib/format'

  interface Props {
    alert: Alert
    onAcknowledge?: (alertId: string) => void
    onSelectRun?: (runId: string) => void
    busy?: boolean
  }

  let { alert, onAcknowledge, onSelectRun, busy = false }: Props = $props()

  const STATES: Record<AlertState, { shape: string; label: string }> = {
    open: { shape: '◆', label: 'Open' },
    acknowledged: { shape: '✓', label: 'Acknowledged' },
    resolved: { shape: '✓', label: 'Resolved' },
  }

  // An unrecognized state is shown verbatim. Assuming it is "open" would offer
  // an action the server may reject, and assuming it is handled would hide it.
  const presentation = $derived(STATES[alert.state] ?? null)
  const shape = $derived(presentation?.shape ?? '?')
  const label = $derived(presentation?.label ?? String(alert.state))
  const settled = $derived(alert.state !== 'open')
</script>

<aside class="alert" role="alert">
  <div class="alert__body">
    <p class="alert__message">{alert.message}</p>
    <p class="alert__meta">
      <span class="status">
        <span class="status__shape" aria-hidden="true">{shape}</span>
        {label}
      </span>
      <span class="alert__type">{alert.type}</span>
      {#if alert.source}
        <span class="alert__source">{alert.source}</span>
      {/if}
      <span class="alert__time">raised {formatTimestamp(alert.created_at)}</span>
      {#if alert.state === 'acknowledged' && alert.acknowledged_at}
        <span class="alert__time">acknowledged {formatTimestamp(alert.acknowledged_at)}</span>
      {/if}
    </p>
  </div>

  <div class="alert__actions">
    {#if alert.run_id && onSelectRun}
      <button class="button" type="button" onclick={() => onSelectRun?.(alert.run_id as string)}>
        Open run
      </button>
    {/if}
    {#if !settled && onAcknowledge}
      <button
        class="button"
        type="button"
        disabled={busy}
        onclick={() => onAcknowledge?.(alert.id)}
      >
        {busy ? 'Acknowledging' : 'Acknowledge'}
      </button>
    {/if}
  </div>
</aside>

<style>
  .alert {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    gap: var(--step-3);
    align-items: center;
    padding: var(--step-3);
    border: 1px solid var(--accent);
  }

  .alert__message {
    margin: 0;
    font-family: var(--mono);
    font-size: 13px;
  }

  .alert__meta {
    display: flex;
    flex-wrap: wrap;
    gap: var(--step-2);
    align-items: baseline;
    margin: var(--step-1) 0 0;
    font-family: var(--mono);
    font-size: 11px;
    color: var(--bone-dim);
  }

  .alert__actions {
    display: flex;
    gap: var(--step-2);
  }

  @media (max-width: 48rem) {
    .alert {
      grid-template-columns: 1fr;
    }
  }
</style>
