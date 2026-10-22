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
export function startLiveDocuments(): LiveDocumentBridge {
  const bridge = new LiveDocumentBridge({
    api,
    store: codeDiagnostics,
    source: () => ({ dir: files.dir, documents: files.displayedDocuments() }),
  });
  files.setPathInvalidator((dir, path) => codeDiagnostics.invalidatePath(dir, path));
  files.setDiagnosticPaths(() => codeDiagnostics.directory === files.dir ? codeDiagnostics.resultSets.flatMap(s => s.diagnostics.map(d => d.path)) : []);
  codeDiagnostics.setReconciler(() => files.reconcileDisplayed());
  codeDiagnostics.setLiveBasis(path => bridge.basis(path));
  codeDiagnostics.setLiveRecovery(() => bridge.statuses, (path, id) => bridge.reopen(path, id));
  bridge.onChange(() => codeDiagnostics.liveChanged());
  files.observe(bridge);
  codeDiagnostics.onLifecycle(() => bridge.sync());
  return bridge;
}
