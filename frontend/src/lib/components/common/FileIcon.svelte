<script lang="ts">
  import { fileIcon } from '../../fileIcons';
  import { theme } from '../../stores/theme.svelte';

  let { path, folder = false, expanded = false }: {
    path: string;
    folder?: boolean;
    expanded?: boolean;
  } = $props();
</script>

<img class="file-icon" src={fileIcon(path, folder, expanded, theme.resolved === 'light')} alt="" aria-hidden="true" width="16" height="16" />

<style>
  .file-icon {
    width: 16px;
    height: 16px;
    flex-shrink: 0;
    object-fit: contain;
  }
</style>
