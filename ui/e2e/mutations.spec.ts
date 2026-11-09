// What changes the seeded data runs last (the `mutations` project), so no
// screenshot sees it: Policy's author's flow (0053), below. Fleet's one
// mutation: a fleet-admin withdraws a proxy,
// and the console makes it look and act destructive — it confirms by typing
// the proxy's id, refuses a near miss, and then the API's answer is the row.
// And signing in and out, which writes the audit trail the audit view shows.

import { expect, test } from "@playwright/test";
import { candidates } from "./fixture/policy";
import { openScreen } from "./open";
import { SCREENS } from "./screens";

const fleet = SCREENS.find((s) => s.id === "fleet");

test("a fleet-admin withdraws a proxy by typing its id", async ({ browser }) => {
  if (!fleet) throw new Error("fleet is not registered");
  const page = await openScreen(browser, { ...fleet, role: "fleet-admin" });
  const row = page.locator('tr[data-row="px-spare-09"]');
  await row.locator('button[aria-haspopup="menu"]').click();
  await page.locator("[role=menu]:popover-open [role=menuitem][data-danger]").click();
  const dialog = page.locator("dialog[open]");
  const confirm = dialog.locator("button[type=submit]");
  await expect(confirm).toBeDisabled();
  await dialog.locator("[data-confirm-input]").fill("px-spare-0");
  await expect(confirm).toBeDisabled();
  await expect(dialog.locator("[aria-invalid=true]")).toHaveCount(1);
  await dialog.locator("[data-confirm-input]").fill("px-spare-09");
  await expect(confirm).toBeEnabled();
  await confirm.click();
  await expect(dialog).toHaveCount(0);
  await expect(page.locator("[role=status]").filter({ hasText: "px-spare-09" })).toHaveCount(1);
  await expect(row.locator('span[data-tone="deny"]')).toHaveText("Revoked");
  await page.context().close();
});

// An operator's preferences are theirs, not their sign-in's: they survive
// signing out and in again, even after someone else signed in on the same
// browser and chose differently. And the account menu signs out.
test("an operator's preferences survive signing in again, and the account menu signs out", async ({ browser }) => {
  const context = await browser.newContext({ viewport: { width: 1280, height: 800 }, timezoneId: "UTC" });
  const page = await context.newPage();
  const signIn = async (login: string, password: string) => {
    const res = await page.request.post("/api/v1/session/local", { data: { login, password } });
    expect(res.ok()).toBe(true);
    await page.goto("/console/deployment");
  };
  const choose = async (theme: string) => {
    await page.locator("[data-testid=account] button[aria-haspopup=menu]").click();
    await page.locator("[role=menu]:popover-open").getByRole("menuitemradio", { name: theme }).click();
    await page.keyboard.press("Escape");
  };
  const signOut = async () => {
    await page.locator("[data-testid=account] button[aria-haspopup=menu]").click();
    await page.locator("[role=menu]:popover-open").getByRole("menuitem", { name: "Sign out" }).click();
    await expect(page).toHaveURL(/\/console\/sign-in/);
  };

  await signIn("avery", "e2e-auditor-password");
  await choose("Dark");
  await signOut();
  await signIn("fenn", "e2e-fleet-password");
  await choose("Light");
  await signOut();
  await signIn("avery", "e2e-auditor-password");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await context.close();
});

// Policy (0053), end to end, as an author: a candidate the compiler refuses
// shows each rejection at its line; corrected, it is simulated against the
// recorded traffic and compared with the active version, then uploaded and
// activated through the typed confirmation, accepting its warning by id;
// then the version before it is rolled back to — refused first for a warning
// not yet accepted, which the dialog says, and then accepted.
test("an author validates, simulates, compares, uploads and activates a candidate, then rolls back", async ({ browser }) => {
  const candidateScreen = SCREENS.find((s) => s.id === "policy-candidate");
  if (!candidateScreen) throw new Error("policy-candidate is not registered");
  const page = await openScreen(browser, candidateScreen);
  const main = page.locator("main#content");
  const c = candidates();

  // Refused: each rejection at the line it names.
  await main.locator("[data-candidate-source]").fill(c.invalid);
  await main.locator("[data-validate]").click();
  await expect(main.locator(`tr[data-note-line="${c.lines.group}"] [data-rejection="match.unknown_group"]`)).toBeVisible();
  await expect(main.locator(`tr[data-note-line="${c.lines.duplicate}"] [data-rejection="rule.id_duplicate"]`)).toBeVisible();

  // Corrected: valid, with its publish-time report.
  await main.locator("[data-candidate-source]").fill(c.valid);
  await main.locator("[data-validate]").click();
  await expect(main.locator("[data-report] [data-finding][data-severity=warning]")).toHaveCount(1);

  // Simulated against the recorded traffic: both directions flip.
  await main.locator("[data-simulate-tab]").click();
  await main.locator("[data-run-replay]").click();
  await expect(main.locator("[data-replay]")).toHaveAttribute("data-flipped", "2");
  await expect(main.locator('[data-flips="allow_to_deny"] tr[data-flip]')).toHaveCount(1);
  await expect(main.locator('[data-flips="deny_to_allow"] tr[data-flip]')).toHaveCount(1);

  // Compared with the active version (v2).
  await main.locator("[data-compare]").click();
  await expect(main.locator("[data-changes] [data-diff=lines]")).toBeVisible();
  await expect(main.locator('[data-rule-change="added"]')).toContainText("sre-build-shell");

  // Uploaded: stored, not active, and the page is the new version's.
  await main.locator("[data-upload]").click();
  await expect(page).toHaveURL(/\/console\/policy\/versions\/4$/);
  await expect(main.locator("header [data-tone]").first()).toHaveText("Superseded");

  // Activated: typed confirmation, the warning accepted by its id.
  await main.locator("[data-activate=activate]").click();
  const dialog = page.locator("dialog[open]");
  const confirm = dialog.locator("button[type=submit]");
  await dialog.locator("[data-activation-gate=ok]").waitFor();
  await dialog.locator("[data-accept]").check();
  await dialog.locator("[data-confirm-input]").fill("v");
  await expect(confirm).toBeDisabled();
  await dialog.locator("[data-confirm-input]").fill("v4");
  await confirm.click();
  await expect(dialog).toHaveCount(0);
  await expect(page.locator("[role=status]").filter({ hasText: /v4\u2069? is active/ })).toHaveCount(1);
  await expect(main.locator("header [data-tone]").first()).toHaveText("Active");
  const record = await page.request.get("/api/v1/tenants/default/audit/records?limit=5");
  expect(JSON.stringify(await record.json())).toContain("authoring.cache_hint_spans_sources/office-db-read");

  // Rolled back to v2: refused while its warning is not accepted, inline; then done.
  await page.goto("/console/policy/versions/2");
  await main.locator("[data-activate=rollback]").click();
  await dialog.locator("[data-activation-gate=ok]").waitFor();
  await dialog.locator("[data-confirm-input]").fill("v2");
  await dialog.locator("button[type=submit]").click();
  await expect(dialog.locator("[role=alert][data-error-code=findings_not_accepted]")).toBeVisible();
  await expect(dialog).toHaveCount(1);
  await dialog.locator("[data-accept]").check();
  await dialog.locator("button[type=submit]").click();
  await expect(dialog).toHaveCount(0);
  await expect(page.locator("[role=status]").filter({ hasText: /Rolled back to \u2068?v2/ })).toHaveCount(1);
  const active = (await (await page.request.get("/api/v1/tenants/default/policy/active")).json()) as { version: number };
  expect(active.version).toBe(2);
  await page.context().close();
});

// Inventory (0054), end to end, as an administrator: a grant is created
// through the form — refused first at the field the API names — shown in the
// list and its detail, and revoked through the typed confirmation, with the
// reason its holder is shown; then the API says it is revoked.
test("an administrator grants access, finds it in the list and its detail, and revokes it by typing its id", async ({ browser }) => {
  const form = SCREENS.find((s) => s.id === "inventory-grant-new");
  if (!form) throw new Error("inventory-grant-new is not registered");
  const page = await openScreen(browser, form);
  const main = page.locator("main#content");
  const grant = main.locator("form[data-grant-form]");

  // Refused at the field: no reason given.
  await grant.locator("input[name=subject]").fill("mallory");
  await grant.locator("input[name=scope]").fill("e2e-audit-read");
  await grant.locator("textarea[name=targets]").fill("vault-01.dmz.internal");
  await grant.locator("input[name=duration]").fill("45m");
  await grant.locator("button[type=submit]").click();
  await expect(grant.locator("textarea[name=reason][aria-invalid=true]")).toHaveCount(1);

  // Created: the page is the grant's, and it is active.
  await grant.locator("textarea[name=reason]").fill("Read the vault's audit log for the quarterly review");
  await grant.locator("button[type=submit]").click();
  await expect(page).toHaveURL(/\/console\/inventory\/grants\/[^/?]+$/);
  const id = decodeURIComponent(new URL(page.url()).pathname.split("/").pop() as string);
  await expect(main.locator("[data-grant-state=active]")).toBeVisible();
  await expect(main.locator("header [data-tone]").first()).toHaveText("Active");

  // In the list, filtered to its holder.
  await page.goto("/console/inventory/grants?subject=mallory");
  await expect(main.locator(`tr[data-row="${id}"]`)).toBeVisible();
  await main.locator(`tr[data-row="${id}"] a`).first().click();
  await expect(page).toHaveURL(new RegExp(`/console/inventory/grants/${encodeURIComponent(id)}$`));

  // Revoked: the typed id, refusing a near miss, and the reason the holder reads.
  await main.locator("[data-revoke]").click();
  const dialog = page.locator("dialog[open]");
  const confirm = dialog.locator("button[type=submit]");
  await dialog.locator("textarea[name=revoke_reason]").fill("Review finished");
  await dialog.locator("[data-confirm-input]").fill(id.slice(0, -1));
  await expect(confirm).toBeDisabled();
  await dialog.locator("[data-confirm-input]").fill(id);
  await confirm.click();
  await expect(dialog).toHaveCount(0);
  await expect(main.locator("[data-grant-state=revoked]")).toBeVisible();
  await expect(main.locator("header [data-tone]").first()).toHaveText("Revoked");
  const stored = (await (await page.request.get(`/api/v1/tenants/default/grants/${encodeURIComponent(id)}`)).json()) as {
    grant: { state: string; revoke_reason: string };
  };
  expect(stored.grant.state).toBe("revoked");
  expect(stored.grant.revoke_reason).toBe("Review finished");
  await page.context().close();
});

// A fleet-admin removes a target, which is destructive: it confirms by typing
// the target's id, and the row is gone from the list the API answers.
test("a fleet-admin removes a target by typing its id", async ({ browser }) => {
  const targets = SCREENS.find((s) => s.id === "inventory-targets");
  if (!targets) throw new Error("inventory-targets is not registered");
  const page = await openScreen(browser, { ...targets, role: "fleet-admin" });
  const row = page.locator('tr[data-row="scratch-01"]');
  await row.locator('button[aria-haspopup="menu"]').click();
  await page.locator("[role=menu]:popover-open [role=menuitem][data-danger]").click();
  const dialog = page.locator("dialog[open]");
  await dialog.locator("[data-confirm-input]").fill("scratch-0");
  await expect(dialog.locator("button[type=submit]")).toBeDisabled();
  await dialog.locator("[data-confirm-input]").fill("scratch-01");
  await dialog.locator("button[type=submit]").click();
  await expect(dialog).toHaveCount(0);
  await expect(row).toHaveCount(0);
  expect((await page.request.get("/api/v1/tenants/default/targets/scratch-01")).status()).toBe(404);
  await page.context().close();
});
