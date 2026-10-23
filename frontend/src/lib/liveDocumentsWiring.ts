import { api } from './api';
import { LiveDocumentBridge } from './liveDocuments';
import { files } from './stores/files.svelte';
import { codeDiagnostics } from './stores/codeDiagnostics.svelte';

/**
 * Connects the files store and the diagnostics store through the bridge. Without a
 * live-capable attachment (liveCodeDiagnostics is advertised only by the desktop
 * local route, experimental, Go only)
 * every hook is a no-op and no document call is made.
 */
let focusRefresh: Promise<void> | undefined;
/** At most one read-only reconciliation at a time; nothing is saved or replayed. */
export function refreshOnFocus(): void {
  if (focusRefresh || !codeDiagnostics.liveAttachment || codeDiagnostics.pending) return;
  focusRefresh = files.reconcileDisplayed().catch(() => {}).finally(() => { focusRefresh = undefined; });
}

export function startLiveDocuments(): LiveDocumentBridge {
  const bridge = new LiveDocumentBridge({
    api,
    store: codeDiagnostics,
    source: () => ({ dir: files.dir, documents: files.displayedDocuments() }),
  });
  files.setPathInvalidator((dir, path) => codeDiagnostics.invalidatePath(dir, path));
  files.setExternalReader(path => codeDiagnostics.readNavigationFile(path));
  files.setDiagnosticPaths(() => codeDiagnostics.directory === files.dir ? codeDiagnostics.resultSets.flatMap(s => s.diagnostics.map(d => d.path)) : []);
  codeDiagnostics.setReconciler(() => files.reconcileDisplayed());
  codeDiagnostics.setLiveBasis(path => bridge.basis(path));
  codeDiagnostics.setLiveRecovery(() => bridge.statuses, (path, id) => bridge.reopen(path, id));
  bridge.onChange(() => codeDiagnostics.liveChanged());
  files.observe(bridge);
  codeDiagnostics.onLifecycle(() => bridge.sync());
  // Language servers see only the app's document text, so a fix made in another
  // editor or by an agent stays reported until the open tabs are re-read. Like
  // editors, re-read them when the window regains focus while live checks run.
  if (typeof window !== 'undefined') window.addEventListener('focus', refreshOnFocus);
  return bridge;
}
