<script lang="ts">
  import { hostLabel, shellCommand } from '../../hosts';

  interface Props {
    host: string;
  }

  let { host }: Props = $props();
</script>

<!-- Commands for a remote session are built for that host. ssh with a
     command runs a non-interactive shell, which often lacks the PATH that
     finds npm or nvm installed agents, so the user runs them interactively.
     A WSL distribution's shell opens with wsl -d instead. -->
<p class="remote-note">
  Run this on <strong>{hostLabel(host)}</strong>: open an interactive shell there with
  <code>{shellCommand(host)}</code>, then paste the command. Your login shell loads the PATH that finds the
  agent, even when it was installed with npm or nvm.
</p>

<style>
  .remote-note {
    margin: 0;
    font-size: 0.72rem;
    line-height: 1.4;
    color: var(--text-muted);
  }

  .remote-note code {
    font-family: var(--font-mono, monospace);
    color: var(--text-secondary);
  }
</style>
