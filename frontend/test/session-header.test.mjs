import test from 'node:test';
import assert from 'node:assert/strict';
import { canOpenExternal, canOpenPanel, openMode } from '../src/lib/terminal.ts';
import { renderSessionHeader } from './helpers.mjs';

test('SessionHeader shows the selected transcript display mode', async () => {
  const html = await renderSessionHeader({ link: { dataHost: undefined }, launcher: { canOpen: false } }, { mode: 'chat' });
  assert.match(html, /aria-label="Transcript display"/);
  assert.match(html, /class="action-btn mode-btn[^"]* active"[^>]*aria-pressed="true"[^>]*>\s*Chat/);
  assert.match(html, /aria-pressed="false"[^>]*>\s*Activity/);
  assert.match(html, /aria-pressed="false"[^>]*>\s*All/);
  assert.doesNotMatch(html, /Show Meta|Hide Meta/);
});

test('SessionHeader splits Resume into open and copy when a terminal is available', async () => {
  const split = await renderSessionHeader({ link: { dataHost: undefined }, launcher: { canOpen: true } });
  assert.match(split, /class="action-btn resume-btn resume-main[^"]*"[^>]*title="Resume this session in a new terminal window/);
  assert.match(split, /aria-label="More ways to resume"/);

  const opening = await renderSessionHeader({ link: { dataHost: undefined }, launcher: { canOpen: true } }, { resumeOpening: true });
  assert.match(opening, /resume-main[^"]*"[^>]*disabled[^>]*>\s*Opening…/);

  // Without a terminal, or for a remote host's session, Resume only copies.
  for (const fixture of [
    { link: { dataHost: undefined }, launcher: { canOpen: false } },
    { link: { dataHost: 'plgl' }, launcher: { canOpen: false } },
  ]) {
    const html = await renderSessionHeader(fixture);
    assert.doesNotMatch(html, /More ways to resume/);
    assert.match(html, /title="Copy shell command to resume this session/);
  }
});

test('SessionHeader Resume ▾ offers the place agents do not open by default', async () => {
  // A server render keeps the menu closed: check the button and its toggle.
  const panel = await renderSessionHeader({
    link: { dataHost: 'plgl' },
    launcher: { canOpen: true, openMode: 'panel', canOpenPanel: true, canOpenExternal: false },
  });
  assert.match(panel, /resume-main[^"]*"[^>]*title="Resume this session in the built-in terminal on plgl/);
  assert.match(panel, /aria-label="More ways to resume"/);

  // External mode where the session has no external terminal (SSH): the
  // main button copies and ▾ still offers the built-in panel.
  const ssh = await renderSessionHeader({
    link: { dataHost: 'plgl' },
    launcher: { canOpen: false, openMode: 'external', canOpenPanel: true, canOpenExternal: false },
  });
  assert.match(ssh, /resume-main[^"]*"[^>]*title="Copy shell command to resume this session/);
  assert.match(ssh, /aria-label="More ways to resume"/);

  // Neither place opens: Resume only copies.
  const none = await renderSessionHeader({
    link: { dataHost: 'plgl' },
    launcher: { canOpen: false, openMode: 'external', canOpenPanel: false, canOpenExternal: false },
  });
  assert.doesNotMatch(none, /More ways to resume/);
});
