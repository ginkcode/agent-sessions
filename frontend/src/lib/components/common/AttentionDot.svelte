<script lang="ts">
  // Where an agent tab stands: working pulses in the accent colour, needing
  // the user is amber, finished is green. Other states show nothing.
  import type { AgentAttention } from '../../attention';
  import { attentionLabel } from '../../alerts';

  interface Props {
    state: AgentAttention | null | undefined;
    size?: number;
    /** Names the state for screen readers too, where nothing else says it. */
    announce?: boolean;
  }

  let { state, size = 7, announce = false }: Props = $props();

  let label = $derived(attentionLabel(state));
</script>

{#if label}
  <span
    class="attention-dot {state}"
    style="--dot-size: {size}px"
    title={label}
    role={announce ? 'img' : undefined}
    aria-label={announce ? label : undefined}
    aria-hidden={announce ? undefined : 'true'}
  ></span>
{/if}

<style>
  .attention-dot {
    flex-shrink: 0;
    width: var(--dot-size);
    height: var(--dot-size);
    border-radius: 50%;
  }

  .working {
    background: var(--accent-color);
    animation: attention-pulse 1.6s infinite ease-in-out;
  }

  .needs {
    background: var(--status-needs);
    box-shadow: 0 0 0 2px color-mix(in srgb, var(--status-needs) 30%, transparent);
  }

  .done {
    background: var(--status-live);
  }

  @keyframes attention-pulse {
    0%,
    100% {
      opacity: 0.45;
    }
    50% {
      opacity: 1;
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .working {
      animation: none;
    }
  }
</style>
