<script lang="ts">
  import { Svc, errorMessage } from './api';
  import type { Settings } from './api';
  import Modal from './Modal.svelte';
  import Icon from './Icon.svelte';

  interface Props {
    onclose: () => void;
  }

  let { onclose }: Props = $props();

  const uid = $props.id();
  const SSH_DEFAULT = 'C:\\Windows\\System32\\OpenSSH\\ssh.exe';
  const PLINK_DEFAULT = 'C:\\Program Files\\PuTTY\\plink.exe';

  let settings: Settings | null = $state(null);
  let sshPath = $state('');
  let plinkPath = $state('');
  let autostart = $state(false);
  let notifications = $state(true);
  let loading = $state(true);
  let saving = $state(false);
  let error = $state('');

  $effect(() => {
    let cancelled = false;
    Svc.Settings()
      .then((s) => {
        if (cancelled) return;
        settings = s;
        sshPath = s.sshPath ?? '';
        plinkPath = s.plinkPath ?? '';
        autostart = s.autostart;
        notifications = s.notifications;
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        error = `Could not load settings: ${errorMessage(err)}`;
      })
      .finally(() => {
        if (!cancelled) loading = false;
      });
    return () => {
      cancelled = true;
    };
  });

  async function browse(target: 'ssh' | 'plink'): Promise<void> {
    try {
      const picked = await Svc.PickFile(target === 'ssh' ? 'Select ssh.exe' : 'Select plink.exe');
      if (picked === '') return;
      if (target === 'ssh') {
        sshPath = picked;
      } else {
        plinkPath = picked;
      }
    } catch (err) {
      error = `Could not open the file dialog: ${errorMessage(err)}`;
    }
  }

  async function save(): Promise<void> {
    if (!settings) return;
    saving = true;
    error = '';
    try {
      const next: Settings = {
        ...settings,
        autostart,
        notifications,
        sshPath: sshPath.trim(),
        plinkPath: plinkPath.trim(),
      };
      await Svc.SaveSettings(next);
      onclose();
    } catch (err) {
      error = errorMessage(err);
    } finally {
      saving = false;
    }
  }

  function onSubmit(e: SubmitEvent): void {
    e.preventDefault();
    void save();
  }
</script>

<Modal title="Settings" width={520} {onclose} testid="settings-panel">
  <form id="{uid}-form" onsubmit={onSubmit} novalidate>
    <div class="field">
      <label class="label" for="{uid}-ssh">ssh.exe path</label>
      <div class="input-row">
        <input
          id="{uid}-ssh"
          class="input mono"
          data-testid="settings-ssh-path"
          bind:value={sshPath}
          placeholder={SSH_DEFAULT}
          disabled={loading}
          autocomplete="off"
          spellcheck="false"
        />
        <button type="button" class="btn" onclick={() => browse('ssh')} disabled={loading} aria-label="Browse for ssh.exe">
          <Icon name="folder" size={14} />
          Browse…
        </button>
      </div>
      <p class="hint">Leave blank to use the Windows OpenSSH client.</p>
    </div>

    <div class="field">
      <label class="label" for="{uid}-plink">plink.exe path</label>
      <div class="input-row">
        <input
          id="{uid}-plink"
          class="input mono"
          data-testid="settings-plink-path"
          bind:value={plinkPath}
          placeholder={PLINK_DEFAULT}
          disabled={loading}
          autocomplete="off"
          spellcheck="false"
        />
        <button type="button" class="btn" onclick={() => browse('plink')} disabled={loading} aria-label="Browse for plink.exe">
          <Icon name="folder" size={14} />
          Browse…
        </button>
      </div>
      <p class="hint">Leave blank to use the default PuTTY install location.</p>
    </div>

    <div class="field">
      <label class="check">
        <input type="checkbox" data-testid="settings-autostart" bind:checked={autostart} disabled={loading} />
        <span>Start with Windows</span>
      </label>
      <p class="hint">Adds SSH Tunnel Manager to your sign-in apps; auto-connect tunnels connect after sign-in.</p>
    </div>

    <label class="check">
      <input type="checkbox" data-testid="settings-notifications" bind:checked={notifications} disabled={loading} />
      <span>Show notifications</span>
    </label>
  </form>

  {#snippet footer()}
    {#if error}
      <div class="error" data-testid="settings-error" role="alert">
        <Icon name="alert" size={14} />
        <span>{error}</span>
      </div>
    {:else}
      <span class="spacer"></span>
    {/if}
    <button type="button" class="btn" onclick={onclose} disabled={saving}>Cancel</button>
    <button
      type="submit"
      class="btn btn-primary"
      data-testid="settings-save"
      form="{uid}-form"
      disabled={saving || loading || settings === null}
    >
      {saving ? 'Saving…' : 'Save'}
    </button>
  {/snippet}
</Modal>

<style>
  form {
    display: flex;
    flex-direction: column;
    gap: var(--s-4);
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
