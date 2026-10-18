// Imports the vscode-icons file and folder icons for the Go to File dialog.
//
//   node scripts/file-icons.mjs
//
// Downloads the pinned vscode-icons release from Open VSX, copies its SVG
// icons unchanged to public/file-icons/ and writes the table that picks an
// icon for a file or folder name to src/lib/fileIcons.json. The table follows
// the extension's own icon theme: file names, then extensions, then the
// language VS Code would give the file (from the extension's language list).
// Needs `unzip`.
//
// The icons are CC BY-SA 4.0 (see public/file-icons/LICENSE.md).
import { execFileSync } from 'node:child_process';
import { cpSync, mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { basename, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const VERSION = '13.0.0';
const VSIX = `https://open-vsx.org/api/vscode-icons-team/vscode-icons/${VERSION}/file/vscode-icons-team.vscode-icons-${VERSION}.vsix`;
const LANGUAGES = `https://raw.githubusercontent.com/vscode-icons/vscode-icons/v${VERSION}/src/iconsManifest/languages.ts`;

const root = fileURLToPath(new URL('..', import.meta.url));
const iconsOut = join(root, 'public', 'file-icons');
const tableOut = join(root, 'src', 'lib', 'fileIcons.json');

async function download(url) {
  const res = await fetch(url);
  if (!res.ok) throw new Error(`${url}: ${res.status}`);
  return Buffer.from(await res.arrayBuffer());
}

const work = mkdtempSync(join(tmpdir(), 'file-icons-'));
try {
  writeFileSync(join(work, 'x.vsix'), await download(VSIX));
  execFileSync('unzip', ['-q', join(work, 'x.vsix'), 'extension/*', '-d', work]);
  const ext = join(work, 'extension');
  const theme = JSON.parse(readFileSync(join(ext, 'dist', 'src', 'vsicons-icon-theme.json'), 'utf8'));
  const languages = parseLanguages((await download(LANGUAGES)).toString('utf8'));

  // Icon definition id (_f_go) -> SVG name without .svg (file_type_go).
  const svg = new Map();
  for (const [id, def] of Object.entries(theme.iconDefinitions)) {
    if (def.iconPath) svg.set(id, basename(def.iconPath, '.svg'));
  }
  const used = new Set();
  const icon = (id) => {
    const name = svg.get(id);
    if (name && existsSync(join(ext, 'icons', `${name}.svg`))) {
      used.add(name);
      return name;
    }
    return undefined;
  };

  const build = (t, base) => {
    const out = { fileNames: {}, fileExtensions: {}, folderNames: {}, folderNamesExpanded: {} };
    // A language's icon applies to its known extensions and file names,
    // below the theme's own entries.
    for (const [langId, id] of Object.entries(t.languageIds ?? {})) {
      const name = icon(id);
      const lang = languages.get(langId);
      if (!name || !lang) continue;
      for (const e of lang.extensions) out.fileExtensions[e.toLowerCase()] ??= name;
      for (const f of lang.filenames) out.fileNames[f.toLowerCase()] ??= name;
    }
    for (const key of ['fileNames', 'fileExtensions', 'folderNames', 'folderNamesExpanded']) {
      for (const [n, id] of Object.entries(t[key] ?? {})) {
        const name = icon(id);
        if (name) out[key][n.toLowerCase()] = name;
      }
    }
    for (const key of ['file', 'folder', 'folderExpanded']) {
      const name = t[key] && icon(t[key]);
      if (name) out[key] = name;
    }
    if (!base) return out;
    // The light table keeps only what differs from the dark one.
    const diff = {};
    for (const [key, value] of Object.entries(out)) {
      if (typeof value === 'string') {
        if (value !== base[key]) diff[key] = value;
        continue;
      }
      const d = Object.fromEntries(Object.entries(value).filter(([n, v]) => base[key][n] !== v));
      if (Object.keys(d).length) diff[key] = d;
    }
    return diff;
  };

  const dark = build(theme);
  const light = build({ ...theme, ...mergeLight(theme) }, dark);
  writeFileSync(tableOut, JSON.stringify({ version: VERSION, ...dark, light }) + '\n');

  rmSync(iconsOut, { recursive: true, force: true });
  mkdirSync(iconsOut, { recursive: true });
  for (const name of used) cpSync(join(ext, 'icons', `${name}.svg`), join(iconsOut, `${name}.svg`));
  writeFileSync(join(iconsOut, 'LICENSE.md'), licence(used.size));
  cpSync(join(ext, 'LICENSE.txt'), join(iconsOut, 'UPSTREAM-LICENSE.txt'));
  console.log(`vscode-icons ${VERSION}: ${used.size} icons, table ${tableOut}`);
} finally {
  rmSync(work, { recursive: true, force: true });
}

// The light theme lists every entry again, light icon or not; entries it
// leaves out keep the dark icon.
function mergeLight(theme) {
  const merged = {};
  for (const [key, value] of Object.entries(theme.light ?? {})) {
    merged[key] = typeof value === 'object' ? { ...theme[key], ...value } : value || theme[key];
  }
  return merged;
}

// Reads languages.ts: { name: { ids, knownExtensions, knownFilenames } }.
function parseLanguages(source) {
  const body = source
    .replace(/^import[^\n]*\n/gm, '')
    .replace(/export const languages =/, 'return')
    .replace(/\}\s*satisfies[^;]*;/, '};');
  const table = new Function(body)();
  const out = new Map();
  for (const lang of Object.values(table)) {
    const ids = Array.isArray(lang.ids) ? lang.ids : [lang.ids];
    for (const id of ids) {
      const cur = out.get(id) ?? { extensions: [], filenames: [] };
      cur.extensions.push(...(lang.knownExtensions ?? []));
      cur.filenames.push(...(lang.knownFilenames ?? []));
      out.set(id, cur);
    }
  }
  return out;
}

function licence(count) {
  return `# File icons

These ${count} SVG icons are from [vscode-icons](https://github.com/vscode-icons/vscode-icons)
${VERSION}, copied unchanged by \`frontend/scripts/file-icons.mjs\`.

Copyright the vscode-icons contributors. The icons are licensed under the
[Creative Commons Attribution-ShareAlike 4.0 International](https://creativecommons.org/licenses/by-sa/4.0/)
licence (CC BY-SA 4.0). Branded icons are licensed under their owners' copyright
licence. vscode-icons' source code is MIT licensed.
`;
}
