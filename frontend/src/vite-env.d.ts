/// <reference types="svelte" />
/// <reference types="vite/client" />

// Ambient declaration for the Wails v2 desktop runtime. In the desktop app
// window.go is injected by the Wails asset server; in a plain browser
// (npm run dev / tests) it is undefined and the app runs in mock mode.
interface WailsRuntime {
  go?: Record<string, Record<string, (...args: unknown[]) => unknown>>
  runtime?: Record<string, (...args: unknown[]) => unknown>
}

interface Window {
  go?: WailsRuntime['go']
  runtime?: WailsRuntime['runtime']
}