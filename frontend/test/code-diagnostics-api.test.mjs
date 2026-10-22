import test from 'node:test';
import assert from 'node:assert/strict';
import { createAPI } from '../src/lib/api.ts';

test('Wails maps exact Stage 1 arguments/host and normalizes error codes; old binding unsupported', async () => {
  const previous = globalThis.window;
  try {
    const calls = [];
    const binding = {};
    globalThis.window = { go: { app: { App: binding } } };
    const api = createAPI();
    await assert.rejects(api.attachCodeDiagnostics('/p', 1, 'stable-host'), e => e.code === 'unsupported');
    const methods = ['AttachCodeDiagnostics', 'DetachCodeDiagnostics', 'GetCodeDiagnosticsSnapshot', 'ApproveCodeDiagnostics', 'RevokeCodeDiagnostics', 'RunCodeDiagnostics', 'CancelCodeDiagnostics'];
    for (const method of methods) binding[method] = async (...args) => { calls.push([method, args]); return 'ok'; };
    const c = { connectionId: 'c', attachmentId: 'a', generation: 'g' };
    await api.attachCodeDiagnostics('/p', 1, 'stable-host');
    await api.detachCodeDiagnostics(c, 'stable-host');
    await api.getCodeDiagnosticsSnapshot(c, 'stable-host');
    await api.approveCodeDiagnostics(c, 'session', 'fp', 'stable-host');
    await api.revokeCodeDiagnostics(c, 'stable-host');
    await api.runCodeDiagnostics(c, undefined, 'stable-host');
    await api.cancelCodeDiagnostics(c, [{ providerId: 'p', runId: 'r' }], 'stable-host');
    assert.deepEqual(calls.map(c => c[0]), methods);
    assert.deepEqual(calls[0][1], ['/p', 1, 'stable-host']);
    assert.deepEqual(calls[3][1], [c, 'session', 'fp', 'stable-host']);
    for (const [, args] of calls) assert.equal(args.at(-1), 'stable-host');
    binding.RunCodeDiagnostics = async () => { throw 'approvalRequired: Approve checks first'; };
    await assert.rejects(api.runCodeDiagnostics(c, [], 'stable-host'), e => e.code === 'approvalRequired');
    binding.RunCodeDiagnostics = async () => { throw { data: { code: 'staleContext' }, message: 'Changed' }; };
    await assert.rejects(api.runCodeDiagnostics(c, [], 'stable-host'), e => e.code === 'staleContext');
  } finally { globalThis.window = previous; }
});
