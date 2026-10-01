/**
 * Environment preconditions for the Desktop development runner.
 *
 * Desktop is a graphical ACP client. On a Linux host reached over SSH it has no
 * usable display: Chromium aborts during platform initialization ("Missing X
 * server or $DISPLAY" / "The platform failed to initialize") and dies on
 * SIGSEGV. Because the window is created frameless with a white background, the
 * user only ever sees a white window followed by a crash line, after waiting
 * through a full build. Reporting the missing display up front turns an
 * unexplained white screen into an actionable message.
 *
 * Two checks cooperate, because both failure shapes exist in practice:
 *  - `displayProblem` catches a host with no display variable at all and fails
 *    before the build.
 *  - `looksLikeDisplayFailure` catches a host whose DISPLAY is set but unusable
 *    (a stale `:0`, X11 forwarding that is not running, an X server that died),
 *    which no environment check can predict. It is applied to Electron's own
 *    stderr, so the diagnosis comes from the component that actually failed.
 *
 * Both are pure and dependency-injected so they can be tested without starting
 * Electron.
 */

export interface DisplayEnvironment {
  platform: NodeJS.Platform;
  env: NodeJS.ProcessEnv;
}

/**
 * Chromium's platform-initialization failures, as it reports them on stderr.
 * These strings are Chromium's own diagnostics, not ours, so they are matched
 * loosely: any of them means Desktop cannot create a window here.
 */
const DISPLAY_FAILURE_PATTERNS = [
  /Missing X server or \$DISPLAY/i,
  /The platform failed to initialize/i,
  /Could not open X display/i,
  /Failed to open X display/i,
  /ozone_platform_x11.*X server/i,
  /DISPLAY environment variable is not set/i,
  /XDG_RUNTIME_DIR is not set/i,
];

/** True when a chunk of Electron output describes a display that cannot be used. */
export function looksLikeDisplayFailure(text: string): boolean {
  return DISPLAY_FAILURE_PATTERNS.some((pattern) => pattern.test(text));
}

/** The actionable explanation shown for a host without a usable display. */
export function displayFailureHelp(): string {
  return [
    'Desktop needs a display server, and this host has none it can use.',
    'Without one Electron cannot create a window: it shows a blank white window',
    'and then exits ("The platform failed to initialize").',
    'Desktop looks for the DISPLAY and WAYLAND_DISPLAY environment variables.',
    '',
    'Use one of:',
    '  * a real desktop session on this host,',
    '  * X11 forwarding from your local machine:  ssh -X <host>',
    '  * a virtual display, e.g.  Xvfb :99 & export DISPLAY=:99  (or VNC/x11vnc on top)',
    '',
    'For terminal-only work on a headless host, use the TUI (mothx) or',
    'mothx serve plus the Web UI in your local browser instead of Desktop.',
  ].join('\n');
}

/**
 * Returns an explanation when the environment has no display variable at all,
 * or null when a display is configured (possibly wrongly — `looksLikeDisplayFailure`
 * is the net for that case).
 */
export function displayProblem({ platform, env }: DisplayEnvironment): string | null {
  if (platform !== 'linux') return null;
  if (env.DISPLAY || env.WAYLAND_DISPLAY) return null;
  return displayFailureHelp();
}
