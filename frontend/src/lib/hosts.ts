/** Hosts named "wsl:<distro>" are WSL distributions on this Windows computer. */
const WSL_PREFIX = 'wsl:';

/** The WSL distribution host names, or undefined for an SSH alias. */
export function wslDistro(host: string | undefined): string | undefined {
  if (!host?.startsWith(WSL_PREFIX)) return undefined;
  return host.slice(WSL_PREFIX.length) || undefined;
}

/** How a host is shown: "Ubuntu (WSL)" for a distribution, else the alias. */
export function hostLabel(host: string): string {
  const distro = wslDistro(host);
  return distro ? `${distro} (WSL)` : host;
}

/** The command that opens an interactive shell on host from this computer. */
export function shellCommand(host: string): string {
  const distro = wslDistro(host);
  return distro ? `wsl -d ${distro}` : `ssh ${host}`;
}
