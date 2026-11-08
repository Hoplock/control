// FLEET against the seeded database (0050's acceptance criteria): each
// configuration state as the proxy reported it, a never-reported proxy as its
// own state and never `applied` (M17), a rollout in progress as in progress
// and never a fault (M19), a fetch failure as an outage and never a deny
// (M11), one unreachable proxy as PARTIAL rather than an error page, and the
// table paged through the bounded list (M26) by the answer's own cursor.

import { expect, test, type Page } from "@playwright/test";
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
