// Thin re-export layer over the generated Wails bindings so components import
// from one place. Nothing in frontend/bindings is modified.

export * as Svc from '../../bindings/ssh-tunnel-manager/tunnelservice';
export type { State, LogEvent } from '../../bindings/ssh-tunnel-manager/models';
export type {
  Tunnel,
  Settings,
  Auth,
  Forward,
  Server,
} from '../../bindings/ssh-tunnel-manager/internal/config/models';
export {
  Backend,
  AuthMethod,
  Agent,
  ForwardType,
  ServerMode,
} from '../../bindings/ssh-tunnel-manager/internal/config/models';
export type { Kit } from '../../bindings/ssh-tunnel-manager/internal/serverkit/models';
export type { TunnelView, HostKeyRequest } from '../../bindings/ssh-tunnel-manager/internal/manager/models';
export type { Status } from '../../bindings/ssh-tunnel-manager/internal/supervisor/models';
export { State as TunnelState } from '../../bindings/ssh-tunnel-manager/internal/supervisor/models';
export type { Line } from '../../bindings/ssh-tunnel-manager/internal/logs/models';
export {
  State as StateEvent,
  Log as LogEventName,
  UiShowLogs as UiShowLogsEvent,
} from '../../bindings/events';

/** Extracts a human-readable message from a rejected backend call. */
export function errorMessage(err: unknown): string {
  if (err instanceof Error) {
    return err.message || err.name;
  }
  if (typeof err === 'string') {
    return err;
  }
  if (typeof err === 'object' && err !== null && 'message' in err) {
    const msg: unknown = (err as { message: unknown }).message;
    if (typeof msg === 'string' && msg.length > 0) {
      return msg;
    }
  }
  return 'Unknown error';
}
