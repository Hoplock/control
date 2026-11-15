// THE EMERGENCY RUNBOOK, WALKED (0058). It changes the data — it activates a
// ban, invalidates every cached decision and ends sessions — so it runs in
// the mutations project, after every screenshot, on its own server in CI.
//
// It walks the three steps in order against the fixture's five sessions that
// negotiated aes128-ctr, read two to a page (`?limit=2`), so the negotiated
// list is longer than one page; asserts that the third step is closed until
// the second has answered, that the kill `reason` is shown before it is
// sent, and that every listed session was ended — by the calls the console
// made, each answered, and by the records the server wrote of them.

import { expect, test, type Page, type Request } from "@playwright/test";
import { BANNED, NEGOTIATED } from "./fixture/algorithms";
import { TENANT } from "./harness";
import { openScreen } from "./open";
import { SCREENS } from "./screens";

const API = `/api/v1/tenants/${TENANT}`;
const main = (page: Page) => page.locator("main#content");
const runbook = SCREENS.find((s) => s.id === "policy-runbook");
const REASON = "Your session used a cipher an advisory withdrew. Reconnect to continue.";

test("the runbook drives its three calls in order and ends every listed session", async ({ browser }) => {
  if (!runbook) throw new Error("policy-runbook is not registered");
  const page = await openScreen(browser, { ...runbook, path: `/console/policy/runbook?axis=ciphers&name=${BANNED}&limit=2` });
  const posts: { body: Record<string, unknown>; request: Request }[] = [];
  const pages: string[] = [];
  page.on("request", (r) => {
    const url = new URL(r.url());
    if (r.method() === "POST" && url.pathname === `${API}/revocations`) posts.push({ body: r.postDataJSON() as Record<string, unknown>, request: r });
    if (r.method() === "GET" && url.pathname === `${API}/audit/negotiated`) pages.push(url.search);
  });
  const step = (n: number) => main(page).locator(`[data-step="${n}"]`);

  // Before step 1: steps 2 and 3 wait, and their controls are closed.
  await expect(step(2)).toHaveAttribute("data-step-state", "waiting");
  await expect(main(page).locator("[data-runbook-invalidate]")).toBeDisabled();
  await expect(main(page).locator("[data-runbook-find]")).toBeDisabled();
  await expect(main(page).locator("[data-runbook-kill]")).toBeDisabled();

  // Step 1: the candidate carries the ban everywhere; check it, accept each
  // warning by id, apply it, confirmed by typing the identifier.
  await expect(main(page).locator("[data-runbook-source]")).toHaveValue(new RegExp(`algorithm_bans:\\n  ciphers: \\[${BANNED}\\]`));
  await main(page).locator("[data-runbook-check]").click();
  await main(page).locator("[data-runbook-report]").waitFor();
  const accepts = main(page).locator("[data-runbook-report] [data-accept]");
  expect(await accepts.count()).toBeGreaterThan(0);
  for (const box of await accepts.all()) await box.check();
  await main(page).locator("[data-runbook-apply]").click();
  await page.locator("dialog[open] [data-confirm-input]").fill(BANNED);
  const applied = page.waitForResponse((r) => r.url().endsWith(`${API}/policy/apply`));
  await page.locator("dialog[open] form button[type=submit]").click();
  expect((await applied).status()).toBe(200);
  await expect(step(1)).toHaveAttribute("data-step-state", "done");

  // Still closed: step 3 cannot run before step 2.
  await expect(step(3)).toHaveAttribute("data-step-state", "waiting");
  await expect(main(page).locator("[data-runbook-find]")).toBeDisabled();
  expect(posts).toHaveLength(0);

  // Step 2: cache_invalidate with all, confirmed by typing the tenant.
  await main(page).locator("[data-runbook-invalidate]").click();
  await page.locator("dialog[open] [data-confirm-input]").fill(TENANT);
  await page.locator("dialog[open] form button[type=submit]").click();
  await expect(step(2)).toHaveAttribute("data-step-state", "done");
  await expect(step(2).locator('[data-receipt="cache_invalidate"]')).toBeVisible();
  expect(posts.map((p) => p.body)).toEqual([{ type: "cache_invalidate", all: true }]);

  // Step 3: every page of the negotiated list, two at a time.
  await main(page).locator("[data-runbook-find]").click();
  await expect(main(page).locator("[data-runbook-found]")).toHaveAttribute("data-runbook-found", String(NEGOTIATED.length));
  expect(pages.length).toBe(Math.ceil(NEGOTIATED.length / 2));
  expect(pages.slice(1).every((q) => q.includes("after="))).toBe(true);
  for (const id of NEGOTIATED) await expect(main(page).locator(`tr[data-session="${id}"]`)).toHaveCount(1);

  // The reason is asked for, and shown — beside the form and in the confirm — before it is sent.
  await expect(main(page).locator("[data-runbook-kill]")).toBeDisabled();
  await main(page).locator("[data-runbook-reason]").fill(REASON);
  await expect(main(page).locator("[data-runbook-reason-preview]")).toHaveText(REASON);
  await main(page).locator("[data-runbook-kill]").click();
  await expect(page.locator("dialog[open] [data-runbook-reason-confirm]")).toHaveText(REASON);
  expect(posts).toHaveLength(1);
  await page.locator("dialog[open] [data-confirm-input]").fill(BANNED);
  await page.locator("dialog[open] form button[type=submit]").click();
  await expect(main(page).locator("[data-runbook-finished]")).toBeVisible();

  // Every listed session was ended: by the calls the console made…
  const kills = posts.filter((p) => p.body.type === "session_kill");
  expect(kills.length).toBeGreaterThan(0);
  for (const k of kills) {
    expect(k.body.reason).toBe(REASON);
    expect((await k.request.response())?.status()).toBe(200);
  }
  expect(kills.flatMap((k) => k.body.session_ids as string[]).sort()).toEqual([...NEGOTIATED].sort());
  // …and by the server's own record of each publication.
  const records = await page.request.get(`${API}/audit/records?event=operator.revocation.published&limit=50`);
  expect(records.ok()).toBe(true);
  const text = JSON.stringify(await records.json());
  for (const id of NEGOTIATED) expect(text).toContain(id);
  await page.context().close();
});
