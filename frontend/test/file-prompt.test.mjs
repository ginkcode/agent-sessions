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

test('the prompt keeps focus and owns find/save/close shortcuts while it decides', async () => {
  const { readFileSync } = await import('node:fs');
  const prompt = readFileSync(new URL('../src/lib/components/common/go-to-file/FilePrompt.svelte', import.meta.url), 'utf8');
  assert.match(prompt, /document\.addEventListener\('focusin', contain\)/);
  assert.match(prompt, /document\.removeEventListener\('focusin', contain\)/);
  assert.match(prompt, /\['f', 's', 'w'\]\.includes\(e\.key\.toLowerCase\(\)\)\) e\.preventDefault\(\)/);
  const editor = readFileSync(new URL('../src/lib/components/common/go-to-file/FileEditor.svelte', import.meta.url), 'utf8');
  assert.match(editor, /if \(!filePrompt\.request\) editor\.focus\(\)/, 'a lazily loaded editor must not take focus from an open prompt');
});
