// What the console remembers for an operator (DESIGN.md §3.7, §8, §9): the
// theme, the density and the tenant, per principal, in this browser. Before
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
