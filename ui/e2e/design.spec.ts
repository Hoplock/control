// DESIGN.md §10.3–§10.6, over every registered screen (e2e/screens.ts):
//   §10.3 axe: zero violations at serious or above, both themes, every state;
//   §10.4 the screenshot baseline: both themes, both densities where a screen
//         holds a table, and each of its loading, empty, error states;
//   §10.5 no network at runtime: every external origin blocked, the page
//         renders as the baseline does and asks nothing of another origin;
//   §10.6 reduced motion: no transition or animation is longer than 0ms.

import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";
import { fitContent, NORTH, THEMES, volatile } from "./harness";
import { openScreen, statesOf } from "./open";
import { SCREENS, type E2EScreen } from "./screens";

const SERIOUS = new Set(["serious", "critical"]);

function defaultDensity(screen: E2EScreen) {
  return screen.densities ? (["compact", "comfortable"] as const) : ([undefined] as const);
}

async function snap(page: import("@playwright/test").Page, name: string, settled = true) {
  // A loaded screen waits for its reads to finish; a loading state holds one open on purpose.
  if (settled) await page.waitForLoadState("networkidle");
  await fitContent(page);
  await expect(page).toHaveScreenshot(`${name}.png`, { mask: volatile(page), fullPage: true });
}

for (const screen of SCREENS) {
  test.describe(screen.id, () => {
    for (const theme of THEMES) {
      test(`axe, ${theme}`, async ({ browser }) => {
        for (const state of [undefined, ...statesOf(screen)]) {
          const page = await openScreen(browser, screen, { theme, state });
          const result = await new AxeBuilder({ page })
            .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"])
            .analyze();
          const serious = result.violations
            .filter((v) => SERIOUS.has(v.impact ?? ""))
            .map((v) => `${v.id} (${v.impact}): ${v.help} — ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`);
          expect(serious, `${screen.id} ${state ?? "loaded"} ${theme}`).toEqual([]);
          await page.context().close();
        }
      });

      test(`baseline, ${theme}`, async ({ browser }) => {
        for (const density of defaultDensity(screen)) {
          const page = await openScreen(browser, screen, { theme, density });
          await snap(page, [screen.id, theme, density].filter(Boolean).join("-"));
          await page.context().close();
        }
        for (const state of statesOf(screen)) {
          const page = await openScreen(browser, screen, { theme, state });
          await snap(page, `${screen.id}-${state}-${theme}`, state !== "loading");
          await page.context().close();
        }
      });
    }

    test("renders with every external origin blocked", async ({ browser }) => {
      const elsewhere: string[] = [];
      const page = await openScreen(browser, screen, {
        density: screen.densities ? "compact" : undefined,
        before: async (context) => {
          await context.route("**/*", (route) => {
            const url = route.request().url();
            if (url.startsWith(`${NORTH}/`) || url.startsWith("data:")) return route.fallback();
            elsewhere.push(url);
            return route.abort("blockedbyclient");
          });
        },
      });
      await page.waitForLoadState("networkidle");
      expect(elsewhere, "requests to another origin").toEqual([]);
      await snap(page, [screen.id, "light", screen.densities ? "compact" : undefined].filter(Boolean).join("-"));
      await page.context().close();
    });

    test("collapses every transition under reduced motion", async ({ browser }) => {
      const page = await openScreen(browser, screen, { reducedMotion: true });
      const moving = await page.evaluate(() => {
        const out: string[] = [];
        const longest = (list: string) => Math.max(0, ...list.split(",").map((d) => parseFloat(d) * (d.trim().endsWith("ms") ? 1 : 1000)));
        for (const el of Array.from(document.querySelectorAll("*"))) {
          for (const pseudo of [null, "::before", "::after", "::backdrop"]) {
            const cs = getComputedStyle(el, pseudo);
            const t = longest(cs.transitionDuration);
            const a = cs.animationName === "none" ? 0 : longest(cs.animationDuration);
            if (t > 0 || a > 0) out.push(`${el.tagName.toLowerCase()}.${el.className}${pseudo ?? ""}: transition ${t}ms, animation ${a}ms`);
          }
        }
        return out;
      });
      expect(moving).toEqual([]);
      await page.context().close();
    });
  });
}
