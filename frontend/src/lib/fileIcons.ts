import table from './fileIcons.json' with { type: 'json' };

type IconTable = {
  file?: string;
  folder?: string;
  folderExpanded?: string;
  fileNames?: Record<string, string>;
  fileExtensions?: Record<string, string>;
  folderNames?: Record<string, string>;
  folderNamesExpanded?: Record<string, string>;
};

const dark: IconTable = table;
const light: IconTable = table.light;

// Exact file names win over extensions. Try compound extensions first
// (foo.d.ts before ts), using the same case-insensitive names as VS Code.
export function fileIcon(path: string, folder = false, expanded = false, lightTheme = false): string {
  const name = path.replace(/\\/g, '/').replace(/\/$/, '').split('/').pop()!.toLowerCase();
  const variant = lightTheme ? light : undefined;
  const lookup = (key: 'fileNames' | 'fileExtensions' | 'folderNames' | 'folderNamesExpanded', value: string) => {
    const own = (map: Record<string, string> | undefined) =>
      map && Object.hasOwn(map, value) ? map[value] : undefined;
    return own(variant?.[key]) ?? own(dark[key]);
  };
  let icon: string | undefined;
  if (folder) {
    const key = expanded ? 'folderExpanded' : 'folder';
    icon = lookup(expanded ? 'folderNamesExpanded' : 'folderNames', name) ?? variant?.[key] ?? dark[key];
  } else {
    icon = lookup('fileNames', name);
    for (let dot = name.indexOf('.'); !icon && dot >= 0; dot = name.indexOf('.', dot + 1)) {
      icon = lookup('fileExtensions', name.slice(dot + 1));
    }
    icon ??= variant?.file ?? dark.file;
  }
  return `/file-icons/${icon}.svg`;
}
