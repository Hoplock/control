// STREAM GAPS AND RETENTION against the seeded database (0057's acceptance
// criteria). A session's timeline shows an `evicted` and a `refused` gap at
// their spans, as different states and neither as denied; two reports of one
// cause are summed; the session reads as partial, never complete; replay
// across a hole shows it and joins nothing across it; a gap that belongs to
// no session is on the per-proxy view. With the fixture's archive registered
// and behind (fixture/host/retention.go), the audit view shows its lag, a
// pass by sequence beyond it is a refusal that deleted nothing, and the pass
// by instant it offers is held back by the archive; with none registered, no
// lag is shown. The data is fixture/audit.ts's.

import { expect, test, type Browser, type Page } from "@playwright/test";
import { stateFile, substitute, TENANT } from "./harness";
import { BARE } from "./screens";

const API = `/api/v1/tenants/${TENANT}`;
const SESSION = "/console/audit/sessions/sess-e2e-gaps/replay";

async function open(browser: Browser, path: string, role: "admin" | "auditor" = "admin", before?: (page: Page) => Promise<void>): Promise<Page> {
  const context = await browser.newContext({ storageState: stateFile(role), viewport: { width: 1280, height: 900 }, locale: "en-US", timezoneId: "UTC" });
  const page = await context.newPage();
  if (before) await before(page);
  await page.goto(path);
  return page;
}

const main = (page: Page) => page.locator("main#content");
const event = (page: Page, id: string) => main(page).locator(`ol[aria-label] li[data-event="${id}"]`);
const settled = (page: Page) => page.waitForFunction(() => document.querySelectorAll("main#content [aria-busy=true]").length === 0);

test.describe("a session's holes", () => {
  test("its timeline shows an evicted and a refused gap at their spans, as different states, neither denied", async ({ browser }) => {
    const page = await open(browser, SESSION);
    await expect(event(page, "rec-e2e-gap-refused")).toBeVisible();
    await settled(page);
    const order = await main(page).locator("ol[aria-label] li[data-event]").evaluateAll((els) => els.map((e) => e.getAttribute("data-event")));
    // Each gap where its span starts, between the records either side of the hole …
    expect(order).toEqual([
      "rec-e2e-gaps-01",
      "rec-e2e-gaps-02",
      "rec-e2e-gaps-03",
      "rec-e2e-gaps-04",
      "rec-e2e-gap-evicted-1",
      "rec-e2e-gaps-05",
      "rec-e2e-gap-refused",
      "rec-e2e-gap-evicted-2",
      "rec-e2e-gaps-06",
      "rec-e2e-gaps-07",
    ]);
    // … at the instant its span starts, never at its report's, which came after the session ended.
    await expect(event(page, "rec-e2e-gap-evicted-1").locator("time").first()).toHaveAttribute("datetime", "2026-11-01T09:00:30.000Z");
    await expect(event(page, "rec-e2e-gap-refused").locator("time").first()).toHaveAttribute("datetime", "2026-11-01T09:04:30.120Z");
    // Two different states: evicted an outage, refused at ingest neither a deny nor an outage.
    await expect(event(page, "rec-e2e-gap-evicted-1").locator("[data-gap-status]")).toHaveAttribute("data-gap-status", "evicted");
    await expect(event(page, "rec-e2e-gap-evicted-1").locator('[data-tone="outage"]').first()).toBeVisible();
    await expect(event(page, "rec-e2e-gap-refused").locator("[data-gap-status]")).toHaveAttribute("data-gap-status", "refusedAtIngest");
    await expect(event(page, "rec-e2e-gap-refused").locator('[data-tone="warn"]').first()).toBeVisible();
    await expect(event(page, "rec-e2e-gap-refused").locator('[data-tone="outage"]')).toHaveCount(0);
    for (const id of ["rec-e2e-gap-evicted-1", "rec-e2e-gap-evicted-2", "rec-e2e-gap-refused"]) {
      await expect(event(page, id).locator('[data-tone="deny"]')).toHaveCount(0);
      await expect(event(page, id)).not.toContainText(/denied/i);
    }
    await expect(event(page, "rec-e2e-gap-refused").locator("[data-gap-critical]")).toBeVisible();
    await page.context().close();
  });

  test("is partial, never complete, and sums two reports of one cause", async ({ browser }) => {
    const page = await open(browser, SESSION);
    const notice = main(page).locator("[data-state=partial] [data-notice=partial]");
    await expect(notice).toContainText("52 records of this session never reached this deployment, in 2 reports");
    await expect(notice).toContainText("2 records of this session reached this deployment and it refused them itself, in 1 report");
    await expect(notice).toContainText("1 of them was critical");
    await expect(main(page).locator("[data-loss=none]")).toHaveCount(0);
    await page.context().close();
  });

  test("a step onto the refused hole shows this server's own answer and the records it refused", async ({ browser }) => {
    const page = await open(browser, `${SESSION}?step=rec-e2e-gap-refused`);
    const detail = main(page).locator('[data-gap-detail="rec-e2e-gap-refused"]');
    await expect(detail.locator("[data-gap-status]").first()).toHaveAttribute("data-gap-status", "refusedAtIngest");
    await expect(detail).toContainText("Refused at ingest");
    await expect(detail.locator("[data-gap-answer]")).toContainText("invalid_record");
    await expect(detail.locator("[data-gap-answer]")).toContainText("record 2 (rec-px-0412) is malformed");
    await expect(detail).toContainText("rec-px-0411");
    await expect(detail).toContainText("rec-px-0412");
    await expect(detail.locator("[data-gap-critical]")).toBeVisible();
    await expect(detail.locator('[data-tone="deny"], [data-tone="outage"]')).toHaveCount(0);
    await page.context().close();
  });

  test("replay across a hole shows it, and never joins the capture on either side", async ({ browser }) => {
    const page = await open(browser, SESSION);
    const terminal = main(page).locator("[role=region]");
    await expect(terminal.locator("[data-stream-gap]")).toHaveCount(3);
    await settled(page);
    const pieces = await terminal.locator(":scope > *").evaluateAll((els) =>
      els.map((e) => (e.tagName === "PRE" ? `text:${(e.textContent ?? "").split("\n")[0]}` : `hole:${e.getAttribute("data-stream-gap")}`)),
    );
    expect(pieces).toEqual([
      expect.stringMatching(/^text:/),
      "hole:evicted",
      "text:worker 9 started",
      "hole:refused",
      "hole:evicted",
      "text:exit",
    ]);
    // The text before the first hole ends there: nothing after it was spliced on.
    await expect(terminal.locator("pre").first()).toContainText("worker 2 started");
    await expect(terminal.locator("pre").first()).not.toContainText("worker 9 started");
    // Stepping to just before the hole shows no hole yet.
    await page.goto(`${SESSION}?step=rec-e2e-gaps-04`);
    await expect(terminal.locator("pre")).toContainText("worker 2 started");
    await expect(terminal.locator("[data-stream-gap]")).toHaveCount(0);
    await page.context().close();
  });

  test("a session the proxy reported nothing missing from is not called complete", async ({ browser }) => {
    const page = await open(browser, "/console/audit/sessions/sess-e2e-allow/replay");
    await expect(main(page).locator("[data-loss=none]")).toContainText("a chain that verifies does not prove nothing was lost");
    await expect(main(page)).not.toContainText(/nothing (is )?missing/i);
    await page.context().close();
  });
});

test.describe("the per-proxy view", () => {
  test("shows a gap that belongs to no session on its proxy's stream, placed by span", async ({ browser }) => {
    const page = await open(browser, "/console/audit/gaps?proxy=px-dmz-03");
    const stream = main(page).locator('[data-stream="proxy:px-dmz-03"]');
    const gap = stream.locator('li[data-event="rec-e2e-gap-sessionless"]');
    await expect(gap).toBeVisible();
    await expect(gap.locator("[data-gap-sessionless]")).toBeVisible();
    await expect(gap.locator("time").first()).toHaveAttribute("datetime", "2026-11-01T07:00:00.000Z");
    await expect(gap.locator("[data-gap-status]")).toHaveAttribute("data-gap-status", "evicted");
    await expect(main(page).locator("[data-stream]")).toHaveCount(1);
    await page.context().close();
  });

  test("filters to the gaps that belong to no session", async ({ browser }) => {
    const page = await open(browser, "/console/audit/gaps?scope=sessionless");
    await expect(main(page).locator("[data-stream] li[data-event]")).toHaveCount(1);
    await expect(main(page).locator('li[data-event="rec-e2e-gap-sessionless"]')).toBeVisible();
    await page.context().close();
  });

  test("places each proxy's gaps by span, whatever order they were reported in", async ({ browser }) => {
    const page = await open(browser, "/console/audit/gaps?proxy=px-core-01");
    const events = main(page).locator('[data-stream="proxy:px-core-01"] li[data-event]');
    await expect(events).toHaveCount(3);
    const order = await events.evaluateAll((els) => els.map((e) => e.getAttribute("data-event")));
    expect(order).toEqual(["rec-e2e-gap-evicted-1", "rec-e2e-gap-refused", "rec-e2e-gap-evicted-2"]);
    await page.context().close();
  });

  test("a gap report opened from the trail is its facts, never a deny", async ({ browser }) => {
    const page = await open(browser, "/console/audit/records?record=rec-e2e-gap-evicted-1");
    const facts = page.locator('dialog[open] [data-gap-facts="rec-e2e-gap-evicted-1"]');
    await expect(facts).toHaveAttribute("data-gap-status", "evicted");
    await expect(facts).toContainText("40 records missing");
    await expect(facts.locator('[data-tone="deny"]')).toHaveCount(0);
    await page.context().close();
  });
});

test.describe("the archive and retention", () => {
  test("with an archive registered and behind, the audit view shows its lag", async ({ browser }) => {
    const page = await open(browser, "/console/audit/retention");
    const archive = main(page).locator('[data-archive="registered"]');
    await expect(archive).toContainText("e2e-archive");
    await expect(archive.locator("[data-lag-state]")).toHaveAttribute("data-lag-state", "behind");
    await expect(archive.locator('[data-tone="outage"], [data-tone="deny"]')).toHaveCount(0);
    await page.context().close();
  });

  test("a pass by sequence beyond the archive is a refusal that deleted nothing, and the pass by instant is held back", async ({ browser }) => {
    const page = await open(browser, "/console/audit/retention");
    const passes = async () => {
      const res = await page.request.get(`${API}/audit/records?event=operator.audit.retained&limit=100`);
      return ((await res.json()) as { records: unknown[] }).records.length;
    };
    const before = await passes();
    const form = main(page).locator("[data-retention-form] form");
    await form.locator('input[name="stream"]').fill("proxy:px-core-01");
    await form.locator('input[name="through_seq"]').fill("3");
    await form.locator("[data-retention-run]").click();
    // It deletes records, so it confirms by the stream's name: a near miss is refused.
    const dialog = page.locator("dialog[open]");
    const confirm = dialog.locator("form button[type=submit]");
    await dialog.locator("[data-confirm-input]").fill("proxy:px-core-0");
    await expect(confirm).toBeDisabled();
    await dialog.locator("[data-confirm-input]").fill("proxy:px-core-01");
    await confirm.click();
    await expect(dialog).toHaveCount(0);

    const refused = main(page).locator('[data-retention-result="refused"]');
    await expect(refused.locator('[data-error-code="retention_not_archived"]')).toHaveAttribute("data-kind", "refused");
    await expect(refused).toContainText("Nothing was deleted: stream proxy:px-core-01 is archived through sequence 0");
    await expect(refused.locator('[data-tone="outage"]')).toHaveCount(0);
    expect(await passes()).toBe(before);

    // The pass by instant it offers deletes as far as the archive holds.
    await refused.locator("[data-retention-by-instant]").click();
    await expect(form.locator('input[name="before"]')).not.toHaveValue("");
    await form.locator("[data-retention-run]").click();
    await dialog.locator("[data-confirm-input]").fill("proxy:px-core-01");
    await confirm.click();
    const held = main(page).locator('[data-retention-result="held"]');
    await expect(held).toContainText("Held back by the archive");
    await expect(held).toContainText("The archive has confirmed stream proxy:px-core-01 through sequence 0");
    await expect(held.locator('[data-tone="outage"], [data-tone="deny"]')).toHaveCount(0);
    await page.context().close();
  });

  test("with no archive registered, no lag is shown (substituted)", async ({ browser }) => {
    const page = await open(browser, "/console/audit/retention", "admin", (p) => substitute(p, `${API}/extensions`, BARE));
    await expect(main(page).locator("#retention-trail")).toBeVisible();
    await settled(page);
    await expect(main(page).locator("[data-archive]")).toHaveCount(0);
    await expect(main(page).locator("[data-lag-state]")).toHaveCount(0);
    await expect(main(page).locator("[data-retention-form]")).toBeVisible();
    await page.context().close();
  });

  test("the trail says when the archive held a pass back (substituted)", async ({ browser }) => {
    const page = await open(browser, "/console/audit/retention", "admin", (p) =>
      substitute(p, `${API}/audit/records`, {
        kind: "answer",
        status: 200,
        body: {
          truncated: false,
          records: [
            {
              record_id: "rec-e2e-retained",
              stream: "control",
              chain_seq: 41,
              hash: "sha256:e2e",
              session_id: "",
              kind: "policy_decision",
              severity: "info",
              event: "operator.audit.retained",
              attributes: { event: "operator.audit.retained", principal_id: "ada", after: "40", "operator.stream": "proxy:px-core-01", "operator.deleted": "40", "operator.held_by_archive": "true", "operator.archived_through_seq": "40" },
              recorded_at: "2026-11-01T11:00:00Z",
              received_at: "2026-11-01T11:00:00Z",
              body: "{}",
            },
          ],
        },
      }),
    );
    const row = main(page).locator('tr[data-row="rec-e2e-retained"]');
    await expect(row.locator('[data-held-by-archive="true"]')).toContainText("Held back: archived through 40");
    await page.context().close();
  });

  test("a role without audit:retain sees no pass, and the API refuses it", async ({ browser }) => {
    const page = await open(browser, "/console/audit/retention", "auditor");
    await expect(main(page).locator("[data-retention-readonly]")).toBeVisible();
    await expect(main(page).locator("[data-retention-form], [data-retention-run]")).toHaveCount(0);
    const res = await page.request.post(`${API}/audit/retention`, { data: { stream: "proxy:px-core-01", through_seq: 1 } });
    expect(res.status()).toBe(403);
    expect(((await res.json()) as { code: string }).code).toBe("forbidden");
    await page.context().close();
  });
});
