export interface FileLineSelection {
  anchor: number;
  head: number;
}

export function selectFileLine(current: FileLineSelection | undefined, line: number, extend: boolean): FileLineSelection {
  return { anchor: extend && current ? current.anchor : line, head: line };
}

export function includesFileLine(selection: FileLineSelection | undefined, line: number): boolean {
  return !!selection && line >= Math.min(selection.anchor, selection.head) && line <= Math.max(selection.anchor, selection.head);
}
