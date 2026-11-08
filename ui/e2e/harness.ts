// What every browser check shares: signing in as a role, the two themes and
// densities, serving a pseudolocale or gallery build in place of the console's
// files, substituting an API answer IN THE BROWSER for a state one server
// cannot produce (docs/PROTOCOL.md §8 — never a route the server binds), and
// forcing a pseudo-class so a component's every state shows in one image.

import { existsSync, readFileSync } from "node:fs";
import { extname, join } from "node:path";
import { fileURLToPath } from "node:url";
import type { BrowserContext, Page, Route } from "@playwright/test";

export const UI = fileURLToPath(new URL("..", import.meta.url));
export const NORTH = "http://127.0.0.1:18081";
export const SOUTH = "http://127.0.0.1:18080";
export const TENANT = "default";

export type Role = "admin" | "auditor" | "fleet-admin";
export const OPERATORS: Record<Role, { login: string; password: string }> = {
  admin: { login: "ada", password: "e2e-admin-password" },
  auditor: { login: "avery", password: "e2e-auditor-password" },
  "fleet-admin": { login: "fenn", password: "e2e-fleet-password" },
};

/** Where global setup leaves each role's signed-in browser state. */
export function stateFile(role: Role): string {
  return join(UI, "build", "e2e", `state-${role}.json`);
}

export type Theme = "light" | "dark";
export type Density = "compact" | "comfortable";
export const THEMES: readonly Theme[] = ["light", "dark"];
export const DENSITIES: readonly Density[] = ["compact", "comfortable"];

/** Sets the density preference before the console reads it (src/prefs). */
export async function useDensity(page: Page, density: Density): Promise<void> {
  await page.addInitScript((d) => {
    window.localStorage.setItem("hoplock.console.prefs", JSON.stringify({ last: { theme: "system", density: d } }));
  }, density);
}

const TYPES: Record<string, string> = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript",
  ".css": "text/css",
  ".json": "application/json",
  ".woff2": "font/woff2",
  ".txt": "text/plain; charset=utf-8",
};

/** The CSP the server sends with the console (internal/console), kept on a build served in its place. */
const CSP =
  "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'";

/**
 * Serves a build directory at a prefix of the server's own origin, in the
 * browser: the console's API calls still reach the real server, so a
 * pseudolocale build is checked against the same seeded data. A path with no
 * file is the build's index.html, as the server's history fallback does.
 */
export async function serveBuild(context: BrowserContext | Page, dir: string): Promise<void> {
  const prefix = "/console/";
  const root = join(UI, dir);
  const gallery = existsSync(join(root, "gallery.html"));
  if (!gallery && !existsSync(join(root, "index.html"))) {
    throw new Error(`${dir} is not built: run npm run build:pseudo && npm run build:gallery`);
  }
  await context.route(`${NORTH}${prefix}**`, async (route: Route) => {
    const url = new URL(route.request().url());
    const rel = url.pathname.slice(prefix.length);
    let file = join(root, rel);
    if (!rel || !existsSync(file) || !extname(rel)) {
      file = join(root, gallery ? "gallery.html" : "index.html");
      if (!gallery) {
        // Keep the server's stripe: it writes data-stripe before first paint.
        const real = await route.fetch();
        const stripe = /data-stripe="([a-z]+)"/.exec(await real.text())?.[1] ?? "accent";
        const html = readFileSync(file, "utf8").replace(/data-stripe="[a-z]+"/, `data-stripe="${stripe}"`);
        return route.fulfill({ status: 200, body: html, headers: { "content-type": TYPES[".html"] as string, "content-security-policy": CSP } });
      }
    }
    return route.fulfill({
      status: 200,
      body: readFileSync(file),
      headers: { "content-type": TYPES[extname(file)] ?? "application/octet-stream", "content-security-policy": CSP },
    });
  });
}

/** An API answer substituted in the browser, for a state one server cannot produce. */
export type Substitute =
  | { kind: "hang" }
  | { kind: "answer"; status: number; body: unknown };

export async function substitute(page: Page, path: string, s: Substitute): Promise<void> {
  await page.route(`${NORTH}${path}**`, async (route) => {
    if (s.kind === "hang") return; // never answered: the loading state, held
    await route.fulfill({ status: s.status, contentType: "application/json", body: JSON.stringify(s.body) });
  });
}

/**
 * Forces each pseudo-class a gallery cell asks for (data-force), through the
 * browser's own developer protocol, so hover, focus-visible and active are
 * all visible in one screenshot without a stylesheet that imitates them.
 */
export async function forceStates(page: Page): Promise<void> {
  const cdp = await page.context().newCDPSession(page);
  await cdp.send("DOM.enable");
  await cdp.send("CSS.enable");
  const { root } = await cdp.send("DOM.getDocument", { depth: -1 });
  const nodes = async (selector: string) =>
    (await cdp.send("DOM.querySelectorAll", { nodeId: root.nodeId, selector })).nodeIds;
  const force = (nodeId: number, state: string) =>
    cdp.send("CSS.forcePseudoState", {
      nodeId,
      forcedPseudoClasses: state === "focus-visible" ? ["focus", "focus-visible"] : [state],
    });
  for (const state of ["hover", "focus-visible", "active"]) {
    for (const id of await nodes(`[data-force="${state}"]`)) await force(id, state);
  }
  // A cell whose state belongs to a part it cannot reach: a child button, a parent wrapper.
  for (const id of await nodes("[data-force-child] button")) await force(id, "hover");
  for (const id of await nodes(":has(> [data-force-parent])")) await force(id, "hover");
}

/** The page's own scroll container is main (0049: the page does not scroll). Grow the viewport to show all of it. */
export async function fitContent(page: Page): Promise<void> {
  const height = await page.evaluate(() => {
    const main = document.querySelector("main");
    const extra = main ? main.scrollHeight - main.clientHeight : 0;
    return Math.max(document.documentElement.scrollHeight, window.innerHeight + Math.max(0, extra));
  });
  const vp = page.viewportSize();
  if (vp && height > vp.height) await page.setViewportSize({ width: vp.width, height: Math.min(height, 4000) });
}

/** What differs on every run and is masked in a screenshot: times, generated ids. */
export function volatile(page: Page) {
  return [page.locator("time"), page.locator("[data-volatile]"), page.locator("[data-instance-id]")];
}
