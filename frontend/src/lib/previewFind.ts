export interface PreviewText {
  element: Element;
  nodes: Node[];
  text: string;
}

/** Search document text, not Markdown syntax, code controls, or hidden diagrams. */
export function previewText(root: HTMLElement): PreviewText[] {
  const groups: PreviewText[] = [];
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const parent = node.parentElement;
    if (!parent || parent.closest('button, .code-block-header, svg, script, style, [hidden], [aria-hidden="true"]')) continue;
    if (!parent.getClientRects().length || getComputedStyle(parent).visibility === 'hidden') continue;
    const element = parent.closest('p, h1, h2, h3, h4, h5, h6, li, th, td, pre, blockquote') ?? parent;
    let group = groups[groups.length - 1];
    if (!group || group.element !== element) {
      group = { element, nodes: [], text: '' };
      groups.push(group);
    }
    group.nodes.push(node);
    group.text += node.nodeValue ?? '';
  }
  return groups;
}
