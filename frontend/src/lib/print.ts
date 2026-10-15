/**
 * Prints one message, or saves it as a PDF from the print dialog. The print
 * sheet lives in App.svelte and registers itself here, like the viewer.
 */
import type { Part, SessionRef } from './types';

export interface PrintJob {
  sessionRef: SessionRef;
  /** The session's title. */
  title: string;
  role: 'user' | 'assistant';
  model?: string;
  /** The message's time as the transcript shows it. */
  time: string;
  /** What Chat level shows of the message, translated when it is shown so. */
  parts: Part[];
}

let printer: ((job: PrintJob) => void) | null = null;

/** Sets who prints messages; null when nobody does. */
export function setPrinter(fn: ((job: PrintJob) => void) | null): void {
  printer = fn;
}

export function printMessage(job: PrintJob): void {
  printer?.(job);
}

/**
 * The document title while printing, which the dialog offers as the PDF's
 * file name: the session title and the role, without characters file
 * systems reject.
 */
export function printTitle(title: string, role: 'user' | 'assistant'): string {
  const clean = title
    .replace(/[\\/:*?"<>|\u0000-\u001f]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
    .slice(0, 80)
    .trim();
  const who = role === 'user' ? 'User message' : 'Assistant message';
  return clean ? `${clean} - ${who}` : who;
}
