import type { Terminal } from '@xterm/xterm';

type ColorScheme = 'light' | 'dark';

/**
 * xterm answers OSC 10/11 color queries itself, but does not implement the
 * color-scheme notification protocol yet:
 * https://github.com/contour-terminal/contour/blob/master/docs/vt-extensions/color-palette-update-notifications.md
 * Install before attaching/replaying PTY output so startup queries aren't lost.
 */
export function installTerminalColorScheme(terminal: Terminal, initial: ColorScheme) {
  let scheme = initial;
  let subscribed = false;
  let disposed = false;
  // Feed reports through the same onData route as xterm's built-in replies,
  // without treating them as user input (e.g. scrolling to the cursor).
  const report = () => terminal.input(`\x1b[?997;${scheme === 'dark' ? 1 : 2}n`, false);
  const mode = (enabled: boolean) => (params: (number | number[])[]) => {
    if (!params.includes(2031)) return false;
    subscribed = enabled;
    // Let xterm process any other modes in a combined DECSET/DECRST.
    return params.length === 1;
  };
  const reset = () => {
    subscribed = false;
    return false; // xterm must still perform its normal terminal reset.
  };
  const handlers = [
    terminal.parser.registerCsiHandler({ prefix: '?', final: 'h' }, mode(true)),
    terminal.parser.registerCsiHandler({ prefix: '?', final: 'l' }, mode(false)),
    terminal.parser.registerCsiHandler({ prefix: '?', final: 'n' }, (params) => {
      if (params.length !== 1 || params[0] !== 996) return false;
      report();
      return true;
    }),
    // DECRQM: advertise 2031 as supported, set (1) or reset (2).
    terminal.parser.registerCsiHandler({ prefix: '?', intermediates: '$', final: 'p' }, (params) => {
      if (params.length !== 1 || params[0] !== 2031) return false;
      terminal.input(`\x1b[?2031;${subscribed ? 1 : 2}$y`, false);
      return true;
    }),
    terminal.parser.registerEscHandler({ final: 'c' }, reset),
    terminal.parser.registerCsiHandler({ intermediates: '!', final: 'p' }, reset),
  ];
  return {
    // Call after applying the palette so subsequent OSC queries see new colors.
    update(next: ColorScheme) {
      if (disposed || next === scheme) return;
      scheme = next;
      // Never send unsolicited escape sequences to a shell that didn't opt in.
      if (subscribed) report();
    },
    dispose() {
      disposed = true;
      for (const handler of handlers) handler.dispose();
    },
  };
}
