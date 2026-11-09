// POLICY'S ACCEPTANCE CHECKS THAT CHANGE NOTHING (0053). Against the seeded
// database (fixture/policy.ts: v1 older, v2 active, v3 stored):
//   - a compiler rejection renders from its code and parameters, at the line
//     it names, and never as the server's English;
//   - one the catalogue does not know renders the fallback naming the code
//     and the correlation id of the answer that carried it;
//   - an upload the compiler refuses reports at the form and the source,
//     never by a toast alone, and stores nothing;
//   - an auditor sees the read-only view, and the API refuses activation and
//     rollback (and upload) to them — asserted at the API, not the button;
//   - the deep links hold.
// The flow that changes the data — upload, activate, roll back — is in
// mutations.spec.ts, which runs after every screenshot.

import { expect, test, type Page } from "@playwright/test";
import { candidates } from "./fixture/policy";
import { TENANT } from "./harness";
import { openScreen } from "./open";
import { SCREENS } from "./screens";

const API = `/api/v1/tenants/${TENANT}/policy`;
const screen = (id: string) => {
  const s = SCREENS.find((x) => x.id === id);
  if (!s) throw new Error(`${id} is not registered`);
  return s;
};
const main = (page: Page) => page.locator("main#content");

test("a compiler rejection renders from its code and parameters at the line it names", async ({ browser }) => {
  const page = await openScreen(browser, screen("policy-candidate"));
  const c = candidates();
  await main(page).locator("[data-candidate-source]").fill(c.invalid);
  const answer = page.waitForResponse((r) => r.url().endsWith(`${API}/validate`));
  await main(page).locator("[data-validate]").click();
  const body = (await (await answer).json()) as { rejections: { code: string; message: string }[] };

  const at = (line: number, code: string) => main(page).locator(`tr[data-note-line="${line}"] [data-rejection="${code}"][data-known]`);
  await expect(at(c.lines.group, "match.unknown_group")).toContainText("sre-oncall");
  await expect(at(c.lines.duplicate, "rule.id_duplicate")).toContainText(String(c.lines.group));
  await expect(main(page).locator("[data-rejections]")).toHaveAttribute("data-rejections", "2");
  // The server's English is for policyctl and curl; the console builds its own sentence.
  for (const r of body.rejections) await expect(main(page).getByText(r.message, { exact: false })).toHaveCount(0);
  // The list beside the source goes to a rejection's line.
  await main(page).locator("[data-rejections] ol button").first().click();
  await expect(main(page).locator(`tr[data-line="${c.lines.group}"]`)).toBeFocused();
  await page.context().close();
});

test("a rejection the catalogue does not know names its code and the correlation id", async ({ browser }) => {
  const page = await openScreen(browser, screen("policy-candidate"));
  let correlation = "";
  await page.route(`**${API}/validate`, async (route) => {
    const real = await route.fetch();
    correlation = real.headers()["x-correlation-id"] ?? "";
    const body = (await real.json()) as { rejections: object[] };
    body.rejections.push({ code: "route.from_a_newer_server", rule: "ops-db-shell", line: 3, parameters: { knob: "x" }, message: "x" });
    await route.fulfill({ response: real, body: JSON.stringify(body) });
  });
  await main(page).locator("[data-candidate-source]").fill(candidates().invalid);
  await main(page).locator("[data-validate]").click();
  const unknown = main(page).locator('tr[data-note-line="3"] [data-rejection="route.from_a_newer_server"]');
  await expect(unknown).not.toHaveAttribute("data-known", /.*/);
  await expect(unknown).toContainText("route.from_a_newer_server");
  expect(correlation).not.toBe("");
  await expect(unknown.locator("[data-volatile]")).toContainText(correlation);
  await page.context().close();
});

test("an upload the compiler refuses reports at the form and the source, and stores nothing", async ({ browser }) => {
  const page = await openScreen(browser, screen("policy-candidate"));
  const before = ((await (await page.request.get(`${API}/bundles`)).json()) as { versions: unknown[] }).versions.length;
  await main(page).locator("[data-candidate-source]").fill(candidates().invalid);
  await main(page).locator("[data-upload]").click();
  await expect(main(page).locator("[role=alert][data-error-code=policy_invalid]")).toBeVisible();
  await expect(main(page).locator("[data-rejections] [data-rejection]")).toHaveCount(2);
  // Never a toast: not a success's, not an error's.
  await expect(page.locator('[role=status] > [data-kind="success"], [role=alert] > [data-kind="error"]')).toHaveCount(0);
  const after = ((await (await page.request.get(`${API}/bundles`)).json()) as { versions: unknown[] }).versions.length;
  expect(after).toBe(before);
  await page.context().close();
});

test("an auditor sees the read-only view, and the API refuses activation, rollback and upload", async ({ browser }) => {
  const version = await openScreen(browser, { ...screen("policy-version"), role: "auditor" });
  await expect(main(version).locator("[data-activate]")).toHaveCount(0);
  await expect(main(version).locator("[data-edit-candidate]")).toHaveCount(1);
  for (const v of [3, 1]) {
    const res = await version.request.post(`${API}/bundles/${v}/activate`, { data: { accept: [] } });
    expect(res.status(), `activating v${v} as an auditor`).toBe(403);
    expect(((await res.json()) as { code: string }).code).toBe("forbidden");
  }
  const upload = await version.request.post(`${API}/bundles`, { data: { source: candidates().valid } });
  expect(upload.status()).toBe(403);
  // The active version is unchanged: v2.
  const active = (await (await version.request.get(`${API}/active`)).json()) as { version: number };
  expect(active.version).toBe(2);
  await version.context().close();

  const list = await openScreen(browser, { ...screen("policy"), role: "auditor" });
  for (const v of ["3", "1"]) {
    await list.locator(`tr[data-row="${v}"] button[aria-haspopup="menu"]`).click();
    await expect(list.locator("[role=menu]:popover-open [role=menuitem]")).not.toHaveCount(0);
    await expect(list.locator("[role=menu]:popover-open [role=menuitem][data-danger]")).toHaveCount(0);
    await list.keyboard.press("Escape");
  }
  await list.context().close();

  const candidate = await openScreen(browser, { ...screen("policy-candidate"), role: "auditor" });
  await expect(main(candidate).locator("[data-upload]")).toHaveCount(0);
  await expect(main(candidate).locator("[data-validate]")).toBeEnabled();
  await candidate.context().close();
});

test("the deep links hold", async ({ browser }) => {
  const page = await openScreen(browser, screen("policy-version-changes"));
  await page.goto("/console/policy/versions/3?tab=changes&against=1");
  await expect(main(page).locator("[data-diff-against]")).toHaveValue("1");
  await expect(main(page).locator("[data-diff=lines] figcaption")).toContainText(/v3\u2069? against \u2068?v1/);
  await expect(main(page).locator('[data-rule-change="removed"]')).toContainText("no-shell-on-build-hosts");

  await page.goto("/console/policy/versions/3?tab=simulate&range=24h");
  await expect(main(page).locator("[data-simulate] select")).toHaveValue("24h");

  await page.goto("/console/policy/versions/99");
  await expect(main(page).locator("[data-state=empty]")).toBeVisible();
  await page.goto("/console/policy/versions/latest");
  await expect(main(page).locator("[data-state=empty]")).toBeVisible();

  // Editing a version starts the candidate from its source.
  await page.goto("/console/policy/versions/1");
  await main(page).locator("[data-edit-candidate]").click();
  await expect(page).toHaveURL(/\/console\/policy\/candidate\?from=1$/);
  await expect(main(page).locator("[data-candidate-source]")).toHaveValue(/no-shell-on-build-hosts/);
  await page.context().close();
});
