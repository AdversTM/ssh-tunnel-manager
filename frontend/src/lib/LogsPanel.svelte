<script lang="ts">
  import { tick } from 'svelte';
  import { Events } from '@wailsio/runtime';
  import { LogEventName, Svc, errorMessage } from './api';
  import type { Line } from './api';
  import { formatClock } from './format';
  import Icon from './Icon.svelte';

  interface Props {
    tunnelId: string;
    tunnelName: string;
    onclose: () => void;
    onerror: (message: string) => void;
  }

  let { tunnelId, tunnelName, onclose, onerror }: Props = $props();

  const MAX_LINES = 2000;
  const uid = $props.id();

  let lines: Line[] = $state.raw([]);
  let verbose = $state(false);
  let loading = $state(false);
  let stickToBottom = $state(true);
  let scroller: HTMLDivElement | undefined = $state();

  // Lines received over the event bus while the initial fetch is in flight.
  let pending: Line[] = [];
  let requestSeq = 0;

  function cap(list: Line[]): Line[] {
    return list.length > MAX_LINES ? list.slice(list.length - MAX_LINES) : list;
  }

  function sameLine(a: Line, b: Line): boolean {
    return a.time === b.time && a.text === b.text && a.debug === b.debug;
  }

  // (Re)load history whenever the tunnel or the verbosity changes.
  $effect(() => {
    const id = tunnelId;
    const v = verbose;
    const seq = ++requestSeq;
    pending = [];
    loading = true;
    stickToBottom = true;
    Svc.Logs(id, v)
      .then((result) => {
        if (seq !== requestSeq) return;
        const history = result ?? [];
        const tail = history.slice(-50);
        const fresh = pending.filter((p) => !tail.some((h) => sameLine(h, p)));
        lines = cap([...history, ...fresh]);
      })
      .catch((err: unknown) => {
        if (seq !== requestSeq) return;
        lines = [];
        onerror(`Load logs for "${tunnelName}": ${errorMessage(err)}`);
      })
      .finally(() => {
        if (seq === requestSeq) {
          loading = false;
          pending = [];
        }
      });
  });

  // Live tail.
  $effect(() => {
    const id = tunnelId;
    const v = verbose;
    return Events.On(LogEventName, (ev) => {
      const { tunnelId: from, line } = ev.data;
      if (from !== id) return;
      if (line.debug && !v) return;
      if (loading) {
        pending.push(line);
        return;
      }
      lines = cap([...lines, line]);
    });
  });

  // Follow the tail unless the user scrolled up.
  $effect(() => {
    void lines.length;
    if (!stickToBottom || !scroller) return;
    const el = scroller;
    void tick().then(() => {
      el.scrollTop = el.scrollHeight;
    });
  });

  function onScroll(): void {
    if (!scroller) return;
    const distance = scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight;
    stickToBottom = distance < 12;
  }

  function clearView(): void {
    lines = [];
  }
</script>

<section class="logs" data-testid="logs-panel" data-tunnel-id={tunnelId} aria-labelledby="{uid}-title">
  <header class="bar">
    <Icon name="terminal" size={14} />
    <h2 class="title" id="{uid}-title">
      <span class="name">{tunnelName}</span>
      <span class="muted">· logs</span>
    </h2>
    {#if loading}
      <span class="muted loading">Loading…</span>
    {/if}
    <label class="check">
      <input type="checkbox" data-testid="logs-verbose" bind:checked={verbose} />
      <span>Verbose</span>
    </label>
    <button type="button" class="btn btn-ghost btn-sm" onclick={clearView} title="Clear the view (does not delete logs)">
      <Icon name="eraser" size={14} />
      Clear view
    </button>
    <button type="button" class="btn btn-ghost btn-icon btn-sm" data-testid="logs-close" aria-label="Close logs" onclick={onclose}>
      <Icon name="close" size={14} />
    </button>
  </header>

  <div class="scroll mono" bind:this={scroller} onscroll={onScroll}>
    {#if lines.length === 0 && !loading}
      <p class="empty">No log output yet.</p>
    {/if}
    {#each lines as line}
      <div class="line" class:debug={line.debug} data-testid="log-line">
        <span class="ts">{formatClock(line.time)}</span>
        <span class="txt">{line.text}</span>
      </div>
    {/each}
  </div>
</section>

<style>
  .logs {
    display: flex;
    flex-direction: column;
    min-height: 0;
    height: 100%;
    background: var(--log-bg);
    border-top: 1px solid var(--line);
  }

  .bar {
    display: flex;
    align-items: center;
    gap: var(--s-3);
    padding: var(--s-1) var(--s-3) var(--s-1) var(--s-4);
    border-bottom: 1px solid var(--line);
    color: var(--text-2);
    flex: none;
  }

  .title {
    flex: 1;
    min-width: 0;
    font-size: var(--fs-1);
    font-weight: 600;
    color: var(--text);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .name {
    color: var(--text);
  }

  .loading {
    font-size: var(--fs-0);
  }

  .scroll {
    flex: 1;
    min-height: 0;
    overflow: auto;
    padding: var(--s-2) var(--s-4);
    font-size: var(--fs-1);
    line-height: 1.5;
    user-select: text;
  }

  .empty {
    font-family: var(--font-ui);
    color: var(--text-3);
    padding: var(--s-2) 0;
  }

  .line {
    display: flex;
    gap: var(--s-3);
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }

  .line.debug {
    color: var(--text-3);
  }

  .ts {
    flex: none;
    color: var(--text-3);
    user-select: none;
  }

  .line.debug .ts {
    opacity: 0.7;
  }

  .txt {
    min-width: 0;
  }
</style>
