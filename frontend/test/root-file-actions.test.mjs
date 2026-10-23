import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { loadServerComponent, renderWithFixture } from './helpers.mjs';

const source = await readFile(new URL('../src/lib/components/common/GoToFileDialog.svelte', import.meta.url), 'utf8');

test('blank-space native actions use the project-root dot path rather than an invalid empty path', () => {
  for (const method of ['revealFile', 'copyFile', 'downloadFile']) {
    assert.ok(source.includes(`api.${method}(dir, path || '.')`), method);
  }
  assert.ok(source.includes('${path || dir} is ready to paste'));
});

test('relative path copy is only offered for project children', () => {
  assert.match(source, /if \(path\) copy\.push\(\{ label: 'Copy Relative Path'/);
  assert.match(source, /copy\.push\(\{ label: 'Copy Path'/);
});

test('tree menu groups create, file manager, path and rename/delete actions', () => {
  const order = ['New File…', 'New Folder…', "remote ? 'Upload File…'", "label: 'Reveal'", "remote ? 'Download'", "label: 'Copy Path'", "label: 'Copy Relative Path'", "label: 'Rename…'", "label: 'Delete…'"];
  const at = order.map(label => source.indexOf(label));
  assert.ok(at.every(i => i > 0), 'every item present');
  assert.deepEqual([...at].sort((a, b) => a - b), at, 'items in group order');
  assert.match(source, /\[start, create, open, copy, change\]\.filter\(group => group\.length\)/);
});

test('only the project root offers new agent sessions, first in its menu', () => {
  assert.match(source, /const start = directory && !path && newSession\.available\s*\? newSessionMenuItems\(/);
});

test('context menu draws a divider before each later group, never before the first item', async () => {
  const menu = await loadServerComponent(new URL('../src/lib/components/common/ContextMenu.svelte', import.meta.url), [["'svelte'", JSON.stringify(import.meta.resolve('svelte'))]]);
  const item = (label, separator) => ({ label, separator, onSelect() {} });
  const html = await renderWithFixture(menu, {}, { x: 0, y: 0, ariaLabel: 'File actions', onClose() {}, items: [item('New File…', true), item('Copy Path', true), item('Copy Relative Path'), item('Delete…', true)] });
  assert.equal((html.match(/role="separator"/g) ?? []).length, 2);
  assert.ok(html.indexOf('role="separator"') > html.indexOf('New File…') && html.indexOf('role="separator"') < html.indexOf('Copy Path'));
  assert.equal((html.match(/role="menuitem"/g) ?? []).length, 4, 'dividers are not menu items');
});
