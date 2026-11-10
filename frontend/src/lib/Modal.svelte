<script module lang="ts">
  // Open dialogs, bottom to top — only the topmost one reacts to Escape.
  const stack: symbol[] = [];
</script>

<script lang="ts">
  import type { Snippet } from 'svelte';

  interface Props {
    /** Accessible title; rendered in the header unless `header` snippet is given. */
    title: string;
    /** Escape closes the dialog when true. Backdrop clicks never close (desktop convention). */
    dismissible?: boolean;
    onclose?: () => void;
    width?: number;
    testid?: string;
    /** Extra data-* attributes for the dialog root (e.g. data-changed). */
    data?: Record<string, string>;
    tone?: 'default' | 'danger';
    children: Snippet;
    footer?: Snippet;
  }

  let {
    title,
    dismissible = true,
    onclose,
    width = 520,
    testid,
    data = {},
    tone = 'default',
    children,
    footer,
  }: Props = $props();

  const uid = $props.id();
  const token = Symbol('modal');
  let root: HTMLDivElement | undefined = $state();

  const dataAttrs = $derived(
    Object.fromEntries(Object.entries(data).map(([k, v]) => [`data-${k}`, v])),
  );

  $effect(() => {
    stack.push(token);
    return () => {
      const i = stack.indexOf(token);
      if (i !== -1) stack.splice(i, 1);
    };
  });

  $effect(() => {
    if (!root) return;
    const previous = document.activeElement;
    const focusable =
      'input:not([type="hidden"]):not([disabled]), select:not([disabled]), textarea:not([disabled]), button:not([disabled])';
    const first =
      root.querySelector<HTMLElement>(`.modal-body ${focusable}`) ??
      root.querySelector<HTMLElement>(`.modal-footer ${focusable}`);
    (first ?? root).focus();
    return () => {
      if (previous instanceof HTMLElement && previous.isConnected) previous.focus();
    };
  });

  function onKeydown(e: KeyboardEvent): void {
    if (e.key !== 'Escape') return;
    if (stack[stack.length - 1] !== token) return;
    e.preventDefault();
    if (dismissible) onclose?.();
  }
</script>

<svelte:window onkeydown={onKeydown} />

<div class="modal-backdrop">
  <div
    class="modal"
    class:danger={tone === 'danger'}
    role="dialog"
    aria-modal="true"
    aria-labelledby="{uid}-title"
    tabindex="-1"
    style="--modal-w: {width}px"
    data-testid={testid}
    {...dataAttrs}
    bind:this={root}
  >
    <header class="modal-header">
      <h2 class="modal-title" id="{uid}-title">{title}</h2>
      {#if dismissible && onclose}
        <button type="button" class="btn btn-ghost btn-icon" onclick={onclose} aria-label="Close">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" aria-hidden="true">
            <path d="M18 6 6 18M6 6l12 12" />
          </svg>
        </button>
      {/if}
    </header>
    <div class="modal-body">
      {@render children()}
    </div>
    {#if footer}
      <footer class="modal-footer">
        {@render footer()}
      </footer>
    {/if}
  </div>
</div>

<style>
  .modal.danger .modal-title {
    color: var(--err);
  }
</style>
