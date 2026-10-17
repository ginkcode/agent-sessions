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
