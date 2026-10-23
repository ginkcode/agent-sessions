/** Hosts named "wsl:<distro>" are WSL distributions on this Windows computer. */
const WSL_PREFIX = 'wsl:';

/** The WSL distribution host names, or undefined for an SSH alias. */
export function wslDistro(host: string | undefined): string | undefined {
  if (!host?.startsWith(WSL_PREFIX)) return undefined;
  return host.slice(WSL_PREFIX.length) || undefined;
}

/** How a host is shown: "Ubuntu (WSL)" for a distribution, else the alias. */
export function hostLabel(host: string): string {
  const distro = wslDistro(host);
  return distro ? `${distro} (WSL)` : host;
}

const APP_TITLE = 'Agent Sessions';

/**
 * The window title for a window on host, or on this computer when host is
 * empty. The connection comes first so windows stay apart in a taskbar or
 * window switcher that cuts titles short.
 */
export function windowTitle(host: string | undefined): string {
  return `${host ? hostLabel(host) : 'Local'} — ${APP_TITLE}`;
}

let baseTitle = '';
let needsCount = 0;

/**
 * The window title while count agent tabs need the user: "(2) Local —
 * Agent Sessions".
 */
export function countedTitle(title: string, count: number): string {
  return count > 0 ? `(${count}) ${title}` : title;
}

/** Shows title on the desktop window and, in a browser, on the page. */
export function setWindowTitle(title: string): void {
  baseTitle = title;
  showTitle();
}

/** Counts the agent tabs that need the user in the window title. */
export function setTitleCount(count: number): void {
  if (count === needsCount) return;
  needsCount = count;
  if (baseTitle) showTitle();
}

function showTitle(): void {
  const title = countedTitle(baseTitle, needsCount);
  if (typeof document !== 'undefined') document.title = title;
  if (typeof window === 'undefined') return;
  try {
    (window as any).runtime?.WindowSetTitle?.(title);
  } catch {
    // The title is cosmetic; a missing runtime changes nothing else.
  }
}

/** The command that opens an interactive shell on host from this computer. */
export function shellCommand(host: string): string {
  const distro = wslDistro(host);
  return distro ? `wsl -d ${distro}` : `ssh ${host}`;
}
