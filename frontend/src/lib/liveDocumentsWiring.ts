import { api } from './api';
import { LiveDocumentBridge } from './liveDocuments';
import { files } from './stores/files.svelte';
import { codeDiagnostics } from './stores/codeDiagnostics.svelte';

/**
 * Connects the files store and the diagnostics store through the bridge. Without a
 * live-capable attachment (liveCodeDiagnostics is not advertised in production yet)
 * every hook is a no-op and no document call is made.
 */
export function startLiveDocuments(): LiveDocumentBridge {
  const bridge = new LiveDocumentBridge({
    api,
    store: codeDiagnostics,
    source: () => ({ dir: files.dir, documents: files.displayedDocuments() }),
  });
  codeDiagnostics.setReconciler(() => files.reconcileDisplayed());
  codeDiagnostics.setLiveBasis(path => bridge.basis(path));
  bridge.onChange(() => codeDiagnostics.liveChanged());
  files.observe(bridge);
  codeDiagnostics.onLifecycle(() => bridge.sync());
  return bridge;
}
