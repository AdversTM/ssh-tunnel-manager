<script lang="ts">
  import { Svc, TunnelState, errorMessage } from './api';
  import type { TunnelView } from './api';
  import { forwardSpec } from './format';
  import Modal from './Modal.svelte';
  import Icon from './Icon.svelte';

  interface Props {
    view: TunnelView;
    ondeleted: () => void;
    oncancel: () => void;
  }

  let { view, ondeleted, oncancel }: Props = $props();

  let busy = $state(false);
  let error = $state('');

  const running = $derived(view.status.state !== TunnelState.Stopped);

  async function confirm(): Promise<void> {
    busy = true;
    error = '';
    try {
      await Svc.DeleteTunnel(view.tunnel.id);
      ondeleted();
    } catch (err) {
      error = errorMessage(err);
    } finally {
      busy = false;
    }
  }
</script>

<Modal title="Delete tunnel?" width={440} onclose={oncancel} tone="danger" testid="confirm-delete-dialog">
  <p>
    <strong>{view.tunnel.name}</strong>
    <span class="mono spec">{forwardSpec(view.tunnel)}</span>
  </p>
  <p class="muted detail">
    {#if running}
      The tunnel will be disconnected first. Its configuration and any stored secret are removed. This cannot be undone.
    {:else}
      Its configuration and any stored secret are removed. This cannot be undone.
    {/if}
  </p>

  {#snippet footer()}
    {#if error}
      <div class="error" role="alert">
        <Icon name="alert" size={14} />
        <span>{error}</span>
      </div>
    {:else}
      <span class="spacer"></span>
    {/if}
    <button type="button" class="btn" data-testid="cancel-delete" onclick={oncancel} disabled={busy}>Cancel</button>
    <button type="button" class="btn btn-danger" data-testid="confirm-delete" onclick={confirm} disabled={busy}>
      {busy ? 'Deleting…' : 'Delete'}
    </button>
  {/snippet}
</Modal>

<style>
  .spec {
    display: block;
    margin-top: var(--s-1);
    font-size: var(--fs-1);
    color: var(--text-2);
  }

  .detail {
    margin-top: var(--s-3);
    font-size: var(--fs-1);
  }

  .error {
    display: flex;
    align-items: flex-start;
    gap: var(--s-2);
    flex: 1;
    min-width: 0;
    color: var(--err);
    font-size: var(--fs-1);
    user-select: text;
    overflow-wrap: anywhere;
  }

  .error :global(svg) {
    flex: none;
    margin-top: 2px;
  }
</style>
