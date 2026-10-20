import test from 'node:test';
import assert from 'node:assert/strict';
import {
  isTranscriptMode,
  messageMarkdown,
  messageText,
  visibleTranscriptParts,
  withTranslation,
} from '../src/lib/transcript.ts';
import { printBlocks, printFile, printMarkdown, printMessage, printTitle, setPrintEditor } from '../src/lib/print.ts';
import { MockBackendAPI } from '../src/lib/mock/mockApi.ts';
import { fixtureStore, loadServerModule } from './helpers.mjs';

test('isTranscriptMode accepts only the display levels', () => {
  for (const mode of ['chat', 'activity', 'all']) assert.equal(isTranscriptMode(mode), true, mode);
  for (const value of [null, '', 'meta']) assert.equal(isTranscriptMode(value), false, String(value));
});

test('visibleTranscriptParts filters chat, activity and all display levels', () => {
  const text = { kind: 'text', text: 'Answer' };
  const blank = { kind: 'text', text: ' \n' };
  const thinking = { kind: 'reasoning', text: 'Thinking' };
  const tool = { kind: 'tool', tool: { id: 'call', name: 'Read', status: 'completed' } };
  const file = { kind: 'file', file: { name: 'image.png' } };
  const error = { kind: 'notice', text: 'Assistant error' };
  const agentNotice = { kind: 'notice', text: 'Agent: build' };
  const assistant = { id: 'a', role: 'assistant', time: '', parts: [thinking, text, blank, tool, error, agentNotice] };
  const user = { id: 'u', role: 'user', time: '', parts: [text, file] };
  const toolOnly = { id: 't', role: 'assistant', time: '', parts: [thinking, tool] };
  const meta = { id: 'm', role: 'user', time: '', isMeta: true, parts: [text] };
  const system = { id: 's', role: 'system', time: '', parts: [{ kind: 'compaction', text: 'Summary' }] };

  assert.deepEqual(visibleTranscriptParts(assistant, 'chat'), [text, error]);
  assert.deepEqual(visibleTranscriptParts(user, 'chat'), [text, file]);
  assert.deepEqual(visibleTranscriptParts(toolOnly, 'chat'), []);
  assert.deepEqual(visibleTranscriptParts(system, 'chat'), []);
  assert.deepEqual(visibleTranscriptParts(meta, 'chat'), []);

  assert.deepEqual(visibleTranscriptParts(toolOnly, 'activity'), toolOnly.parts);
  assert.deepEqual(visibleTranscriptParts(system, 'activity'), system.parts);
  assert.deepEqual(visibleTranscriptParts(meta, 'activity'), []);
  assert.deepEqual(visibleTranscriptParts(meta, 'all'), meta.parts);

  // A search target shows in full, whatever the selected level.
  assert.deepEqual(visibleTranscriptParts(toolOnly, 'chat', true), toolOnly.parts);
  const empty = { id: 'n', role: 'assistant', time: '', parts: null };
  for (const mode of ['chat', 'activity', 'all']) assert.deepEqual(visibleTranscriptParts(empty, mode), []);
  assert.deepEqual(visibleTranscriptParts(meta, 'activity', true), meta.parts);
});

test('messageMarkdown copies what Chat level shows', () => {
  const assistant = {
    id: 'a', role: 'assistant', time: '',
    parts: [
      { kind: 'reasoning', text: 'Thinking' },
      { kind: 'text', text: '\nFirst **bold**\n' },
      { kind: 'tool', tool: { id: 'c', name: 'Read', status: 'completed' } },
      { kind: 'text', text: '```go\nfmt.Println()\n```' },
      { kind: 'notice', text: 'Assistant error' },
    ],
  };
  assert.equal(messageMarkdown(assistant), 'First **bold**\n\n```go\nfmt.Println()\n```\n\n> Assistant error');
  assert.equal(messageText(assistant), 'First **bold**\n\n```go\nfmt.Println()\n```');

  const user = {
    id: 'u', role: 'user', time: '',
    parts: [
      { kind: 'text', text: 'See these' },
      { kind: 'file', file: { name: 'shot.png', path: '/tmp/shot.png' } },
      { kind: 'file', file: { path: '/tmp/my file (1).txt' } },
      { kind: 'file', file: { name: 'pasted [1]' } },
    ],
  };
  assert.equal(messageMarkdown(user), 'See these\n\n[shot.png](/tmp/shot.png)\n\n[my file (1).txt](</tmp/my file (1).txt>)\n\npasted [1]');
  assert.equal(messageText(user), 'See these');

  const fileOnly = { id: 'f', role: 'user', time: '', parts: [{ kind: 'file', file: { name: 'a.png' } }] };
  assert.equal(messageMarkdown(fileOnly), 'a.png');
  assert.equal(messageText(fileOnly), '');
  // A pasted image has no name or path; its blob reference isn't copied.
  const pasted = (mime) => ({ id: 'p', role: 'user', time: '', parts: [{ kind: 'file', file: { mime, ref: 'rec:10:0' } }] });
  assert.equal(messageMarkdown(pasted('image/png')), 'Image');
  assert.equal(messageMarkdown(pasted('application/pdf')), 'Attachment');
  for (const empty of [
    { id: 't', role: 'assistant', time: '', parts: [{ kind: 'tool', tool: { id: 'c', name: 'Read', status: 'completed' } }] },
    { id: 's', role: 'system', time: '', parts: [{ kind: 'text', text: 'System' }] },
    { id: 'm', role: 'user', time: '', isMeta: true, parts: [{ kind: 'text', text: 'Meta' }] },
    { id: 'n', role: 'assistant', time: '', parts: null },
  ]) {
    assert.equal(messageMarkdown(empty), '', empty.id);
    assert.equal(messageText(empty), '', empty.id);
  }
});

const TRANSLATIONS_STORE_URL = new URL('../src/lib/stores/translations.svelte.ts', import.meta.url);

let translationsStoreModule;

async function loadTranslationsStore() {
  translationsStoreModule ??= await loadServerModule(TRANSLATIONS_STORE_URL, [
    ["import { api } from '../api';", fixtureStore('api', '__storeFixture')],
    ["'../manage'", JSON.stringify(new URL('../manage.ts', TRANSLATIONS_STORE_URL).href)],
  ]);
  return translationsStoreModule;
}

test('translationKey separates sessions, messages and languages', async () => {
  const { translationKey } = await loadTranslationsStore();
  const ref = { agent: 'claude', id: 's1' };
  assert.equal(translationKey(ref, { id: 'm1' }, 3, 'Vietnamese'), 'claude:s1|id:m1|Vietnamese');
  assert.equal(translationKey(ref, { id: '' }, 3, 'Vietnamese'), 'claude:s1|#3|Vietnamese');
  const keys = new Set([
    translationKey(ref, { id: 'm1' }, 0, 'Vietnamese'),
    translationKey(ref, { id: 'm1' }, 0, 'French'),
    translationKey({ agent: 'codex', id: 's1' }, { id: 'm1' }, 0, 'Vietnamese'),
    translationKey(ref, { id: 'm2' }, 0, 'Vietnamese'),
    translationKey(ref, { id: '' }, 0, 'Vietnamese'),
  ]);
  assert.equal(keys.size, 5);
});

test('TranslationsStore tracks loading, done and error and keeps the newest', async () => {
  const { TranslationsStore, MAX_TRANSLATIONS } = await loadTranslationsStore();
  const pending = [];
  globalThis.__storeFixture = {
    api: { translate: (text) => new Promise((resolve, reject) => pending.push({ text, resolve, reject })) },
  };
  try {
    const store = new TranslationsStore();
    const done = store.translate('k', 'Hello');
    assert.equal(store.get('k').status, 'loading');
    // A second request while one runs is ignored.
    void store.translate('k', 'Hello');
    assert.equal(pending.length, 1);
    pending[0].resolve('Xin chào');
    await done;
    assert.deepEqual({ ...store.get('k') }, { status: 'done', text: 'Xin chào', error: '', showOriginal: false });

    store.toggleOriginal('k');
    assert.equal(store.get('k').showOriginal, true);
    store.toggleOriginal('k');
    assert.equal(store.get('k').showOriginal, false);

    const failed = store.translate('bad', 'Hi');
    pending[1].reject('translate: 401 Unauthorized');
    await failed;
    assert.equal(store.get('bad').status, 'error');
    assert.equal(store.get('bad').error, 'translate: 401 Unauthorized');
    store.toggleOriginal('bad');
    assert.equal(store.get('bad').showOriginal, false);

    // A result that lands after clear() is dropped.
    const late = store.translate('late', 'Hi');
    store.clear();
    pending[2].resolve('late');
    await late;
    assert.equal(store.get('late'), undefined);

    // The oldest entries go once the cap is reached.
    for (let i = 0; i <= MAX_TRANSLATIONS; i++) void store.translate(`n${i}`, 'x');
    assert.equal(store.get('n0'), undefined);
    assert.equal(store.get('n1').status, 'loading');
    assert.equal(Object.keys(store.entries).length, MAX_TRANSLATIONS);
  } finally {
    delete globalThis.__storeFixture;
  }
});

test('MockBackendAPI keeps the translation key and translates once set up', async () => {
  const mock = new MockBackendAPI();
  mock.translateDelayMs = 0;
  let s = await mock.translateSettings();
  assert.deepEqual(s, { baseURL: '', model: '', language: 'Vietnamese', apiKeySet: false, configured: false });
  await assert.rejects(mock.translate('Hello'), /not set up/);
  await assert.rejects(mock.setTranslateSettings({ baseURL: 'api.example.com', model: 'm', language: '', apiKey: 'k', clearApiKey: false }), /http or https/);

  s = await mock.setTranslateSettings({ baseURL: 'https://api.example.com/v1', model: 'm', language: '', apiKey: 'sk-1', clearApiKey: false });
  assert.deepEqual(s, { baseURL: 'https://api.example.com/v1', model: 'm', language: 'Vietnamese', apiKeySet: true, configured: true });
  assert.equal(JSON.stringify(s).includes('sk-1'), false);
  assert.equal(await mock.translate('Hello'), '[Vietnamese] Hello');

  s = await mock.setTranslateSettings({ baseURL: 'https://api.example.com/v1', model: 'm', language: 'French', apiKey: '', clearApiKey: false });
  assert.equal(s.apiKeySet, true);
  s = await mock.setTranslateSettings({ baseURL: 'https://api.example.com/v1', model: 'm', language: 'French', apiKey: '', clearApiKey: true });
  assert.deepEqual([s.apiKeySet, s.configured], [false, false]);
});

test('a translation replaces the text parts where the first was', () => {
  const file = { kind: 'file', file: { name: 'a.png', mime: 'image/png', ref: 'r' } };
  assert.deepEqual(
    withTranslation([{ kind: 'text', text: 'one' }, file, { kind: 'text', text: 'two' }], 'xin chào'),
    [{ kind: 'text', text: 'xin chào' }, file],
  );
  assert.deepEqual(withTranslation([file], 'xin chào'), [{ kind: 'text', text: 'xin chào' }, file]);
});

test('Markdown files use the shared print editor without a session and preserve their source', () => {
  const jobs = [];
  setPrintEditor((job) => jobs.push(job));
  try {
    const markdown = '  # Notes\r\n\r\n```js\r\nconst x = 1;\r\n```\r\n';
    printFile('docs/notes.md', markdown);
    printFile('docs\\notes.markdown', markdown);
    assert.deepEqual(jobs, [
      { title: 'notes.md', markdown },
      { title: 'notes.markdown', markdown },
    ]);
    const message = { sessionRef: { id: 'session' }, title: 'Session', parts: [{ kind: 'text', text: 'Hello' }] };
    printMessage(message);
    assert.equal(jobs[2], message);
  } finally {
    setPrintEditor(null);
  }
  printFile('notes.md', '# No editor mounted');
  assert.equal(jobs.length, 3);
});

test('the print title is the edited title without unsafe file name characters', () => {
  assert.equal(printTitle('Fix: a/b "c"?'), 'Fix a b c');
  assert.equal(printTitle('  '), 'Message');
  assert.equal(printTitle('x'.repeat(200)), 'x'.repeat(100));
});

test('a message to print becomes Markdown with its images as attachment lines', () => {
  const shot = { mime: 'image/png', ref: 'r1' };
  const named = { name: 'diagram [v2].png', mime: 'image/png', ref: 'r2' };
  const { markdown, attachments } = printMarkdown([
    { kind: 'text', text: '  Hello  ' },
    { kind: 'file', file: shot },
    { kind: 'file', file: { name: 'notes.txt', mime: 'text/plain' } },
    { kind: 'file', file: named },
    { kind: 'notice', text: 'Assistant error' },
  ]);
  assert.equal(
    markdown,
    'Hello\n\n![Image](attachment:1)\n\n📎 notes.txt\n\n![diagram v2.png](attachment:2)\n\n> Assistant error',
  );
  assert.deepEqual(attachments, [shot, named]);
});

test('print blocks split the Markdown at attachment lines outside code', () => {
  assert.deepEqual(printBlocks('Intro\n\n![a.png](attachment:2)\nAfter\n```\n![x](attachment:1)\n```'), [
    { kind: 'markdown', text: 'Intro\n' },
    { kind: 'image', index: 2, alt: 'a.png' },
    { kind: 'markdown', text: 'After\n```\n![x](attachment:1)\n```' },
  ]);
  assert.deepEqual(printBlocks('Inline ![a](attachment:1) stays'), [{ kind: 'markdown', text: 'Inline ![a](attachment:1) stays' }]);
  assert.deepEqual(printBlocks(''), []);
});
