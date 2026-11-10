<script lang="ts">
  import { onMount } from 'svelte';
  import { Events } from '@wailsio/runtime';
  import './app.css';
  import { StateEvent, Svc, TunnelState, UiShowLogsEvent, errorMessage } from './lib/api';
  import type { State, Tunnel, TunnelView } from './lib/api';
  import Icon from './lib/Icon.svelte';
  import TunnelRow from './lib/TunnelRow.svelte';
  import TunnelForm from './lib/TunnelForm.svelte';
  import LogsPanel from './lib/LogsPanel.svelte';
  import HostKeyDialog from './lib/HostKeyDialog.svelte';
  import SettingsDialog from './lib/SettingsDialog.svelte';
  import ConfirmDeleteDialog from './lib/ConfirmDeleteDialog.svelte';
  import ServerSetupDialog from './lib/ServerSetupDialog.svelte';

  interface FormSession {
    mode: 'new' | 'edit';
    tunnel: Tunnel;
    hasSecret: boolean;
  }

  let app: State = $state({ tunnels: [], hostKeyRequests: [], startupError: '' });
  let now = $state(Date.now());
  let appError = $state('');
  let form: FormSession | null = $state(null);
  let settingsOpen = $state(false);
  let deleting: TunnelView | null = $state(null);
  let serverSetup: TunnelView | null = $state(null);
  let logsFor: string | null = $state(null);

  const tunnels = $derived(app.tunnels ?? []);
  const hostKeyRequest = $derived((app.hostKeyRequests ?? [])[0] ?? null);
  const upCount = $derived(tunnels.filter((v) => v.status.state === TunnelState.Up).length);
  const logsView = $derived(logsFor === null ? null : (tunnels.find((v) => v.tunnel.id === logsFor) ?? null));

  // Close the logs drawer when its tunnel disappears (deleted elsewhere).
  $effect(() => {
    if (logsFor !== null && tunnels.length > 0 && logsView === null) logsFor = null;
  });

  onMount(() => {
    const offs: Array<() => void> = [];

    Svc.Snapshot()
      .then((s) => {
        app = s;
      })
      .catch((err: unknown) => {
        appError = `Could not load state: ${errorMessage(err)}`;
      });

    offs.push(
      Events.On(StateEvent, (ev) => {
        app = ev.data;
      }),
    );
    offs.push(
      Events.On(UiShowLogsEvent, (ev) => {
        if (typeof ev.data === 'string' && ev.data !== '') logsFor = ev.data;
      }),
    );

    const timer = window.setInterval(() => {
      now = Date.now();
    }, 1000);

    return () => {
      window.clearInterval(timer);
      for (const off of offs) off();
    };
  });

  function showError(message: string): void {
    appError = message;
  }

  async function addTunnel(): Promise<void> {
    try {
      const tunnel = await Svc.NewTunnel();
      form = { mode: 'new', tunnel, hasSecret: false };
    } catch (err) {
      showError(`Could not start a new tunnel: ${errorMessage(err)}`);
    }
  }

  function editTunnel(view: TunnelView): void {
    form = { mode: 'edit', tunnel: view.tunnel, hasSecret: view.hasSecret };
  }

  function onSaved(): void {
    form = null;
  }

  function onDeleted(): void {
    if (deleting && logsFor === deleting.tunnel.id) logsFor = null;
    deleting = null;
  }

  async function dismissStartupError(): Promise<void> {
    try {
      await Svc.DismissStartupError();
      app.startupError = '';
    } catch (err) {
      showError(`Could not dismiss the startup message: ${errorMessage(err)}`);
    }
  }
</script>

<div class="app">
  <header class="topbar">
    <div class="brand">
      <span class="logo" aria-hidden="true">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round">
          <path d="M4 12h4M16 12h4" />
          <rect x="8" y="8" width="8" height="8" rx="2" />
          <path d="M2 12a2 2 0 1 0 4 0 2 2 0 0 0-4 0zM18 12a2 2 0 1 0 4 0 2 2 0 0 0-4 0z" />
        </svg>
      </span>
      <h1 class="title">SSH Tunnel Manager</h1>
      {#if tunnels.length > 0}
        <span class="count mono">{tunnels.length} {tunnels.length === 1 ? 'tunnel' : 'tunnels'} · {upCount} up</span>
      {/if}
    </div>
    <div class="top-actions">
      <button type="button" class="btn btn-ghost" data-testid="nav-settings" onclick={() => (settingsOpen = true)}>
        <Icon name="settings" />
        Settings
      </button>
      <button type="button" class="btn btn-primary" data-testid="add-tunnel" onclick={addTunnel}>
        <Icon name="plus" />
        Add tunnel
      </button>
    </div>
  </header>

  {#if app.startupError || appError}
    <div class="banners">
      {#if app.startupError}
        <div class="banner banner-warn" data-testid="startup-error" role="status">
          <span class="banner-icon"><Icon name="alert" /></span>
          <div class="banner-body">
            <span class="banner-title">Startup problem.</span>
            {app.startupError}
          </div>
          <button type="button" class="btn btn-sm" data-testid="startup-error-dismiss" onclick={dismissStartupError}>
            Dismiss
          </button>
        </div>
      {/if}
      {#if appError}
        <div class="banner banner-error" data-testid="app-error" role="alert">
          <span class="banner-icon"><Icon name="alert" /></span>
          <div class="banner-body">{appError}</div>
          <button type="button" class="btn btn-ghost btn-icon btn-sm" aria-label="Dismiss error" onclick={() => (appError = '')}>
            <Icon name="close" size={14} />
          </button>
        </div>
      {/if}
    </div>
  {/if}

  <main class="content">
    {#if tunnels.length === 0}
      <div class="empty">
        <span class="empty-icon" aria-hidden="true"><Icon name="key" size={28} /></span>
        <h2 class="empty-title">No tunnels yet</h2>
        <p class="empty-text">
          Forward a port to or from a remote server over SSH. Tunnels reconnect on their own and can connect when the app starts.
        </p>
        <button type="button" class="btn btn-primary" data-testid="add-tunnel-empty" onclick={addTunnel}>
          <Icon name="plus" />
          Add tunnel
        </button>
      </div>
    {:else}
      <ul class="list">
        {#each tunnels as view (view.tunnel.id)}
          <TunnelRow
            {view}
            {now}
            onedit={() => editTunnel(view)}
            onlogs={() => (logsFor = view.tunnel.id)}
            onserver={() => (serverSetup = view)}
            ondelete={() => (deleting = view)}
            onerror={showError}
          />
        {/each}
      </ul>
    {/if}
  </main>

  {#if logsView}
    <aside class="drawer">
      <LogsPanel
        tunnelId={logsView.tunnel.id}
        tunnelName={logsView.tunnel.name}
        onclose={() => (logsFor = null)}
        onerror={showError}
      />
    </aside>
  {/if}
</div>

{#if form}
  <TunnelForm initial={form.tunnel} hasSecret={form.hasSecret} mode={form.mode} onsaved={onSaved} oncancel={() => (form = null)} />
{/if}

{#if settingsOpen}
  <SettingsDialog onclose={() => (settingsOpen = false)} />
{/if}

{#if deleting}
  <ConfirmDeleteDialog view={deleting} ondeleted={onDeleted} oncancel={() => (deleting = null)} />
{/if}

{#if serverSetup}
  <ServerSetupDialog
    tunnelId={serverSetup.tunnel.id}
    tunnelName={serverSetup.tunnel.name}
    onclose={() => (serverSetup = null)}
  />
{/if}

{#if hostKeyRequest}
  {#key hostKeyRequest.id}
    <HostKeyDialog request={hostKeyRequest} onerror={showError} />
  {/key}
{/if}

<style>
  .app {
    display: flex;
    flex-direction: column;
    height: 100%;
  }

  .topbar {
    display: flex;
    align-items: center;
    gap: var(--s-4);
    padding: var(--s-3) var(--s-5);
    border-bottom: 1px solid var(--line);
    background: var(--bg);
    flex: none;
  }

  .brand {
    display: flex;
    align-items: center;
    gap: var(--s-3);
    flex: 1;
    min-width: 0;
  }

  .logo {
    display: inline-flex;
    width: 28px;
    height: 28px;
    align-items: center;
    justify-content: center;
    border-radius: var(--r-2);
    background: var(--accent-wash);
    color: var(--accent);
    flex: none;
  }

  .logo svg {
    width: 18px;
    height: 18px;
  }

  .title {
    font-size: var(--fs-3);
    font-weight: 600;
    white-space: nowrap;
  }

  .count {
    font-size: var(--fs-1);
    color: var(--text-3);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .top-actions {
    display: flex;
    align-items: center;
    gap: var(--s-2);
    flex: none;
  }

  .banners {
    display: flex;
    flex-direction: column;
    gap: var(--s-2);
    padding: var(--s-3) var(--s-5) 0;
    flex: none;
  }

  .banners .btn {
    flex: none;
  }

  .content {
    flex: 1 1 auto;
    min-height: 0;
    overflow: auto;
    padding: var(--s-4) var(--s-5) var(--s-5);
  }

  .list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: var(--s-2);
  }

  .empty {
    height: 100%;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    text-align: center;
    gap: var(--s-3);
    padding: var(--s-8) var(--s-4);
  }

  .empty-icon {
    display: inline-flex;
    width: 56px;
    height: 56px;
    align-items: center;
    justify-content: center;
    border-radius: 50%;
    background: var(--accent-wash);
    color: var(--accent);
    margin-bottom: var(--s-2);
  }

  .empty-title {
    font-size: var(--fs-4);
    font-weight: 600;
  }

  .empty-text {
    max-width: 380px;
    color: var(--text-2);
    margin-bottom: var(--s-2);
  }

  .drawer {
    flex: none;
    height: clamp(160px, 38vh, 320px);
    min-height: 0;
  }

  @media (max-width: 820px) {
    .count {
      display: none;
    }
  }
</style>
