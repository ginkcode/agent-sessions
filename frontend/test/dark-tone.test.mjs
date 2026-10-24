import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const source = path => readFileSync(new URL(path, import.meta.url), 'utf8');
const css = source('../src/style.css');
function tokens(selector) {
  const block = css.slice(css.indexOf(selector)).split('}')[0];
  return Object.fromEntries(Array.from(block.matchAll(/(--[\w-]+):\s*([^;]+);/g), m => [m[1], m[2]]));
}
function luminance(hex) {
  const channels = hex.slice(1).match(/../g).map(c => parseInt(c, 16) / 255)
    .map(c => c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4);
  return channels[0] * 0.2126 + channels[1] * 0.7152 + channels[2] * 0.0722;
}

test('dark surfaces retain native-tone layers with readable text', () => {
  const dark = tokens('[data-theme="dark"]');
  assert.equal(dark['--bg-primary'], '#242423');
  assert.ok(luminance(dark['--bg-primary']) < luminance(dark['--bg-secondary']));
  assert.ok(luminance(dark['--bg-secondary']) < luminance(dark['--bg-tertiary']));
  assert.equal(dark['--bg-secondary'], '#2c2c2b');
  assert.equal(dark['--bg-tertiary'], '#383837');
  assert.equal(dark['--text-muted'], '#b0b0ad');
  for (const surface of ['--bg-primary', '--bg-secondary', '--bg-tertiary']) {
    for (const text of ['--text-primary', '--text-secondary', '--text-muted']) {
      assert.ok((luminance(dark[text]) + 0.05) / (luminance(dark[surface]) + 0.05) >= 4.5, `${text} on ${surface}`);
    }
  }
  assert.equal(dark['--accent-color'], '#0284c7');
  assert.equal(dark['--active-bg'], 'rgba(14, 165, 233, 0.2)');
  assert.equal(dark['--status-live'], '#34d399');
  assert.equal(dark['--danger'], '#f87171');
});

test('light surfaces and text retain their original palette', () => {
  const light = tokens(':root {');
  assert.deepEqual(['--bg-primary', '--bg-secondary', '--bg-tertiary', '--text-primary', '--text-secondary', '--text-muted'].map(k => light[k]),
    ['#ffffff', '#f1f3f6', '#e2e7ef', '#0b1220', '#334155', '#5f6b7c']);
});

test('command block exception is screen-dark only and editor uses shared surfaces', () => {
  const dialog = source('../src/lib/components/common/ImportDialog.svelte');
  assert.match(dialog, /@media screen\s*\{\s*:global\(\[data-theme="dark"\]\) \.command-code\s*\{\s*background-color: var\(--bg-primary\);\s*color: var\(--text-primary\);/);
  assert.match(dialog, /\.command-code\s*\{[^}]*background-color: #121214;/);
  const editor = source('../src/lib/fileEditor.ts');
  assert.match(editor, /backgroundColor: 'var\(--bg-primary\)'/);
  assert.match(editor, /backgroundColor: 'var\(--bg-secondary\)'/);
});

test('session count tooltip matches the light and dark themes', () => {
  const tooltip = source('../src/lib/components/sessionlist/CountTooltip.svelte');
  const dark = tokens('[data-theme="dark"]');
  const light = tokens(':root {');
  const contrast = (a, b) => (Math.max(luminance(a), luminance(b)) + 0.05) / (Math.min(luminance(a), luminance(b)) + 0.05);
  const colors = (block, prefix = '') => Object.fromEntries(['user', 'assistant', 'tools', 'errors'].map(role =>
    [role, block.match(new RegExp(`${prefix}\\.count-item\\.${role}\\s*\\{\\s*color: (#[0-9a-f]{6});`))?.[1]]));
  const [base, screenDark] = tooltip.split('@media screen');
  // Light: a light elevated surface, not the old inverted dark chip.
  assert.match(base, /\.count-tooltip\s*\{[^}]*background-color: var\(--bg-primary\);\s*color: var\(--text-primary\);\s*border: 1px solid var\(--border-color\);/);
  assert.doesNotMatch(base, /background-color: var\(--text-primary\)/);
  const lightRoles = colors(base);
  assert.deepEqual(lightRoles, { user: '#2563eb', assistant: '#7c3aed', tools: '#047857', errors: '#dc2626' });
  for (const color of [...Object.values(lightRoles), light['--text-primary']]) {
    assert.ok(contrast(color, light['--bg-primary']) >= 4.5, `${color} on light tooltip`);
  }
  // Dark: the raised tertiary surface, inheriting text and border tokens.
  assert.match(screenDark, /:global\(\[data-theme="dark"\]\) \.count-tooltip\s*\{\s*background-color: var\(--bg-tertiary\);/);
  const darkRoles = colors(screenDark, ':global\\(\\[data-theme="dark"\\]\\) ');
  assert.deepEqual(darkRoles, { user: '#93c5fd', assistant: '#c4b5fd', tools: '#34d399', errors: '#fca5a5' });
  for (const color of [...Object.values(darkRoles), dark['--text-primary']]) {
    assert.ok(contrast(color, dark['--bg-tertiary']) >= 4.5, `${color} on dark tooltip`);
  }
});
