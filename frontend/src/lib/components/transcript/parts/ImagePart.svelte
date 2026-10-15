<script lang="ts" module>
  import type { FileRef, SessionRef } from '../../../types';
  import { api } from '../../../api';
  import { epoch } from '../../../link';
  import { ImageCache, type LoadedImage } from '../../../images';

  const cache = new ImageCache();

  // The epoch keeps another host's session with the same id apart.
  function imageKey(sessionRef: SessionRef, file: FileRef): string {
    return `${epoch()}|${sessionRef.agent}|${sessionRef.id}|${file.ref}`;
  }

  /** The image attached as file, from the cache or fetched from the session. */
  export function loadImage(sessionRef: SessionRef, file: FileRef): Promise<LoadedImage> {
    const ref = file.ref;
    if (!ref) return Promise.reject(new Error('The session does not keep this image.'));
    return cache.load(imageKey(sessionRef, file), () => api.getBlob(sessionRef, ref));
  }
</script>

<script lang="ts">
  import { untrack } from 'svelte';
  import { openViewer } from '../../../viewer';

  // An image attached to a message. It is fetched once it scrolls into view
  // and opens in the full-window viewer when clicked.
  interface Props {
    file: FileRef;
    sessionRef: SessionRef;
  }

  let { file, sessionRef }: Props = $props();

  let name = $derived(file.name || file.path?.split(/[\\/]/).pop() || 'Image');
  let key = $derived(imageKey(sessionRef, file));
  let image = $state<LoadedImage | null>(null);
  let error = $state<string | null>(null);
  let el = $state<HTMLElement>();

  // Shown from the cache when there, else fetched by the effect below.
  $effect(() => {
    const wanted = key;
    untrack(() => {
      image = cache.get(wanted) ?? null;
      error = file.ref ? null : 'The session does not keep this image.';
    });
  });

  // The placeholder is there only while loading.
  $effect(() => {
    if (!el) return;
    void key;
    let cancelled = false;
    const observer = new IntersectionObserver((entries) => {
      if (!entries.some((e) => e.isIntersecting)) return;
      observer.disconnect();
      loadImage(sessionRef, file).then(
        (loaded) => {
          if (!cancelled) image = loaded;
        },
        (err: unknown) => {
          if (!cancelled) error = err instanceof Error ? err.message : String(err);
        }
      );
    });
    observer.observe(el);
    return () => {
      cancelled = true;
      observer.disconnect();
    };
  });

  function expand() {
    if (image) openViewer({ kind: 'image', url: image.url, data: image.data, format: image.format, name });
  }
</script>

{#if image}
  <button type="button" class="image-thumb" title="Show {name} in the whole window" onclick={expand}>
    <img src={image.url} alt={name} draggable="false" />
  </button>
{:else if error}
  <div class="image-failed" title={error}>
    <span>⚠️</span>
    <span>{name}</span>
    <span class="image-failed-note">not shown</span>
  </div>
{:else}
  <div class="image-loading" bind:this={el}>Loading image…</div>
{/if}

<style>
  .image-thumb {
    display: block;
    margin: 6px 0;
    padding: 0;
    border: 1px solid var(--border-color);
    border-radius: 6px;
    overflow: hidden;
    background: var(--bg-tertiary);
    cursor: zoom-in;
    max-width: 100%;
    width: fit-content;
  }

  .image-thumb img {
    display: block;
    max-width: min(320px, 100%);
    max-height: 240px;
    object-fit: contain;
  }

  .image-loading {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 160px;
    height: 100px;
    margin: 6px 0;
    border: 1px dashed var(--border-color);
    border-radius: 6px;
    color: var(--text-muted);
    font-size: 12px;
  }

  .image-failed {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 4px 8px;
    margin: 4px 0;
    border-radius: 4px;
    background: var(--bg-tertiary);
    font-size: 12px;
    font-family: var(--font-mono);
  }

  .image-failed-note {
    color: var(--text-muted);
  }
</style>
