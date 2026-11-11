// 0056's acceptance criteria, against the fixture host's extensions
// (fixture/host/extensions.go): an identity sync that started and an action
// handler whose Start failed, a failing sink and a caught-up one, a report
// pack and a pack that cannot list its reports. Where one server cannot hold
// a state still — a sink a retention pass overtook, a handler that started,
// a sync that did not, Control with nothing registered — the server's own
// answer is changed in the browser (docs/PROTOCOL.md §8). Generating changes
// nothing, so nothing here waits for mutations.spec.ts.

import { readFileSync } from "node:fs";
import { expect, test, type Page } from "@playwright/test";
import { substitute, TENANT } from "./harness";
import { openScreen } from "./open";
import { BARE, COMPLIANCE, SCREENS, type E2EScreen } from "./screens";

const API = `/api/v1/tenants/${TENANT}`;

function screen(id: string): E2EScreen {
  const s = SCREENS.find((x) => x.id === id);
  if (!s) throw new Error(`${id} is not registered`);
  return s;
}

const main = (page: Page) => page.locator("main#content");
const pointOf = (page: Page, code: string) => main(page).locator(`[data-point="${code}"]`);
const sinkOf = (page: Page, provider: string) => main(page).locator(`[data-sink="${provider}"]`);

/** No status in scope is drawn as an outage: a degraded extension is not Control down (M11). */
async function expectNoOutage(scope: ReturnType<Page["locator"]>) {
  await expect(scope.locator('[data-tone="outage"]')).toHaveCount(0);
}

/** Reads the extension listing again with its answer changed. */
async function reread(page: Page, patch: (p: Record<string, unknown>) => Record<string, unknown>) {
  await substitute(page, `${API}/extensions`, {
    kind: "patch",
    patch: (b) => ({ ...(b as object), points: (b as { points: Record<string, unknown>[] }).points.map(patch) }),
  });
  await main(page).locator("[data-refresh]").click();
}

test.describe("extensions", () => {
  test("the identity sync that started is Running; the action handler whose Start failed is Not started, with its kind, and neither is an outage", async ({ browser }) => {
    const page = await openScreen(browser, screen("extensions"));
    const sync = pointOf(page, "IdentitySync");
    await expect(sync).toHaveAttribute("data-point-status", "running");
    await expect(sync.locator('[data-registration="e2e-directory"]')).toBeVisible();
    const actions = pointOf(page, "ActionHandler");
    await expect(actions).toHaveAttribute("data-point-status", "notStarted");
    await expect(actions.locator("[data-start-error]")).toHaveAttribute("data-start-error", "unavailable");
    await expect(actions.locator('[data-tone="warn"]').filter({ hasText: "Not started" })).toBeVisible();
    await expectNoOutage(sync);
    await expectNoOutage(actions);
    // What is in play from outside Control is said first.
    const inPlay = main(page).locator("[data-in-play]");
    for (const p of ["e2e-directory", "e2e-actions", "e2e-siem", "e2e-lake", COMPLIANCE, "example.com/e2e/flaky"]) {
      await expect(inPlay.locator(`[data-extension="${p}"]`)).toHaveCount(1);
    }
  });

  test("a handler that started reads Running, and a sync whose Start failed reads Not started (substituted)", async ({ browser }) => {
    const page = await openScreen(browser, screen("extensions"));
    await reread(page, (p) =>
      p.point === "ActionHandler"
        ? { ...p, started: true, start_error: undefined }
        : p.point === "IdentitySync"
          ? { ...p, started: false, start_error: "internal" }
          : p,
    );
    await expect(pointOf(page, "ActionHandler")).toHaveAttribute("data-point-status", "running");
    await expect(pointOf(page, "IdentitySync")).toHaveAttribute("data-point-status", "notStarted");
    await expect(pointOf(page, "IdentitySync").locator("[data-start-error]")).toHaveAttribute("data-start-error", "internal");
    await expectNoOutage(main(page).locator("[data-point]"));
  });

  test("each sink's lag is on its own row: the failing one failing, the caught-up one caught up", async ({ browser }) => {
    const page = await openScreen(browser, screen("extensions"));
    await expect(sinkOf(page, "e2e-siem").locator("[data-lag-state]")).toHaveAttribute("data-lag-state", "failing");
    await expect(sinkOf(page, "e2e-lake").locator("[data-lag-state]")).toHaveAttribute("data-lag-state", "caughtUp");
    await expectNoOutage(pointOf(page, "AuditSink"));
    // A sink's lag is on the sink, never on the archive's row, which is empty.
    await expect(pointOf(page, "ArchiveStore")).toHaveAttribute("data-point-status", "notRegistered");
    await expect(pointOf(page, "ArchiveStore").locator("[data-lag-state]")).toHaveCount(0);
  });

  test("a sink a retention pass overtook shows its skipped count and what explains it (substituted)", async ({ browser }) => {
    const page = await openScreen(browser, screen("extensions-overtaken"));
    const lake = sinkOf(page, "e2e-lake");
    await expect(lake.locator("[data-lag-state]")).toHaveAttribute("data-skipped", "42");
    await expect(lake.locator('[data-fact="skipped"] dd')).toHaveText("42");
    await expect(lake.locator("[data-retention-gap] a")).toHaveAttribute("href", "/console/audit/records?kind=operator.audit.retained");
    await expect(sinkOf(page, "e2e-siem").locator("[data-lag-state]")).toHaveAttribute("data-lag-state", "failing");
    await expectNoOutage(pointOf(page, "AuditSink"));
    await expect(pointOf(page, "AuditSink").locator('[data-tone="deny"]')).toHaveCount(0);
  });

  test("with nothing registered, every point shows what Control does instead, no sink lag, and nothing broken (substituted)", async ({ browser }) => {
    const page = await openScreen(browser, screen("extensions"));
    await substitute(page, `${API}/extensions`, BARE);
    await main(page).locator("[data-refresh]").click();
    await expect(main(page).locator('[data-in-play="0"]')).toBeVisible();
    for (const code of ["AuditSink", "ActionHandler", "IdentitySync", "ReportProvider", "ArchiveStore"]) {
      await expect(pointOf(page, code)).toHaveAttribute("data-point-status", "notRegistered");
      await expect(pointOf(page, code).locator("[data-absent]")).toBeVisible();
    }
    await expect(main(page).locator("[data-sink], [data-lag-state]")).toHaveCount(0);
    await expect(pointOf(page, "ClusterCoordinator")).toHaveAttribute("data-point-status", "active");
    await expectNoOutage(main(page));
    await expect(main(page).locator('[data-tone="deny"], [data-tone="warn"]')).toHaveCount(0);
  });
});

test.describe("reports", () => {
  test("lists the pack's reports, and the pack that could not answer as unavailable, never as having none", async ({ browser }) => {
    const page = await openScreen(browser, screen("reports"));
    const pack = main(page).locator(`[data-provider="${COMPLIANCE}"]`);
    await expect(pack.locator('[data-report="access-review"]')).toBeVisible();
    await expect(pack.locator('[data-report="privileged-sessions"]')).toBeVisible();
    const flaky = main(page).locator('[data-provider="example.com/e2e/flaky"]');
    await expect(flaky).toHaveAttribute("data-provider-state", "unavailable");
    await expect(flaky.locator("[data-provider-error]")).toHaveAttribute("data-provider-error", "report_provider_unavailable");
    await expect(flaky.locator('[data-tone="outage"]').filter({ hasText: "Unavailable" })).toBeVisible();
    await expect(flaky.locator("[data-no-reports], [data-report]")).toHaveCount(0);
    await expect(main(page).locator("[data-state=partial]")).toBeVisible();
  });

  test("generates a report with a parameter and a format chosen, and saves the file", async ({ browser }) => {
    const page = await openScreen(browser, screen("reports"));
    await main(page).locator('[data-open-report="access-review"]').click();
    await expect(page).toHaveURL(/\/console\/reports\?provider=example\.com%2Fe2e%2Fcompliance&report=access-review$/);
    const form = main(page).locator('form[data-report-form="access-review"]');
    await form.locator('select[name="parameters.scope"]').selectOption("privileged");
    await form.locator('input[name="parameters.owner"]').fill("security-team");
    await form.getByLabel("JSON").check();
    const sent = page.waitForRequest((r) => r.url().endsWith(`${API}/reports/generate`));
    const download = page.waitForEvent("download");
    await form.locator("button[type=submit]").click();
    const body = (await sent).postDataJSON() as Record<string, unknown>;
    expect(body).toMatchObject({ provider: COMPLIANCE, report: "access-review", parameters: { scope: "privileged", owner: "security-team" }, format: "json" });
    const file = await download;
    expect(file.suggestedFilename()).toMatch(/^access-review_\d{8}T\d{6}Z_\d{8}T\d{6}Z\.json$/);
    const saved = JSON.parse(readFileSync((await file.path()) as string, "utf8")) as { scope: string; owner: string; rows: unknown[] };
    expect(saved).toMatchObject({ scope: "privileged", owner: "security-team" });
    expect(saved.rows).toHaveLength(1);
    await expect(form.locator("[data-saved]")).toHaveAttribute("data-saved", file.suggestedFilename());
  });

  test("a required parameter left empty is refused at its field, and nothing is asked", async ({ browser }) => {
    const page = await openScreen(browser, screen("reports-form"));
    const form = main(page).locator('form[data-report-form="access-review"]');
    let asked = false;
    page.on("request", (r) => {
      if (r.url().endsWith("/reports/generate")) asked = true;
    });
    await form.locator("button[type=submit]").click();
    await expect(form.locator('select[name="parameters.scope"]')).toHaveAttribute("aria-invalid", "true");
    expect(asked).toBe(false);
  });

  test("a generation that ends broken is Failed, and nothing is saved", async ({ browser }) => {
    const page = await openScreen(browser, screen("reports"));
    let downloaded = false;
    page.on("download", () => {
      downloaded = true;
    });
    await main(page).locator('[data-open-report="privileged-sessions"]').click();
    const form = main(page).locator('form[data-report-form="privileged-sessions"]');
    await form.locator("button[type=submit]").click();
    await expect(form.locator("[data-failed]")).toHaveAttribute("data-failed", "broken");
    await expect(form.locator('[data-tone="outage"]').filter({ hasText: "Failed" })).toBeVisible();
    await expect(form.locator("[data-saved]")).toHaveCount(0);
    expect(downloaded).toBe(false);
  });

  test("with no pack registered, the audit view is the reporting available, and nothing is broken (substituted)", async ({ browser }) => {
    const page = await openScreen(browser, screen("reports"), { state: "empty" });
    const empty = main(page).locator("[data-state=empty]");
    await expect(empty.locator('a[href="/console/audit/records"]')).toBeVisible();
    await expect(main(page).locator("[data-state=error]")).toHaveCount(0);
    await expect(main(page).locator('[data-tone="outage"], [data-tone="deny"]')).toHaveCount(0);
  });
});
