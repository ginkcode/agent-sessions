// Bundled fonts so the UI looks the same on every platform (see style.css).
// Inter's optical-size axis keeps small UI text open and legible.
import '@fontsource-variable/inter/opsz.css'
import '@fontsource-variable/inter/opsz-italic.css'
import '@fontsource-variable/jetbrains-mono/wght.css'
import '@fontsource-variable/jetbrains-mono/wght-italic.css'
import './style.css'
import { mount } from 'svelte'
import App from './App.svelte'
import { api } from './lib/api'
import { startLiveDocuments } from './lib/liveDocumentsWiring'
import { setupDropGuard, setupLinkInterceptor } from './lib/navigation'

setupLinkInterceptor(api);
setupDropGuard();
startLiveDocuments();

// WebKitGTK paints native GTK scrollbars on a layer above page content, so
// they show through dialogs and floating buttons. style.css replaces them
// with CSS scrollbars on Linux only; macOS keeps its native overlay ones.
export const isMac = /Mac/.test(navigator.userAgent) && !/iPhone/.test(navigator.userAgent) && !/iPad/.test(navigator.userAgent);

if (/Linux/.test(navigator.userAgent) && !/Android/.test(navigator.userAgent)) {
  document.documentElement.dataset.platform = 'linux';
}

// Svelte 5 mount API
const app = mount(App, {
  target: document.getElementById('app')!,
})

export default app
