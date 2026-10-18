import test from 'node:test';
import assert from 'node:assert/strict';
import { absoluteFilePath, markdownFileTarget } from '../src/lib/fileActions.ts';
import { createAPI } from '../src/lib/api.ts';

test('absolute file paths use the host directory style, not the UI platform', () => {
  assert.equal(absoluteFilePath('/home/user/project', 'src/main.go'), '/home/user/project/src/main.go');
  assert.equal(absoluteFilePath('/home/user/project/', '.env'), '/home/user/project/.env');
  assert.equal(absoluteFilePath('/', 'etc/config'), '/etc/config');
  assert.equal(absoluteFilePath('C:\\Projects\\app', 'src/main.go'), 'C:\\Projects\\app\\src\\main.go');
  assert.equal(absoluteFilePath('C:/Projects/app/', 'src/main.go'), 'C:\\Projects\\app\\src\\main.go');
  assert.equal(absoluteFilePath('\\\\server\\share\\', 'src/main.go'), '\\\\server\\share\\src\\main.go');
});

test('file action API passes the right-click target to native bindings', async () => {
  const previous = globalThis.window;
  const calls = [];
  const App = Object.fromEntries(['RevealFile', 'CopyFile', 'DownloadFile'].map(name => [name, async (...args) => {
    calls.push([name, ...args]);
    return name === 'DownloadFile' ? '/Downloads/src.zip' : undefined;
  }]));
  globalThis.window = { go: { app: { App } } };
  try {
    const backend = createAPI();
    await backend.revealFile('/project', 'src');
    await backend.copyFile('/project', 'src/main.go');
    assert.equal(await backend.downloadFile('/project', 'src'), '/Downloads/src.zip');
    assert.deepEqual(calls, [
      ['RevealFile', '/project', 'src'],
      ['CopyFile', '/project', 'src/main.go'],
      ['DownloadFile', '/project', 'src'],
    ]);
    App.CopyFile = async () => { throw 'Clipboard unavailable'; };
    await assert.rejects(backend.copyFile('/project', 'src'), /Clipboard unavailable/);
  } finally {
    if (previous === undefined) delete globalThis.window;
    else globalThis.window = previous;
  }
});

test('Markdown file links resolve relative to the document and stay in the project', () => {
  assert.equal(markdownFileTarget('course/README.md', 'week-01.md'), 'course/week-01.md');
  assert.equal(markdownFileTarget('course/README.md', '../guide.md#intro'), 'guide.md');
  assert.equal(markdownFileTarget('course/README.md', './notes%20one.md'), 'course/notes one.md');
  for (const href of ['../../outside.md', '%2e%2e/%2e%2e/outside.md', '/etc/passwd', 'file:///etc/passwd', 'javascript:alert', '//host/file', '%5csecret', '%00bad', '%ZZ']) {
    assert.equal(markdownFileTarget('course/README.md', href), null, href);
  }
});
