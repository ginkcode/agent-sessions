import { vitePreprocess } from '@sveltejs/vite-plugin-svelte'

export default {
  // Svelte 5: vitePreprocess handles TypeScript in <script lang="ts"> blocks
  // (replaces the deprecated svelte-preprocess from the upstream template).
  preprocess: vitePreprocess(),
}