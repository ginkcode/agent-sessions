/**
 * Replacing search matches in the browser the way the host does: for
 * unsaved drafts, the browser mock, and the preview in the Search pane.
 */
import type { ToastMessage } from './guidance';
import type { ReplaceMatch, ReplaceSkipReason, ReplaceStatus, SearchMatch } from './types';

/**
 * The replacement as typed, made ready to send. For a regular expression \n
 * and \t stand for a line break and a tab and \\ for a backslash, as in
 * editors; plain text is taken as it is.
 */
export function replacementTemplate(typed: string, regex: boolean): string {
  if (!regex) return typed;
  return typed.replace(/\\([\\nt])/g, (_, c: string) => (c === 'n' ? '\n' : c === 't' ? '\t' : '\\'));
}

const NAME = /^[\p{L}\p{Nd}_]+/u;

/**
 * Expands $1, ${1}, $name and ${name} in template for match m exactly as Go's
 * Regexp.Expand does on the host: $$ is a dollar sign, a name is the longest
 * run of letters, digits and underscores, a group that is missing or did not
 * take part is empty, and a $ that starts no name stays as it is.
 */
export function expandReplacement(template: string, m: RegExpMatchArray): string {
  let out = '';
  let rest = template;
  for (let at = rest.indexOf('$'); at >= 0; at = rest.indexOf('$')) {
    out += rest.slice(0, at);
    rest = rest.slice(at + 1);
    if (rest.startsWith('$')) {
      out += '$';
      rest = rest.slice(1);
      continue;
    }
    const brace = rest.startsWith('{');
    const body = brace ? rest.slice(1) : rest;
    const name = NAME.exec(body)?.[0];
    if (!name || (brace && body[name.length] !== '}')) {
      out += '$';
      continue;
    }
    rest = body.slice(name.length + (brace ? 1 : 0));
    out += group(m, name) ?? '';
  }
  return out + rest;
}

function group(m: RegExpMatchArray, name: string): string | undefined {
  // As in Go: ASCII digits without a leading zero are a group number.
  if (/^(0|[1-9]\d*)$/.test(name)) {
    const n = Number(name);
    return n < m.length ? m[n] : undefined;
  }
  return m.groups?.[name];
}

export interface TextReplacement {
  text: string;
  replaced: number;
  /** False when a chosen match is not in the text; text is then unchanged. */
  ok: boolean;
}

/**
 * Replaces matches of re (a global expression, as searchRegExp makes) in
 * text line by line, as the host does. With chosen, only the matches at
 * those lines and UTF-16 starts are replaced.
 */
export function replaceInText(text: string, re: RegExp, template: string, regex: boolean, chosen?: ReplaceMatch[]): TextReplacement {
  const want = chosen && new Set(chosen.map(c => `${c.line}:${c.start}`));
  let found = 0, replaced = 0;
  const lines = text.split('\n').map((raw, i) => {
    // A CR before the line break is not part of the line, as in Search.
    const cr = raw.endsWith('\r') ? '\r' : '';
    const line = cr ? raw.slice(0, -1) : raw;
    re.lastIndex = 0;
    let out = '', last = 0;
    for (const m of line.matchAll(re)) {
      if (m[0] === '') continue; // an empty match marks nothing, as in Search
      const at = m.index ?? 0;
      if (want) {
        if (!want.has(`${i + 1}:${at}`)) continue;
        found++;
      }
      out += line.slice(last, at) + (regex ? expandReplacement(template, m) : template);
      last = at + m[0].length;
      replaced++;
    }
    return out + line.slice(last) + cr;
  });
  if (want && found !== want.size) return { text, replaced: 0, ok: false };
  return { text: lines.join('\n'), replaced, ok: true };
}

/** A sticky copy of a search expression, for previewReplacement. */
export function stickyRegExp(re: RegExp): RegExp {
  return new RegExp(re.source, re.flags.replace('g', '') + 'y');
}

/**
 * What one match becomes, for the preview. The match is found again in its
 * preview; when that cannot tell (a lookbehind past the cut, say), the
 * template shows as it is.
 */
export function previewReplacement(m: SearchMatch, sticky: RegExp | null, template: string, regex: boolean): string {
  if (!regex || !sticky) return template;
  sticky.lastIndex = m.previewStart;
  const hit = sticky.exec(m.preview);
  if (!hit || hit[0].length !== m.previewEnd - m.previewStart) return template;
  return expandReplacement(template, hit);
}

export interface TextChange { from: number; to: number; insert: string }

/**
 * The edits that turn before into after, in before's UTF-16 offsets: one
 * per changed line when the lines are the same in number (as after a
 * replace, so the cursor stays where it was on other lines), else one for
 * the whole changed span. A change never splits a surrogate pair.
 */
export function textChanges(before: string, after: string): TextChange[] {
  if (before === after) return [];
  const a = before.split('\n'), b = after.split('\n');
  if (a.length !== b.length) {
    const [from, end, endAfter] = span(before, after);
    return [{ from, to: end, insert: after.slice(from, endAfter) }];
  }
  const out: TextChange[] = [];
  let offset = 0;
  for (let i = 0; i < a.length; offset += a[i].length + 1, i++) {
    if (a[i] === b[i]) continue;
    const [from, end, endAfter] = span(a[i], b[i]);
    out.push({ from: offset + from, to: offset + end, insert: b[i].slice(from, endAfter) });
  }
  return out;
}

/** Where a and b differ: [start, end in a, end in b]. */
function span(a: string, b: string): [number, number, number] {
  let start = 0;
  const max = Math.min(a.length, b.length);
  while (start < max && a.charCodeAt(start) === b.charCodeAt(start)) start++;
  let tail = 0;
  while (tail < max - start && a.charCodeAt(a.length - 1 - tail) === b.charCodeAt(b.length - 1 - tail)) tail++;
  // Keep surrogate pairs whole on both sides.
  if (start > 0 && (isLow(a.charCodeAt(start)) || isLow(b.charCodeAt(start)))) start--;
  if (tail > 0 && (isLow(a.charCodeAt(a.length - tail)) || isLow(b.charCodeAt(b.length - tail)))) tail--;
  return [start, a.length - tail, b.length - tail];
}

const isLow = (c: number) => c >= 0xdc00 && c <= 0xdfff;

/** What happened to one file; draft: replaced in the unsaved draft. */
export interface ReplaceOutcome {
  path: string;
  status: ReplaceStatus | 'draft';
  replaced?: number;
  reason?: ReplaceSkipReason | 'draft_unsearched';
  message?: string;
}

const REASONS: Record<string, string> = {
  protected: 'protected',
  linked: 'a link',
  read_only: 'read-only',
  too_large: 'too large',
  unsupported_text: 'not plain text',
  unsupported_newlines: 'mixed line endings',
  stale_match: 'matches changed',
  duplicate: 'listed twice',
  draft_unsearched: 'unsaved draft the browser cannot search with this pattern',
};

const plural = (n: number, one: string, many: string) => `${n.toLocaleString()} ${n === 1 ? one : many}`;
const MAX_NAMES = 5;

function names(paths: string[]): string {
  const shown = paths.slice(0, MAX_NAMES).join(', ');
  return paths.length > MAX_NAMES ? `${shown} and ${paths.length - MAX_NAMES} more` : shown;
}

/** The toast after a replace: how many changed, and every file left as it was by name. */
export function replaceSummary(outcomes: ReplaceOutcome[]): ToastMessage {
  const done = outcomes.filter(o => o.status === 'replaced' || o.status === 'draft');
  const n = done.reduce((sum, o) => sum + (o.replaced ?? 0), 0);
  const lines: string[] = [];
  const drafts = outcomes.filter(o => o.status === 'draft').map(o => o.path);
  if (drafts.length) lines.push(`Changed in unsaved ${drafts.length === 1 ? 'draft' : 'drafts'}, not yet saved: ${names(drafts)}.`);
  const conflicts = outcomes.filter(o => o.status === 'conflict').map(o => o.path);
  if (conflicts.length) lines.push(`Changed on disk since the search, left as they are: ${names(conflicts)}. The results show them as they are now.`);
  const skipped = new Map<string, string[]>();
  for (const o of outcomes.filter(o => o.status === 'skipped')) {
    const why = REASONS[o.reason ?? ''] ?? o.reason ?? 'cannot be changed';
    skipped.set(why, [...(skipped.get(why) ?? []), o.path]);
  }
  for (const [why, paths] of skipped) lines.push(`Skipped (${why}): ${names(paths)}.`);
  const failed = outcomes.filter(o => o.status === 'failed');
  if (failed.length) {
    lines.push(`Failed: ${failed.slice(0, MAX_NAMES).map(o => `${o.path} (${o.message || 'unknown error'})`).join(', ')}${failed.length > MAX_NAMES ? ` and ${failed.length - MAX_NAMES} more` : ''}.`);
  }
  const title = n > 0 ? `Replaced ${plural(n, 'match', 'matches')} in ${plural(done.length, 'file', 'files')}` : 'Nothing replaced';
  const left = outcomes.length - done.length;
  return { title, body: lines.join(' ') || (n > 0 ? 'Every file was saved.' : 'No matches were left to replace.'), tone: left > 0 ? 'error' : 'info' };
}
