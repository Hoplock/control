// EXPLAIN AND AUDIT against the seeded database (0051's acceptance criteria):
// an id pasted into Explain gives the whole story, for an allow and a deny; a
// record's deep link opens its explanation; a paged audit query follows `next`
// to its last page and says when an answer was cut (M26); a supervisor's word
// sits beside its `sup-` principal and is linked to nothing (M19, 0048); the
// supervised badge opens the audit view filtered to the supervisor's acts; a
// recorded session replays its capture; and both screens are keyboard paths
// that read at 768px (DESIGN.md §2, §4). The data is fixture/audit.ts's.

import { expect, test, type Browser, type Page } from "@playwright/test";
import { stateFile, TENANT } from "./harness";

async function open(browser: Browser, path: string, width = 1280): Promise<Page> {
  const context = await browser.newContext({
    storageState: stateFile("admin"),
    viewport: { width, height: 900 },
    locale: "en-US",
    timezoneId: "UTC",
  });
  const page = await context.newPage();
  await page.goto(path);
  return page;
}

const main = (page: Page) => page.locator("main#content");
const story = (page: Page) => main(page).locator("article[data-effect]");
const section = (page: Page, id: string) => story(page).locator(`[data-story-section=${id}]`);

async function paste(page: Page, id: string) {
  await page.locator("#explain-id").fill(id);
  await page.locator("#explain-id").press("Enter");
}

test("an id pasted into Explain shows the whole story of an allow", async ({ browser }) => {
  const page = await open(browser, "/console/explain");
  await expect(main(page).locator("[data-state=empty]")).toBeVisible();
  await paste(page, "sess-e2e-allow");
  await expect(page).toHaveURL(/\/console\/explain\/sessions\/sess-e2e-allow$/);
  await expect(story(page)).toHaveAttribute("data-effect", "allow");
  await expect(story(page).locator("header [data-tone]").first()).toHaveAttribute("data-tone", "allow");
  await expect(section(page, "explain-outcome")).toContainText("Allowed by rule ops-db-shell");
  await expect(section(page, "explain-identity")).toContainText("grace");
  await expect(section(page, "explain-identity")).toContainText("sre");
  await expect(section(page, "explain-rule")).toContainText("ops-db-shell");
  await expect(section(page, "explain-mapping")).toContainText("No claim mapping was involved");
  await expect(section(page, "explain-grant")).toContainText("No grant was involved");
  await expect(section(page, "explain-obligations")).toContainText("record-session");
  await expect(section(page, "explain-route")).toContainText("direct");
  await expect(section(page, "explain-route")).toContainText("db-01.core.internal:22");
  const hops = section(page, "explain-hops-path").locator("[role=tree]");
  await expect(hops.locator("[role=treeitem]")).toHaveCount(2);
  await expect(hops).toContainText("px-core-01");
  await expect(hops).toContainText("db-01.core.internal");
  await page.context().close();
});

test("a session across two hops shows each hop's decision, the grant and the path", async ({ browser }) => {
  const page = await open(browser, "/console/explain");
  await paste(page, "sess-e2e-hops");
  await expect(main(page).locator("ol[aria-label] > li")).toHaveCount(2);
  await expect(section(page, "explain-grant")).toContainText("administrator");
  await expect(section(page, "explain-grant")).toContainText("CHG-1042");
  await expect(section(page, "explain-rule")).toContainText("grant.scope=vault-maintenance");
  const hops = section(page, "explain-hops-path").locator("[role=treeitem]");
  await expect(hops).toHaveCount(3);
  await expect(hops.nth(0)).toContainText("px-core-01");
  await expect(hops.nth(1)).toContainText("px-dmz-03");
  await expect(hops.nth(2)).toContainText("vault-01.dmz.internal");
  // The innermost hop opens; the edge's decision is one key away.
  await expect(story(page).locator("h2")).toContainText("px-dmz-03");
  await main(page).locator("ol[aria-label] [data-handle][tabindex='0']").focus();
  await page.keyboard.press("ArrowUp");
  await expect(story(page).locator("h2")).toContainText("px-core-01");
  await expect(page).toHaveURL(/decision=d_/);
  await expect(section(page, "explain-route")).toContainText("nexthop");
  await page.context().close();
});

test("an id pasted into Explain shows the whole story of a deny, never drawn as an outage", async ({ browser }) => {
  const page = await open(browser, "/console/explain");
  await paste(page, "sess-e2e-rule-deny");
  await expect(story(page)).toHaveAttribute("data-effect", "deny");
  await expect(story(page).locator("header [data-tone]").first()).toHaveAttribute("data-tone", "deny");
  await expect(section(page, "explain-outcome")).toContainText("Denied by rule no-shell-on-build-hosts");
  await expect(section(page, "explain-outcome")).toContainText("build hosts are reached by the pipeline only");
  await expect(section(page, "explain-identity")).toContainText("grace");
  await expect(section(page, "explain-rule")).toContainText("target.labels.env=dev");
  await expect(section(page, "explain-mapping")).toBeVisible();
  await expect(section(page, "explain-grant")).toContainText("No grant was involved");
  await expect(section(page, "explain-obligations")).toContainText("no obligations");
  await expect(section(page, "explain-route")).toContainText("nothing was routed");
  await expect(section(page, "explain-hops-path").locator("[role=treeitem]").first()).toContainText("Denied");
  await expect(main(page).locator("[data-tone=outage]")).toHaveCount(0);

  // The default deny: no rule matched, which is a decision too.
  await paste(page, "sess-e2e-deny");
  await expect(page).toHaveURL(/sess-e2e-deny$/);
  await expect(section(page, "explain-outcome")).toContainText("no rule matched");
  await expect(story(page).locator("header [data-tone]").first()).toHaveAttribute("data-tone", "deny");
  await page.context().close();
});

test("an outage reading a story is an outage, never a deny (M11)", async ({ browser }) => {
  const page = await open(browser, "about:blank");
  await page.route("**/api/v1/tenants/*/sessions/*/decisions", (r) =>
    r.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ code: "decision_unexplainable", correlation_id: "corr-e2e-unexplainable" }) }),
  );
  await page.goto("/console/explain/sessions/sess-e2e-allow");
  const error = main(page).locator("[data-state=error]");
  await expect(error).toHaveAttribute("data-kind", "outage");
  await expect(error).toContainText("integrity fault");
  await expect(error.locator("[data-tone=deny]")).toHaveCount(0);
  await page.context().close();
});

test("a deep link from an audit record opens its explanation", async ({ browser }) => {
  const page = await open(browser, "/console/audit/records?session=sess-e2e-allow");
  const row = main(page).locator('tr[data-row="rec-e2e-allow-02"]');
  await row.locator("a[data-explain=decision]").click();
  await expect(page).toHaveURL(/\/console\/explain\/decisions\/d_[a-z0-9]+$/);
  await expect(section(page, "explain-outcome")).toContainText("ops-db-shell");
  await page.goBack();
  // The record's own detail links there too, and to its session's explanation.
  await main(page).locator('tr[data-row="rec-e2e-allow-02"] th button').click();
  const drawer = page.locator("dialog[open]");
  await expect(drawer).toContainText("rec-e2e-allow-02");
  await drawer.locator("a[data-explain=decision]").click();
  await expect(page).toHaveURL(/\/console\/explain\/decisions\/d_/);
  await page.goto("/console/audit/records?session=sess-e2e-deny");
  await main(page).locator('tr[data-row="rec-e2e-deny-01"] a[data-explain=session]').click();
  await expect(page).toHaveURL(/\/console\/explain\/sessions\/sess-e2e-deny$/);
  await expect(story(page)).toHaveAttribute("data-effect", "deny");
  await page.context().close();
});

test("a paged audit query follows next to its last page, and a cut answer says so (M26)", async ({ browser }) => {
  const page = await open(browser, "/console/audit/records?limit=5");
  const all = await page.request.get(`/api/v1/tenants/${TENANT}/audit/records?limit=10000`);
  const total = ((await all.json()) as { records: unknown[] }).records.length;
  expect(total).toBeGreaterThan(10);
  const seen = new Set<string>();
  const where = main(page).locator("nav[aria-label] [aria-live]");
  for (let n = 1; ; n++) {
    // The pager names the page it shows once that page has arrived.
    await expect(where).toContainText(`Page ${n}:`);
    await expect(main(page).locator("table tbody tr[data-row]").first()).toBeVisible();
    const rows = await main(page).locator("table tbody tr[data-row]").evaluateAll((trs) => trs.map((tr) => tr.getAttribute("data-row")));
    for (const r of rows) seen.add(r as string);
    const next = main(page).getByRole("button", { name: "Next page" });
    if (await next.isDisabled()) {
      await expect(where).toContainText("end of the list");
      expect(n).toBe(Math.ceil(total / 5));
      break;
    }
    // A cut page says so, beside its rows and in the counts.
    await expect(main(page).getByText("One page of the matching records")).toBeVisible();
    await expect(main(page).locator("[data-chart=audit-counts] caption")).toContainText("more pages");
    await next.click();
    if (n > 50) throw new Error("the pages do not end");
  }
  expect(seen.size).toBe(total);
  await page.context().close();
});

test("every filter is in the URL under /console/, and a filtered link opens filtered", async ({ browser }) => {
  const page = await open(browser, "/console/audit/records");
  await main(page).getByLabel("Subject", { exact: true }).fill("grace");
  await main(page).getByLabel("Subject", { exact: true }).press("Enter");
  await main(page).getByLabel("Kind").selectOption("command");
  await expect(page).toHaveURL(/\/console\/audit\/records\?(.*&)?subject=grace/);
  await expect(page).toHaveURL(/kind=command/);
  await expect(main(page).locator("table tbody tr[data-row]")).toHaveCount(2);
  const url = page.url();
  const again = await open(browser, url);
  await expect(main(again).locator("table tbody tr[data-row]")).toHaveCount(2);
  await expect(main(again).getByLabel("Subject", { exact: true })).toHaveValue("grace");
  await again.context().close();
  await page.context().close();
});

test("a supervisor's word sits beside its principal, linked to nothing; a record without it shows the principal alone", async ({ browser }) => {
  const page = await open(browser, "/console/audit/records?principal=sup-acme-ops");
  const rows = main(page).locator("table tbody tr[data-row]");
  await expect(rows).toHaveCount(2);
  const named = rows.filter({ has: page.locator("[data-acting-operator]") });
  await expect(named).toHaveCount(1);
  const word = named.locator("[data-acting-operator]");
  await expect(word).toContainText("the supervisor's word");
  await expect(word).toContainText("Dana Ruiz");
  // In the same cell as the sup- principal, and no link anywhere near it.
  await expect(word.locator("xpath=..")).toContainText("sup-acme-ops");
  await expect(word.locator("a")).toHaveCount(0);
  await expect(word.locator("xpath=ancestor::a")).toHaveCount(0);
  const plain = rows.filter({ hasNot: page.locator("[data-acting-operator]") });
  await expect(plain).toHaveCount(1);
  await expect(plain).toContainText("sup-acme-ops");
  await expect(plain).not.toContainText("Dana Ruiz");
  await page.context().close();
});

test("the supervised badge opens the audit view filtered to the supervisor's acts", async ({ browser }) => {
  const page = await open(browser, "/console/deployment");
  const badge = page.locator("[data-testid=supervisor-badge]");
  await expect(badge).toContainText("Acme Managed Ops");
  await badge.click();
  await expect(page).toHaveURL(/\/console\/audit\/records\?principal=sup-acme-ops$/);
  await expect(main(page).locator("[data-principal-notice]")).toContainText("What a supervisor did to this deployment");
  const actors = main(page).locator("table tbody tr[data-row] td:nth-of-type(3)");
  await expect(actors).toHaveCount(2);
  for (const a of await actors.all()) await expect(a).toContainText("sup-acme-ops");
  await page.context().close();
});

test("the blocked-command view joins a blocked command to the access that let the session in", async ({ browser }) => {
  const page = await open(browser, "/console/audit/blocked-commands?label=env%3Dprod");
  const row = main(page).locator('tr[data-row="rec-e2e-allow-06"]');
  await expect(row).toContainText("systemctl stop postgresql");
  await expect(row).toContainText("ops-db-shell");
  await expect(row).toContainText("env=prod");
  await row.locator("a[data-explain=decision]").click();
  await expect(page).toHaveURL(/\/console\/explain\/decisions\/d_/);
  await page.context().close();
});

test("a recorded session replays its capture, step by step, by keyboard", async ({ browser }) => {
  const page = await open(browser, "/console/audit/sessions/sess-e2e-allow/replay");
  const terminal = main(page).locator("[role=region] pre");
  await expect(terminal).toContainText("48213");
  await expect(terminal).toContainText("this command is not permitted on this route");
  // Escape sequences are the terminal's, not text.
  expect(await terminal.innerText()).not.toContain("[1;32m");
  await main(page).locator("ol[aria-label] [data-handle][tabindex='0']").focus();
  await page.keyboard.press("Home");
  await expect(page).toHaveURL(/step=rec-e2e-allow-01$/);
  await expect(main(page).locator("[aria-live]").first()).toContainText("Step 1 of 8");
  await expect(terminal).toHaveCount(0);
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("ArrowDown");
  await expect(terminal).toContainText("grace@db-01:~$");
  await expect(terminal).not.toContainText("48213");
  await page.context().close();
});

for (const path of ["/console/explain/sessions/sess-e2e-hops", "/console/audit/records", "/console/audit/sessions/sess-e2e-allow/replay"]) {
  test(`reads at 768px without a sideways page scroll: ${path}`, async ({ browser }) => {
    const page = await open(browser, path, 768);
    await expect(main(page).locator("h1")).toBeVisible();
    await page.waitForLoadState("networkidle");
    const overflow = await page.evaluate(() => {
      const m = document.querySelector("main") as HTMLElement;
      return { page: document.documentElement.scrollWidth - document.documentElement.clientWidth, main: m.scrollWidth - m.clientWidth };
    });
    expect(overflow.page).toBeLessThanOrEqual(0);
    expect(overflow.main).toBeLessThanOrEqual(0);
    await page.context().close();
  });
}

test("the record drawer opens and closes from the keyboard", async ({ browser }) => {
  const page = await open(browser, "/console/audit/records?session=sess-e2e-allow");
  await main(page).locator('tr[data-row="rec-e2e-allow-06"] th button').focus();
  await page.keyboard.press("Enter");
  const drawer = page.locator("dialog[open]");
  await expect(drawer).toContainText("block_command");
  await page.keyboard.press("Escape");
  await expect(drawer).toHaveCount(0);
  await expect(page).not.toHaveURL(/record=/);
  await page.context().close();
});
