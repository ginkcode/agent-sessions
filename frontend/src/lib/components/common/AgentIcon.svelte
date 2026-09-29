<script lang="ts">
  // Small per-agent glyphs in each agent's brand color, drawn inline. Codex
  // uses its reference mark; the others are simplified marks.
  interface Props {
    agent?: string;
    size?: number;
  }

  let { agent, size = 14 }: Props = $props();

  function title(agent?: string): string {
    if (agent === 'claude-code') return 'Claude Code';
    if (agent === 'codex') return 'Codex';
    if (agent === 'opencode') return 'OpenCode';
    return agent || 'Session';
  }
</script>

<span class="agent-icon" title={title(agent)} aria-hidden="true">
  {#if agent === 'claude-code'}
    <!-- Claude spark: radial spokes around a center. -->
    <svg width={size} height={size} viewBox="0 0 16 16" class="claude">
      <g stroke="currentColor" stroke-width="1.8" stroke-linecap="round">
        <line x1="8" y1="1.5" x2="8" y2="14.5" />
        <line x1="1.5" y1="8" x2="14.5" y2="8" />
        <line x1="3.4" y1="3.4" x2="12.6" y2="12.6" />
        <line x1="12.6" y1="3.4" x2="3.4" y2="12.6" />
      </g>
    </svg>
  {:else if agent === 'codex'}
    <!-- Codex: OpenAI knot with a prompt cut out (frontend/assets/codex-openai.svg). -->
    <svg width={size} height={size} viewBox="0 0 24 24" class="codex">
      <path
        fill="currentColor"
        fill-rule="evenodd"
        clip-rule="evenodd"
        d="M8.086.457a6.105 6.105 0 013.046-.415c1.333.153 2.521.72 3.564 1.7a.117.117 0 00.107.029c1.408-.346 2.762-.224 4.061.366l.063.03.154.076c1.357.703 2.33 1.77 2.918 3.198.278.679.418 1.388.421 2.126a5.655 5.655 0 01-.18 1.631.167.167 0 00.04.155 5.982 5.982 0 011.578 2.891c.385 1.901-.01 3.615-1.183 5.14l-.182.22a6.063 6.063 0 01-2.934 1.851.162.162 0 00-.108.102c-.255.736-.511 1.364-.987 1.992-1.199 1.582-2.962 2.462-4.948 2.451-1.583-.008-2.986-.587-4.21-1.736a.145.145 0 00-.14-.032c-.518.167-1.04.191-1.604.185a5.924 5.924 0 01-2.595-.622 6.058 6.058 0 01-2.146-1.781c-.203-.269-.404-.522-.551-.821a7.74 7.74 0 01-.495-1.283 6.11 6.11 0 01-.017-3.064.166.166 0 00.008-.074.115.115 0 00-.037-.064 5.958 5.958 0 01-1.38-2.202 5.196 5.196 0 01-.333-1.589 6.915 6.915 0 01.188-2.132c.45-1.484 1.309-2.648 2.577-3.493.282-.188.55-.334.802-.438.286-.12.573-.22.861-.304a.129.129 0 00.087-.087A6.016 6.016 0 015.635 2.31C6.315 1.464 7.132.846 8.086.457zm-.804 7.85a.848.848 0 00-1.473.842l1.694 2.965-1.688 2.848a.849.849 0 001.46.864l1.94-3.272a.849.849 0 00.007-.854l-1.94-3.393zm5.446 6.24a.849.849 0 000 1.695h4.848a.849.849 0 000-1.696h-4.848z"
      />
    </svg>
  {:else if agent === 'opencode'}
    <!-- OpenCode: blocky square ring. -->
    <svg width={size} height={size} viewBox="0 0 16 16" class="opencode">
      <path
        fill="currentColor"
        fill-rule="evenodd"
        d="M2 2 H14 V14 H2 Z M5.5 5.5 V10.5 H10.5 V5.5 Z"
      />
    </svg>
  {:else}
    <span class="fallback">📄</span>
  {/if}
</span>

<style>
  .agent-icon {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
  }

  svg {
    display: block;
  }

  .claude {
    color: var(--agent-claude);
  }

  .codex {
    color: var(--agent-codex);
  }

  .opencode {
    color: var(--agent-opencode);
  }

  .fallback {
    font-size: 0.85rem;
  }
</style>
