// The design-direction screenshots (0052). A tool, not a check: it asserts
// nothing, it is never a CI job, and its `.shoot.ts` name keeps it out of the
// `*.spec.ts` set that TestEveryConsoleSpecRunsInCI requires CI to run.
//
// For each candidate token file in design/round-<DESIGN_ROUND>/ (a complete
// copy of src/styles/tokens.css with its values changed), it opens the same
// screens of the seeded fixture — at 2×, 1280px wide, full content height —
// applies the candidate over the console's own tokens IN THE BROWSER, and
// writes one image per screen. The server binds nothing new, and
// src/styles/tokens.css is untouched until a direction is approved.
//
// A candidate names the theme it is shown in first in its header comment,
// `first-theme: dark` (light otherwise). DESIGN_ONLY=<id>,<id> limits a run
// to some candidates; DESIGN_EXTRA=<screen>,… adds screens the operator asked
// to see (any id from SHOTS' optional list below).

import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { test, type Browser, type Page } from "@playwright/test";
import { fitContent, NORTH, serveBuild, stateFile, steady, useDensity, UI, type Density, type Theme } from "./harness";
import { SCREENS, type E2EScreen } from "./screens";

interface Shot {
  /** The image's screen name. */
  name: string;
  /** The registered screen (e2e/screens.ts) it opens, ready as that defines. */
  screen: string;
  density?: Density;
  /** "first" or "other": the candidate's first theme, or its other one. */
  theme: "first" | "other";
}

// The same five for every direction, in every round (0052's prompt).
const SHOTS: Shot[] = [
  { name: "explain", screen: "explain", theme: "first" },
  { name: "explain", screen: "explain", theme: "other" },
  { name: "audit", screen: "audit-records", density: "compact", theme: "first" },
  { name: "fleet", screen: "fleet", density: "compact", theme: "first" },
  { name: "status", screen: "gallery-status", theme: "first" },
];

// Screens the operator may ask to see in a round, added with DESIGN_EXTRA.
const EXTRA: Shot[] = [
  { name: "fleet", screen: "fleet", density: "compact", theme: "other" },
  { name: "audit", screen: "audit-records", density: "compact", theme: "other" },
  { name: "status", screen: "gallery-status", theme: "other" },
  { name: "deployment", screen: "deployment", density: "comfortable", theme: "first" },
  { name: "buttons", screen: "gallery-buttons", theme: "first" },
  { name: "buttons", screen: "gallery-buttons", theme: "other" },
  { name: "fields", screen: "gallery-fields", theme: "first" },
  { name: "data", screen: "gallery-data", theme: "first" },
  { name: "replay", screen: "audit-replay", theme: "first" },
];

const round = process.env.DESIGN_ROUND ?? "1";
const dir = join(UI, "design", `round-${round}`);

/** The candidate's CSS, without its @font-face rules: the faces are the console's own, already loaded. */
function candidate(path: string): { css: string; first: Theme } {
  const raw = readFileSync(path, "utf8");
  const first: Theme = /first-theme:\s*dark/.test(raw) ? "dark" : "light";
  return { css: raw.replace(/@font-face\s*\{[^}]*\}/g, ""), first };
}

function screenById(id: string): E2EScreen {
  const s = SCREENS.find((x) => x.id === id);
  if (!s) throw new Error(`no registered screen ${id}`);
  return s;
}

async function shoot(browser: Browser, screen: E2EScreen, css: string, theme: Theme, density?: Density): Promise<Page> {
  const context = await browser.newContext({
    baseURL: NORTH,
    viewport: { width: 1280, height: 800 },
    deviceScaleFactor: 2,
    locale: "en-US",
    timezoneId: "UTC",
    colorScheme: theme,
    // The candidate is an inline <style>; the console's CSP refuses one.
    bypassCSP: true,
    storageState: screen.kind === "console" ? stateFile(screen.role ?? "admin") : undefined,
  });
  const page = await context.newPage();
  if (screen.kind === "gallery") await serveBuild(page, "build/gallery");
  else await useDensity(page, density ?? "screen");
  await page.goto(screen.path);
  await screen.ready(page);
  if (screen.prepare) await screen.prepare(page);
  await page.waitForLoadState("networkidle");
  // Over the console's own tokens: same selectors, later in the cascade.
  await page.addStyleTag({ content: css });
  await page.evaluate(() => document.fonts.ready);
  await page.evaluate(() =>
    Promise.all(
      document
        .getAnimations()
        .filter((a) => a.effect?.getComputedTiming().iterations !== Infinity)
        .map((a) => a.finished.catch(() => undefined)),
    ),
  );
  await steady(page);
  await fitContent(page);
  return page;
}

const only = (process.env.DESIGN_ONLY ?? "").split(",").filter(Boolean);
const extra = (process.env.DESIGN_EXTRA ?? "").split(",").filter(Boolean);
const files = readdirSync(dir)
  .filter((f) => f.endsWith(".css"))
  .map((f) => f.replace(/\.css$/, ""))
  .filter((id) => only.length === 0 || only.includes(id))
  .sort();

for (const id of files) {
  test(`round ${round}: ${id}`, async ({ browser }) => {
    const { css, first } = candidate(join(dir, `${id}.css`));
    const other: Theme = first === "dark" ? "light" : "dark";
    const shots = [...SHOTS, ...EXTRA.filter((e) => extra.includes(`${e.name}-${e.theme}`) || extra.includes(e.name))];
    let k = 0;
    for (const shot of shots) {
      k++;
      const theme = shot.theme === "first" ? first : other;
      const page = await shoot(browser, screenById(shot.screen), css, theme, shot.density);
      await page.screenshot({ path: join(dir, `${id}-${k}-${shot.name}-${theme}.png`), fullPage: true, animations: "disabled", caret: "hide" });
      await page.context().close();
    }
  });
}
