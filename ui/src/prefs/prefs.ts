// What the console remembers for an operator (DESIGN.md §3.7, §8, §9): the
// theme, the density and the tenant, per operator (operatorKey), in this
// browser. Before
// anyone has signed in, the last operator's theme applies, so the sign-in page
// does not flash the other theme. Storage that throws (a private window, a
// policy) leaves the defaults; nothing here is a control.

export type Theme = "system" | "light" | "dark";
export type Density = "screen" | "compact" | "comfortable";

export interface Prefs {
  theme: Theme;
  density: Density;
  tenant?: string;
  locale?: string;
}

export const DEFAULT_PREFS: Prefs = { theme: "system", density: "screen" };

/**
 * Whose preferences these are: the operator, not the sign-in. A session's
 * principal id (`sess-…`) is new at every sign-in, so keying by it forgot an
 * operator's theme, density and saved views each time they signed in again;
 * the subject is the person. A credential with no subject (an API token) is
 * its own principal, which does not change.
 */
export function operatorKey(session: { subject?: string; principal_id: string }): string {
  return session.subject ? `subject:${session.subject}` : session.principal_id;
}

const KEY = "hoplock.console.prefs";

interface Stored {
  last?: Prefs;
  byPrincipal?: Record<string, Prefs>;
}

function read(): Stored {
  try {
    const raw = window.localStorage.getItem(KEY);
    return raw ? (JSON.parse(raw) as Stored) : {};
  } catch {
    return {};
  }
}

function valid(p: Partial<Prefs> | undefined): Prefs {
  const theme: Theme = p?.theme === "light" || p?.theme === "dark" ? p.theme : "system";
  const density: Density = p?.density === "compact" || p?.density === "comfortable" ? p.density : "screen";
  const out: Prefs = { theme, density };
  if (typeof p?.tenant === "string") out.tenant = p.tenant;
  if (typeof p?.locale === "string") out.locale = p.locale;
  return out;
}

/** An operator's preferences, or the last operator's before sign-in. */
export function loadPrefs(principal?: string): Prefs {
  const s = read();
  if (principal && s.byPrincipal?.[principal]) return valid(s.byPrincipal[principal]);
  return valid(s.last);
}

export function savePrefs(principal: string | undefined, prefs: Prefs): void {
  const s = read();
  s.last = prefs;
  if (principal) s.byPrincipal = { ...s.byPrincipal, [principal]: prefs };
  try {
    window.localStorage.setItem(KEY, JSON.stringify(s));
  } catch {
    // Not remembered; the choice still holds for this page.
  }
}

/** Puts the theme on <html>: data-theme is read by tokens.css. */
export function applyTheme(theme: Theme, root: HTMLElement = document.documentElement): void {
  if (theme === "system") root.removeAttribute("data-theme");
  else root.setAttribute("data-theme", theme);
}

const VIEWS_KEY = "hoplock.console.views";

/** A screen's saved views: named URLs, per operator, in this browser only. */
export function loadViews(principal: string, screen: string): { name: string; query: string }[] {
  try {
    const raw = window.localStorage.getItem(VIEWS_KEY);
    const all = raw ? (JSON.parse(raw) as Record<string, { name: string; query: string }[]>) : {};
    const list = all[`${principal}|${screen}`];
    return Array.isArray(list) ? list.filter((v) => typeof v?.name === "string" && typeof v?.query === "string") : [];
  } catch {
    return [];
  }
}

export function saveViews(principal: string, screen: string, views: { name: string; query: string }[]): void {
  try {
    const raw = window.localStorage.getItem(VIEWS_KEY);
    const all = raw ? (JSON.parse(raw) as Record<string, unknown>) : {};
    all[`${principal}|${screen}`] = views;
    window.localStorage.setItem(VIEWS_KEY, JSON.stringify(all));
  } catch {
    // Not remembered; the view is still the URL.
  }
}
