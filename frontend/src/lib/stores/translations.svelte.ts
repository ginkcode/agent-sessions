import { api } from '../api';
import type { Message, SessionRef } from '../types';
import { errorText, refKey } from '../manage';

export interface Translation {
  status: 'loading' | 'done' | 'error';
  text: string;
  error: string;
  /** The user switched back to the original text. */
  showOriginal: boolean;
}

/** How many translations are kept; the oldest go first. */
export const MAX_TRANSLATIONS = 200;

/**
 * Identifies one message's translation into one language. A message without
 * an id falls back to its index in the transcript.
 */
export function translationKey(
  ref: SessionRef | null | undefined,
  message: Pick<Message, 'id'>,
  index: number,
  language: string,
): string {
  const session = ref ? refKey(ref) : '';
  const id = message.id ? `id:${message.id}` : `#${index}`;
  return `${session}|${id}|${language}`;
}

/** Message translations, kept in memory for this run only. */
export class TranslationsStore {
  entries = $state<Record<string, Translation>>({});
  private order: string[] = [];
  private seq = new Map<string, number>();
  private next = 0;

  get(key: string): Translation | undefined {
    return this.entries[key];
  }

  /** Translates text under key, replacing an earlier result. */
  async translate(key: string, text: string): Promise<void> {
    if (this.entries[key]?.status === 'loading') return;
    const seq = ++this.next;
    this.seq.set(key, seq);
    this.put(key, { status: 'loading', text: '', error: '', showOriginal: false });
    let result: Translation;
    try {
      result = { status: 'done', text: await api.translate(text), error: '', showOriginal: false };
    } catch (err) {
      result = { status: 'error', text: '', error: errorText(err, 'Translation failed'), showOriginal: false };
    }
    // Dropped meanwhile, or superseded.
    if (this.seq.get(key) !== seq || !this.entries[key]) return;
    this.entries[key] = result;
  }

  /** Switches between the translation and the original text. */
  toggleOriginal(key: string): void {
    const entry = this.entries[key];
    if (entry?.status === 'done') entry.showOriginal = !entry.showOriginal;
  }

  clear(): void {
    this.entries = {};
    this.order = [];
    this.seq.clear();
  }

  private put(key: string, entry: Translation): void {
    this.order = this.order.filter((k) => k !== key);
    this.order.push(key);
    this.entries[key] = entry;
    while (this.order.length > MAX_TRANSLATIONS) {
      const old = this.order.shift()!;
      delete this.entries[old];
      this.seq.delete(old);
    }
  }
}

export const translations = new TranslationsStore();
