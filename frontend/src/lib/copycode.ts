import { expandMermaid, toggleMermaidView } from './mermaid';

/**
 * Shared click handler for copy-code buttons injected by the markdown
 * renderer (`<button class="copy-code-btn" data-code="…">`). Containers of
 * rendered markdown use event delegation so the handler works no matter how
 * many code blocks are present or which component renders them. It also
 * handles the Diagram/Source toggle and the Expand of Mermaid blocks.
 */
export function handleCopyCodeClick(e: MouseEvent): void {
  const target = e.target as HTMLElement | null;
  if (!target) return;
  const toggle = target.closest('.mermaid-toggle-btn') as HTMLElement | null;
  if (toggle) {
    e.stopPropagation();
    toggleMermaidView(toggle);
    return;
  }
  if (target.closest('.mermaid-expand-btn, .mermaid-diagram')) {
    e.stopPropagation();
    expandMermaid(target);
    return;
  }
  const btn = target.closest('.copy-code-btn') as HTMLElement | null;
  if (!btn) return;

  const code = btn.getAttribute('data-code');
  if (!code || typeof navigator === 'undefined' || !navigator.clipboard) return;

  e.stopPropagation();
  navigator.clipboard.writeText(code).then(() => {
    const originalText = btn.textContent;
    btn.textContent = '✓ Copied';
    btn.classList.add('copied');
    setTimeout(() => {
      btn.textContent = originalText;
      btn.classList.remove('copied');
    }, 1500);
  });
}