import test from 'node:test';
import assert from 'node:assert/strict';
import { existsSync, readFileSync } from 'node:fs';
import { fileIcon } from '../src/lib/fileIcons.ts';

const icon = (path, ...args) => fileIcon(path, ...args).split('/').pop();

test('file icons use file names, compound extensions and language defaults', () => {
  assert.equal(icon('src/main.go'), 'file_type_go.svg');
  assert.equal(icon('src/App.svelte'), 'file_type_svelte.svg');
  assert.equal(icon('src/main.ts'), 'file_type_typescript.svg');
  assert.equal(icon('src/types.d.ts'), 'file_type_typescriptdef.svg');
  assert.equal(icon('package.json'), 'file_type_npm.svg');
  assert.equal(icon('nested/PACKAGE.JSON'), icon('package.json'));
  assert.equal(icon('C:\\src\\main.go'), icon('main.go'));
  assert.equal(icon('some.unknown-extension'), 'default_file.svg');
  assert.equal(icon('no-extension'), 'default_file.svg');
  assert.equal(icon('__proto__'), 'default_file.svg');
  assert.equal(icon('constructor'), 'default_file.svg');
});

test('folder icons follow folder names and expanded state', () => {
  assert.equal(icon('project/src', true), 'folder_type_src.svg');
  assert.equal(icon('project/src/', true, true), 'folder_type_src_opened.svg');
  assert.equal(icon('unrecognized', true), 'default_folder.svg');
  assert.equal(icon('unrecognized', true, true), 'default_folder_opened.svg');
});

test('every generated icon reference has a local SVG, including light variants', () => {
  const table = JSON.parse(readFileSync(new URL('../src/lib/fileIcons.json', import.meta.url), 'utf8'));
  function check(value) {
    for (const [key, entry] of Object.entries(value)) {
      if (key === 'version') continue;
      if (typeof entry === 'object') check(entry);
      else assert.ok(existsSync(new URL(`../public/file-icons/${entry}.svg`, import.meta.url)), entry);
    }
  }
  check(table);
  const [name, lightIcon] = Object.entries(table.light.fileNames)[0];
  assert.equal(icon(name, false, false, true), `${lightIcon}.svg`);
  assert.equal(icon('main.go', false, false, true), icon('main.go'));
});
