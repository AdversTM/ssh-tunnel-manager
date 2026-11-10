<script lang="ts">
  import { tick, untrack } from 'svelte';
  import { Agent, AuthMethod, Backend, ForwardType, ServerMode, Svc, errorMessage } from './api';
  import type { Tunnel } from './api';
  import { applyForm, toForm, validateForm } from './validate';
  import type { FieldErrors, FieldName } from './validate';
  import Modal from './Modal.svelte';
  import Icon from './Icon.svelte';

  interface Props {
    initial: Tunnel;
    hasSecret: boolean;
    mode: 'new' | 'edit';
    onsaved: (saved: Tunnel) => void;
    oncancel: () => void;
  }

  let { initial, hasSecret, mode, onsaved, oncancel }: Props = $props();

  const uid = $props.id();
  const base: Tunnel = untrack(() => $state.snapshot(initial));
  let f = $state(toForm(base));
  let secret = $state('');
  let removeSecret = $state(false);
  let attempted = $state(false);
  let saving = $state(false);
  let formError = $state('');
  let formEl: HTMLFormElement | undefined = $state();

  const errors: FieldErrors = $derived(attempted ? validateForm(f) : {});
  const isPlink = $derived(f.backend === Backend.BackendPlink);
  const isKey = $derived(f.authMethod === AuthMethod.AuthKey);
  const isAgent = $derived(f.authMethod === AuthMethod.AuthAgent);
  const isPassword = $derived(f.authMethod === AuthMethod.AuthPassword);
  const isCaddy = $derived(f.serverMode === ServerMode.ServerSSHDCaddy);
  const secretLabel = $derived(isPassword ? 'Password' : 'Key passphrase (optional)');
  const secretPlaceholder = $derived(
    hasSecret ? 'Stored — leave empty to keep' : isPassword ? '' : 'Leave empty if the key has no passphrase',
  );

  function id(field: string): string {
    return `${uid}-${field}`;
  }

  function invalid(field: FieldName): true | undefined {
    return errors[field] ? true : undefined;
  }

  function onBackendChange(): void {
    if (f.backend === Backend.BackendPlink) f.agent = Agent.AgentPageant;
  }

  function onMethodChange(): void {
    if (f.authMethod === AuthMethod.AuthAgent && f.backend === Backend.BackendPlink) {
      f.agent = Agent.AgentPageant;
    }
  }

  function onRemoveSecretChange(): void {
    if (removeSecret) secret = '';
  }

  async function browseKey(): Promise<void> {
    try {
      const picked = await Svc.PickFile('Select private key');
      if (picked !== '') f.keyPath = picked;
    } catch (err) {
      formError = `Could not open the file dialog: ${errorMessage(err)}`;
    }
  }

  function secretArgument(): string | null {
    if (isAgent) return null;
    if (removeSecret) return '';
    return secret.length > 0 ? secret : null;
  }

  async function save(): Promise<void> {
    attempted = true;
    formError = '';
    const errs = validateForm(f);
    const firstInvalid = (Object.keys(errs) as FieldName[])[0];
    if (firstInvalid) {
      await tick();
      formEl?.querySelector<HTMLElement>(`#${CSS.escape(id(firstInvalid))}`)?.focus();
      return;
    }
    saving = true;
    try {
      const saved = await Svc.SaveTunnel(applyForm(base, $state.snapshot(f)), secretArgument());
      onsaved(saved);
    } catch (err) {
      formError = errorMessage(err);
    } finally {
      saving = false;
    }
  }

  function onSubmit(e: SubmitEvent): void {
    e.preventDefault();
    void save();
  }
</script>

<Modal title={mode === 'new' ? 'Add tunnel' : 'Edit tunnel'} width={580} onclose={oncancel} testid="tunnel-form-dialog">
  <form id={id('form')} data-testid="tunnel-form" novalidate onsubmit={onSubmit} bind:this={formEl}>
    <section class="group">
      <div class="field">
        <label class="label" for={id('name')}>Name</label>
        <input
          id={id('name')}
          class="input"
          data-testid="form-name"
          bind:value={f.name}
          aria-invalid={invalid('name')}
          autocomplete="off"
          spellcheck="false"
        />
        {#if errors.name}<p class="field-error" data-testid="field-error" data-field="name">{errors.name}</p>{/if}
      </div>
    </section>

    <section class="group">
      <h3 class="group-title">Connection</h3>
      <div class="grid host-grid">
        <div class="field">
          <label class="label" for={id('backend')}>Client</label>
          <select id={id('backend')} class="select" data-testid="form-backend" bind:value={f.backend} onchange={onBackendChange}>
            <option value={Backend.BackendOpenSSH}>OpenSSH (ssh.exe)</option>
            <option value={Backend.BackendPlink}>plink (PuTTY)</option>
          </select>
        </div>
        <div class="field">
          <label class="label" for={id('host')}>Host</label>
          <input
            id={id('host')}
            class="input mono"
            data-testid="form-host"
            bind:value={f.host}
            aria-invalid={invalid('host')}
            placeholder="example.com"
            autocomplete="off"
            spellcheck="false"
          />
          {#if errors.host}<p class="field-error" data-testid="field-error" data-field="host">{errors.host}</p>{/if}
        </div>
        <div class="field">
          <label class="label" for={id('port')}>Port</label>
          <input
            id={id('port')}
            class="input mono"
            data-testid="form-port"
            bind:value={f.port}
            aria-invalid={invalid('port')}
            inputmode="numeric"
            autocomplete="off"
          />
          {#if errors.port}<p class="field-error" data-testid="field-error" data-field="port">{errors.port}</p>{/if}
        </div>
      </div>
      <div class="field">
        <label class="label" for={id('user')}>User</label>
        <input
          id={id('user')}
          class="input mono"
          data-testid="form-user"
          bind:value={f.user}
          aria-invalid={invalid('user')}
          autocomplete="off"
          spellcheck="false"
        />
        {#if errors.user}<p class="field-error" data-testid="field-error" data-field="user">{errors.user}</p>{/if}
      </div>
    </section>

    <section class="group">
      <h3 class="group-title">Authentication</h3>
      <div class="field">
        <label class="label" for={id('method')}>Method</label>
        <select id={id('method')} class="select" data-testid="form-auth-method" bind:value={f.authMethod} onchange={onMethodChange}>
          <option value={AuthMethod.AuthKey}>Private key file</option>
          <option value={AuthMethod.AuthAgent}>SSH agent</option>
          <option value={AuthMethod.AuthPassword}>Password</option>
        </select>
      </div>

      {#if isKey}
        <div class="field">
          <label class="label" for={id('keyPath')}>Key path</label>
          <div class="input-row">
            <input
              id={id('keyPath')}
              class="input mono"
              data-testid="form-key-path"
              bind:value={f.keyPath}
              aria-invalid={invalid('keyPath')}
              placeholder={isPlink ? 'C:\\Users\\you\\.ssh\\id_ed25519.ppk' : 'C:\\Users\\you\\.ssh\\id_ed25519'}
              autocomplete="off"
              spellcheck="false"
            />
            <button type="button" class="btn" data-testid="form-key-browse" onclick={browseKey}>
              <Icon name="folder" size={14} />
              Browse…
            </button>
          </div>
          {#if errors.keyPath}<p class="field-error" data-testid="field-error" data-field="keyPath">{errors.keyPath}</p>{/if}
          {#if isPlink}
            <p class="hint">A .ppk with a passphrase works only via Pageant</p>
          {/if}
        </div>
      {/if}

      {#if isAgent}
        <div class="field">
          <label class="label" for={id('agent')}>Agent</label>
          <select id={id('agent')} class="select" data-testid="form-agent" bind:value={f.agent} aria-invalid={invalid('agent')}>
            <option value={Agent.AgentPageant}>Pageant</option>
            {#if !isPlink}
              <option value={Agent.AgentOpenSSHAgent}>Windows OpenSSH agent</option>
            {/if}
          </select>
          {#if errors.agent}<p class="field-error" data-testid="field-error" data-field="agent">{errors.agent}</p>{/if}
          {#if isPlink}
            <p class="hint">plink can only use Pageant.</p>
          {/if}
        </div>
      {/if}

      {#if !isAgent}
        <div class="field">
          <label class="label" for={id('secret')}>{secretLabel}</label>
          <input
            id={id('secret')}
            class="input"
            type="password"
            data-testid="form-secret"
            bind:value={secret}
            placeholder={secretPlaceholder}
            disabled={removeSecret}
            autocomplete="new-password"
          />
          {#if hasSecret}
            <label class="check">
              <input type="checkbox" data-testid="form-remove-secret" bind:checked={removeSecret} onchange={onRemoveSecretChange} />
              <span>Remove stored secret</span>
            </label>
          {/if}
        </div>
      {/if}
    </section>

    <section class="group">
      <h3 class="group-title">Forwarding</h3>
      <div class="field">
        <label class="label" for={id('forwardType')}>Direction</label>
        <select id={id('forwardType')} class="select" data-testid="form-forward-type" bind:value={f.forwardType}>
          <option value={ForwardType.ForwardRemote}>Remote (-R)</option>
          <option value={ForwardType.ForwardLocal}>Local (-L)</option>
        </select>
        <p class="hint">
          {#if f.forwardType === ForwardType.ForwardLocal}
            This PC listens on the bind address and port; connections are forwarded to the target as reached from the server.
          {:else}
            The server listens on the bind address and port; connections are forwarded to the target as reached from this PC.
          {/if}
        </p>
      </div>
      <div class="grid pair-grid">
        <div class="field">
          <label class="label" for={id('bindAddress')}>Bind address</label>
          <input
            id={id('bindAddress')}
            class="input mono"
            data-testid="form-bind-address"
            bind:value={f.bindAddress}
            aria-invalid={invalid('bindAddress')}
            placeholder="127.0.0.1"
            autocomplete="off"
            spellcheck="false"
          />
          {#if errors.bindAddress}<p class="field-error" data-testid="field-error" data-field="bindAddress">{errors.bindAddress}</p>{/if}
        </div>
        <div class="field">
          <label class="label" for={id('bindPort')}>Bind port</label>
          <input
            id={id('bindPort')}
            class="input mono"
            data-testid="form-bind-port"
            bind:value={f.bindPort}
            aria-invalid={invalid('bindPort')}
            inputmode="numeric"
            autocomplete="off"
          />
          {#if errors.bindPort}<p class="field-error" data-testid="field-error" data-field="bindPort">{errors.bindPort}</p>{/if}
        </div>
        <div class="field">
          <label class="label" for={id('targetHost')}>Target host</label>
          <input
            id={id('targetHost')}
            class="input mono"
            data-testid="form-target-host"
            bind:value={f.targetHost}
            aria-invalid={invalid('targetHost')}
            placeholder="127.0.0.1"
            autocomplete="off"
            spellcheck="false"
          />
          {#if errors.targetHost}<p class="field-error" data-testid="field-error" data-field="targetHost">{errors.targetHost}</p>{/if}
        </div>
        <div class="field">
          <label class="label" for={id('targetPort')}>Target port</label>
          <input
            id={id('targetPort')}
            class="input mono"
            data-testid="form-target-port"
            bind:value={f.targetPort}
            aria-invalid={invalid('targetPort')}
            inputmode="numeric"
            autocomplete="off"
          />
          {#if errors.targetPort}<p class="field-error" data-testid="field-error" data-field="targetPort">{errors.targetPort}</p>{/if}
        </div>
      </div>
      <label class="check">
        <input type="checkbox" data-testid="form-autoconnect" bind:checked={f.autoConnect} />
        <span>Auto-connect at startup</span>
      </label>
    </section>

    <section class="group">
      <h3 class="group-title">Server setup</h3>
      <div class="field">
        <label class="label" for={id('serverMode')}>Mode</label>
        <select
          id={id('serverMode')}
          class="select"
          data-testid="form-server-mode"
          bind:value={f.serverMode}
          aria-invalid={invalid('serverMode')}
        >
          <option value={ServerMode.ServerNone}>None - no server changes</option>
          <option value={ServerMode.ServerSSHD}>sshd - restricted forwarding user</option>
          <option value={ServerMode.ServerSSHDCaddy}>sshd + Caddy HTTPS proxy</option>
        </select>
        {#if errors.serverMode}<p class="field-error" data-testid="field-error" data-field="serverMode">{errors.serverMode}</p>{/if}
        <p class="hint">
          {#if f.serverMode === ServerMode.ServerNone}
            The server is left as it is.
          {:else if isCaddy}
            Generates a setup script for a forwarding-only user plus a Caddyfile that serves the domain over HTTPS. Get both from the tunnel's Server setup action after saving.
          {:else}
            Generates a setup script that creates a forwarding-only user with this tunnel's public key. Get it from the tunnel's Server setup action after saving.
          {/if}
        </p>
      </div>
      {#if isCaddy}
        <div class="field">
          <label class="label" for={id('serverDomain')}>Domain</label>
          <input
            id={id('serverDomain')}
            class="input mono"
            data-testid="form-server-domain"
            bind:value={f.serverDomain}
            aria-invalid={invalid('serverDomain')}
            placeholder="app.example.com"
            autocomplete="off"
            spellcheck="false"
          />
          {#if errors.serverDomain}<p class="field-error" data-testid="field-error" data-field="serverDomain">{errors.serverDomain}</p>{/if}
        </div>
      {/if}
    </section>
  </form>

  {#snippet footer()}
    {#if formError}
      <div class="form-error" data-testid="form-error" role="alert">
        <Icon name="alert" size={14} />
        <span>{formError}</span>
      </div>
    {:else}
      <span class="spacer"></span>
    {/if}
    <button type="button" class="btn" data-testid="form-cancel" onclick={oncancel} disabled={saving}>Cancel</button>
    <button type="submit" class="btn btn-primary" data-testid="form-save" form={id('form')} disabled={saving}>
      {saving ? 'Saving…' : mode === 'new' ? 'Add tunnel' : 'Save changes'}
    </button>
  {/snippet}
</Modal>

<style>
  form {
    display: flex;
    flex-direction: column;
    gap: var(--s-5);
  }

  .group {
    display: flex;
    flex-direction: column;
    gap: var(--s-3);
  }

  .group-title {
    font-size: var(--fs-0);
    font-weight: 600;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--text-3);
  }

  .grid {
    display: grid;
    gap: var(--s-3);
  }

  .host-grid {
    grid-template-columns: minmax(0, 1fr) minmax(0, 1.4fr) 112px;
  }

  .pair-grid {
    grid-template-columns: minmax(0, 1fr) 112px;
  }

  .form-error {
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

  .form-error :global(svg) {
    flex: none;
    margin-top: 2px;
  }

  @media (max-width: 560px) {
    .host-grid {
      grid-template-columns: minmax(0, 1fr) 112px;
    }
  }
</style>
