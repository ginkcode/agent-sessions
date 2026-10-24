import test from 'node:test';
import assert from 'node:assert/strict';
import { refsEqual, filterSessionsByAge } from '../src/lib/manage.ts';
import {
  defaultCollapsedKeys,
  isCollapsed,
  nodeKind,
  directoryName,
  pruneKeys,
  sessionToSelect,
  nodesHolding,
  nodeToHighlight,
  knownDirectories,
  directoryNodeKey,
  hashHost,
  targetKey,
  storageKeyForCollapsed,
  BASE_COLLAPSED_STORAGE_KEY,
  loadCollapsedKeys,
  saveCollapsedKeys,
} from '../src/lib/tree.ts';
import { WINDOWS_DIRS, fixtureStore, loadServerComponent, renderWithFixture } from './helpers.mjs';

test('directoryName takes the last name of a directory label', () => {
  assert.equal(directoryName('~/Workspaces/app'), 'app');
  assert.equal(directoryName('/srv/app/'), 'app');
  assert.equal(directoryName('C:\\Users\\me\\app'), 'app');
  for (const label of ['~', '/', 'C:\\', '(unknown directory)']) {
    assert.equal(directoryName(label), label);
  }
});

function treeNode(key, kind, childCount = 0, childKind = 'session') {
  const children = Array.from({ length: childCount }, (_, i) => ({
    key: `${key}/${i}`,
    label: `${key}/${i}`,
    kind: childKind,
    sessionCount: 1,
  }));
  return { key, label: key, kind, sessionCount: childCount, children };
}

test('defaultCollapsedKeys opens top-level groups and closes the groups inside them', () => {
  assert.equal(defaultCollapsedKeys([]).size, 0);

  // By directory: each directory lists its agents, closed, however many.
  const small = { ...treeNode('small', 'directory'), children: [treeNode('small/claude', 'agent', 1)] };
  const big = { ...treeNode('big', 'directory'), children: [treeNode('big/claude', 'agent', 30), treeNode('big/codex', 'agent', 2)] };
  assert.deepEqual([...defaultCollapsedKeys([small, big])].sort(), ['big/claude', 'big/codex', 'small/claude']);

  // By agent: each agent lists its directories, closed.
  const agent = { ...treeNode('claude', 'agent'), children: [treeNode('claude/be', 'directory', 18), treeNode('claude/tmp', 'directory', 1)] };
  assert.deepEqual([...defaultCollapsedKeys([agent])].sort(), ['claude/be', 'claude/tmp']);

  // A top-level group of sessions stays open; an empty group has nothing to close.
  assert.equal(defaultCollapsedKeys([treeNode('flat', 'agent', 50), treeNode('empty', 'directory')]).size, 0);
});

test('defaultCollapsedKeys collapses every session with subagents', () => {
  const withOne = treeNode('sess1', 'session', 1);
  const withMany = treeNode('sessN', 'session', 7);
  const leaf = treeNode('leaf', 'session');
  const agent = { ...treeNode('agent', 'agent'), children: [withOne, withMany, leaf] };

  const keys = defaultCollapsedKeys([agent]);
  assert.deepEqual([...keys].sort(), ['sess1', 'sessN']);
});

test('the tree highlights the row where the open session is', () => {
  const a = { agent: 'claude-code', id: 'a' };
  const b = { agent: 'codex', id: 'b' };
  const sub = { agent: 'claude-code', id: 'a-sub' };
  const session = (ref, children) => ({ key: `session:${ref.id}`, label: ref.id, kind: 'session', sessionCount: 1, sessions: children ? [ref, ...children.flatMap((c) => c.sessions)] : [ref], children });
  const tree = [
    {
      key: 'dir:/x', label: '/x', kind: 'directory', sessionCount: 3, sessions: [a, sub, b],
      children: [
        { key: 'dir-agent:/x:claude', label: 'Claude', kind: 'agent', sessionCount: 2, sessions: [a, sub], children: [session(a, [session(sub)])] },
        { key: 'dir-agent:/x:codex', label: 'Codex', kind: 'agent', sessionCount: 1, sessions: [b] },
      ],
    },
    { key: 'dir:/y', label: '/y', kind: 'directory', sessionCount: 0, sessions: [] },
  ];
  assert.deepEqual([...nodesHolding(tree, b)], ['dir:/x', 'dir-agent:/x:codex']);
  assert.deepEqual([...nodesHolding(tree, sub)], ['dir:/x', 'dir-agent:/x:claude', 'session:a', 'session:a-sub']);
  assert.deepEqual([...nodesHolding(tree, { ...b })], ['dir:/x', 'dir-agent:/x:codex'], 'refs compare by value');
  assert.equal(nodesHolding(tree, { agent: 'codex', id: 'gone' }).size, 0);
  assert.equal(nodesHolding(tree, null).size, 0);

  const none = new Set();
  assert.equal(nodeToHighlight(tree, b, 'dir:/x', none, none), 'dir:/x', 'the clicked group holds it');
  assert.equal(nodeToHighlight(tree, b, 'dir:/y', none, none), 'dir-agent:/x:codex', 'opened from outside the group');
  assert.equal(nodeToHighlight(tree, b, null, none, none), 'dir-agent:/x:codex');
  assert.equal(nodeToHighlight(tree, sub, null, none, none), 'session:a-sub');
  assert.equal(nodeToHighlight(tree, b, null, new Set(['dir:/x']), none), 'dir:/x', 'a closed group stands for its rows');
  assert.equal(nodeToHighlight(tree, sub, null, none, new Set(['session:a'])), 'session:a', 'closed by default');
  assert.equal(nodeToHighlight(tree, { agent: 'codex', id: 'gone' }, 'dir:/y', none, none), 'dir:/y', 'filtered out of the tree');
  assert.equal(nodeToHighlight(tree, null, null, none, none), null);
});

test('isCollapsed flips the default state for manually toggled keys', () => {
  const defaults = new Set(['big']);
  assert.equal(isCollapsed('big', new Set(), defaults), true);
  assert.equal(isCollapsed('big', new Set(['big']), defaults), false);
  assert.equal(isCollapsed('small', new Set(), defaults), false);
  assert.equal(isCollapsed('small', new Set(['small']), defaults), true);
});

test('nodeKind prefers kind and falls back to field inference', () => {
  assert.equal(nodeKind({ key: 'a', label: 'a', kind: 'session', agent: 'codex', sessionCount: 1 }), 'session');
  assert.equal(nodeKind({ key: 'a', label: 'a', agent: 'codex', sessionCount: 1 }), 'agent');
  assert.equal(nodeKind({ key: 'a', label: 'a', cwd: '/x', sessionCount: 1 }), 'directory');
  assert.equal(nodeKind({ key: 'a', label: 'a', sessionCount: 1 }), 'session');
});

test('pruneKeys drops keys whose nodes no longer exist', () => {
  const tree = [treeNode('dir', 'directory', 2, 'agent')];
  const pruned = pruneKeys(new Set(['dir', 'dir/1', 'gone']), tree);
  assert.deepEqual([...pruned].sort(), ['dir', 'dir/1']);
});

test('tree helpers treat Windows and UNC keys as opaque strings', () => {
  const driveKey = 'dir-agent:C:\\Users\\me\\repo';
  const drive = { ...treeNode(driveKey, 'directory'), children: [treeNode(`${driveKey}/0`, 'agent', 1)] };
  const unc = treeNode('dir-agent:\\\\fileserver\\share\\team-repo\\', 'directory', 1, 'agent');
  const root = { ...treeNode('dir-agent:C:\\', 'directory'), cwd: 'C:\\' };
  const tree = [drive, unc, root];

  assert.deepEqual([...defaultCollapsedKeys(tree)], [`${driveKey}/0`]);
  assert.equal(isCollapsed(`${driveKey}/0`, new Set(), defaultCollapsedKeys(tree)), true);
  assert.equal(
    isCollapsed('dir-agent:\\\\fileserver\\share\\team-repo\\', new Set(['dir-agent:\\\\fileserver\\share\\team-repo\\']), new Set()),
    true
  );

  // Keys are matched exactly: no case folding or separator normalization.
  const pruned = pruneKeys(
    new Set([
      'dir-agent:C:\\Users\\me\\repo',
      'dir-agent:C:\\Users\\me\\repo/0',
      'dir-agent:c:\\users\\me\\repo',
      'dir-agent:C:/Users/me/repo',
      'dir-agent:\\\\fileserver\\share\\team-repo\\',
      'dir-agent:\\\\fileserver\\share\\team-repo',
    ]),
    tree
  );
  assert.deepEqual([...pruned].sort(), [
    'dir-agent:C:\\Users\\me\\repo',
    'dir-agent:C:\\Users\\me\\repo/0',
    'dir-agent:\\\\fileserver\\share\\team-repo\\',
  ].sort());

  for (const cwd of WINDOWS_DIRS) {
    assert.equal(nodeKind({ key: `dir-agent:${cwd}`, label: cwd, cwd, sessionCount: 1 }), 'directory');
  }

  const store = new Map();
  globalThis.localStorage = {
    getItem: (k) => store.get(k) ?? null,
    setItem: (k, v) => store.set(k, String(v)),
    removeItem: (k) => store.delete(k),
    clear: () => store.clear(),
  };
  try {
    const keys = new Set(WINDOWS_DIRS.map((cwd) => `dir-agent:${cwd}`));
    saveCollapsedKeys(keys);
    assert.deepEqual([...loadCollapsedKeys()], [...keys]);
  } finally {
    delete globalThis.localStorage;
  }
});

test('sessionToSelect picks the node itself for sessions, else the first visible', () => {
  const parent = { agent: 'claude-code', id: 'parent' };
  const sub = { agent: 'claude-code', id: 'sub' };
  const other = { agent: 'codex', id: 'x' };
  // Subagent sorts first (more recently updated) but the session node wins.
  const visible = [{ ref: sub }, { ref: parent }];
  const sessionNode = { key: 's', label: 's', kind: 'session', sessionCount: 2, sessions: [parent, sub] };
  assert.deepEqual(sessionToSelect(sessionNode, visible), parent);

  const group = { key: 'g', label: 'g', kind: 'agent', sessionCount: 2, sessions: [parent, sub] };
  assert.deepEqual(sessionToSelect(group, visible), sub);

  // Session hidden by the age filter falls back to the first visible row.
  assert.deepEqual(sessionToSelect(sessionNode, [{ ref: other }]), other);
  assert.equal(sessionToSelect(group, []), null);
});

test('knownDirectories lists existing directories once, sorted', () => {
  const groups = [
    { key: 'a:claude', label: 'claude', kind: 'agent', agent: 'claude-code', sessionCount: 3, children: [
      { key: 'a:claude:/b', label: '/b', kind: 'directory', cwd: '/b', sessionCount: 1 },
      { key: 'a:claude:/a', label: '/a', kind: 'directory', cwd: '/a', sessionCount: 1 },
      { key: 'a:claude:/gone', label: '/gone', kind: 'directory', cwd: '/gone', cwdMissing: true, sessionCount: 1 },
    ] },
    { key: 'a:codex', label: 'codex', kind: 'agent', agent: 'codex', sessionCount: 1, children: [
      { key: 'a:codex:/b', label: '/b', kind: 'directory', cwd: '/b', sessionCount: 1 },
    ] },
  ];
  assert.deepEqual(knownDirectories(groups), ['/a', '/b']);
  assert.deepEqual(knownDirectories([]), []);
});

test('directoryNodeKey prefers the directory under the matching agent', () => {
  const byAgent = [
    { key: 'a:claude', label: 'claude', kind: 'agent', agent: 'claude-code', sessionCount: 1, children: [
      { key: 'a:claude:/p', label: '/p', kind: 'directory', cwd: '/p', sessionCount: 1 },
    ] },
    { key: 'a:codex', label: 'codex', kind: 'agent', agent: 'codex', sessionCount: 1, children: [
      { key: 'a:codex:/p', label: '/p', kind: 'directory', cwd: '/p', sessionCount: 1 },
    ] },
  ];
  assert.equal(directoryNodeKey(byAgent, '/p', 'codex'), 'a:codex:/p');
  assert.equal(directoryNodeKey(byAgent, '/p', 'opencode'), 'a:claude:/p');
  assert.equal(directoryNodeKey(byAgent, '/missing', 'codex'), null);

  const byDir = [
    { key: 'd:/p', label: '/p', kind: 'directory', cwd: '/p', sessionCount: 2, children: [
      { key: 'd:/p:codex', label: 'codex', kind: 'agent', agent: 'codex', sessionCount: 1 },
    ] },
  ];
  assert.equal(directoryNodeKey(byDir, '/p', 'codex'), 'd:/p');
});

test('hashHost and targetKey provide deterministic namespaces per host', () => {
  assert.equal(targetKey(undefined), 'local');
  assert.equal(targetKey(''), 'local');
  assert.equal(targetKey('local'), 'local');

  const devHash = hashHost('dev-box');
  assert.equal(typeof devHash, 'string');
  assert.equal(devHash.length, 8);
  assert.equal(targetKey('dev-box'), devHash);

  const prodHash = hashHost('prod-server');
  assert.notEqual(devHash, prodHash);

  // Deterministic
  assert.equal(hashHost('dev-box'), devHash);
});

test('storageKeyForCollapsed namespaces keys and drops legacy toggles', () => {
  // Mock localStorage for node test environment
  const store = new Map();
  globalThis.localStorage = {
    getItem: (k) => store.get(k) ?? null,
    setItem: (k, v) => store.set(k, String(v)),
    removeItem: (k) => store.delete(k),
    clear: () => store.clear(),
  };

  try {
    // 1. Remote host uses hashed namespace
    const remoteKey = storageKeyForCollapsed('dev-box');
    assert.equal(remoteKey, `${BASE_COLLAPSED_STORAGE_KEY}:${hashHost('dev-box')}`);

    // 2. Toggles saved by earlier versions flip other defaults; they're dropped.
    const legacy = 'agent-sessions:tree-collapsed';
    store.set(legacy, '["a"]');
    store.set(`${legacy}:local`, '["b"]');
    store.set(`${legacy}:${hashHost('dev-box')}`, '["c"]');
    assert.equal(storageKeyForCollapsed(), `${BASE_COLLAPSED_STORAGE_KEY}:local`);
    assert.deepEqual([...store.keys()], [`${legacy}:${hashHost('dev-box')}`]);
    storageKeyForCollapsed('dev-box');
    assert.equal(store.size, 0);

    // 3. loadCollapsedKeys and saveCollapsedKeys per target host
    saveCollapsedKeys(new Set(['group:1', 'group:2']));
    saveCollapsedKeys(new Set(['remote:node']), 'dev-box');
    assert.deepEqual([...loadCollapsedKeys('dev-box')], ['remote:node']);
    assert.deepEqual([...loadCollapsedKeys()], ['group:1', 'group:2']); // Local untouched
  } finally {
    delete globalThis.localStorage;
  }
});

const GROUP_TREE_URL = new URL('../src/lib/components/sidebar/GroupTree.svelte', import.meta.url);

let groupTreeModule;

async function renderGroupTree(filter) {
  groupTreeModule ??= await loadServerComponent(GROUP_TREE_URL, [
    ["import { appState } from '../../stores/appState.svelte';", fixtureStore('appState')],
    ["import { discovery } from '../../stores/discovery.svelte';", "const discovery = { outcome: 'unavailable', status: null };"],
    ["'../../discovery'", JSON.stringify(new URL('../src/lib/discovery.ts', import.meta.url).href)],
    ["import { manage } from '../../stores/manage.svelte';", 'const manage = {};'],
    ["import { filterSessionsByAge, refsEqual } from '../../manage';", 'const filterSessionsByAge = () => [];const refsEqual = (a, b) => a === b;'],
    ["import { nodeToHighlight, sessionToSelect } from '../../tree';", 'const nodeToHighlight = () => null;const sessionToSelect = () => null;'],
    ["import GroupNodeItem from './GroupNodeItem.svelte';", 'const GroupNodeItem = () => {};'],
  ]);
  return renderWithFixture(groupTreeModule, {
    appState: { loadingGroups: false, groups: [], selectedSessionRef: null, filter: { query: '', path: '', liveOnly: false, archived: false, ...filter } },
  });
}

test('the empty tree offers to clear only the directory filter', async () => {
  const byPath = await renderGroupTree({ path: 'api', query: 'deploy' });
  assert.match(byPath, /No sessions match current filter/);
  assert.match(byPath, /Clear directory filter/);
  assert.doesNotMatch(byPath, /Clear filters/);

  // Without a directory filter the session list's own button clears the rest.
  const byQuery = await renderGroupTree({ query: 'deploy' });
  assert.match(byQuery, /No sessions match current filter/);
  assert.doesNotMatch(byQuery, /Clear directory filter|Clear filters/);
});
