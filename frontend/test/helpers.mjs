// Shared by the test files: compiling real components and stores for
// server rendering with fixture stores, and small fixtures.
import assert from 'node:assert/strict';

// Store imports resolve per render to the fixture set for that render.
export function fixtureStore(name, fixture = '__componentFixture') {
  return `const ${name} = new Proxy({}, { get: (_, key) => globalThis.${fixture}.${name}[key] });`;
}

// Compiles a real component for server rendering. Only the listed imports
// are replaced: store imports with fixtures, helpers with the real modules.
export async function loadServerComponent(url, replacements) {
  const [{ readFileSync }, { fileURLToPath }, { compile }] = await Promise.all([
    import('node:fs'),
    import('node:url'),
    import('svelte/compiler'),
  ]);
  const filename = fileURLToPath(url);
  const code = compile(readFileSync(filename, 'utf8'), { generate: 'server', filename }).js.code;
  return importCompiled(filename, code, replacements);
}

// Compiles a real .svelte.ts store module for the server the same way: types
// are stripped, runes compiled, and only the listed imports replaced.
export async function loadServerModule(url, replacements) {
  const [{ readFileSync }, { fileURLToPath }, { compileModule }, { default: ts }] = await Promise.all([
    import('node:fs'),
    import('node:url'),
    import('svelte/compiler'),
    import('typescript'),
  ]);
  const filename = fileURLToPath(url);
  const js = ts.transpileModule(readFileSync(filename, 'utf8'), {
    fileName: filename,
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext, useDefineForClassFields: true },
  }).outputText;
  const code = compileModule(js, { generate: 'server', filename: filename.replace(/\.ts$/, '.js') }).js.code;
  return importCompiled(filename, code, replacements);
}

async function importCompiled(filename, code, replacements) {
  replacements = [
    ["'svelte/internal/server'", JSON.stringify(import.meta.resolve('svelte/internal/server'))],
    ...replacements,
  ];
  // Pure display-label helpers shared by the diagnostics components resolve to the real module.
  for (const [module, file] of [["'../../../codeCheckLabels'", 'codeCheckLabels'], ["'../../../managedTools'", 'managedTools'], ["'../managedTools'", 'managedTools'], ["'../../../date'", 'date']]) {
    if (code.includes(module) && !replacements.some(([from]) => from === module)) {
      replacements.push([module, JSON.stringify(new URL(`../src/lib/${file}.ts`, import.meta.url).href)]);
    }
  }
  // The browser-only splitter renders as a bare separator.
  const splitter = "import Splitter from '../Splitter.svelte';";
  if (code.includes(splitter) && !replacements.some(([from]) => from === splitter)) {
    replacements.push([splitter, `const Splitter = ($$renderer, $$props) => $$renderer.push(\`<div role="separator" aria-label="\${$$props.ariaLabel}"></div>\`);`]);
  }
  // The diagnostics store wires its singleton to the saved setting; store tests construct their own.
  const preferences = "import { preferences } from './preferences.svelte';";
  if (code.includes(preferences) && !replacements.some(([from]) => from === preferences)) {
    replacements.push([preferences, 'const preferences = { autoInstallLanguageServers: false, setAutoInstallLanguageServers() {} };']);
  }
  // Go to File reads only the time zone, for blame times.
  const viewerPreferences = "import { preferences } from '../../../stores/preferences.svelte';";
  if (code.includes(viewerPreferences) && !replacements.some(([from]) => from === viewerPreferences)) {
    replacements.push([viewerPreferences, "const preferences = { timeZone: 'utc' };"]);
  }
  // Go to File's content search: a fixture store (Files mode unless the
  // render says otherwise), a stub pane, and the real pure helpers.
  for (const store of ["import { contentSearch } from '../../../stores/contentSearch.svelte';", "import { contentSearch } from '../../stores/contentSearch.svelte';"]) {
    if (code.includes(store) && !replacements.some(([from]) => from === store)) {
      replacements.push([store, "const contentSearch = new Proxy({}, { get: (_, key) => (globalThis.__componentFixture?.contentSearch ?? { mode: 'files' })[key] });"]);
    }
  }
  for (const store of ["import { gitChanges } from '../../../stores/gitChanges.svelte';", "import { gitChanges } from '../../stores/gitChanges.svelte';"]) {
    if (code.includes(store) && !replacements.some(([from]) => from === store)) {
      replacements.push([store, "const gitChanges = new Proxy({}, { get: (_, key) => (globalThis.__componentFixture?.gitChanges ?? { status: null, refresh() {} })[key] });"]);
    }
  }
  for (const module of ["'../../gitChanges'", "'../../../gitChanges'"]) {
    if (code.includes(module) && !replacements.some(([from]) => from === module)) replacements.push([module, JSON.stringify(new URL('../src/lib/gitChanges.ts', import.meta.url).href)]);
  }
  const changesPane = "import ChangesPane from './ChangesPane.svelte';";
  if (code.includes(changesPane) && !replacements.some(([from]) => from === changesPane)) replacements.push([changesPane, "const ChangesPane = ($$renderer) => $$renderer.push('<div data-changes-pane></div>');"]);
  const searchPane = "import SearchPane from './SearchPane.svelte';";
  if (code.includes(searchPane) && !replacements.some(([from]) => from === searchPane)) {
    replacements.push([searchPane, "const SearchPane = ($$renderer) => $$renderer.push('<div data-search-pane></div>');"]);
  }
  for (const module of ["'../../contentSearch'", "'../../../contentSearch'"]) {
    if (code.includes(module) && !replacements.some(([from]) => from === module)) {
      replacements.push([module, JSON.stringify(new URL('../src/lib/contentSearch.ts', import.meta.url).href)]);
    }
  }
  for (const module of ["'../../referenceResults'", "'../../../referenceResults'"]) {
    if (code.includes(module) && !replacements.some(([from]) => from === module)) {
      replacements.push([module, JSON.stringify(new URL('../src/lib/referenceResults.ts', import.meta.url).href)]);
    }
  }
  for (const [from, to] of replacements) {
    assert.ok(code.includes(from), `compiled ${filename} no longer contains ${from}`);
    code = code.replace(from, to);
  }
  return import(`data:text/javascript,${encodeURIComponent(code)}`);
}

export async function renderWithFixture(module, fixture, props = {}) {
  const { render } = await import('svelte/server');
  globalThis.__componentFixture = fixture;
  try {
    return render(module.default, { props }).body;
  } finally {
    delete globalThis.__componentFixture;
  }
}

export function renderedButtons(html) {
  return [...html.matchAll(/<button\b([^>]*)>([\s\S]*?)<\/button>/g)].map(([, attrs, text]) => ({
    text: text.replace(/<!--[\s\S]*?-->/g, '').trim(),
    disabled: /\sdisabled(?:=|\s|$)/.test(attrs),
  }));
}

const RECONNECT_BANNER_URL = new URL('../src/lib/components/common/ReconnectBanner.svelte', import.meta.url);

let reconnectBannerModule;

export async function renderReconnectBanner(connectionStore) {
  reconnectBannerModule ??= await loadServerComponent(RECONNECT_BANNER_URL, [
    ["'../../guidance'", JSON.stringify(new URL('../../guidance.ts', RECONNECT_BANNER_URL).href)],
    ["import { connectionStore } from '../../stores/connection.svelte';", fixtureStore('connectionStore')],
  ]);
  return renderWithFixture(reconnectBannerModule, {
    connectionStore: { generation: 1, connect() {}, disconnect() {}, ...connectionStore },
  });
}

export function unescapeHTML(text) {
  return text.replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&quot;/g, '"').replace(/&#39;/g, "'").replace(/&amp;/g, '&');
}

export const WINDOWS_DIRS = [
  'C:\\Users\\me\\repo',
  'C:\\',
  '\\\\fileserver\\share\\team-repo\\',
  '\\\\?\\D:\\Workspaces\\x',
  'C:/Users/me\\mixed/repo',
];

const SESSION_HEADER_URL = new URL('../src/lib/components/transcript/SessionHeader.svelte', import.meta.url);

let sessionHeaderModule;

export async function renderSessionHeader(fixture, props = {}) {
  const lib = (rel) => JSON.stringify(new URL(rel, SESSION_HEADER_URL).href);
  sessionHeaderModule ??= await loadServerComponent(SESSION_HEADER_URL, [
    ["'../../format'", lib('../../format.ts')],
    ["'../../date'", lib('../../date.ts')],
    ["'../../portable'", lib('../../portable.ts')],
    ["'../../link'", lib('../../link.ts')],
    ["'../../transcript'", lib('../../transcript.ts')],
    ["'../../guidance'", lib('../../guidance.ts')],
    ["'../../terminal'", lib('../../terminal.ts')],
    ["import { newSession } from '../../stores/newSession.svelte';", fixtureStore('newSession')],
    ["import { terminals } from '../../stores/terminals.svelte';", fixtureStore('terminals')],
    ["import { toast } from '../../stores/toast.svelte';", fixtureStore('toast')],
    ["import AgentIcon from '../common/AgentIcon.svelte';", 'const AgentIcon = () => {};'],
    ["import NavButtons from './NavButtons.svelte';", 'const NavButtons = () => {};'],
    ["import { appState } from '../../stores/appState.svelte';", fixtureStore('appState')],
    ["import { manage } from '../../stores/manage.svelte';", fixtureStore('manage')],
    ["import { handoff } from '../../stores/handoff.svelte';", fixtureStore('handoff')],
    ["import { exporter } from '../../stores/export.svelte';", fixtureStore('exporter')],
    ["import { link } from '../../stores/link.svelte';", fixtureStore('link')],
    ["import { launcher } from '../../stores/launcher.svelte';", fixtureStore('launcher')],
    ["import { preferences } from '../../stores/preferences.svelte';", fixtureStore('preferences')],
  ]);
  const meta = {
    ref: { agent: 'codex', id: 's1' },
    title: 'Fix the build',
    cwd: 'C:\\work\\proj',
    counts: { user: 1, assistant: 1 },
    tokens: { input: 10, output: 5 },
    createdAt: '2026-10-01T10:00:00Z',
    updatedAt: '2026-10-01T11:00:00Z',
  };
  return renderWithFixture(
    sessionHeaderModule,
    { appState: {}, manage: { settings: { enabled: false } }, handoff: {}, exporter: {}, toast: {}, preferences: { timeZone: 'utc' }, newSession: { available: false, choices: [], starting: false, refresh() {} }, terminals: { tabFor: () => undefined }, ...fixture },
    { meta, mode: 'activity', onModeChange() {}, onResume() {}, onReveal() {}, onDelete() {}, ...props },
  );
}

const HANDOFF_DIALOG_URL = new URL('../src/lib/components/common/HandoffDialog.svelte', import.meta.url);

let handoffDialogModule;

export async function renderHandoffDialog(fixture) {
  const lib = (rel) => JSON.stringify(new URL(rel, HANDOFF_DIALOG_URL).href);
  globalThis.__remoteCommandNote ??= (await loadRemoteCommandNote()).default;
  handoffDialogModule ??= await loadServerComponent(HANDOFF_DIALOG_URL, [
    ["'../../portable'", lib('../../portable.ts')],
    ["'../../format'", lib('../../format.ts')],
    ["import AgentIcon from './AgentIcon.svelte';", 'const AgentIcon = () => {};'],
    ["import RemoteCommandNote from './RemoteCommandNote.svelte';", 'const RemoteCommandNote = globalThis.__remoteCommandNote;'],
    ["import MarkdownDoc from './MarkdownDoc.svelte';", 'const MarkdownDoc = () => {};'],
    ["import { handoff } from '../../stores/handoff.svelte';", fixtureStore('handoff')],
    ["import { link } from '../../stores/link.svelte';", fixtureStore('link')],
    ["import { launcher } from '../../stores/launcher.svelte';", fixtureStore('launcher')],
  ]);
  const handoff = {
    dialogOpen: true,
    session: { ref: { agent: 'claude-code', id: 's1' }, cwd: 'C:\\work' },
    target: 'codex',
    budget: 80000,
    cwd: 'C:\\work',
    launching: false,
    preview: {
      command: "Set-Location -LiteralPath C:\\work -ErrorAction Stop; codex 'Read it'",
      promptMarkdown: 'Read it',
      fullMarkdown: '# Handoff',
      promptBytes: 7,
      report: { estimatedTokens: 100, trimmed: false, droppedItems: [] },
    },
    ...fixture.handoff,
  };
  return renderWithFixture(handoffDialogModule, { link: { dataHost: undefined }, ...fixture, handoff });
}

const REMOTE_COMMAND_NOTE_URL = new URL('../src/lib/components/common/RemoteCommandNote.svelte', import.meta.url);

let remoteCommandNoteModule;

export async function loadRemoteCommandNote() {
  remoteCommandNoteModule ??= await loadServerComponent(REMOTE_COMMAND_NOTE_URL, [
    ["'../../hosts'", JSON.stringify(new URL('../../hosts.ts', REMOTE_COMMAND_NOTE_URL).href)],
  ]);
  return remoteCommandNoteModule;
}
