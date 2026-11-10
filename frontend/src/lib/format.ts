import { Backend, ForwardType, TunnelState } from './api';
import type { Status, Tunnel } from './api';

/** Go's zero time ("0001-01-01T00:00:00Z") and unparsable values count as unset. */
export function parseTime(value: string | undefined): Date | null {
  if (!value) return null;
  const d = new Date(value);
  if (Number.isNaN(d.getTime()) || d.getUTCFullYear() < 2000) return null;
  return d;
}

function pad2(n: number): string {
  return n < 10 ? `0${n}` : String(n);
}

/** "12m 05s" under an hour, "1h 02m" under a day, "2d 03h" beyond. */
export function formatDuration(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000));
  const s = total % 60;
  const m = Math.floor(total / 60) % 60;
  const h = Math.floor(total / 3600) % 24;
  const d = Math.floor(total / 86400);
  if (d > 0) return `${d}d ${pad2(h)}h`;
  if (h > 0) return `${h}h ${pad2(m)}m`;
  return `${m}m ${pad2(s)}s`;
}

/** "12s" or "1m 05s" remaining until `until`; never negative. */
export function formatCountdown(until: Date, now: number): string {
  const secs = Math.max(0, Math.ceil((until.getTime() - now) / 1000));
  if (secs < 60) return `${secs}s`;
  return `${Math.floor(secs / 60)}m ${pad2(secs % 60)}s`;
}

/** HH:MM:SS in local time; falls back to the raw value when unparsable. */
export function formatClock(value: string): string {
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  return `${pad2(d.getHours())}:${pad2(d.getMinutes())}:${pad2(d.getSeconds())}`;
}

const ERROR_KIND_TEXT: Record<string, string> = {
  network: 'connection failed',
  'remote-port-busy': 'remote port already in use',
  'local-port-busy': 'local port already in use',
  config: 'configuration error',
  auth: 'authentication failed',
  'host-key-unknown': 'host key not verified',
  'host-key-changed': 'host key changed',
};

export function describeErrorKind(kind: string | undefined): string {
  if (!kind) return 'failed';
  return ERROR_KIND_TEXT[kind] ?? kind.replace(/-/g, ' ');
}

/** Live, human status label for the row badge. */
export function statusText(status: Status, now: number): string {
  switch (status.state) {
    case TunnelState.Up: {
      const since = parseTime(status.since);
      return since ? `Up · ${formatDuration(now - since.getTime())}` : 'Up';
    }
    case TunnelState.Connecting:
      return status.attempt > 1 ? `Connecting (attempt ${status.attempt})…` : 'Connecting…';
    case TunnelState.Backoff: {
      const retry = parseTime(status.nextRetry);
      const head = `Reconnecting (attempt ${Math.max(1, status.attempt)})`;
      if (!retry) return head;
      return retry.getTime() > now ? `${head} · retry in ${formatCountdown(retry, now)}` : `${head} · retrying…`;
    }
    case TunnelState.Error:
      return `Error: ${describeErrorKind(status.errorKind)}`;
    case TunnelState.Stopped:
    default:
      return 'Stopped';
  }
}

export type Tone = 'ok' | 'warn' | 'err' | 'off';

export function statusTone(state: TunnelState): Tone {
  switch (state) {
    case TunnelState.Up:
      return 'ok';
    case TunnelState.Connecting:
    case TunnelState.Backoff:
      return 'warn';
    case TunnelState.Error:
      return 'err';
    default:
      return 'off';
  }
}

/** The switch is "on" while the supervisor is trying to keep the tunnel alive. */
export function isActive(state: TunnelState): boolean {
  return state === TunnelState.Connecting || state === TunnelState.Up || state === TunnelState.Backoff;
}

export function backendLabel(b: Backend): string {
  return b === Backend.BackendPlink ? 'plink' : 'OpenSSH';
}

/** `R 127.0.0.1:8080 → 127.0.0.1:3000` */
export function forwardSpec(t: Tunnel): string {
  const f = t.forward;
  const type = f.type === ForwardType.ForwardLocal ? 'L' : 'R';
  return `${type} ${f.bindAddress}:${f.bindPort} → ${f.targetHost}:${f.targetPort}`;
}

/** `user@host:22` */
export function endpointSpec(t: Tunnel): string {
  return `${t.user}@${t.host}:${t.port}`;
}

/** Whether the row should offer "Review host key". */
export function canReviewHostKey(t: Tunnel, status: Status): boolean {
  if (status.errorKind === 'host-key-changed') return true;
  return status.errorKind === 'host-key-unknown' && t.backend === Backend.BackendPlink;
}
