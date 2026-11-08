// Opening a registered screen the way every check does: as its role, in a
// theme and a density, in one of its states, from the console's own build or
// a pseudolocale's or the gallery's.

import type { Browser, BrowserContext, Page } from "@playwright/test";
import { forceStates, NORTH, serveBuild, stateFile, substitute, useDensity, type Density, type Theme } from "./harness";
import type { E2EScreen, StateName } from "./screens";

export interface OpenOptions {
  theme?: Theme;
  density?: Density;
  state?: StateName;
  /** A build to serve in place of the console's or gallery's files: "en-XA", "en-XB". */
  locale?: "en-XA" | "en-XB";
  reducedMotion?: boolean;
  /**
   * Turns kerning and ligatures off: text set right-to-left kerns different
   * pairs, so content-sized columns drift a pixel or two from their mirror.
   */
  noKerning?: boolean;
  /** Called with the context before anything loads (to watch or block requests). */
  before?(context: BrowserContext): Promise<void>;
}

export async function openScreen(browser: Browser, screen: E2EScreen, o: OpenOptions = {}): Promise<Page> {
  const context = await browser.newContext({
    baseURL: NORTH,
    viewport: { width: 1280, height: 800 },
    deviceScaleFactor: 1,
    locale: "en-US",
    timezoneId: "UTC",
    colorScheme: o.theme ?? "light",
    reducedMotion: o.reducedMotion ? "reduce" : "no-preference",
    storageState: screen.kind === "console" ? stateFile(screen.role ?? "admin") : undefined,
    bypassCSP: o.noKerning ?? false,
  });
  if (o.before) await o.before(context);
  const page = await context.newPage();
  if (screen.kind === "gallery") {
    await serveBuild(page, o.locale ? `build/gallery-${o.locale}` : "build/gallery");
  } else if (o.locale) {
    await serveBuild(page, `build/${o.locale}`);
  }
  if (o.density && screen.kind !== "gallery") await useDensity(page, o.density);
  const state = o.state ? screen.states[o.state] : undefined;
  for (const [path, s] of state?.substitute ?? []) await substitute(page, path, s);
  const url = screen.kind === "gallery" && o.density === "compact" ? `${screen.path}&density=compact` : screen.path;
  await page.goto(url);
  await (state ? state.ready(page) : screen.ready(page));
  if (screen.prepare) await screen.prepare(page);
  if (o.noKerning) {
    await page.addStyleTag({ content: "* { font-kerning: none !important; font-variant-ligatures: none !important; }" });
  }
  await page.evaluate(() => document.fonts.ready);
  if (screen.kind === "gallery") await forceStates(page);
  return page;
}

/** Every state a screen defines a way to produce, besides its loaded one. */
export function statesOf(screen: E2EScreen): StateName[] {
  return (["loading", "empty", "error", "partial"] as const).filter((s) => screen.states[s] && !screen.states[s]?.seeded);
}
