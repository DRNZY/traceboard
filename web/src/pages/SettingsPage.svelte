<script lang="ts">
  import type { ApiClient } from '../lib/api'
  import type { Settings } from '../lib/types'
  import { formatBytes, formatCount, formatTimestamp } from '../lib/format'

  interface Props {
    client: ApiClient
  }

  let { client }: Props = $props()

  let settings = $state<Settings | null>(null)
  let error = $state<string | null>(null)

  async function load(): Promise<void> {
    try {
      settings = await client.getSettings()
      error = null
    } catch {
      error = 'Settings could not be read from the local collector.'
    }
  }

  $effect(() => {
    void load()
  })
</script>

<section class="settings" aria-labelledby="settings-heading">
  <header class="settings__header">
    <h2 id="settings-heading" class="display settings__title">Settings</h2>
    <p class="settings__note">
      Traceboard keeps every captured run on this machine. It sends nothing to a hosted service and
      loads no external script, font, or image.
    </p>
  </header>

  {#if error}
    <p class="settings__error" role="alert">{error}</p>
  {/if}

  {#if settings}
    <dl class="settings__grid">
      <div class="settings__row">
        <dt class="label">Listen address</dt>
        <dd class="mono">{settings.listen_address}</dd>
      </div>
      <div class="settings__row">
        <dt class="label">Version</dt>
        <dd class="mono">
          {settings.version}
          {#if settings.commit && settings.commit !== 'none'}
            <span class="settings__commit">{settings.commit}</span>
          {/if}
        </dd>
      </div>
      <div class="settings__row">
        <dt class="label">Database</dt>
        <dd class="mono">{settings.database_path}</dd>
      </div>
      <div class="settings__row">
        <dt class="label">Configuration</dt>
        <dd class="mono">{settings.config_path}</dd>
      </div>
      <div class="settings__row">
        <dt class="label">Retention</dt>
        <dd class="mono">
          {settings.retention_days > 0
            ? `keep ${settings.retention_days} days`
            : 'keep runs indefinitely'}
          {#if settings.keep_newest_per_project > 0}
            · newest {settings.keep_newest_per_project} per project
          {/if}
        </dd>
      </div>
      <div class="settings__row">
        <dt class="label">Active sessions</dt>
        <dd class="mono">{formatCount(settings.sessions_valid)}</dd>
      </div>
      <div class="settings__row">
        <dt class="label">Ingest token</dt>
        <dd class="mono">
          {settings.ingest_token_configured ? 'configured in the local credential file' : 'not configured'}
        </dd>
      </div>
      <div class="settings__row">
        <dt class="label">Dashboard token</dt>
        <dd class="mono">
          {settings.dashboard_token_configured ? 'configured in the local credential file' : 'not configured'}
        </dd>
      </div>
    </dl>

    <section class="settings__limits" aria-labelledby="limits-heading">
      <h3 id="limits-heading" class="label">Compiled limits</h3>
      <ul class="settings__limit-list">
        <li class="mono">request body: {formatBytes(4 * 1024 * 1024)}</li>
        <li class="mono">raw payload: {formatBytes(1024 * 1024)}</li>
        <li class="mono">content field: {formatBytes(256 * 1024)}</li>
        <li class="mono">events per batch: {formatCount(1000)}</li>
      </ul>
      <p class="settings__note">
        These are safety ceilings. An operator may lower them in the configuration file; no setting
        raises them.
      </p>
    </section>
  {/if}
</section>

<style>
  .settings {
    display: grid;
    gap: var(--step-6);
    max-width: 60rem;
  }

  .settings__header {
    display: grid;
    gap: var(--step-2);
    padding-bottom: var(--step-3);
    border-bottom: 1px solid var(--rule-strong);
  }

  .settings__title {
    margin: 0;
    font-size: clamp(24px, 3.5vw, 36px);
    line-height: 1;
  }

  .settings__note {
    max-width: 62ch;
    margin: 0;
    color: var(--bone-dim);
  }

  .settings__error {
    margin: 0;
    padding: var(--step-2) var(--step-3);
    border: 1px solid var(--accent);
    color: var(--accent);
  }

  .settings__grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
    gap: var(--step-3) var(--step-6);
    margin: 0;
  }

  .settings__row {
    display: grid;
    gap: var(--step-1);
    padding-bottom: var(--step-2);
    border-bottom: 1px solid var(--rule);
  }

  .settings__row dd {
    margin: 0;
    word-break: break-all;
  }

  .settings__commit {
    margin-left: var(--step-2);
    color: var(--bone-dim);
  }

  .settings__limits {
    display: grid;
    gap: var(--step-2);
    padding-top: var(--step-4);
    border-top: 1px solid var(--rule);
  }

  .settings__limit-list {
    display: grid;
    gap: 0;
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .settings__limit-list li {
    padding: var(--step-1) 0;
    border-bottom: 1px solid var(--rule);
  }
</style>
