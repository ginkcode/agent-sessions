import { isTranscriptMode, type TranscriptMode } from '../transcript';
import { isTimeZoneMode, type TimeZoneMode } from '../date';

const STORAGE_KEY = 'agent-sessions:pane-widths';
const TIME_ZONE_KEY = 'agent-sessions:time-zone';
// Separate key so the pane-widths JSON format stays unchanged.
const TRANSCRIPT_MODE_KEY = 'agent-sessions:transcript-mode';

export interface PaneWidths {
  sidebar: number;
  sessionList: number;
}

export const LIMITS = {
  sidebar: { min: 200, max: 450, default: 260 },
  sessionList: { min: 260, max: 550, default: 340 },
  transcriptMin: 380,
};

export class PreferencesStore {
  widths = $state<PaneWidths>({
    sidebar: LIMITS.sidebar.default,
    sessionList: LIMITS.sessionList.default,
  });
  // The level each session opens at; the header selector overrides it per session.
  transcriptMode = $state<TranscriptMode>('activity');
  // Zone for message and session timestamps.
  timeZone = $state<TimeZoneMode>('utc');

  init(): void {
    if (typeof localStorage === 'undefined') return;

    try {
      const saved = localStorage.getItem(STORAGE_KEY);
      if (saved) {
        const parsed = JSON.parse(saved);
        if (typeof parsed.sidebar === 'number' && typeof parsed.sessionList === 'number') {
          this.widths = {
            sidebar: this.clamp(parsed.sidebar, LIMITS.sidebar.min, LIMITS.sidebar.max),
            sessionList: this.clamp(parsed.sessionList, LIMITS.sessionList.min, LIMITS.sessionList.max),
          };
        }
      }
    } catch {
      // Ignore JSON parse errors and keep defaults
    }

    try {
      const mode = localStorage.getItem(TRANSCRIPT_MODE_KEY);
      if (isTranscriptMode(mode)) this.transcriptMode = mode;
    } catch {
      // Storage disabled; keep Activity
    }

    try {
      const zone = localStorage.getItem(TIME_ZONE_KEY);
      if (isTimeZoneMode(zone)) this.timeZone = zone;
    } catch {
      // Storage disabled; keep UTC
    }

    if (typeof window !== 'undefined') {
      window.addEventListener('resize', () => this.handleWindowResize());
      this.handleWindowResize();
    }
  }

  setSidebarWidth(width: number): void {
    this.widths.sidebar = this.clamp(width, LIMITS.sidebar.min, LIMITS.sidebar.max);
    this.ensureTranscriptVisible();
    this.save();
  }

  setSessionListWidth(width: number): void {
    this.widths.sessionList = this.clamp(width, LIMITS.sessionList.min, LIMITS.sessionList.max);
    this.ensureTranscriptVisible();
    this.save();
  }

  setTranscriptMode(mode: TranscriptMode): void {
    this.transcriptMode = mode;
    if (typeof localStorage === 'undefined') return;
    try {
      localStorage.setItem(TRANSCRIPT_MODE_KEY, mode);
    } catch {
      // Storage quota or disabled
    }
  }

  setTimeZone(zone: TimeZoneMode): void {
    this.timeZone = zone;
    if (typeof localStorage === 'undefined') return;
    try {
      localStorage.setItem(TIME_ZONE_KEY, zone);
    } catch {
      // Storage quota or disabled
    }
  }

  private handleWindowResize(): void {
    this.ensureTranscriptVisible();
  }

  private ensureTranscriptVisible(): void {
    if (typeof window === 'undefined') return;
    const windowWidth = window.innerWidth;
    const available = windowWidth - LIMITS.transcriptMin;

    if (available > 0 && this.widths.sidebar + this.widths.sessionList > available) {
      const ratio = this.widths.sidebar / (this.widths.sidebar + this.widths.sessionList);
      this.widths.sidebar = Math.max(LIMITS.sidebar.min, Math.round(available * ratio));
      this.widths.sessionList = Math.max(LIMITS.sessionList.min, available - this.widths.sidebar);
    }
  }

  private clamp(val: number, min: number, max: number): number {
    return Math.max(min, Math.min(max, val));
  }

  private save(): void {
    if (typeof localStorage === 'undefined') return;
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(this.widths));
    } catch {
      // Storage quota or disabled
    }
  }
}

export const preferences = new PreferencesStore();
