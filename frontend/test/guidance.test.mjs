import test from 'node:test';
import assert from 'node:assert/strict';
import { copiedGuidance, copyFailed, openedGuidance, openFailed } from '../src/lib/guidance.ts';
import { resumeButtonTitle } from '../src/lib/link.ts';

test('resumeButtonTitle tells remote users to paste into a shell on the host', () => {
  assert.equal(resumeButtonTitle(undefined), 'Copy shell command to resume this session');
  assert.equal(
    resumeButtonTitle('plgl'),
    'Copy shell command to resume this session. Run it on plgl: open a shell with "ssh plgl", then paste it.',
  );
});

test('copiedGuidance tells local users to paste into a terminal', () => {
  const resume = copiedGuidance('resume');
  assert.equal(resume.title, 'Resume command copied');
  assert.match(resume.body, /^Paste it into a terminal\./);
  assert.equal(resume.code, undefined);

  const launch = copiedGuidance('launch', { agent: 'codex' });
  assert.equal(launch.title, 'Launch command copied');
  assert.match(launch.body, /Codex starts in the project directory, restores the context and waits/);
});

test('copiedGuidance sends remote users to a shell on the host', () => {
  const launch = copiedGuidance('launch', { host: 'plgl', agent: 'opencode' });
  assert.match(launch.body, /^Open a shell on plgl, then paste it there\. OpenCode starts/);
  assert.equal(launch.code, 'ssh plgl');

  const prompt = copiedGuidance('prompt', { host: 'plgl', agent: 'claude-code' });
  assert.equal(prompt.body, 'Start Claude Code in the project directory on plgl and paste it as your first message.');
  assert.equal(prompt.code, undefined);
});

test('copiedGuidance distinguishes a full document from a launch prompt', () => {
  const doc = copiedGuidance('doc', { host: 'plgl', agent: 'codex' });
  assert.equal(doc.title, 'Document copied');
  assert.equal(doc.body, 'The full handoff document is on your clipboard.');
  assert.equal(doc.code, undefined);
});

test('resumeButtonTitle offers the terminal for local sessions, never over SSH', () => {
  assert.equal(
    resumeButtonTitle(undefined, true),
    'Resume this session in a new terminal window. Use ▾ to copy the command instead',
  );
  assert.match(resumeButtonTitle('plgl', true), /^Copy shell command .* Run it on plgl/);
  // The built-in panel opens sessions from every host, SSH included.
  assert.equal(
    resumeButtonTitle(undefined, true, 'panel'),
    'Resume this session in the built-in terminal. Use ▾ for other ways to resume',
  );
  assert.equal(
    resumeButtonTitle('plgl', true, 'panel'),
    'Resume this session in the built-in terminal on plgl. Use ▾ for other ways to resume',
  );
});

test('copiedGuidance tells local Windows users to paste into PowerShell', () => {
  const resume = copiedGuidance('resume', { shell: 'powershell' });
  assert.match(resume.body, /^Paste it into PowerShell\. It opens the session/);
  const launch = copiedGuidance('launch', { shell: 'powershell', agent: 'codex' });
  assert.match(launch.body, /^Paste it into PowerShell\. Codex starts/);
  // A remote command is built for the host's shell, not the local one.
  const remote = copiedGuidance('resume', { host: 'plgl', shell: 'powershell' });
  assert.match(remote.body, /^Open a shell on plgl, then paste it there\./);
  assert.match(copiedGuidance('resume', { shell: 'posix' }).body, /^Paste it into a terminal\./);
});

test('openedGuidance and openFailed describe Open in terminal', () => {
  assert.equal(openedGuidance('resume').title, 'Opened in a terminal');
  assert.match(openedGuidance('resume').body, /resumes in its directory in a new terminal window/);
  assert.match(openedGuidance('launch', 'opencode').body, /^OpenCode starts in the project directory in a new terminal window/);
  const failed = openFailed(new Error('Codex was not found.'));
  assert.equal(failed.title, "Couldn't open a terminal");
  assert.equal(failed.body, 'Codex was not found.');
  assert.equal(failed.tone, 'error');
});

test('copyFailed reports the error as an error toast', () => {
  const t = copyFailed('resume command', new Error('not connected'));
  assert.equal(t.title, "Couldn't copy resume command");
  assert.equal(t.body, 'not connected');
  assert.equal(t.tone, 'error');
});

// --- WSL distributions as hosts ---
