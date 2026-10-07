import { api } from '../api';
import type { TranslateSettings, TranslateSettingsRequest } from '../types';
import { errorText } from '../manage';

export const DEFAULT_LANGUAGE = 'Vietnamese';

/** Translation settings, kept by the desktop app on this computer. */
export class TranslateStore {
  settings = $state<TranslateSettings | null>(null);
  error = $state<string | null>(null);
  saving = $state(false);
  testing = $state(false);
  /** The result of the last Test: the translated sample, or null. */
  testResult = $state<string | null>(null);
  testError = $state<string | null>(null);

  async init(): Promise<void> {
    try {
      this.settings = await api.translateSettings();
      this.error = null;
    } catch (err) {
      this.error = errorText(err, 'Failed to load translation settings');
    }
  }

  get configured(): boolean {
    return !!this.settings?.configured;
  }

  get language(): string {
    return this.settings?.language || DEFAULT_LANGUAGE;
  }

  async save(req: TranslateSettingsRequest): Promise<boolean> {
    this.saving = true;
    this.testResult = null;
    this.testError = null;
    try {
      this.settings = await api.setTranslateSettings(req);
      this.error = null;
      return true;
    } catch (err) {
      this.error = errorText(err, 'Failed to save translation settings');
      return false;
    } finally {
      this.saving = false;
    }
  }

  /** Translates a short sample with the saved settings. */
  async test(): Promise<void> {
    this.testing = true;
    this.testResult = null;
    this.testError = null;
    try {
      this.testResult = await api.translate('Hello');
    } catch (err) {
      this.testError = errorText(err, 'Translation failed');
    } finally {
      this.testing = false;
    }
  }

  clearTest(): void {
    this.testResult = null;
    this.testError = null;
  }
}

export const translateSettings = new TranslateStore();
