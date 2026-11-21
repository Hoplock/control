// THE VISUAL POLICY EDITOR, END TO END (0066). It stores and activates a
// version, so it runs in the mutations project, after every screenshot. A
// headless author, starting from the active version (v2) and never writing
// YAML:
//   builds a rule — matched on a group and a label, routed — and drags it
//   above another; validates the candidate; traces a request to see which
//   rule it lands on; uploads it; and activates it through 0053's dialog,
//   typing its name and accepting each warning by id.
// Then the version made active is the one built, rule for rule, in the order
// built; and the active version goes back to v2, as the fixture left it.

import { expect, test, type Page } from "@playwright/test";
import { TENANT } from "./harness";
import { openScreen } from "./open";
import { SCREENS } from "./screens";

const API = `/api/v1/tenants/${TENANT}/policy`;
const main = (page: Page) => page.locator("main#content");

test("an author builds a rule visually, reorders it, validates, traces, uploads and activates it", async ({ browser }) => {
  const visual = SCREENS.find((s) => s.id === "policy-candidate-visual");
  if (!visual) throw new Error("policy-candidate-visual is not registered");
  const page = await openScreen(browser, visual);
  const ids = () => main(page).locator("[data-sortable-key] [data-rule-card]").evaluateAll((cs) => cs.map((c) => c.getAttribute("data-rule-card")));
  const before = await ids();
  expect(before).toContain("vault-through-dmz");

  // A rule, added last and named.
  await main(page).locator("[data-visual-add]").click();
  const editor = page.locator("dialog[open] [data-rule-editor]");
  await editor.locator("[data-visual-field=id]").fill("sre-vault-shell");
  await editor.locator("[data-visual-field=id]").press("Enter");
  await expect(page.locator("dialog[open] [data-rule-editor=sre-vault-shell]")).toBeVisible();
  // An allow, matched on a group and a label, each a choice from the bundle's own declarations.
  await editor.locator("[data-visual-field=effect]").selectOption("allow");
  await editor.locator("[data-chip-editor='subject.groups'] [data-chip-add]").selectOption("sre");
  await editor.locator("[data-chip-editor='target.labels.kind'] [data-chip-add]").selectOption("vault");
  // Routed: every field a choice from the server's vocabulary.
  await editor.locator("[data-visual-field=intent]").selectOption("direct");
  await editor.locator("[data-chip-editor='route.channels'] [data-chip-add]").selectOption("session");
  await editor.locator("[data-visual-field=filter-mode]").selectOption("blacklist");
  await editor.locator("[data-visual-field=ladder-add]").selectOption("ephemeral-user");
  await editor.locator("[data-visual-field=ladder-0-username]").selectOption("subject-local-part");
  await editor.locator("[data-visual-done]").click();
  await expect(page.locator("dialog[open]")).toHaveCount(0);

  // Dragged above the rule it would otherwise follow: the vault through the DMZ.
  const built = main(page).locator("[data-sortable-key]").filter({ has: page.locator("[data-rule-card=sre-vault-shell]") });
  const target = main(page).locator("[data-sortable-key]").filter({ has: page.locator("[data-rule-card=vault-through-dmz]") });
  // The page's main region scrolls, so both ends of the drag are brought into view first.
  await target.scrollIntoViewIfNeeded();
  const box = await target.boundingBox();
  if (!box) throw new Error("the vault rule is not on the page");
  await built.locator("[data-sortable-control=handle]").dragTo(target, { targetPosition: { x: box.width / 2, y: 6 } });
  await expect.poll(ids).toEqual(
    (() => {
      const order = before.filter((x) => x !== null) as string[];
      order.splice(order.indexOf("vault-through-dmz"), 0, "sre-vault-shell");
      return order;
    })(),
  );
  await expect(main(page).locator("[data-sortable-said]")).toContainText("sre-vault-shell");

  // Validated: it compiles, and no note sits on the rule built.
  await expect(main(page).locator("[data-visual-check=ok]")).toBeVisible();
  await expect(main(page).locator("[data-rule-card=sre-vault-shell] [data-card-notes] [data-rejection]")).toHaveCount(0);
  await main(page).locator("[data-visual-actions] [data-validate]").click();
  await expect(main(page).locator("[data-report]")).toBeVisible();
  await expect(main(page).locator("[data-rejections]")).toHaveCount(0);

  // Traced: an SRE to a vault lands on the rule built, above the DMZ rule that would also match a vault in the DMZ.
  const trace = main(page).locator("[data-visual-trace]");
  await trace.locator("[data-trace-field=subject]").fill("alice");
  await trace.locator("[data-chip-editor='trace.groups'] [data-chip-add]").selectOption("sre");
  await trace.locator("[data-trace-field=hostname]").fill("vault-02.dmz.internal");
  await trace.locator("[data-trace-label=kind]").selectOption("vault");
  await trace.locator("[data-trace-field=zone]").fill("dmz");
  await trace.locator("[data-trace-run]").click();
  await expect(main(page).locator("[data-rule-card=sre-vault-shell][data-trace=matched]")).toBeVisible();
  await expect(main(page).locator('[data-rule-card=sre-vault-shell] [data-term="labels.kind=vault"][data-matched]')).toBeVisible();
  await expect(main(page).locator("[data-trace-result=allow]")).toBeVisible();

  // Uploaded: the page is the new version's.
  await main(page).locator("[data-visual-actions] [data-upload]").click();
  await expect(page).toHaveURL(/\/console\/policy\/versions\/\d+$/);
  const version = Number(/versions\/(\d+)$/.exec(page.url())?.[1]);
  const name = `v${version}`;

  // Activated through 0053's dialog: typed, every warning accepted by its id.
  await main(page).locator("[data-activate=activate]").click();
  const dialog = page.locator("dialog[open]");
  await dialog.locator("[data-activation-gate=ok]").waitFor();
  for (const box of await dialog.locator("[data-accept]").all()) await box.check();
  await dialog.locator("[data-confirm-input]").fill(name);
  await dialog.locator("button[type=submit]").click();
  await expect(dialog).toHaveCount(0);
  await expect(main(page).locator("header [data-tone]").first()).toHaveText("Active");

  // The active version is the one built, in the order built.
  const active = (await (await page.request.get(`${API}/active`)).json()) as { version: number; source: string };
  expect(active.version).toBe(version);
  expect(active.source.indexOf("- id: sre-vault-shell")).toBeGreaterThan(-1);
  expect(active.source.indexOf("- id: sre-vault-shell")).toBeLessThan(active.source.indexOf("- id: vault-through-dmz"));

  // v2 active again, as the fixture left it, accepting its warnings by id.
  const v2 = (await (await page.request.get(`${API}/bundles/2`)).json()) as { source: string };
  const report = (await (await page.request.post(`${API}/validate`, { data: { source: v2.source } })).json()) as { findings: { id: string; severity: string }[] };
  const back = await page.request.post(`${API}/bundles/2/activate`, { data: { accept: report.findings.filter((f) => f.severity === "warning").map((f) => f.id) } });
  expect(back.ok()).toBe(true);
  await page.context().close();
});
