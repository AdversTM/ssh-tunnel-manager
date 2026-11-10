<script lang="ts">
  import { Svc, errorMessage } from './api';
  import type { HostKeyRequest } from './api';
  import Modal from './Modal.svelte';
  import Icon from './Icon.svelte';

  interface Props {
    request: HostKeyRequest;
    onerror: (message: string) => void;
  }

  let { request, onerror }: Props = $props();

  let verified = $state(false);
  let busy = $state(false);

  // A new request (different id) must start unverified.
  $effect(() => {
    void request.id;
    verified = false;
  });

  async function answer(accept: boolean): Promise<void> {
    busy = true;
    try {
      await Svc.AnswerHostKey(request.id, accept);
    } catch (err) {
      onerror(`Host key answer for "${request.tunnelName}": ${errorMessage(err)}`);
    } finally {
      busy = false;
    }
  }
</script>

<Modal
  title={request.changed ? 'Host key changed' : 'Verify host key'}
  dismissible={false}
  width={560}
  testid="hostkey-dialog"
  data={{ changed: request.changed ? 'true' : 'false' }}
  tone={request.changed ? 'danger' : 'default'}
>
  {#if request.changed}
    <div class="warning" role="alert">
      <Icon name="alert" size={20} />
      <div>
        <p class="warning-title">HOST KEY CHANGED — possible man-in-the-middle attack</p>
        <p class="warning-text">
          The key presented by <span class="mono">{request.host}</span> does not match the key stored for
          <strong>{request.tunnelName}</strong>. The connection has been blocked.
        </p>
      </div>
    </div>

    <div class="pair">
      <div class="fp-block">
        <p class="label">Stored key</p>
        <p class="fp mono old" data-testid="hostkey-old-fingerprint">{request.oldFingerprint || 'unknown'}</p>
      </div>
      <div class="fp-block">
        <p class="label">Key offered now{request.keyType ? ` (${request.keyType})` : ''}</p>
        <p class="fp mono" data-testid="hostkey-fingerprint">{request.fingerprint}</p>
      </div>
    </div>

    <p class="guidance">
      Only replace the stored key if the server was reinstalled or its host key was intentionally rotated.
      Confirm the new fingerprint through a channel you trust before continuing.
    </p>

    <label class="check confirm">
      <input type="checkbox" data-testid="hostkey-replace-confirm" bind:checked={verified} disabled={busy} />
      <span>I verified the new fingerprint with the server administrator</span>
    </label>
  {:else}
    <dl class="facts">
      <dt>Tunnel</dt>
      <dd>{request.tunnelName}</dd>
      <dt>Host</dt>
      <dd class="mono">{request.host}</dd>
      {#if request.keyType}
        <dt>Key type</dt>
        <dd class="mono">{request.keyType}</dd>
      {/if}
    </dl>

    <p class="label">Fingerprint</p>
    <p class="fp fp-large mono" data-testid="hostkey-fingerprint">{request.fingerprint}</p>

    <p class="guidance">
      This host is not known yet. Compare with the server:
      <code class="mono cmd">ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub</code>
    </p>
  {/if}

  {#snippet footer()}
    <span class="spacer"></span>
    {#if request.changed}
      <button type="button" class="btn btn-primary" data-testid="hostkey-reject" disabled={busy} onclick={() => answer(false)}>
        Keep blocked
      </button>
      <button
        type="button"
        class="btn btn-danger"
        data-testid="hostkey-replace"
        disabled={busy || !verified}
        onclick={() => answer(true)}
      >
        Replace key
      </button>
    {:else}
      <button type="button" class="btn" data-testid="hostkey-reject" disabled={busy} onclick={() => answer(false)}>
        Reject
      </button>
      <button type="button" class="btn btn-primary" data-testid="hostkey-accept" disabled={busy} onclick={() => answer(true)}>
        Trust and connect
      </button>
    {/if}
  {/snippet}
</Modal>

<style>
  .warning {
    display: flex;
    gap: var(--s-3);
    padding: var(--s-3);
    border-radius: var(--r-2);
    background: var(--err-wash);
    color: var(--err);
    margin-bottom: var(--s-4);
  }

  .warning :global(svg) {
    flex: none;
    margin-top: 1px;
  }

  .warning-text {
    color: var(--text);
  }

  .warning-title {
    font-weight: 700;
    letter-spacing: 0.02em;
    margin-bottom: var(--s-1);
  }

  .pair {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: var(--s-3);
  }

  .fp-block {
    min-width: 0;
  }

  .label {
    font-size: var(--fs-0);
    font-weight: 600;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--text-3);
    margin-bottom: var(--s-1);
  }

  .fp {
    padding: var(--s-2) var(--s-3);
    border-radius: var(--r-1);
    background: var(--surface-3);
    font-size: var(--fs-1);
    overflow-wrap: anywhere;
    user-select: text;
  }

  .fp.old {
    color: var(--text-2);
  }

  .fp-large {
    font-size: var(--fs-3);
    letter-spacing: 0.02em;
    margin-bottom: var(--s-4);
  }

  .guidance {
    margin-top: var(--s-4);
    color: var(--text-2);
    font-size: var(--fs-1);
  }

  .cmd {
    display: block;
    margin-top: var(--s-2);
    padding: var(--s-1) var(--s-2);
    border-radius: var(--r-1);
    background: var(--surface-3);
    color: var(--text);
    font-size: var(--fs-1);
    user-select: text;
  }

  .confirm {
    margin-top: var(--s-4);
    align-items: flex-start;
  }

  .confirm input {
    margin-top: 1px;
  }

  .facts {
    display: grid;
    grid-template-columns: auto 1fr;
    gap: var(--s-1) var(--s-4);
    margin: 0 0 var(--s-4);
    font-size: var(--fs-2);
  }

  .facts dt {
    color: var(--text-3);
  }

  .facts dd {
    margin: 0;
    user-select: text;
  }

  @media (max-width: 520px) {
    .pair {
      grid-template-columns: 1fr;
    }
  }
</style>
