import type { FileContent, FileEditInfo, FileEditReason } from './types';

export const MAX_EDIT_BYTES = 500_000;

/** Remove only the original BOM, and normalize the supported disk newlines. */
export function editorText(content: FileContent): string {
  let text = content.text ?? '';
  if (content.edit?.bom && text.startsWith('\uFEFF')) text = text.slice(1);
  return text.replace(/\r\n/g, '\n');
}

/** Validate the editor snapshot and count disk bytes, including BOM/CRLF expansion. */
export function serializedEditSize(text: string, format: FileEditInfo): number {
  if (/[\r\0]/.test(text) || /[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/u.test(text)) {
    throw new Error('Unsupported edit text.');
  }
  if (!['lf', 'crlf', 'none'].includes(format.newline ?? '')) {
    throw new Error('Unsupported file newline format.');
  }
  const size = new TextEncoder().encode(text).length + (format.bom ? 3 : 0)
    + (format.newline === 'crlf' ? (text.match(/\n/g)?.length ?? 0) : 0);
  if (size > MAX_EDIT_BYTES) throw new Error('Edit text is too large.');
  return size;
}

export function canEditContent(content: FileContent | null): content is FileContent & { text: string; revision: string; edit: FileEditInfo } {
  return !!content && typeof content.text === 'string' && !!content.revision
    && content.edit?.eligible === true && !content.binary && !content.tooLarge && !content.image;
}

/** How a file that cannot be edited is labelled. */
export function readOnlyText(reason: FileEditReason | undefined): string {
  switch (reason) {
    case 'too_large': return 'Read-only: too large to edit';
    case 'unsupported_newlines': return 'Read-only: mixed line endings';
    case 'not_checked': return 'Read-only: editing not available';
    case 'protected': return 'Read-only: protected file';
    case 'linked': return 'Read-only: linked file';
    case 'read_only': return 'Read-only file';
    default: return 'Read-only';
  }
}
