import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { render } from 'svelte/server';
import { compile } from 'svelte/compiler';
import { loadServerComponent } from './helpers.mjs';

const base = new URL('../src/lib/components/common/', import.meta.url);
const url = new URL('DialogCloseButton.svelte', base);
const source = readFileSync(url, 'utf8');
const { default: Close } = await loadServerComponent(url, []);

test('shared close button renders a centered decorative SVG and accessible name', () => {
  const html = render(Close, { props: { onclick() {} } }).body;
  assert.match(html, /<button type="button"/);
  assert.match(html, /aria-label="Close" title="Close"/);
  assert.match(html, /width="16" height="16" viewBox="0 0 16 16" aria-hidden="true" focusable="false"/);
  assert.match(html, /d="M4 4l8 8M12 4l-8 8"/);
  assert.doesNotMatch(html, /[×✕]/);
  assert.doesNotMatch(html, /\sdisabled/);
});

test('shared close button forwards labels, disabled, alignment and focus-hook class', () => {
  const html = render(Close, { props: { onclick() {}, label: 'Close port forwarding', title: 'Close (Esc)', disabled: true, alignEnd: true, class: 'forward-close' } }).body;
  assert.match(html, /aria-label="Close port forwarding" title="Close \(Esc\)" disabled/);
  assert.match(html, /class="[^"]*forward-close/);
  assert.match(html, /class="[^"]*align-end/);
  const css = compile(source, { filename: 'DialogCloseButton.svelte', generate: 'client' }).css.code;
  assert.match(css, /display:\s*inline-grid/);
  assert.match(css, /place-items:\s*center/);
  assert.match(css, /width:\s*28px/);
  assert.match(css, /height:\s*28px/);
  assert.match(css, /padding:\s*0/);
  assert.match(css, /border-radius:\s*4px/);
  assert.match(css, /:focus-visible/);
  assert.match(css, /:hover:not\(:disabled\)/);
});

test('all ten modal header controls use the shared button without obsolete glyph styles', () => {
  const dialogs = ['GoToFileDialog', 'GlobalSearchDialog', 'ManageSettingsDialog', 'PortForwardDialog', 'ImportDialog', 'ExportDialog', 'HandoffDialog', 'DeleteConfirmDialog', 'HostEnvDialog', 'MediaViewer'];
  for (const name of dialogs) {
    const text = readFileSync(new URL(`${name}.svelte`, base), 'utf8');
    assert.equal((text.match(/<DialogCloseButton\b/g) ?? []).length, 1, name);
    assert.match(text, /import DialogCloseButton from '\.\/DialogCloseButton.svelte'/);
    assert.doesNotMatch(text, /\.(?:dialog-close|close-button|forward-close)(?::hover)?\s*\{/, name);
  }
  const file = name => readFileSync(new URL(`${name}.svelte`, base), 'utf8');
  assert.match(file('GoToFileDialog'), /<DialogCloseButton onclick=\{close\} alignEnd/);
  assert.match(file('DeleteConfirmDialog'), /<DialogCloseButton[^>]*onclick=\{handleCancel\}/);
  assert.match(file('PortForwardDialog'), /querySelector<HTMLButtonElement>\('\.forward-close'\)/);
  assert.match(file('PortForwardDialog'), /<DialogCloseButton class="forward-close"/);
  assert.match(file('MediaViewer'), /<DialogCloseButton title="Close \(Esc\)" onclick=\{\(\) => void close\(\)\}/);
});
