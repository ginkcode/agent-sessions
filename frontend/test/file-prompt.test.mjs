import test from 'node:test';
import assert from 'node:assert/strict';
import { loadServerModule } from './helpers.mjs';

const { FilePromptStore } = await loadServerModule(new URL('../src/lib/stores/filePrompt.svelte.ts', import.meta.url), []);

test('file prompts serialize decisions and cancel overlapping actions', async () => {
  const p = new FilePromptStore();
  const first = p.ask('Discard?', 'draft', [{ value: 'discard', label: 'Discard' }]);
  assert.equal(p.request.title, 'Discard?');
  assert.equal(await p.ask('Close?', 'other', []), null);
  p.answer('discard');
  assert.equal(await first, 'discard');
  assert.equal(p.request, null);
  const cancelled = p.ask('Close?', 'draft', []);
  p.answer(null);
  assert.equal(await cancelled, null);
  p.answer('duplicate');
  assert.equal(p.request, null);
});
