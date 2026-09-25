<script lang="ts">
  import type { ApiClient } from '../lib/api'
  import type { CaptureMode, Project, QuarantineEntry, Source } from '../lib/types'
  import SourceHealth from '../components/SourceHealth.svelte'

  interface Props {
    client: ApiClient
  }

  let { client }: Props = $props()

  let sources = $state<Source[]>([])
  let projects = $state<Project[]>([])
  let quarantine = $state<QuarantineEntry[]>([])
  let savingSource = $state<string | null>(null)
  let error = $state<string | null>(null)
  let now = $state(new Date().toISOString())

  async function load(): Promise<void> {
    try {
      const [sourceList, projectList, quarantineList] = await Promise.all([
        client.listSources(),
        client.listProjects(),
        client.listQuarantine(),
      ])
      sources = sourceList
      projects = projectList
      quarantine = quarantineList
      error = null
    } catch {
      error = 'Source health could not be read from the local collector.'
    }
  }

  async function setCaptureMode(source: string, mode: CaptureMode): Promise<void> {
    savingSource = source
    try {
      const updated = await client.setCaptureMode(source, mode)
      sources = sources.map((entry) => (entry.name === updated.name ? updated : entry))
      error = null
    } catch {
      error = `The capture mode for ${source} could not be changed.`
    } finally {
      savingSource = null
    }
  }

  $effect(() => {
    void load()
  })
</script>

<div class="sources-page">
  {#if error}
    <p class="sources-page__error" role="alert">{error}</p>
  {/if}
  <SourceHealth
    {sources}
    {projects}
    {quarantine}
    heartbeatSeconds={30}
    {now}
    {savingSource}
    onSetCaptureMode={setCaptureMode}
  />
</div>

<style>
  .sources-page {
    display: grid;
    gap: var(--step-4);
  }

  .sources-page__error {
    margin: 0;
    padding: var(--step-2) var(--step-3);
    border: 1px solid var(--accent);
    color: var(--accent);
  }
</style>
