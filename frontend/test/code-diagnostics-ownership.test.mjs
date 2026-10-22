import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

test('diagnostics integration has no editor/text ownership or UI activation', async () => {
  const store = await readFile(new URL('../src/lib/stores/codeDiagnostics.svelte.ts', import.meta.url), 'utf8');
  const editor = await readFile(new URL('../src/lib/components/common/go-to-file/FileEditor.svelte', import.meta.url), 'utf8');
  assert.doesNotMatch(store, /from ['"].*(?:files\.svelte|fileEditor|codemirror)/);
  assert.doesNotMatch(store, /localStorage|sessionStorage|console\./);
  assert.match(editor, /import\(['"].*fileEditor/);
});
