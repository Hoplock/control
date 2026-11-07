// EVERY URL THE CONSOLE WRITES IS UNDER /console/ (PLAN M27, DESIGN.md §4).
//
// The shell, every asset (vite.config.ts's base), every deep link and every
// URL-serialisable filter. consoleHref is the only way to make one, so a URL
// outside the prefix cannot be written by accident: a screen names a path
// inside the console ("audit/records") and gets "/console/audit/records". A
// host's screens are under /host-console/<provider>/ and are linked with
// hostConsoleHref, never from inside this prefix.

export const CONSOLE_PREFIX = "/console/";
export const HOST_CONSOLE_PREFIX = "/host-console/";

// The build's asset base and the router's base are one value. (A test run
// has no base: vitest serves from "/".)
if (import.meta.env.MODE !== "test" && import.meta.env.BASE_URL !== CONSOLE_PREFIX) {
  throw new Error(`the console is built for ${import.meta.env.BASE_URL}, not ${CONSOLE_PREFIX}`);
}

/** A path inside the console, as a URL. Query values are encoded. */
export function consoleHref(path: string, query?: Record<string, string>): string {
  const clean = path.replace(/^\/+/, "");
  if (clean.split("/").some((s) => s === ".." || s === ".")) {
    throw new Error(`${path} leaves the console`);
  }
  const q = new URLSearchParams(query ?? {}).toString();
  return CONSOLE_PREFIX + clean + (q ? `?${q}` : "");
}

/** A path in a host's screens: the other console's prefix, named. */
export function hostConsoleHref(provider: string, path: string): string {
  if (!/^[a-z0-9][a-z0-9-]*\/[a-z0-9][a-z0-9-]*$/.test(provider)) throw new Error(`${provider} is not <owner>/<name>`);
  return HOST_CONSOLE_PREFIX + provider + "/" + path.replace(/^\/+/, "");
}

/** The console path a location names, without the prefix, or null outside it. */
export function insideConsole(pathname: string): string | null {
  if (pathname === CONSOLE_PREFIX.slice(0, -1)) return "";
  return pathname.startsWith(CONSOLE_PREFIX) ? pathname.slice(CONSOLE_PREFIX.length) : null;
}

/**
 * A return path a sign-in may send the browser back to: inside the console,
 * and never a scheme-relative or absolute URL (an open redirect).
 */
export function safeReturn(value: string | null | undefined): string {
  if (!value || !value.startsWith(CONSOLE_PREFIX) || value.startsWith("//") || value.includes("\\")) {
    return CONSOLE_PREFIX;
  }
  return value;
}
