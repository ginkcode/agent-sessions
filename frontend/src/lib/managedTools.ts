/** Closed TI-03 contract. Installer policy is backend-owned, never editable here. */
export type ManagedToolName = 'gopls' | 'ruff' | 'vtsls' | 'pyright';
export const MANAGED_TOOLS: readonly ManagedToolName[] = ['gopls', 'ruff', 'vtsls', 'pyright'];
/** Provider whose missing server a managed install supplies. */
export const MANAGED_TOOL_FOR_PROVIDER: Readonly<Record<string, ManagedToolName>> = { gopls: 'gopls', typescript: 'vtsls', ruff: 'ruff', pyright: 'pyright' };
export type ManagedToolAvailability = 'available' | 'missingTool' | 'unavailable';
export interface ManagedToolRequest {
  action: 'status' | 'propose' | 'install' | 'dismiss' | 'cancel';
  expectedHostId: string;
  tool?: ManagedToolName;
  token?: string;
  jobId?: string;
}
export interface ManagedToolProposal {
  token: string;
  hostId: string;
  connectionId: string;
  tool: ManagedToolName;
  version: string;
  source: string;
  integrity: string;
  /** What else the install uses or downloads (the host Go, Node.js). */
  note?: string;
  destination: string;
  expires: string;
}
export interface ManagedToolState {
  hostId: string;
  state: 'idle' | 'unavailable' | 'available' | 'missingTool' | 'proposed' | 'installing' | 'installed' | 'failed' | 'canceled';
  message?: string;
  jobId?: string;
  tool?: ManagedToolName;
  proposal?: ManagedToolProposal;
  /** Status only: each app-managed install on the host. */
  tools?: Partial<Record<ManagedToolName, ManagedToolAvailability>>;
}
