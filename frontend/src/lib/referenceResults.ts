import type { CodeNavigationLocation } from './codeDiagnostics';
import type { SearchFile, SearchMatch, SearchResult } from './types';

/** References retain the server's full range, including multiline targets. */
export interface ReferenceMatch extends SearchMatch {
  location: CodeNavigationLocation;
  /** A failed read still leaves a navigable location, not a fabricated preview. */
  unavailable?: string;
}

export interface ReferenceSource { text?: string; revision?: string; draft?: boolean; error?: string }

/** Build the search pane's groups without searching the text again: the server
 * chose these locations. Reading a draft supplies previews, never new matches.
 */
export function referenceResults(locations: CodeNavigationLocation[], sources: ReadonlyMap<string, ReferenceSource>, truncated = false): SearchResult {
  const groups = new Map<string, SearchFile>();
  const lines = new Map<string, string[]>();
  for (const location of locations) {
    const source = sources.get(location.path);
    let file = groups.get(location.path);
    if (!file) {
      file = { path: location.path, revision: source?.revision ?? '', draft: source?.draft, matches: [] };
      groups.set(location.path, file);
    }
    const { start, end } = location.range;
    if (source?.text !== undefined && !lines.has(location.path)) lines.set(location.path, source.text.split(/\r\n?|\n/));
    const line = lines.get(location.path)?.[start.line];
    const valid = line !== undefined && start.character <= line.length;
    const left = valid ? Math.max(0, start.character - 100) : 0;
    const right = valid ? Math.min(line.length, start.character + 200) : 0;
    const match: ReferenceMatch = {
      location,
      line: start.line + 1, start: start.character,
      end: end.line === start.line ? end.character : (line?.length ?? start.character),
      preview: valid ? line.slice(left, right) : '',
      previewStart: valid ? start.character - left : 0,
      previewEnd: valid ? Math.min(right, end.line === start.line ? end.character : line.length) - left : 0,
      unavailable: !valid ? source?.error ?? 'Preview unavailable; open the location to read it.' : undefined,
    };
    file.matches.push(match);
  }
  return { files: [...groups.values()].sort((a, b) => a.path.localeCompare(b.path)), matches: locations.length, searched: groups.size, truncated };
}

export function isReferenceMatch(match: SearchMatch): match is ReferenceMatch {
  return 'location' in match;
}
