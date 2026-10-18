// Listing paths always use '/', but the root belongs to the target host,
// not necessarily the OS running the UI.
export function absoluteFilePath(root: string, relative: string): string {
  const windows = /^[a-z]:[\\/]/i.test(root) || root.startsWith('\\\\');
  const separator = windows ? '\\' : '/';
  const base = windows ? root.replace(/\//g, separator) : root;
  return base.replace(/[\\/]+$/, '') + separator + relative.replace(/\//g, separator);
}
