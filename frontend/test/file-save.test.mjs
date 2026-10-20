import test from 'node:test';
import assert from 'node:assert/strict';
import { MockBackendAPI } from '../src/lib/mock/mockApi.ts';

test('mock file saves return a new baseline; stale revisions conflict without writing', async () => {
  const api = new MockBackendAPI();
  const original = await api.readFile('/project', 'main.go');
  assert.match(original.revision, /^sha256:[0-9a-f]{64}$/);
  assert.equal(original.edit.eligible, true);
  const result = await api.saveFile('/project', { path: 'main.go', text: 'hello\n', expectedRevision: original.revision });
  assert.equal(result.status, 'saved');
  assert.equal(result.content.text, 'hello\n');
  assert.equal(result.content.size, 6);
  const conflict = await api.saveFile('/project', { path: 'main.go', text: 'stale', expectedRevision: original.revision });
  assert.equal(conflict.status, 'conflict');
  assert.equal(conflict.observedRevision, result.content.revision);
  assert.equal((await api.readFile('/project', 'main.go')).text, 'hello\n');
  assert.notEqual((await api.readFile('/other', 'main.go')).text, 'hello\n');
});

test('mock refuses unsupported text and oversized snapshots', async () => {
  const api = new MockBackendAPI();
  const original = await api.readFile('/project', 'main.go');
  for (const text of ['a\rb', 'a\0b', 'x'.repeat(500_001)]) {
    await assert.rejects(api.saveFile('/project', { path: 'main.go', text, expectedRevision: original.revision }));
  }
  assert.equal((await api.readFile('/project', 'main.go')).text, original.text);
});
