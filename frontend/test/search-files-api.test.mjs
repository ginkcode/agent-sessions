import test from 'node:test';
import assert from 'node:assert/strict';
import { MockBackendAPI } from '../src/lib/mock/mockApi.ts';
import { createAPI, REPLACE_FILES_UNSUPPORTED, SEARCH_FILES_UNSUPPORTED } from '../src/lib/api.ts';

function withWailsApp(App, fn) {
  const hadWindow = 'window' in globalThis;
  const previous = globalThis.window;
  globalThis.window = { go: { app: { App } } };
  return Promise.resolve()
    .then(fn)
    .finally(() => {
      if (hadWindow) globalThis.window = previous;
      else delete globalThis.window;
    });
}

test('Wails searchFiles forwards the query and search ID and normalizes errors', async () => {
  const calls = [];
  const reply = { files: [], matches: 0, searched: 3 };
  const App = {
    async SearchFiles(...args) { calls.push(['search', ...args]); return reply; },
    async CancelSearch(...args) { calls.push(['cancel', ...args]); },
  };
  await withWailsApp(App, async () => {
    const backend = createAPI();
    assert.ok(!(backend instanceof MockBackendAPI));
    const query = { pattern: 'needle', regex: true, include: '*.go' };
    assert.deepEqual(await backend.searchFiles('/repo', query, 'pane'), reply);
    await backend.cancelSearch('pane');
    assert.deepEqual(calls, [['search', '/repo', query, 'pane'], ['cancel', 'pane']]);
    App.SearchFiles = () => Promise.reject('invalid search pattern: missing closing )');
    await assert.rejects(
      backend.searchFiles('/repo', { pattern: '(' }, 'pane'),
      (err) => err instanceof Error && err.message.includes('missing closing )'),
    );
  });
});

test('Wails searchFiles rejects clearly on an older backend without the binding', async () => {
  await withWailsApp({}, async () => {
    const backend = createAPI();
    await assert.rejects(
      backend.searchFiles('/repo', { pattern: 'x' }, 'pane'),
      (err) => err instanceof Error && err.message === SEARCH_FILES_UNSUPPORTED,
    );
    await backend.cancelSearch('pane'); // nothing to cancel, no error
  });
});

test('mock search finds matches with UTF-16 columns, in path order', async () => {
  const api = new MockBackendAPI();
  const result = await api.searchFiles('/project', { pattern: 'package' }, 's');
  const paths = result.files.map(f => f.path);
  assert.ok(paths.length > 0);
  assert.deepEqual(paths, [...paths].sort());
  assert.equal(result.matches, result.files.reduce((n, f) => n + f.matches.length, 0));
  const m = result.files[0].matches[0];
  assert.equal(m.end - m.start, 'package'.length);
  assert.equal(m.preview.slice(m.previewStart, m.previewEnd).toLowerCase(), 'package');
  assert.ok(result.files[0].revision);
});

test('mock search applies include globs and includes saved drafts', async () => {
  const api = new MockBackendAPI();
  const original = await api.readFile('/project', 'main.go');
  await api.saveFile('/project', { path: 'main.go', text: 'zebra here\n', expectedRevision: original.revision });
  const all = await api.searchFiles('/project', { pattern: 'zebra' }, 's');
  assert.deepEqual(all.files.map(f => f.path), ['main.go']);
  const none = await api.searchFiles('/project', { pattern: 'zebra', include: '*.md' }, 's');
  assert.equal(none.matches, 0);
});

test('mock search cancels by ID', async () => {
  const api = new MockBackendAPI();
  const pending = api.searchFiles('/project', { pattern: 'e' }, 'pane');
  await api.cancelSearch('pane');
  await assert.rejects(pending, /canceled/);
});

test('mock search reports an invalid regex', async () => {
  const api = new MockBackendAPI();
  await assert.rejects(api.searchFiles('/project', { pattern: '(', regex: true }, 's'), /invalid search pattern/);
});

test('mock hosts report content search support', async () => {
  const api = new MockBackendAPI();
  await api.connect('dev-box');
  const state = await api.connectionState();
  assert.equal(state.capabilities.searchFiles, true);
});

test('Wails replaceInFiles forwards the request and host, and rejects clearly without the binding', async () => {
  const calls = [];
  const reply = { files: [{ path: 'a.go', status: 'replaced', replaced: 1, revision: 'r2' }], replaced: 1 };
  const req = { query: { pattern: 'foo' }, replacement: 'bar', files: [{ path: 'a.go', expectedRevision: 'r1', matches: [{ line: 1, start: 0 }] }] };
  await withWailsApp({ async ReplaceInFiles(...args) { calls.push(args); return reply; } }, async () => {
    const backend = createAPI();
    assert.deepEqual(await backend.replaceInFiles('/repo', req, 'box'), reply);
    assert.deepEqual(await backend.replaceInFiles('/repo', req, undefined), reply);
    assert.deepEqual(calls, [['/repo', req, 'box'], ['/repo', req, '']], 'Local is the empty host');
  });
  await withWailsApp({ ReplaceInFiles: () => Promise.reject('invalid replacement: unsupported text') }, async () => {
    await assert.rejects(createAPI().replaceInFiles('/repo', req, ''), (err) => err instanceof Error && err.message.includes('invalid replacement'));
  });
  await withWailsApp({}, async () => {
    await assert.rejects(createAPI().replaceInFiles('/repo', req, ''), (err) => err instanceof Error && err.message === REPLACE_FILES_UNSUPPORTED);
  });
});

test('mock replaceInFiles writes its disk, checks revisions and finds the chosen matches again', async () => {
  const api = new MockBackendAPI();
  const query = { pattern: 'package' };
  const found = await api.searchFiles('/project', query, 's');
  const [first, second] = found.files;
  const res = await api.replaceInFiles('/project', {
    query, replacement: 'pkg',
    files: [
      { path: first.path, expectedRevision: first.revision, matches: [{ line: first.matches[0].line, start: first.matches[0].start }] },
      { path: second.path, expectedRevision: 'stale' },
      { path: first.path, expectedRevision: first.revision },
    ],
  }, undefined);
  assert.deepEqual(res.files.map(f => f.status), ['replaced', 'conflict', 'skipped']);
  assert.equal(res.files[2].reason, 'duplicate');
  assert.equal(res.replaced, 1);
  const after = await api.readFile('/project', first.path);
  assert.equal(after.revision, res.files[0].revision);
  assert.ok(after.text.split('\n')[first.matches[0].line - 1].includes('pkg'));
  const again = await api.replaceInFiles('/project', {
    query, replacement: 'pkg', files: [{ path: first.path, expectedRevision: after.revision, matches: [{ line: first.matches[0].line, start: first.matches[0].start }] }],
  }, undefined);
  assert.equal(again.files[0].reason, 'stale_match', 'a match that is no longer there is not guessed at');
});
