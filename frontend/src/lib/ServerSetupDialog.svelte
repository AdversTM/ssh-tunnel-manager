<script lang="ts">
  import { Svc, errorMessage } from './api';
  import type { Kit } from './api';
  import Modal from './Modal.svelte';
  import Icon from './Icon.svelte';

  interface Props {
    tunnelId: string;
    tunnelName: string;
    onclose: () => void;
  }

  let { tunnelId, tunnelName, onclose }: Props = $props();

  const uid = $props.id();
  const EMPTY_PUBKEY_LINE = /^PUBKEY=''\s*$/m;

  let kit: Kit | null = $state(null);
  let loading = $state(true);
  let saving = $state(false);
  let error = $state('');
  let pubKey = $state('');
  let appliedKey = $state('');
  let savedPath = $state('');
  let copied: 'script' | 'caddy' | null = $state(null);
  let seq = 0;
  let copyTimer = 0;

  const needsKey = $derived(lacksPubKey(kit));
  const showKeyInput = $derived(needsKey || appliedKey !== '');
  const canApply = $derived(!loading && pubKey.trim() !== '' && pubKey.trim() !== appliedKey);

  function lacksPubKey(k: Kit | null): boolean {
    return k !== null && EMPTY_PUBKEY_LINE.test(k.script);
  }

  async function load(key: string): Promise<void> {
    const n = ++seq;
    loading = true;
    error = '';
    try {
      const next = await Svc.ServerKit(tunnelId, key);
      if (n !== seq) return;
      kit = next;
      appliedKey = key;
      savedPath = '';
    } catch (err) {
      if (n !== seq) return;
      error = errorMessage(err);
    } finally {
      if (n === seq) loading = false;
    }
  }

  $effect(() => {
    void load('');
    return () => {
      seq++;
      window.clearTimeout(copyTimer);
    };
  });

  async function copy(text: string, which: 'script' | 'caddy'): Promise<void> {
    try {
      await navigator.clipboard.writeText(text);
      copied = which;
      window.clearTimeout(copyTimer);
      copyTimer = window.setTimeout(() => (copied = null), 1500);
    } catch (err) {
      error = `Could not copy to the clipboard: ${errorMessage(err)}`;
    }
  }

  function copyScript(): void {
    if (kit) void copy(kit.script, 'script');
  }

  function copyCaddy(): void {
    if (kit) void copy(kit.caddyfile, 'caddy');
  }

  async function saveScript(): Promise<void> {
    if (!kit) return;
    saving = true;
    try {
      savedPath = await Svc.SaveText(kit.scriptName, kit.script);
    } catch (err) {
      error = `Could not save the script: ${errorMessage(err)}`;
    } finally {
      saving = false;
    }
  }

  function onApplyKey(e: SubmitEvent): void {
    e.preventDefault();
    if (canApply) void load(pubKey.trim());
  }
</script>

<Modal title="Server setup — {tunnelName}" width={640} {onclose} testid="server-setup-dialog">
  <div class="body">
    {#if error}
      <div class="banner banner-error" data-testid="server-error" role="alert">
        <span class="banner-icon"><Icon name="alert" /></span>
        <div class="banner-body">{error}</div>
      </div>
    {/if}

    {#if loading && !kit}
      <p class="hint" role="status">Generating the server setup…</p>
    {/if}

    {#if kit}
      <section class="block">
        <h3 class="block-title">Instructions</h3>
        <pre class="pre mono" data-testid="server-instructions">{kit.instructions}</pre>
      </section>

      {#if showKeyInput}
        <form class="field" onsubmit={onApplyKey} novalidate>
          <label class="label" for="{uid}-pubkey">Public key</label>
          <div class="input-row">
            <input
              id="{uid}-pubkey"
              class="input mono"
              data-testid="server-pubkey"
              bind:value={pubKey}
              placeholder="Paste the public key (ssh-ed25519 AAAA...)"
              disabled={loading}
              autocomplete="off"
              spellcheck="false"
            />
            <button type="submit" class="btn" data-testid="server-pubkey-apply" disabled={!canApply}>
              <Icon name="key" size={14} />
              Apply
            </button>
          </div>
          <p class="hint">
            {#if needsKey}
              No public key was found for this tunnel. Paste one to embed it in the script, or fill in PUBKEY on the server.
            {:else}
              The script embeds this key.
            {/if}
          </p>
        </form>
      {/if}

      {#if kit.script !== ''}
        <section class="block">
          <div class="block-head">
            <h3 class="block-title">Script <span class="mono file">{kit.scriptName}</span></h3>
            <div class="block-actions">
              <button type="button" class="btn btn-ghost btn-sm" data-testid="server-copy-script" onclick={copyScript}>
                <Icon name="copy" size={14} />
                {copied === 'script' ? 'Copied' : 'Copy'}
              </button>
              <button type="button" class="btn btn-ghost btn-sm" data-testid="server-save-script" onclick={saveScript} disabled={saving}>
                <Icon name="download" size={14} />
                {saving ? 'Saving…' : 'Save…'}
              </button>
            </div>
          </div>
          <pre class="pre mono scroll" data-testid="server-script">{kit.script}</pre>
          {#if savedPath !== ''}
            <p class="hint" data-testid="server-saved-path" role="status">Saved to <span class="mono">{savedPath}</span></p>
          {/if}
        </section>
      {/if}

      {#if kit.caddyfile !== ''}
        <section class="block">
          <div class="block-head">
            <h3 class="block-title">Caddyfile</h3>
            <div class="block-actions">
              <button type="button" class="btn btn-ghost btn-sm" data-testid="server-copy-caddy" onclick={copyCaddy}>
                <Icon name="copy" size={14} />
                {copied === 'caddy' ? 'Copied' : 'Copy'}
              </button>
            </div>
          </div>
          <pre class="pre mono scroll" data-testid="server-caddyfile">{kit.caddyfile}</pre>
        </section>
      {/if}
    {/if}
  </div>

  {#snippet footer()}
    <span class="spacer"></span>
    <button type="button" class="btn" data-testid="server-close" onclick={onclose}>Close</button>
  {/snippet}
</Modal>

<style>
  .body {
    display: flex;
    flex-direction: column;
    gap: var(--s-4);
  }

  .block {
    display: flex;
    flex-direction: column;
    gap: var(--s-2);
    min-width: 0;
  }

  .block-head {
    display: flex;
    align-items: center;
    gap: var(--s-2);
  }

  .block-title {
    flex: 1;
    min-width: 0;
    font-size: var(--fs-0);
    font-weight: 600;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--text-3);
  }

  .file {
    margin-left: var(--s-2);
    text-transform: none;
    letter-spacing: normal;
    font-weight: 400;
  }

  .block-actions {
    display: flex;
    align-items: center;
    gap: 2px;
  }

  .pre {
    margin: 0;
    padding: var(--s-2) var(--s-3);
    border: 1px solid var(--line);
    border-radius: var(--r-1);
    background: var(--log-bg);
    font-size: var(--fs-1);
    line-height: 1.5;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    user-select: text;
  }

  .scroll {
    max-height: clamp(120px, 32vh, 280px);
    overflow: auto;
  }
</style>
