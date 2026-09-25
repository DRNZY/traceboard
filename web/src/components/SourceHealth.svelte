<script lang="ts">
  import type { CaptureMode, Project, QuarantineEntry, Source } from '../lib/types'
  import { formatAge, formatCount, formatFilePath, formatTimestamp } from '../lib/format'

  interface Props {
    sources: Source[]
    projects: Project[]
    quarantine: QuarantineEntry[]
    heartbeatSeconds: number
    now: string
    savingSource: string | null
    onSetCaptureMode: (source: string, mode: CaptureMode) => void
  }

  let {
    sources,
    projects,
    quarantine,
    heartbeatSeconds,
    now,
    savingSource,
    onSetCaptureMode,
  }: Props = $props()

  const MODES: CaptureMode[] = ['off', 'metadata', 'detailed']

  /** A source is stale once three heartbeats have been missed. */
  const staleSeconds = $derived(heartbeatSeconds * 3)

  interface Group {
    source: string
    entries: QuarantineEntry[]
  }

  /** Quarantined events grouped by source, with unattributed ones kept apart. */
  const quarantineGroups = $derived.by<Group[]>(() => {
    const order: string[] = []
    const bySource = new Map<string, QuarantineEntry[]>()
    for (const entry of quarantine) {
      const key = entry.source ?? ''
      if (!bySource.has(key)) {
        bySource.set(key, [])
        order.push(key)
      }
      bySource.get(key)?.push(entry)
    }
    return order.map((source) => ({ source, entries: bySource.get(source) ?? [] }))
  })
</script>

<section class="health" aria-labelledby="health-heading">
  <header class="health__header">
    <h2 id="health-heading" class="display health__title">Source health</h2>
    <p class="health__note">
      A source is stale once {staleSeconds}s pass without a heartbeat. Capture mode is applied to every
      event that source sends, and cannot be raised by the source itself.
    </p>
  </header>

  {#if sources.length === 0}
    <p class="health__empty">
      No sources are configured yet. Run <code>traceboard configure &lt;source&gt;</code> to install
      one, then restart that agent.
    </p>
  {:else}
    <table class="health__table">
      <caption class="visually-hidden">Source capture modes, heartbeats, and quarantine counts</caption>
      <thead>
        <tr>
          <th scope="col" class="label">Source</th>
          <th scope="col" class="label">Version</th>
          <th scope="col" class="label">Connection</th>
          <th scope="col" class="label">Runs</th>
          <th scope="col" class="label">Quarantined</th>
          <th scope="col" class="label">Capture mode</th>
        </tr>
      </thead>
      <tbody>
        {#each sources as source (source.name)}
          {@const stale =
            source.last_heartbeat_at !== null &&
            source.last_heartbeat_at !== undefined &&
            !isFresh(source.last_heartbeat_at, staleSeconds, now)}
          <tr>
            <th scope="row" class="mono health__name">{source.name}</th>
            <td class="mono">{source.source_version || 'not reported'}</td>
            <td class="mono">
              {#if source.last_heartbeat_at}
                <span class="status">
                  <span class="status__shape" aria-hidden="true">
                    {source.connected && !stale ? '◆' : '✕'}
                  </span>
                  {source.connected && !stale ? 'Connected' : 'Disconnected'}
                </span>
                <span class="health__age">
                  {formatTimestamp(source.last_heartbeat_at)}
                  ({formatAge(source.last_heartbeat_at, now)})
                </span>
              {:else}
                <span class="status">
                  <span class="status__shape" aria-hidden="true">✕</span>
                  Disconnected
                </span>
                <span class="health__age">never reported</span>
              {/if}
              <span class="health__age">
                stale after
                <span class="health__threshold">{staleSeconds}s</span>
                without a heartbeat
              </span>
            </td>
            <td class="mono">{formatCount(source.run_count)}</td>
            <td class="mono">
              {#if source.quarantine_count === 1}
                1 quarantined event
              {:else}
                {formatCount(source.quarantine_count)} quarantined events
              {/if}
            </td>
            <td>
              <label class="visually-hidden" for="capture-{source.name}">
                Capture mode for {source.name}
              </label>
              <select
                id="capture-{source.name}"
                class="field__select health__select"
                value={source.capture_mode}
                disabled={savingSource === source.name}
                onchange={(event) =>
                  onSetCaptureMode(source.name, event.currentTarget.value as CaptureMode)}
              >
                {#each MODES as mode (mode)}
                  <option value={mode}>{mode}</option>
                {/each}
              </select>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}

  <section class="health__panel" aria-labelledby="quarantine-heading">
    <h3 id="quarantine-heading" class="label">Quarantine</h3>
    {#if quarantine.length === 0}
      <p class="health__empty">No event has been quarantined.</p>
    {:else}
      {#each quarantineGroups as group (group.source || ' unattributed')}
        <h4 class="health__group">
          {group.source || 'unattributed source'}
          <span class="health__group-count">
            {group.entries.length === 1 ? '1 event' : `${group.entries.length} events`}
          </span>
        </h4>
        {#if group.source === ''}
          <p class="health__empty">
            These entries name no source. They are counted here and readable through
            <code>GET /api/v1/quarantine</code>.
          </p>
        {:else}
          <ul class="health__quarantine">
            {#each group.entries as entry (entry.id)}
              <li class="health__entry">
                <p class="mono">{entry.reason}</p>
                <pre class="health__payload mono">{entry.payload}</pre>
              </li>
            {/each}
          </ul>
        {/if}
      {/each}
    {/if}
  </section>

  <section class="health__panel" aria-labelledby="projects-heading">
    <h3 id="projects-heading" class="label">Projects</h3>
    {#if projects.length === 0}
      <p class="health__empty">no projects seen yet</p>
    {:else}
      <ul class="health__projects">
        {#each projects as project (project.id)}
          <li class="health__project">
            <span class="mono health__project-name">{project.name ?? project.id}</span>
            {#if project.path}
              <span class="mono health__project-path">{formatFilePath(project.path)}</span>
            {:else}
              <span class="health__project-missing">path not reported</span>
            {/if}
            <span class="mono health__project-runs">{formatCount(project.run_count)} runs</span>
          </li>
        {/each}
      </ul>
    {/if}
  </section>
</section>

<script lang="ts" module>
  function isFresh(heartbeat: string, staleSeconds: number, now: string): boolean {
    const beat = new Date(heartbeat).getTime()
    const reference = new Date(now).getTime()
    if (Number.isNaN(beat) || Number.isNaN(reference)) return false
    return reference - beat <= staleSeconds * 1000
  }
</script>

<style>
  .health {
    display: grid;
    gap: var(--step-6);
  }

  .health__header {
    display: grid;
    gap: var(--step-2);
    padding-bottom: var(--step-3);
    border-bottom: 1px solid var(--rule-strong);
  }

  .health__title {
    margin: 0;
    font-size: clamp(24px, 3.5vw, 36px);
    line-height: 1;
  }

  .health__note {
    max-width: 68ch;
    margin: 0;
    color: var(--bone-dim);
  }

  .health__empty {
    margin: 0;
    color: var(--bone-dim);
  }

  .health__table {
    width: 100%;
    border-collapse: collapse;
  }

  .health__table th,
  .health__table td {
    padding: var(--step-2) var(--step-3) var(--step-2) 0;
    border-bottom: 1px solid var(--rule);
    text-align: left;
    vertical-align: top;
  }

  .health__table thead th {
    border-bottom-color: var(--rule-strong);
  }

  .health__name {
    font-weight: 700;
  }

  .health__age {
    display: block;
    color: var(--bone-dim);
    font-size: 11px;
  }

  .health__select {
    min-width: 9rem;
  }

  .health__panel {
    display: grid;
    gap: var(--step-2);
    padding-top: var(--step-3);
    border-top: 1px solid var(--rule);
  }

  .health__group {
    display: flex;
    gap: var(--step-2);
    align-items: baseline;
    margin: var(--step-2) 0 0;
    font-family: var(--mono);
    font-size: 12px;
    text-transform: uppercase;
    letter-spacing: 0.08em;
  }

  .health__group-count {
    color: var(--accent);
  }

  .health__quarantine {
    display: grid;
    gap: var(--step-2);
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .health__entry p {
    margin: 0 0 var(--step-1);
  }

  .health__payload {
    max-height: 10rem;
    margin: 0;
    padding: var(--step-2);
    background: var(--ink-raised);
    border: 1px dashed var(--rule);
    overflow: auto;
    white-space: pre-wrap;
    word-break: break-word;
  }

  .health__projects {
    display: grid;
    gap: 0;
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .health__project {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(0, 2fr) auto;
    gap: var(--step-3);
    padding: var(--step-2) 0;
    border-bottom: 1px solid var(--rule);
  }

  .health__project-path,
  .health__project-missing {
    color: var(--bone-dim);
    font-size: 11px;
  }

  code {
    font-family: var(--mono);
    font-size: 12px;
  }
</style>
