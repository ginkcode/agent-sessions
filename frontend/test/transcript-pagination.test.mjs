import test from 'node:test';
import assert from 'node:assert/strict';
import { MockBackendAPI } from '../src/lib/mock/mockApi.ts';

test('latest page returns only recent messages and an offset for loading history', async () => {
  const api = new MockBackendAPI();
  const ref = { agent: 'claude', id: 'pagination-fixture' };
  api.messages[ref.id] = Array.from({ length: 123 }, (_, i) => ({
    id: `m-${i}`, role: 'user', time: '', parts: [{ kind: 'text', text: `${i}` }],
  }));
  const latest = await api.getMessages(ref, -1, 50);
  assert.equal(latest.offset, 73);
  assert.equal(latest.messages.length, 50);
  assert.equal(latest.messages[0].id, 'm-73');
  assert.equal(latest.messages.at(-1).id, 'm-122');
  assert.equal(latest.hasMore, false);
  const earlier = await api.getMessages(ref, latest.offset - 50, 50);
  assert.equal(earlier.messages.at(-1).id, 'm-72');
  assert.equal(earlier.offset + earlier.messages.length, latest.offset);

  api.messages[ref.id].push({ id: 'new', role: 'assistant', time: '', parts: [] });
  const updated = await api.getMessages(ref, -1, 50);
  assert.equal(updated.offset, 74);
  assert.equal(updated.messages.length, 50);
  assert.equal(updated.messages.at(-1).id, 'new');
});

test('latest page handles short and empty transcripts', async () => {
  const api = new MockBackendAPI();
  const ref = { agent: 'claude', id: 'pagination-fixture' };
  for (const count of [0, 1, 49, 50]) {
    api.messages[ref.id] = Array.from({ length: count }, (_, i) => ({ id: `${i}` }));
    const page = await api.getMessages(ref, -1, 50);
    assert.equal(page.offset, 0);
    assert.equal(page.messages.length, count);
    assert.equal(page.hasMore, false);
  }
  await assert.rejects(api.getMessages(ref, -2, 50));
});
