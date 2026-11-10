<script lang="ts">
  import { Svc, TunnelState, errorMessage } from './api';
  import type { TunnelView } from './api';
  import {
    backendLabel,
    canReviewHostKey,
    endpointSpec,
    forwardSpec,
    isActive,
    statusText,
    statusTone,
  } from './format';
  import Icon from './Icon.svelte';

  interface Props {
    view: TunnelView;
    now: number;
    onedit: () => void;
    onlogs: () => void;
    onserver: () => void;
    ondelete: () => void;
    onerror: (message: string) => void;
  }

  let { view, now, onedit, onlogs, onserver, ondelete, onerror }: Props = $props();

  const tunnel = $derived(view.tunnel);
  const status = $derived(view.status);
  const tone = $derived(statusTone(status.state));
  const active = $derived(isActive(status.state));
  const text = $derived(statusText(status, now));
  const showLastError = $derived(
    (status.state === TunnelState.Error || status.state === TunnelState.Backoff) &&
      (status.lastError ?? '').length > 0,
  );
  const review = $derived(canReviewHostKey(tunnel, status));

  // Optimistic switch position; snaps back to the supervisor's truth on failure.
  let checked = $state(false);
  let busy = $state(false);
  $effect(() => {
    checked = active;
  });

  async function onToggle(): Promise<void> {
    const want = checked;
    busy = true;
    try {
      if (want) {
        await Svc.Connect(tunnel.id);
      } else {
        await Svc.Disconnect(tunnel.id);
      }
    } catch (err) {
      checked = active;
      onerror(`${want ? 'Connect' : 'Disconnect'} "${tunnel.name}": ${errorMessage(err)}`);
    } finally {
      busy = false;
    }
  }

  async function onReview(): Promise<void> {
    try {
      await Svc.ReviewHostKey(tunnel.id);
    } catch (err) {
      onerror(`Review host key for "${tunnel.name}": ${errorMessage(err)}`);
    }
  }
</script>

<li
  class="row"
  data-testid="tunnel-row"
  data-name={tunnel.name}
  data-state={status.state}
>
  <span
    class="dot"
    class:tone-ok={tone === 'ok'}
    class:tone-warn={tone === 'warn'}
    class:tone-err={tone === 'err'}
    class:tone-off={tone === 'off'}
    class:pulse={status.state === TunnelState.Connecting}
    aria-hidden="true"
  ></span>

  <div class="main">
    <div class="head">
      <h3 class="name" title={tunnel.name}>{tunnel.name}</h3>
      <span
        class="status mono"
        class:tone-ok={tone === 'ok'}
        class:tone-warn={tone === 'warn'}
        class:tone-err={tone === 'err'}
        class:tone-off={tone === 'off'}
        data-testid="tunnel-status"
      >
        {text}
      </span>
    </div>
    <div class="route mono" title="{forwardSpec(tunnel)} · {endpointSpec(tunnel)} · {backendLabel(tunnel.backend)}">
      <span class="spec">{forwardSpec(tunnel)}</span>
      <span class="sep">·</span>
      <span>{endpointSpec(tunnel)}</span>
      <span class="sep">·</span>
      <span>{backendLabel(tunnel.backend)}</span>
    </div>
    {#if showLastError}
      <p class="last-error" title={status.lastError}>{status.lastError}</p>
    {/if}
  </div>

  <div class="actions">
    {#if review}
      <button type="button" class="btn btn-sm review" data-testid="tunnel-review-hostkey" onclick={onReview}>
        <Icon name="shield" size={14} />
        Review host key
      </button>
    {/if}
    <input
      type="checkbox"
      role="switch"
      class="switch"
      data-testid="tunnel-toggle"
      aria-label="{active ? 'Disconnect' : 'Connect'} {tunnel.name}"
      title={active ? 'Disconnect' : 'Connect'}
      bind:checked
      disabled={busy}
      onchange={onToggle}
    />
    <div class="buttons">
      <button type="button" class="btn btn-ghost btn-sm" data-testid="tunnel-edit" title="Edit" onclick={onedit}>
        <Icon name="pencil" size={14} />
        <span class="lbl">Edit</span>
      </button>
      <button type="button" class="btn btn-ghost btn-sm" data-testid="tunnel-logs" title="Logs" onclick={onlogs}>
        <Icon name="terminal" size={14} />
        <span class="lbl">Logs</span>
      </button>
      <button type="button" class="btn btn-ghost btn-sm" data-testid="tunnel-server-setup" title="Server setup" onclick={onserver}>
        <Icon name="server" size={14} />
        <span class="lbl">Server</span>
      </button>
      <button
        type="button"
        class="btn btn-ghost btn-sm danger"
        data-testid="tunnel-delete"
        title="Delete"
        onclick={ondelete}
      >
        <Icon name="trash" size={14} />
        <span class="lbl">Delete</span>
      </button>
    </div>
  </div>
</li>

<style>
  .row {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr) auto;
    align-items: center;
    gap: var(--s-3);
    padding: var(--s-3) var(--s-4);
    background: var(--surface);
    border: 1px solid var(--line);
    border-radius: var(--r-2);
    transition: background var(--t-fast) var(--ease);
  }

  .row:hover {
    background: var(--surface-2);
  }

  .dot {
    position: relative;
    width: 10px;
    height: 10px;
    border-radius: 50%;
    background: var(--dot, var(--off));
    flex: none;
  }

  .dot.tone-ok {
    --dot: var(--ok);
  }
  .dot.tone-warn {
    --dot: var(--warn);
  }
  .dot.tone-err {
    --dot: var(--err);
  }
  .dot.tone-off {
    background: transparent;
    box-shadow: inset 0 0 0 2px var(--off);
  }

  .dot.pulse::after {
    content: '';
    position: absolute;
    inset: -4px;
    border-radius: 50%;
    border: 2px solid var(--dot);
    animation: ring 1.4s var(--ease) infinite;
  }

  @keyframes ring {
    0% {
      transform: scale(0.6);
      opacity: 0.9;
    }
    100% {
      transform: scale(1.5);
      opacity: 0;
    }
  }

  .main {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }

  .head {
    display: flex;
    align-items: baseline;
    gap: var(--s-3);
    min-width: 0;
  }

  .name {
    font-size: var(--fs-3);
    font-weight: 600;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    min-width: 0;
  }

  .status {
    font-size: var(--fs-1);
    white-space: nowrap;
    flex: none;
  }

  .status.tone-ok {
    color: var(--ok);
  }
  .status.tone-warn {
    color: var(--warn);
  }
  .status.tone-err {
    color: var(--err);
  }
  .status.tone-off {
    color: var(--text-3);
  }

  .route {
    font-size: var(--fs-1);
    color: var(--text-2);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    user-select: text;
  }

  .spec {
    color: var(--text);
  }

  .sep {
    color: var(--text-3);
    margin: 0 var(--s-1);
  }

  .last-error {
    font-size: var(--fs-1);
    color: var(--text-3);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    user-select: text;
  }

  .actions {
    display: flex;
    align-items: center;
    gap: var(--s-3);
  }

  .buttons {
    display: flex;
    align-items: center;
    gap: 2px;
  }

  .review {
    color: var(--err);
    border-color: var(--err);
  }

  @media (max-width: 880px) {
    .lbl {
      display: none;
    }
  }
</style>
