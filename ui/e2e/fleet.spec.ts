// FLEET against the seeded database (0050's acceptance criteria): each
// configuration state as the proxy reported it, a never-reported proxy as its
// own state and never `applied` (M17), a rollout in progress as in progress
// and never a fault (M19), a fetch failure as an outage and never a deny
// (M11), one unreachable proxy as PARTIAL rather than an error page, and the
// table paged through the bounded list (M26) by the answer's own cursor.
// And its health bar (0059): solid segments with no pattern, each wide
// enough one labelled with its status glyph and count, a narrower one still
// drawn and named by the legend, distinct glyphs in greyscale, and mirrored
// in a right-to-left page.

import { expect, test, type BrowserContext, type Locator, type Page } from "@playwright/test";
import { NORTH, TENANT } from "./harness";
import { openScreen } from "./open";
import { SCREENS } from "./screens";

const fleet = SCREENS.find((s) => s.id === "fleet");
if (!fleet) throw new Error("fleet is not registered");

const row = (page: Page, id: string) => page.locator(`tr[data-row="${id}"]`);
/** The tone and the word of the status in a row's cell, by column header. */
async function cell(page: Page, id: string, column: number) {
  const s = row(page, id).locator("> *").nth(column).locator("[data-tone]").first();
  return { tone: await s.getAttribute("data-tone"), text: (await s.innerText()).trim() };
}
const HEALTH = 2;
const CONTRACT = 4;
const CONFIG = 6;

test("shows each configuration state as the proxy reported it", async ({ browser }) => {
  const page = await openScreen(browser, fleet);
  expect(await cell(page, "px-core-01", CONFIG)).toEqual({ tone: "allow", text: "Applied" });
  expect(await cell(page, "px-core-02", CONFIG)).toEqual({ tone: "info", text: "Pending restart" });
  expect(await cell(page, "px-dmz-03", CONFIG)).toEqual({ tone: "warn", text: "Rejected" });
  expect(await cell(page, "px-dmz-04", CONFIG)).toEqual({ tone: "outage", text: "Fetch failed" });
  expect(await cell(page, "px-edge-05", CONFIG)).toEqual({ tone: "info", text: "In progress" });
  await expect(row(page, "px-edge-05").locator("code")).toContainText(["px-edge-05", "edge", "v1", "v2"]);
  // Never reported is its own state, and never applied — even with a document desired.
  expect(await cell(page, "px-edge-06", CONFIG)).toEqual({ tone: "unknown", text: "Not reported" });
  expect(await cell(page, "px-edge-07", CONFIG)).toEqual({ tone: "unknown", text: "Not reported" });

  expect(await cell(page, "px-core-01", HEALTH)).toEqual({ tone: "allow", text: "Healthy" });
  expect(await cell(page, "px-edge-07", HEALTH)).toEqual({ tone: "outage", text: "Unreachable" });
  expect(await cell(page, "px-old-08", HEALTH)).toEqual({ tone: "deny", text: "Revoked" });

  // The detail names what each state carries.
  await row(page, "px-core-02").locator("th button").click();
  const drawer = page.locator("dialog[open]");
  await expect(drawer).toContainText("logging.batch_size");
  await expect(drawer).toContainText("logging.queue_size");
  await page.keyboard.press("Escape");
  await expect(drawer).toHaveCount(0);
  await row(page, "px-dmz-03").locator("th button").click();
  await expect(page.locator("dialog[open] pre")).toContainText("value out of range");
  await page.keyboard.press("Escape");
  await row(page, "px-edge-06").locator("th button").click();
  await expect(page.locator("dialog[open]")).toContainText("never reported its configuration");
  await page.context().close();
});

// A proxy of the wrong contract build (0064): this server serves one policy
// vocabulary, so a proxy that declared another is refused every session —
// Unsupported beside the number it declared, an outage and never a deny
// (M11), in the table, its detail, and the summary's count. A proxy on the
// served vocabulary shows its number and no status.
test("names a proxy of the wrong contract build Unsupported", async ({ browser }) => {
  const page = await openScreen(browser, fleet);
  expect(await cell(page, "px-edge-05", CONTRACT)).toEqual({ tone: "outage", text: "Unsupported" });
  await expect(row(page, "px-core-01").locator("> *").nth(CONTRACT).locator("[data-tone]")).toHaveCount(0);
  await expect(page.getByText("Unsupported contract")).toBeVisible();
  await row(page, "px-edge-05").locator("th button").click();
  const drawer = page.locator("dialog[open]");
  await expect(drawer.locator("[data-tone=outage]").filter({ hasText: "Unsupported" })).toHaveCount(1);
  await expect(drawer).toContainText("every session through this proxy is refused");
  await page.context().close();
});

test("one unreachable proxy is partial, with the gap named, never an error page", async ({ browser }) => {
  const page = await openScreen(browser, fleet);
  const frame = page.locator("main [data-state=partial]");
  await expect(frame).toHaveCount(1);
  await expect(page.locator("[data-notice=partial]")).toContainText("px-edge-07");
  await expect(page.locator("main [data-state=error]")).toHaveCount(0);
  await expect(page.locator("main table tbody tr")).toHaveCount(9);
  await page.context().close();
});

test("pages through the bounded list by its own cursor", async ({ browser }) => {
  const page = await openScreen(browser, { ...fleet, path: "/console/fleet?limit=4" });
  const asked: string[] = [];
  page.on("request", (r) => {
    if (r.url().includes("/proxies?")) asked.push(new URL(r.url()).search);
  });
  const ids = () => page.locator("main table tbody tr").evaluateAll((rs) => rs.map((r) => r.getAttribute("data-row")));
  expect(await ids()).toEqual(["px-core-01", "px-core-02", "px-dmz-03", "px-dmz-04"]);
  const pager = page.getByRole("navigation", { name: "Pages" });
  await expect(pager).toContainText("continues");
  await expect(page.locator("main")).toContainText("This is one page of the fleet");
  await pager.getByRole("button", { name: "Next page" }).click();
  await expect.poll(ids).toEqual(["px-edge-05", "px-edge-06", "px-edge-07", "px-old-08"]);
  expect(asked.some((q) => q.includes("after="))).toBe(true);
  await pager.getByRole("button", { name: "Next page" }).click();
  await expect.poll(ids).toEqual(["px-spare-09"]);
  await expect(pager).toContainText("end of the list");
  await expect(pager.getByRole("button", { name: "Next page" })).toBeDisabled();
  await pager.getByRole("button", { name: "Previous page" }).click();
  await expect.poll(ids).toEqual(["px-edge-05", "px-edge-06", "px-edge-07", "px-old-08"]);
  await page.context().close();
});

test("filters live in the URL under /console/", async ({ browser }) => {
  const page = await openScreen(browser, fleet);
  await page.getByLabel("Configuration", { exact: true }).selectOption("rejected");
  await expect(page).toHaveURL(/\/console\/fleet\?config=rejected$/);
  await expect(page.locator("main table tbody tr")).toHaveCount(1);
  await page.reload();
  await fleet.ready(page);
  await expect(page.locator("main table tbody tr")).toHaveCount(1);
  await page.getByRole("button", { name: /Remove the filter/ }).click();
  await expect(page.locator("main table tbody tr")).toHaveCount(9);
  await page.context().close();
});

test("a deny and an outage are different colours and different words (M11)", async ({ browser }) => {
  const gallery = SCREENS.find((s) => s.id === "gallery-feedback");
  if (!gallery) throw new Error("gallery-feedback is not registered");
  for (const theme of ["light", "dark"] as const) {
    const page = await openScreen(browser, gallery, { theme });
    const outage = page.locator('[data-state=error][data-kind="outage"]');
    const refused = page.locator('[data-state=error][data-kind="refused"]');
    const look = (l: typeof outage) =>
      l.evaluate((el) => ({
        border: getComputedStyle(el).borderInlineStartColor,
        glyph: getComputedStyle(el.querySelector("svg") as Element).color,
        word: (el.querySelector("[data-tone]") as HTMLElement).innerText.trim(),
      }));
    const o = await look(outage);
    const r = await look(refused);
    expect(o.border).not.toBe(r.border);
    expect(o.glyph).not.toBe(r.glyph);
    expect(o.word).toBe("Outage");
    expect(r.word).toBe("Refused");
    await expect(outage).not.toContainText(/denied|refused/i);
    await page.context().close();
  }
});

test("an error code the catalogue does not know names the code and the correlation id", async ({ browser }) => {
  const page = await openScreen(browser, fleet, { state: "error" });
  await expect(page.locator("[data-state=error]")).toContainText("This part of Hoplock Control is unavailable");
  await page.context().close();
  const gallery = SCREENS.find((s) => s.id === "gallery-feedback");
  if (!gallery) throw new Error("gallery-feedback is not registered");
  const g = await openScreen(browser, gallery);
  const unknown = g.locator('[data-error-code="quota_exhausted"]');
  await expect(unknown).toContainText("quota_exhausted");
  await expect(unknown).toContainText("corr-55e0aa13");
  await g.context().close();
});

// --- The health bar (0059) ---------------------------------------------------

/** Each segment's status, as DESIGN.md §6 names its tone: the bar's vocabulary. */
const SEGMENT_TONE: Record<string, string> = { live: "allow", stale: "warn", unreachable: "outage", pending: "info", revoked: "deny" };

const bar = (page: Page) => page.locator("main figure svg[role=img]");

/** The server's fleet summary with these counts in place of its own, in the browser. */
function withSummary(counts: Record<string, number>) {
  return {
    before: async (context: BrowserContext) => {
      await context.route(`${NORTH}/api/v1/tenants/${TENANT}/instance/health**`, async (route) => {
        const real = await route.fetch();
        const body = await real.json();
        for (const f of body.fleet ?? []) if (f.summary) f.summary = { ...f.summary, ...counts };
        await route.fulfill({ response: real, contentType: "application/json", body: JSON.stringify(body) });
      });
    },
  };
}

/** A length token as the bar resolves it, in pixels. */
const token = (page: Page, name: string) =>
  bar(page).evaluate((el, n) => Number.parseFloat(getComputedStyle(el).getPropertyValue(n)), name);

interface Drawn {
  segment: string;
  x: number;
  w: number;
  fill: string;
  label: { tone: string | null; text: string; glyph: { x: number; w: number } } | null;
}

/** Every segment the bar draws, left to right on the page, with its label if it carries one. */
async function drawn(page: Page): Promise<Drawn[]> {
  const out = await bar(page)
    .locator("g[data-segment]")
    .evaluateAll((gs) =>
      gs.map((g) => {
        const rect = g.querySelector("rect") as SVGRectElement;
        const box = rect.getBoundingClientRect();
        const label = g.querySelector("foreignObject div");
        const glyph = label?.querySelector("svg[data-tone]");
        const gb = glyph?.getBoundingClientRect();
        return {
          segment: g.getAttribute("data-segment") ?? "",
          x: box.x,
          w: box.width,
          fill: getComputedStyle(rect).fill,
          label:
            label && glyph && gb
              ? { tone: glyph.getAttribute("data-tone"), text: (label.textContent ?? "").trim(), glyph: { x: gb.x, w: gb.width } }
              : null,
        };
      }),
    );
  return out.sort((a, b) => a.x - b.x);
}

/** The legend's count and glyph colour for each state. */
async function legend(page: Page): Promise<Record<string, { count: string; colour: string }>> {
  return page.locator("main figcaption li[data-segment]").evaluateAll((lis) =>
    Object.fromEntries(
      lis.map((li) => [
        li.getAttribute("data-segment") ?? "",
        {
          count: (li.lastElementChild?.textContent ?? "").trim(),
          colour: getComputedStyle(li.querySelector("svg") as Element).color,
        },
      ]),
    ),
  );
}

for (const theme of ["light", "dark"] as const) {
  test(`the health bar is solid segments, each wide enough one labelled with its glyph and count, ${theme}`, async ({ browser }) => {
    const page = await openScreen(browser, fleet, { theme });
    // No pattern, and no fill that refers to one.
    await expect(bar(page).locator("pattern")).toHaveCount(0);
    const urls = await bar(page)
      .locator("*")
      .evaluateAll((els) => els.filter((el) => /url\(/.test(`${el.getAttribute("fill") ?? ""} ${getComputedStyle(el).fill}`)).length);
    expect(urls).toBe(0);

    // The seed's fleet: seven live, one never heard from, one revoked.
    const segments = await drawn(page);
    const keys = await legend(page);
    expect(segments.map((s) => s.segment)).toEqual(["live", "unreachable", "revoked"]);
    const labelMin = await token(page, "--chart-label-min");
    const gap = await token(page, "--chart-segment-gap");
    expect(labelMin).toBeGreaterThan(0);
    expect(gap).toBeGreaterThan(0);
    for (const s of segments) {
      // Solid in its status hue: the very colour its legend glyph is drawn in.
      expect(s.fill, s.segment).toBe(keys[s.segment]?.colour);
      expect(s.w, s.segment).toBeGreaterThanOrEqual(labelMin);
      expect(s.label, s.segment).not.toBeNull();
      expect(s.label?.tone, s.segment).toBe(SEGMENT_TONE[s.segment]);
      expect(s.label?.text, s.segment).toBe(keys[s.segment]?.count);
    }
    // Consecutive segments are separated by the gap, and the bar ends at its track.
    for (let i = 1; i < segments.length; i++) {
      const prev = segments[i - 1] as Drawn;
      expect((segments[i] as Drawn).x - (prev.x + prev.w)).toBeCloseTo(gap, 1);
    }
    const track = await bar(page).evaluate((el) => el.getBoundingClientRect());
    const last = segments.at(-1) as Drawn;
    expect(last.x + last.w).toBeLessThanOrEqual(track.x + track.width + 0.5);
    expect(segments[0]?.x).toBeCloseTo(track.x, 1);
    await page.context().close();
  });
}

test("a segment too narrow for its label is still drawn, and the legend names it", async ({ browser }) => {
  // One revoked proxy of five hundred and one, substituted in the browser.
  const page = await openScreen(browser, fleet, withSummary({ live: 500, stale: 0, unreachable: 0, pending: 0, revoked: 1 }));
  const segments = await drawn(page);
  expect(segments.map((s) => s.segment)).toEqual(["live", "revoked"]);
  const [live, revoked] = segments as [Drawn, Drawn];
  const minWidth = await token(page, "--chart-segment-min");
  expect(minWidth).toBeGreaterThan(0);
  expect(revoked.w).toBeCloseTo(minWidth, 1);
  expect(revoked.label).toBeNull();
  expect(live.label?.text).toBe("500");
  const keys = await legend(page);
  expect(keys.revoked?.count).toBe("1");
  await expect(page.locator('main figcaption li[data-segment="revoked"]')).toContainText("Revoked");
  const track = await bar(page).evaluate((el) => el.getBoundingClientRect());
  expect(revoked.x + revoked.w).toBeLessThanOrEqual(track.x + track.width + 0.5);
  await page.context().close();
});

/** Which pixels of a glyph's box differ from the ground around it, as drawn: a shape, whatever its colours. */
async function shapeOf(page: Page, glyph: Locator): Promise<{ mask: boolean[][]; contrast: number }> {
  const box = await glyph.boundingBox();
  if (!box) throw new Error("the glyph is not drawn");
  const margin = 2;
  const png = await page.screenshot({
    clip: { x: box.x - margin, y: box.y - margin, width: box.width + 2 * margin, height: box.height + 2 * margin },
  });
  return page.evaluate(async (b64) => {
    const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
    const image = await createImageBitmap(new Blob([bytes], { type: "image/png" }));
    const canvas = new OffscreenCanvas(image.width, image.height);
    const ctx = canvas.getContext("2d") as OffscreenCanvasRenderingContext2D;
    ctx.drawImage(image, 0, 0);
    const { data, width, height } = ctx.getImageData(0, 0, image.width, image.height);
    const grey = (x: number, y: number) => {
      const i = (y * width + x) * 4;
      return ((data[i] ?? 0) + (data[i + 1] ?? 0) + (data[i + 2] ?? 0)) / 765;
    };
    const ground = grey(0, 0);
    let contrast = 0;
    for (let y = 0; y < height; y++) for (let x = 0; x < width; x++) contrast = Math.max(contrast, Math.abs(grey(x, y) - ground));
    const mask: boolean[][] = [];
    for (let y = 0; y < height; y++) {
      const row: boolean[] = [];
      for (let x = 0; x < width; x++) row.push(Math.abs(grey(x, y) - ground) > contrast / 2);
      mask.push(row);
    }
    return { mask, contrast };
  }, png.toString("base64"));
}

/** The share of pixels two shapes differ in, at the alignment that makes them most alike. */
function unlike(a: boolean[][], b: boolean[][]): number {
  const h = Math.min(a.length, b.length);
  const w = Math.min(a[0]?.length ?? 0, b[0]?.length ?? 0);
  let best = Infinity;
  for (let dy = -4; dy <= 4; dy++) {
    for (let dx = -4; dx <= 4; dx++) {
      let n = 0;
      for (let y = 0; y < h; y++) for (let x = 0; x < w; x++) if ((a[y]?.[x] ?? false) !== (b[y + dy]?.[x + dx] ?? false)) n++;
      best = Math.min(best, n);
    }
  }
  return best / (h * w);
}

test("in greyscale, each labelled segment still carries a distinct glyph shape", async ({ browser }) => {
  // All five states, each wide enough for its label, substituted in the browser.
  for (const theme of ["light", "dark"] as const) {
    const page = await openScreen(browser, fleet, {
      theme,
      scale: 4,
      ...withSummary({ live: 3, stale: 2, unreachable: 2, pending: 2, revoked: 2 }),
    });
    // CSSOM, which the console's style-src allows; a <style> it would refuse.
    await page.evaluate(() => {
      document.documentElement.style.filter = "grayscale(1)";
    });
    const segments = await drawn(page);
    expect(segments.map((s) => s.segment).sort()).toEqual(["live", "pending", "revoked", "stale", "unreachable"]);
    const shapes: Record<string, boolean[][]> = {};
    for (const s of segments) {
      expect(s.label, s.segment).not.toBeNull();
      const glyph = bar(page).locator(`g[data-segment="${s.segment}"] foreignObject svg[data-tone]`);
      const { mask, contrast } = await shapeOf(page, glyph);
      // Seen against its own segment without colour.
      expect(contrast, `${theme} ${s.segment}: glyph against its segment in greyscale`).toBeGreaterThan(0.3);
      // The same shape as the legend draws for that state (in its hue, on the
      // surface), so the measure below is seen to call one shape alike: these
      // differ in under 0.1% of pixels, the closest two glyphs (the filled
      // circle and the filled square) in over 5%.
      const key = await shapeOf(page, page.locator(`main figcaption li[data-segment="${s.segment}"] svg`));
      expect(unlike(mask, key.mask), `${theme} ${s.segment}: its legend's glyph`).toBeLessThan(0.01);
      shapes[s.segment] = mask;
    }
    // ...and no two segments' glyphs alike.
    const names = Object.keys(shapes);
    for (const a of names) {
      for (const b of names) {
        if (a >= b) continue;
        expect(unlike(shapes[a] as boolean[][], shapes[b] as boolean[][]), `${theme}: ${a} and ${b}`).toBeGreaterThan(0.03);
      }
    }
    await page.context().close();
  }
});

test("en-XB mirrors the health bar, each label at its segment's inline start", async ({ browser }) => {
  const at = async (locale: "en-XA" | "en-XB") => {
    const page = await openScreen(browser, fleet, { locale, noKerning: true });
    const track = await bar(page).evaluate((el) => {
      const r = el.getBoundingClientRect();
      return { x: r.x, w: r.width };
    });
    const segments = await drawn(page);
    await page.context().close();
    return { track, segments };
  };
  const xa = await at("en-XA");
  const xb = await at("en-XB");
  // The page mirrors the bar's place; the bar mirrors its segments within it.
  const mirror = (x: number, w: number) => xb.track.x + xa.track.w - (x - xa.track.x) - w;
  expect(xb.track.w).toBeCloseTo(xa.track.w, 0);
  expect(xb.segments.map((s) => s.segment)).toEqual([...xa.segments.map((s) => s.segment)].reverse());
  for (const a of xa.segments) {
    const b = xb.segments.find((s) => s.segment === a.segment) as Drawn;
    expect(b.x, a.segment).toBeCloseTo(mirror(a.x, a.w), 0);
    expect(b.w, a.segment).toBeCloseTo(a.w, 1);
    expect(a.label && b.label, a.segment).toBeTruthy();
    if (a.label && b.label) {
      // The glyph opens the label from the inline start: the segment's left in en-XA, its right in en-XB.
      expect(a.label.glyph.x - a.x, a.segment).toBeCloseTo(b.x + b.w - (b.label.glyph.x + b.label.glyph.w), 0);
    }
  }
});
