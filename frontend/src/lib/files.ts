import type { GitStatus, LineChange } from './types';

/**
 * Helpers for the Go to File dialog: matching typed text against file paths,
 * building the project tree, and deciding which files can be shown.
 */

/** A folder or file in the project tree. */
export interface FileTreeNode {
  name: string;
  /** Slash-separated path from the project directory. */
  path: string;
  /** Folders only, sorted folders first, then by name. */
  children?: FileTreeNode[];
}

/** Builds the folder tree from a list of file paths. */
export function buildFileTree(paths: string[]): FileTreeNode[] {
  const root: FileTreeNode = { name: '', path: '', children: [] };
  const folders = new Map<string, FileTreeNode>([['', root]]);
  for (const path of paths) {
    const parts = path.split('/');
    let parent = root;
    for (let i = 0; i < parts.length - 1; i++) {
      const dir = parts.slice(0, i + 1).join('/');
      let folder = folders.get(dir);
      if (!folder) {
        folder = { name: parts[i], path: dir, children: [] };
        folders.set(dir, folder);
        parent.children!.push(folder);
      }
      parent = folder;
    }
    parent.children!.push({ name: parts[parts.length - 1], path });
  }
  const sort = (nodes: FileTreeNode[]) => {
    nodes.sort((a, b) => Number(!a.children) - Number(!b.children) || a.name.localeCompare(b.name));
    for (const n of nodes) if (n.children) sort(n.children);
  };
  sort(root.children!);
  return root.children!;
}

/**
 * Scores how well query matches path, or returns -1 when it does not. Every
 * query character must appear in order (spaces ignored). Matches in the file
 * name, at word starts and in a row score higher.
 */
export function matchScore(path: string, query: string): number {
  const q = query.toLowerCase().replace(/\s+/g, '');
  if (!q) return 0;
  const p = path.toLowerCase();
  const nameStart = p.lastIndexOf('/') + 1;
  let score = 0;
  let pi = 0;
  let prev = -2;
  for (const ch of q) {
    const at = p.indexOf(ch, pi);
    if (at < 0) return -1;
    score += 1;
    if (at === prev + 1) score += 3;
    if (at >= nameStart) score += 2;
    if (at === 0 || '/._-'.includes(p[at - 1])) score += 2;
    prev = at;
    pi = at + 1;
  }
  // Shorter paths win ties, so `app` prefers app.ts over src/lib/apps/x.ts.
  return score * 1000 - path.length;
}

/** The paths matching query, best first, at most limit of them. */
export function filterFiles(paths: string[], query: string, limit = 200): string[] {
  if (!query.trim()) return paths.slice(0, limit);
  const scored: { path: string; score: number }[] = [];
  for (const path of paths) {
    const score = matchScore(path, query);
    if (score >= 0) scored.push({ path, score });
  }
  scored.sort((a, b) => b.score - a.score || a.path.localeCompare(b.path));
  return scored.slice(0, limit).map((s) => s.path);
}

/**
 * The search results over the listed files and the ignored ones (such as
 * .env), best match first. An ignored file ranks below a listed one that
 * matches as well.
 */
export function searchFiles(paths: string[], ignored: string[] | undefined, query: string, limit = 200): string[] {
  if (!ignored?.length) return filterFiles(paths, query, limit);
  if (!query.trim()) return paths.slice(0, limit);
  const scored: { path: string; score: number; ignored: boolean }[] = [];
  for (const [list, isIgnored] of [
    [paths, false],
    [ignored, true],
  ] as const) {
    for (const path of list) {
      const score = matchScore(path, query);
      if (score >= 0) scored.push({ path, score, ignored: isIgnored });
    }
  }
  scored.sort(
    (a, b) => b.score - a.score || Number(a.ignored) - Number(b.ignored) || a.path.localeCompare(b.path)
  );
  return scored.slice(0, limit).map((s) => s.path);
}

// Extensions of source and text files, mapped to a highlight.js language
// ('' shows plain text). The languages are those of highlight.js's common
// bundle; .svelte, .vue and .html go to xml, which highlights their script
// and style blocks too.
const LANGUAGES: Record<string, string> = {
  go: 'go', ts: 'typescript', tsx: 'typescript', mts: 'typescript', cts: 'typescript',
  js: 'javascript', jsx: 'javascript', mjs: 'javascript', cjs: 'javascript',
  svelte: 'xml', vue: 'xml', html: 'xml', htm: 'xml', xml: 'xml', svg: 'xml', plist: 'xml',
  py: 'python', pyi: 'python', sh: 'bash', bash: 'bash', zsh: 'bash', fish: 'bash',
  sql: 'sql', json: 'json', jsonc: 'json', json5: 'json', diff: 'diff', patch: 'diff',
  rs: 'rust', c: 'c', h: 'c', cc: 'cpp', cpp: 'cpp', cxx: 'cpp', hpp: 'cpp', hh: 'cpp',
  m: 'objectivec', mm: 'objectivec', java: 'java', kt: 'kotlin', kts: 'kotlin', swift: 'swift',
  cs: 'csharp', vb: 'vbnet', rb: 'ruby', php: 'php', pl: 'perl', pm: 'perl', lua: 'lua', r: 'r',
  css: 'css', scss: 'scss', less: 'less', graphql: 'graphql', gql: 'graphql',
  yaml: 'yaml', yml: 'yaml', toml: 'ini', ini: 'ini', cfg: 'ini', conf: 'ini', properties: 'ini',
  md: 'markdown', mdx: 'markdown', markdown: 'markdown', mk: 'makefile', wat: 'wasm',
  scala: '', dart: '', ex: '', exs: '', erl: '', hs: '', clj: '', zig: '', nim: '', jl: '', groovy: '',
  gradle: '', proto: '', rst: '', txt: '', adoc: '', tex: '', csv: '', tsv: '', env: '', mod: '', sum: '',
  lock: '', gitignore: '', gitattributes: '', dockerignore: '', editorconfig: '', ps1: '', bat: '',
  cmd: '', cmake: '', tf: '', hcl: '', nix: '', vim: '', el: '', log: '', fs: '',
};

// Files known by their whole name.
const NAMED: Record<string, string> = {
  makefile: 'makefile', gnumakefile: 'makefile', dockerfile: '', containerfile: '', license: '',
  readme: '', changelog: '', gemfile: 'ruby', rakefile: 'ruby', procfile: '', justfile: '',
  vagrantfile: 'ruby', brewfile: 'ruby', 'go.mod': '', 'go.sum': '',
  '.bashrc': 'bash', '.zshrc': 'bash', '.profile': 'bash', '.envrc': 'bash',
};

/**
 * The highlighter language for a file ('' for plain text), or null when the
 * viewer does not support its type.
 */
export function fileLanguage(path: string): string | null {
  const name = path.slice(path.lastIndexOf('/') + 1).toLowerCase();
  if (name in NAMED) return NAMED[name];
  const dot = name.lastIndexOf('.');
  const ext = dot >= 0 ? name.slice(dot + 1) : '';
  if (ext in LANGUAGES) return LANGUAGES[ext];
  // .env.local, Dockerfile.dev and the like.
  if (name.startsWith('.env') || name.startsWith('dockerfile')) return '';
  return null;
}

/** The folders holding path, outermost first: a/b/c.ts gives a, a/b. */
export function parentFolders(path: string): string[] {
  const parts = path.split('/');
  return parts.slice(0, -1).map((_, i) => parts.slice(0, i + 1).join('/'));
}

/**
 * The files to list: the ones on disk, and the deleted ones git still knows
 * about, so the tree can show them struck through.
 */
export function withDeleted(paths: string[], status: Record<string, GitStatus> | undefined): string[] {
  if (!status) return paths;
  const deleted = Object.keys(status).filter((p) => status[p] === 'D' && !paths.includes(p));
  return deleted.length ? [...paths, ...deleted].sort() : paths;
}

// Which letter wins when a folder holds several kinds of change.
const STATUS_RANK: Record<GitStatus, number> = { C: 5, M: 4, D: 3, R: 2, A: 1, U: 0 };

/**
 * The status shown on each folder that holds changed files: the most
 * important of its files' statuses, as VS Code tints folders.
 */
export function folderStatus(status: Record<string, GitStatus> | undefined): Map<string, GitStatus> {
  const out = new Map<string, GitStatus>();
  if (!status) return out;
  for (const [path, st] of Object.entries(status)) {
    for (const dir of parentFolders(path)) {
      const cur = out.get(dir);
      if (!cur || STATUS_RANK[st] > STATUS_RANK[cur]) out.set(dir, st);
    }
  }
  return out;
}

/** How each git status is named in tooltips. */
export const STATUS_LABELS: Record<GitStatus, string> = {
  M: 'Modified',
  A: 'Added',
  U: 'Untracked',
  D: 'Deleted',
  R: 'Renamed',
  C: 'Conflict',
};

/** The gutter mark of one line. */
export type LineMark = 'added' | 'modified' | 'deleted-above' | 'deleted-below' | undefined;

/**
 * The gutter mark for each line (index 0 is line 1). Added and modified
 * lines get a bar; where lines were removed, the line below the gap gets a
 * mark at its top, or the last line one at its bottom when the gap is at
 * the end.
 */
export function lineMarks(changes: LineChange[] | undefined, lineCount: number): LineMark[] {
  const marks: LineMark[] = new Array(lineCount).fill(undefined);
  for (const c of changes ?? []) {
    if (c.kind === 'deleted') {
      if (lineCount === 0) continue;
      if (c.start < lineCount) marks[c.start] ??= 'deleted-above';
      else marks[lineCount - 1] ??= 'deleted-below';
      continue;
    }
    for (let i = c.start - 1; i < c.start - 1 + c.count && i < lineCount; i++) {
      if (i >= 0) marks[i] = c.kind;
    }
  }
  return marks;
}

/** One row of the diff view: a line of the file, or one the change removed. */
export type DiffRow =
  | { kind: 'line'; index: number; mark: LineMark }
  | { kind: 'removed'; text: string };

/**
 * The rows of the inline diff view, as VS Code shows one: the file's lines,
 * with the lines each change removed (as at the last commit) just above the
 * lines that replaced them, or where they were for a pure removal.
 */
export function diffRows(changes: LineChange[] | undefined, lineCount: number): DiffRow[] {
  const marks = lineMarks(changes, lineCount);
  // Removed lines by the index of the line they go above (lineCount: the end).
  const above = new Map<number, string[]>();
  for (const c of changes ?? []) {
    if (!c.removed?.length) continue;
    // A removal sits after line `start`; a replacement above its first line.
    const at = Math.min(c.kind === 'deleted' ? c.start : c.start - 1, lineCount);
    above.set(at, [...(above.get(at) ?? []), ...c.removed]);
  }
  const rows: DiffRow[] = [];
  for (let i = 0; i <= lineCount; i++) {
    for (const text of above.get(i) ?? []) rows.push({ kind: 'removed', text });
    if (i < lineCount) {
      // Removal marks only point at removed lines, which the view now shows.
      const mark = marks[i] === 'added' || marks[i] === 'modified' ? marks[i] : undefined;
      rows.push({ kind: 'line', index: i, mark });
    }
  }
  return rows;
}

/**
 * The changes to show for a file: git's, or for a file git does not track
 * yet, the whole file as added.
 */
export function fileChanges(
  status: GitStatus | undefined,
  changes: LineChange[] | undefined,
  lineCount: number
): LineChange[] | undefined {
  if (status === 'U' && !changes?.length && lineCount > 0) return [{ kind: 'added', start: 1, count: lineCount }];
  return changes;
}
