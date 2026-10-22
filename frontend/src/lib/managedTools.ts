/** Closed TI-03 contract. Installer policy is backend-owned, never editable here. */
export interface ManagedToolRequest {
  action: 'status' | 'propose' | 'install' | 'dismiss' | 'cancel';
  expectedHostId: string;
  tool?: 'gopls';
  token?: string;
  jobId?: string;
}
export interface ManagedToolProposal {
  token: string;
  hostId: string;
  connectionId: string;
  tool: 'gopls';
  version: string;
  source: string;
  integrity: string;
  destination: string;
  expires: string;
}
export interface ManagedToolState {
  hostId: string;
  state: 'idle' | 'unavailable' | 'available' | 'missingTool' | 'proposed' | 'installing' | 'installed' | 'failed' | 'canceled';
  message?: string;
  jobId?: string;
  proposal?: ManagedToolProposal;
}
