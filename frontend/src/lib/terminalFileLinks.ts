import type { ILink, IMarker, Terminal } from '@xterm/xterm';

const MAX_TEXT = 8192;
const MAX_ROWS = 32;
const MAX_LINKS = 128;

export interface TerminalFileCandidate {
  path: string;
  /** UTF-16 offsets in the supplied text; end is exclusive. */
  start: number;
  end: number;
}

/** OSC 7 carries a file URI, not an arbitrary path or a navigation URL.
 * The authority is the shell's hostname (including remote POSIX hosts).
 */
export function parseOsc7Cwd(data: string): string | undefined {
  if (data.length > MAX_TEXT || !/^file:\/\//i.test(data) || /[\x00-\x20\x7f]/.test(data)) return;
  try {
    const url = new URL(data);
    if (url.protocol !== 'file:' || url.username || url.password || url.port || url.search || url.hash) return;
    let path = decodeURIComponent(url.pathname);
    if (!path.startsWith('/') || /[\x00-\x1f\x7f]/.test(path)) return;
    // A drive URI is absolute on Windows, but its URI spelling has a leading slash.
    if (/^\/[a-z]:\//i.test(path)) path = path.slice(1);
    if (!path || /^\/[a-z]:[^/]/i.test(path)) return;
    return path;
  } catch {
    return;
  }
}

/** Candidates only: existence and file-vs-directory checks belong to click handling. */
export function extractTerminalFileCandidates(text: string): TerminalFileCandidate[] {
  if (text.length > MAX_TEXT) return [];
  const results: TerminalFileCandidate[] = [];
  const add = (raw: string, start: number, git = false) => {
    let value = raw;
    if ((value[0] === '"' || value[0] === "'") && value.at(-1) === value[0]) {
      start++;
      value = value.slice(1, -1);
    } else {
      const leading = /^[([{]+/.exec(value)?.[0].length ?? 0;
      start += leading;
      value = value.slice(leading).replace(/[),;\]}]+$/, '');
      value = value.replace(/\.(?=$)/, (dot) => /\.[\w-]+\.$/.test(value) ? '' : dot);
    }
    value = value.replace(/:\d+(?::\d+)?[:]?$/, '');
    if (!value || value.length > 4096 || /[\x00-\x1f\x7f]/.test(value) || /^[-#]/.test(value)) return;
    // Leave web links to WebLinksAddon; never turn a portion of a URI into a file.
    if (/^[a-z][a-z\d+.-]*:/i.test(value) && !/^[a-z]:[\\/]/i.test(value)) return;
    if (value.includes('://') || (!git && /[<>|*?]/.test(value)) || /^\d+(?:\.\d+)+$/.test(value)) return;
    if (!git && !(/[\\/]/.test(value) || /^\.[\w-]+/.test(value) || /\.[a-z\d][\w-]*$/i.test(value)
      || /^(?:[A-Z][\w-]*file|[A-Z][A-Z\d_-]{2,})(?:\.[\w-]+)?$/.test(value))) return;
    if (/^[.\\/]+$/.test(value)) return;
    results.push({ path: value, start, end: start + value.length });
  };

  // Git's human and porcelain status formats give us stronger evidence than prose.
  // The rest of the row is one path, so unquoted spaces are meaningful here.
  const status = /^(?:\s*(?:modified|new file|deleted|renamed|copied|both modified|both added|both deleted|added by us|added to them?):\s+|(?=[ MADRCU?!]{0,1}[MADRCU?!])(?=[ MADRCU?!]{2}\s)[ MADRCU?!]{2}\s+)(.+?)\s*$/.exec(text);
  if (status) {
    let raw = status[1];
    let start = text.trimEnd().length - raw.length;
    // Do not mistake an arrow inside a quoted filename for a rename separator.
    let quote = '';
    let arrow = -1;
    for (let i = 0; i < raw.length; i++) {
      if ((raw[i] === '"' || raw[i] === "'") && raw[i - 1] !== '\\') quote = quote === raw[i] ? '' : quote || raw[i];
      if (!quote && raw.slice(i, i + 4) === ' -> ') arrow = i;
    }
    if (arrow >= 0) {
      start += arrow + 4;
      raw = raw.slice(arrow + 4);
    }
    add(raw, start, true);
    return results;
  }

  // Quoted paths can include spaces. Unquoted whitespace separates candidates.
  const tokens = /"[^"\r\n]+"(?::\d+(?::\d+)?)?|'[^'\r\n]+'(?::\d+(?::\d+)?)?|[^\s"']+/g;
  for (const match of text.matchAll(tokens)) {
    if (results.length >= MAX_LINKS) break;
    // Strip location suffix before unquoting ("some file.ts":12).
    const raw = match[0].replace(/(:\d+(?::\d+)?)$/, '');
    add(raw, match.index!);
  }
  return results;
}

/** Install immediately after constructing xterm, before replaying any output. */
export function installTerminalFileLinks(
  terminal: Terminal,
  options: { isMac: boolean; cwd?: string; onCwd?: (cwd: string) => void; onOpen: (path: string, cwd: string | undefined) => void },
): { dispose(): void } {
  let cwd: string | undefined;
  // xterm markers follow rows through scrollback trimming and terminal reflow.
  // Keep a baseline for output preceding the first remaining directory marker.
  let baseline = options.cwd;
  const directories: { marker: IMarker; cwd: string }[] = [];
  const pruneDirectories = () => {
    while (directories.length && directories[0].marker.isDisposed) {
      baseline = directories.shift()!.cwd;
    }
  };
  const directoryAt = (line: number): string | undefined => {
    pruneDirectories();
    if (terminal.buffer.active.type === 'alternate') return cwd ?? options.cwd;
    let result = baseline;
    for (const entry of directories) {
      if (!entry.marker.isDisposed && entry.marker.line <= line) result = entry.cwd;
    }
    return result;
  };
  let disposed = false;
  let hovered: ILink | undefined;
  let titleElement: HTMLElement | undefined;
  let originalTitle = '';
  let hint = '';
  const restoreTitle = () => {
    if (titleElement && titleElement.title === hint) titleElement.title = originalTitle;
    titleElement = undefined;
    hovered = undefined;
  };
  const osc = terminal.parser.registerOscHandler(7, (data) => {
    const parsed = parseOsc7Cwd(data);
    if (parsed !== undefined) {
      cwd = parsed;
      options.onCwd?.(parsed);
      pruneDirectories();
      if (terminal.buffer.active.type !== 'alternate') {
        const previous = directories.at(-1);
        if (!previous || previous.cwd !== parsed) {
          const marker = terminal.registerMarker(0);
          if (marker) {
            if (previous?.marker.line === marker.line) directories.pop()!.marker.dispose();
            directories.push({ marker, cwd: parsed });
          }
        }
      }
    }
    return true;
  });
  const provider = terminal.registerLinkProvider({
    provideLinks(row, callback) {
      if (disposed) return callback(undefined);
      const buffer = terminal.buffer.active;
      let first = row - 1;
      let last = first;
      if (!buffer.getLine(first)) return callback(undefined);
      while (first > 0 && buffer.getLine(first)?.isWrapped) {
        if (last - first + 1 >= MAX_ROWS) return callback(undefined);
        first--;
      }
      while (buffer.getLine(last + 1)?.isWrapped) {
        if (last - first + 1 >= MAX_ROWS) return callback(undefined);
        last++;
      }
      let text = '';
      const starts: { x: number; y: number }[] = [];
      const ends: { x: number; y: number }[] = [];
      for (let y = first; y <= last; y++) {
        const line = buffer.getLine(y)!;
        for (let x = 0; x < line.length; x++) {
          const cell = line.getCell(x);
          if (!cell || cell.getWidth() === 0) continue;
          // xterm inserts an empty last cell when a wide glyph wraps.
          if (y < last && x === line.length - 1 && !cell.getChars()
            && buffer.getLine(y + 1)?.getCell(0)?.getWidth() === 2) continue;
          const chars = cell.getChars() || ' ';
          text += chars;
          if (text.length > MAX_TEXT) return callback(undefined);
          for (let i = 0; i < chars.length; i++) {
            starts.push({ x: x + 1, y: y + 1 });
            ends.push({ x: x + cell.getWidth(), y: y + 1 });
          }
        }
      }
      const links: ILink[] = extractTerminalFileCandidates(text.trimEnd()).flatMap((candidate) => {
        const start = starts[candidate.start];
        const end = ends[candidate.end - 1];
        if (!start || !end || start.y > row || end.y < row) return [];
        const link: ILink = {
          range: { start, end },
          text: candidate.path,
          activate(event) {
            if (!disposed && (options.isMac ? event.metaKey : event.ctrlKey)) {
              event.preventDefault();
              options.onOpen(candidate.path, directoryAt(start.y - 1));
            }
          },
          hover() {
            restoreTitle();
            hovered = link;
            titleElement = terminal.element;
            if (titleElement) {
              originalTitle = titleElement.title;
              hint = `${options.isMac ? '⌘' : 'Ctrl'}+click to open file`;
              titleElement.title = hint;
            }
          },
          leave() { if (hovered === link) restoreTitle(); },
          dispose() { if (hovered === link) restoreTitle(); },
        };
        return [link];
      });
      callback(links);
    },
  });
  return {
    dispose() {
      if (disposed) return;
      disposed = true;
      restoreTitle();
      provider.dispose();
      osc.dispose();
      for (const entry of directories) entry.marker.dispose();
      directories.length = 0;
    },
  };
}
