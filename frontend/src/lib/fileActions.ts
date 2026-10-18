// Resolve a Markdown link relative to the document, without leaving the project.
export function markdownFileTarget(current: string, href: string): string | null {
  let path: string;
  try {
    path = decodeURIComponent(href.split(/[?#]/, 1)[0]);
  } catch {
    return null;
  }
  if (!path || /^[a-z][a-z\d+.-]*:|^[\\/]|[\u0000-\u001f]/i.test(path) || path.includes('\\')) return null;
  const parts = current.split('/').slice(0, -1);
  for (const part of path.split('/')) {
    if (!part || part === '.') continue;
    if (part === '..') {
      if (!parts.length) return null;
      parts.pop();
    } else parts.push(part);
  }
  return parts.join('/') || null;
}

// Listing paths always use '/', but the root belongs to the target host,
// not necessarily the OS running the UI.
export function absoluteFilePath(root: string, relative: string): string {
  const windows = /^[a-z]:[\\/]/i.test(root) || root.startsWith('\\\\');
  const separator = windows ? '\\' : '/';
  const base = windows ? root.replace(/\//g, separator) : root;
  return base.replace(/[\\/]+$/, '') + separator + relative.replace(/\//g, separator);
}
