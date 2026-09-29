<!-- Pulsing placeholder rows displayed while sessions are loading -->
<div class="skeleton-list" aria-hidden="true">
  {#each Array(7) as _, idx (idx)}
    <div class="skeleton-row" style="animation-delay: {idx * 80}ms">
      <div class="skeleton-line-top">
        <span class="skeleton-pill dot"></span>
        <span class="skeleton-pill title" style="width: {55 + ((idx * 17) % 35)}%"></span>
        <span class="skeleton-pill time"></span>
      </div>
      <div class="skeleton-line-bottom">
        <span class="skeleton-pill badge"></span>
        <span class="skeleton-pill count"></span>
      </div>
    </div>
  {/each}
</div>

<style>
  .skeleton-list {
    display: flex;
    flex-direction: column;
    width: 100%;
    padding: 2px 0;
  }

  .skeleton-row {
    padding: 10px 12px;
    height: 58px;
    margin-bottom: 4px;
    box-sizing: border-box;
    display: flex;
    flex-direction: column;
    justify-content: center;
    gap: 7px;
  }

  .skeleton-line-top,
  .skeleton-line-bottom {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .skeleton-pill {
    display: inline-block;
    height: 12px;
    border-radius: 4px;
    background: linear-gradient(
      90deg,
      var(--bg-secondary) 25%,
      var(--bg-tertiary) 50%,
      var(--bg-secondary) 75%
    );
    background-size: 200% 100%;
    animation: shimmer 1.5s infinite;
  }

  .skeleton-pill.dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
  }

  .skeleton-pill.title {
    flex: 1;
    max-width: 220px;
  }

  .skeleton-pill.time {
    width: 32px;
    margin-left: auto;
  }

  .skeleton-pill.badge {
    width: 60px;
    height: 14px;
    border-radius: 10px;
  }

  .skeleton-pill.count {
    width: 36px;
    height: 14px;
  }

  @keyframes shimmer {
    0% {
      background-position: 200% 0;
    }
    100% {
      background-position: -200% 0;
    }
  }
</style>
