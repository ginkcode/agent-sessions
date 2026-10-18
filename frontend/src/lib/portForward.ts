export type PortForwardStatus = 'active' | 'starting' | 'stopped' | 'failed' | 'disconnected';

export interface PortForwardRule {
  id: string;
  remoteHost: string;
  remotePort: number;
  localPort: number;
  autoStart: boolean;
  status: PortForwardStatus;
  error?: string;
}

export interface PortForwardRequest {
  id?: string;
  remoteHost: string;
  remotePort: number;
  localPort: number;
  autoStart: boolean;
}

export interface PortForwardState {
  host: string;
  rules: PortForwardRule[];
}

export const FORWARD_STATUS_LABELS: Record<PortForwardStatus, string> = {
  active: 'Active', starting: 'Starting…', stopped: 'Stopped', failed: 'Failed', disconnected: 'Disconnected',
};

export function validatePortForward(rule: PortForwardRequest): string | null {
  if (!rule.remoteHost.trim() || /[\s\x00-\x1f]/.test(rule.remoteHost)) return 'Enter a remote hostname without spaces.';
  for (const [label, port] of [['Remote port', rule.remotePort], ['Local port', rule.localPort]] as const) {
    if (!Number.isInteger(port) || port < 1 || port > 65535) return `${label} must be between 1 and 65535.`;
  }
  return null;
}
