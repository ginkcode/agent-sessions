/**
 * Prints a message or Markdown file, or saves it as a PDF. The content
 * opens in an editor first, where its title and Markdown can be changed. The
 * editor and the print sheet live in App.svelte and register themselves here,
 * like the viewer.
 */
import type { FileRef, Part, SessionRef } from './types';
import { isImageFile } from './images';

export type PrintJob = {
  sessionRef: SessionRef;
  /** The title offered in the editor: the session's. */
  title: string;
  /** What Chat level shows of the message, translated when it is shown so. */
  parts: Part[];
} | {
  /** A standalone Markdown file needs no session or message attachments. */
  sessionRef?: never;
  title: string;
  markdown: string;
};

/** What is printed: the edited title and Markdown, and the message's images. */
export interface PrintDocument {
  sessionRef: SessionRef | null;
  title: string;
  /** File names label the PDF but aren't added as headings to its content. */
  showTitle?: boolean;
  markdown: string;
  /** Images placed in the Markdown as ![name](attachment:N), N from 1. */
  attachments: FileRef[];
}

let editor: ((job: PrintJob) => void) | null = null;
let printer: ((doc: PrintDocument) => void) | null = null;

/** Sets who edits a message before printing; null when nobody does. */
export function setPrintEditor(fn: ((job: PrintJob) => void) | null): void {
  editor = fn;
}

/** Sets who prints documents; null when nobody does. */
export function setPrinter(fn: ((doc: PrintDocument) => void) | null): void {
  printer = fn;
}

export function printMessage(job: PrintJob): void {
  editor?.(job);
}

export function printFile(path: string, markdown: string): void {
  const name = path.split(/[\\/]/).pop() || path;
  editor?.({ title: name, markdown });
}

export function printDocument(doc: PrintDocument): void {
  printer?.(doc);
}

export function fileLabel(file: FileRef): string {
  return file.name || file.path?.split(/[\\/]/).pop() || (isImageFile(file) ? 'Image' : 'Attachment');
}

const ATTACHMENT = /^!\[([^\]]*)\]\(attachment:(\d+)\)$/;

/**
 * The message as Markdown to edit: text as it is, images as
 * ![name](attachment:N) lines, other files by name and the error notice as
 * a quote.
 */
export function printMarkdown(parts: Part[]): { markdown: string; attachments: FileRef[] } {
  const blocks: string[] = [];
  const attachments: FileRef[] = [];
  for (const part of parts) {
    if (part.kind === 'text' && part.text?.trim()) {
      blocks.push(part.text.trim());
    } else if (part.kind === 'file' && part.file && isImageFile(part.file)) {
      attachments.push(part.file);
      blocks.push(`![${fileLabel(part.file).replace(/[[\]]/g, '')}](attachment:${attachments.length})`);
    } else if (part.kind === 'file' && part.file) {
      blocks.push(`📎 ${fileLabel(part.file)}`);
    } else if (part.kind === 'notice' && part.text) {
      blocks.push(`> ${part.text}`);
    }
  }
  return { markdown: blocks.join('\n\n'), attachments };
}

export type PrintBlock = { kind: 'markdown'; text: string } | { kind: 'image'; index: number; alt: string };

/**
 * The Markdown split at lines that hold only an attachment image, which the
 * Markdown renderer doesn't draw. Lines in fenced code stay text.
 */
export function printBlocks(markdown: string): PrintBlock[] {
  const blocks: PrintBlock[] = [];
  let text: string[] = [];
  let fenced = false;
  const flush = () => {
    const joined = text.join('\n');
    if (joined.trim()) blocks.push({ kind: 'markdown', text: joined });
    text = [];
  };
  for (const line of markdown.split(/\r?\n/)) {
    if (line.trimStart().startsWith('```')) fenced = !fenced;
    const match = fenced ? null : ATTACHMENT.exec(line.trim());
    if (match) {
      flush();
      blocks.push({ kind: 'image', index: Number(match[2]), alt: match[1] });
    } else {
      text.push(line);
    }
  }
  flush();
  return blocks;
}

/**
 * The document title while printing, which the dialog offers as the PDF's
 * file name: the title without characters file systems reject.
 */
export function printTitle(title: string): string {
  const clean = title
    .replace(/[\\/:*?"<>|\u0000-\u001f]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
    .slice(0, 100)
    .trim();
  return clean || 'Message';
}
