import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const source = await readFile(new URL('../src/lib/components/common/GoToFileDialog.svelte', import.meta.url), 'utf8');

test('blank-space native actions use the project-root dot path rather than an invalid empty path', () => {
  for (const method of ['revealFile', 'copyFile', 'downloadFile']) {
    assert.ok(source.includes(`api.${method}(dir, path || '.')`), method);
  }
  assert.ok(source.includes('${path || dir} is ready to paste'));
});

test('relative path copy is only offered for project children', () => {
  assert.match(source, /if \(path\) items\.push\(\{ label: 'Copy Relative Path'/);
  assert.match(source, /items\.push\(\{ label: 'Copy Path'/);
});
