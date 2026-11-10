import { Agent, AuthMethod, Backend, ForwardType, ServerMode } from './api';
import type { Tunnel } from './api';

/**
 * Editable mirror of config.Tunnel. Ports are kept as strings while editing so
 * that "80.5" or "" can be reported as validation errors instead of being
 * coerced by a number input.
 */
export interface TunnelForm {
  name: string;
  backend: Backend;
  host: string;
  port: string;
  user: string;
  authMethod: AuthMethod;
  keyPath: string;
  agent: Agent;
  forwardType: ForwardType;
  bindAddress: string;
  bindPort: string;
  targetHost: string;
  targetPort: string;
  autoConnect: boolean;
  serverMode: ServerMode;
  serverDomain: string;
}

export type FieldName =
  | 'name'
  | 'host'
  | 'port'
  | 'user'
  | 'keyPath'
  | 'agent'
  | 'bindAddress'
  | 'bindPort'
  | 'targetHost'
  | 'targetPort'
  | 'serverMode'
  | 'serverDomain';

export type FieldErrors = Partial<Record<FieldName, string>>;

function portToString(p: number): string {
  return p > 0 ? String(p) : '';
}

export function toForm(t: Tunnel): TunnelForm {
  return {
    name: t.name,
    backend: t.backend === Backend.BackendPlink ? Backend.BackendPlink : Backend.BackendOpenSSH,
    host: t.host,
    port: portToString(t.port),
    user: t.user,
    authMethod: t.auth.method === AuthMethod.$zero ? AuthMethod.AuthKey : t.auth.method,
    keyPath: t.auth.keyPath ?? '',
    agent: t.auth.agent === Agent.AgentOpenSSHAgent ? Agent.AgentOpenSSHAgent : Agent.AgentPageant,
    forwardType: t.forward.type === ForwardType.ForwardLocal ? ForwardType.ForwardLocal : ForwardType.ForwardRemote,
    bindAddress: t.forward.bindAddress,
    bindPort: portToString(t.forward.bindPort),
    targetHost: t.forward.targetHost,
    targetPort: portToString(t.forward.targetPort),
    autoConnect: t.autoConnect,
    serverMode: serverModeOf(t),
    serverDomain: t.server?.domain ?? '',
  };
}

function serverModeOf(t: Tunnel): ServerMode {
  const m = t.server?.mode;
  return m === ServerMode.ServerSSHD || m === ServerMode.ServerSSHDCaddy ? m : ServerMode.ServerNone;
}

/**
 * Merges the edited fields back into the original tunnel. id, hostKey and
 * credentialRef are carried over untouched, as the backend expects.
 */
export function applyForm(base: Tunnel, f: TunnelForm): Tunnel {
  const isAgent = f.authMethod === AuthMethod.AuthAgent;
  const isKey = f.authMethod === AuthMethod.AuthKey;
  const isCaddy = f.serverMode === ServerMode.ServerSSHDCaddy;
  return {
    ...base,
    name: f.name.trim(),
    backend: f.backend,
    host: f.host.trim(),
    port: parsePort(f.port) ?? 0,
    user: f.user.trim(),
    auth: {
      ...base.auth,
      method: f.authMethod,
      keyPath: isKey ? f.keyPath.trim() : '',
      agent: isAgent ? f.agent : Agent.$zero,
    },
    forward: {
      type: f.forwardType,
      bindAddress: f.bindAddress.trim(),
      bindPort: parsePort(f.bindPort) ?? 0,
      targetHost: f.targetHost.trim(),
      targetPort: parsePort(f.targetPort) ?? 0,
    },
    autoConnect: f.autoConnect,
    server: {
      ...base.server,
      mode: f.serverMode,
      domain: isCaddy ? f.serverDomain.trim() : '',
    },
  };
}

/** Returns the port as an integer in 1–65535, or null when invalid. */
export function parsePort(raw: string): number | null {
  const s = raw.trim();
  if (!/^\d{1,5}$/.test(s)) return null;
  const n = Number(s);
  if (!Number.isInteger(n) || n < 1 || n > 65535) return null;
  return n;
}

const IPV4_RE = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/;

export function isIPv4(s: string): boolean {
  return IPV4_RE.test(s);
}

/** RFC 4291 text form, including "::" compression and an embedded IPv4 tail. */
export function isIPv6(s: string): boolean {
  if (s.length === 0 || s.includes('%')) return false;
  const doubleColon = s.indexOf('::');
  if (doubleColon !== -1 && s.indexOf('::', doubleColon + 1) !== -1) return false;

  const hextet = /^[0-9a-fA-F]{1,4}$/;
  const countGroups = (part: string): number | null => {
    if (part === '') return 0;
    const groups = part.split(':');
    let total = 0;
    for (let i = 0; i < groups.length; i++) {
      const g = groups[i];
      if (i === groups.length - 1 && g.includes('.')) {
        if (!isIPv4(g)) return null;
        total += 2;
      } else if (hextet.test(g)) {
        total += 1;
      } else {
        return null;
      }
    }
    return total;
  };

  if (doubleColon === -1) {
    return countGroups(s) === 8;
  }
  const head = countGroups(s.slice(0, doubleColon));
  const tail = countGroups(s.slice(doubleColon + 2));
  if (head === null || tail === null) return false;
  // an embedded IPv4 may only appear in the tail
  if (s.slice(0, doubleColon).includes('.')) return false;
  return head + tail <= 7;
}

/** Mirrors config.validBindAddress: "localhost" or any IP literal. */
export function isValidBindAddress(s: string): boolean {
  return s === 'localhost' || isIPv4(s) || isIPv6(s);
}

/** Client-side mirror of config.Tunnel.Validate for the editable fields. */
export function validateForm(f: TunnelForm): FieldErrors {
  const errors: FieldErrors = {};
  if (f.name.trim() === '') errors.name = 'Name is required.';
  if (f.host.trim() === '') errors.host = 'Host is required.';
  if (parsePort(f.port) === null) errors.port = 'Must be 1–65535.';
  if (f.user.trim() === '') errors.user = 'User is required.';

  if (f.authMethod === AuthMethod.AuthKey && f.keyPath.trim() === '') {
    errors.keyPath = 'Key path is required for key authentication.';
  }
  if (
    f.authMethod === AuthMethod.AuthAgent &&
    f.backend === Backend.BackendPlink &&
    f.agent !== Agent.AgentPageant
  ) {
    errors.agent = 'plink supports only Pageant.';
  }

  if (!isValidBindAddress(f.bindAddress.trim())) {
    errors.bindAddress = 'Bind address must be an IPv4/IPv6 address or "localhost".';
  }
  if (parsePort(f.bindPort) === null) errors.bindPort = 'Must be 1–65535.';
  if (f.targetHost.trim() === '') errors.targetHost = 'Target host is required.';
  if (parsePort(f.targetPort) === null) errors.targetPort = 'Must be 1–65535.';

  if (f.serverMode === ServerMode.ServerSSHDCaddy) {
    if (f.forwardType !== ForwardType.ForwardRemote) errors.serverMode = 'Caddy mode needs a remote (-R) forward';
    if (f.serverDomain.trim() === '') errors.serverDomain = 'Domain is required for Caddy mode.';
  }
  return errors;
}
