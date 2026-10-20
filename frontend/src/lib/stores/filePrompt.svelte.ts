export interface FilePromptChoice {
  value: string;
  label: string;
  danger?: boolean;
}

/** One coordinated decision at a time; overlapping destructive actions cancel. */
export class FilePromptStore {
  request = $state<{ title: string; message: string; choices: FilePromptChoice[] } | null>(null);
  private resolve: ((value: string | null) => void) | null = null;

  ask(title: string, message: string, choices: FilePromptChoice[]): Promise<string | null> {
    if (this.resolve) return Promise.resolve(null);
    this.request = { title, message, choices };
    return new Promise((resolve) => { this.resolve = resolve; });
  }

  answer(value: string | null): void {
    const resolve = this.resolve;
    this.resolve = null;
    this.request = null;
    resolve?.(value);
  }
}

export const filePrompt = new FilePromptStore();
