// THE VISUAL POLICY EDITOR'S CHECKS THAT CHANGE NOTHING STORED (0066). Against
// the seeded database (fixture/policy.ts: v2 active), in the browser:
//   - an edit made visually is the smallest change: the server's own diff
//     (POST …/policy/diff, against the active version the candidate started
//     from) names exactly the rule or document key edited, and every comment
//     of the original is still in the source;
//   - a move that makes a rule unreachable shows the compiler's
//     `rule.unreachable` on that rule's card;
//   - a source that does not parse turns the visual view off and sets its
//     rejections at their lines;
//   - reordering, adding, removing and editing are each done by keyboard
//     alone, and each announces what it did;
//   - "where would this land?" marks the rule, the terms and the effect, or
//     the default deny;
//   - an auditor, who reads policy and cannot write it, builds, checks and
//     traces visually, is not offered Upload, and is refused it by the API.
// The headless author's whole flow — built, traced, uploaded, activated — is
// in policy-visual-mutations.spec.ts, which runs after every screenshot.

import { expect, test, type Page } from "@playwright/test";
import { commentsOf } from "../src/screens/policyDocument";
import { candidates } from "./fixture/policy";
import { TENANT } from "./harness";
import { openScreen } from "./open";
import { SCREENS, traceRequest } from "./screens";

const API = `/api/v1/tenants/${TENANT}/policy`;
const screen = (id: string) => {
  const s = SCREENS.find((x) => x.id === id);
  if (!s) throw new Error(`${id} is not registered`);
  return s;
};
const main = (page: Page) => page.locator("main#content");
const card = (page: Page, id: string) => main(page).locator(`[data-rule-card="${id}"]`);
const said = (page: Page) => main(page).locator("[data-visual-said]");

/** The candidate's source as it stands, read from the source view and left there. */
async function sourceOf(page: Page): Promise<string> {
  await page.locator('nav [data-view="source"]').click();
  return main(page).locator("[data-candidate-source]").inputValue();
}

/** The server's diff of the candidate against the active version, as the Compare button asks it. */
async function diffOf(page: Page): Promise<{ added: string[]; removed: string[]; changed: string[]; moved: string[]; document: string[] }> {
  const answer = page.waitForResponse((r) => r.url().endsWith(`${API}/diff`));
  await main(page).locator("[data-compare]").first().click();
  return (await (await answer).json()) as { added: string[]; removed: string[]; changed: string[]; moved: string[]; document: string[] };
}

async function activeSource(page: Page): Promise<string> {
  const res = await page.request.get(`${API}/active`);
  return ((await res.json()) as { source: string }).source;
}

test("one edit made visually is the one change the server's diff names, and no comment is lost", async ({ browser }) => {
  const page = await openScreen(browser, screen("policy-candidate-visual"));
  const original = await activeSource(page);
  // A rule's description, through its editor.
  await card(page, "ops-db-shell").locator("[data-rule-edit]").click();
  const editor = page.locator("dialog[open] [data-rule-editor=ops-db-shell]");
  await editor.locator("[data-visual-field=description]").fill("an SRE's recorded shell, edited visually");
  await editor.locator("[data-visual-field=description]").press("Enter");
  await expect(said(page)).toContainText("ops-db-shell");
  await editor.locator("[data-visual-done]").click();
  const edited = await sourceOf(page);
  expect(edited).not.toBe(original);
  expect(await diffOf(page)).toMatchObject({ added: [], removed: [], changed: ["ops-db-shell"], moved: [], document: [] });
  for (const c of commentsOf(original)) expect(commentsOf(edited)).toContain(c);
  await page.context().close();
});

test("an edit to the bundle's own vocabulary is a change to that document key alone", async ({ browser }) => {
  const page = await openScreen(browser, screen("policy-candidate-visual"));
  const original = await activeSource(page);
  const groups = main(page).locator("[data-visual-vocabulary] [data-chip-editor=groups]");
  await groups.locator("[data-chip-add]").fill("dba");
  await groups.locator("[data-chip-add]").press("Enter");
  await expect(groups.locator('[data-chip="dba"]')).toBeVisible();
  const edited = await sourceOf(page);
  expect(await diffOf(page)).toMatchObject({ added: [], removed: [], changed: [], moved: [], document: ["groups"] });
  for (const c of commentsOf(original)) expect(commentsOf(edited)).toContain(c);
  await page.context().close();
});

test("a move that makes a rule unreachable shows rule.unreachable on that rule's card", async ({ browser }) => {
  const page = await openScreen(browser, screen("policy-candidate-visual"));
  // A rule that matches every request, added last and moved first: every rule after it is unreachable.
  await main(page).locator("[data-visual-add]").click();
  const editor = page.locator("dialog[open] [data-rule-editor]");
  await editor.locator("[data-visual-field=reason]").fill("closed for maintenance");
  await editor.locator("[data-visual-field=reason]").press("Enter");
  await editor.locator("[data-visual-done]").click();
  const added = main(page).locator("[data-sortable-key]").last();
  await expect(added.locator("[data-rule-card]")).toHaveAttribute("data-rule-card", /^rule-\d+$/);
  // To the top, by keyboard: End and Home move to the ends.
  await added.locator("[data-sortable-control=handle]").focus();
  await page.keyboard.press("Home");
  await expect(main(page).locator("[data-sortable-key]").first().locator("[data-rule-card]")).toHaveAttribute("data-rule-card", /^rule-\d+$/);
  await expect(card(page, "ops-db-shell").locator('[data-rejection="rule.unreachable"][data-known]')).toBeVisible();
  await page.context().close();
});

test("a source that does not parse turns the visual view off, its rejections at their lines", async ({ browser }) => {
  const page = await openScreen(browser, screen("policy-candidate-visual-off"));
  const off = main(page).locator("[data-visual=off]");
  await expect(off).toHaveAttribute("data-visual-reason", "not_yaml");
  await expect(off.locator("[data-rules-card], [data-sortable]")).toHaveCount(0);
  const line = candidates().unparsable.split("\n").findIndex((l) => l.includes("groups: [sre")) + 1;
  await expect(off.locator(`tr[data-note-line] [data-rejection="policy.document_malformed"][data-known]`).first()).toBeVisible();
  expect(line).toBeGreaterThan(0);
  // Back to the source, which is where it is put right.
  await off.locator("[data-visual-to-source]").click();
  await expect(main(page).locator("[data-candidate-source]")).toBeFocused();
  await page.context().close();
});

test("reordering, adding, removing and editing are each done by keyboard alone, and announced", async ({ browser }) => {
  const page = await openScreen(browser, screen("policy-candidate-visual"));
  const keys = () => main(page).locator("[data-sortable-key] [data-rule-card]").evaluateAll((cs) => cs.map((c) => c.getAttribute("data-rule-card")));
  const before = await keys();
  // Reorder: the first rule's handle, the down arrow.
  const first = main(page).locator("[data-sortable-key]").first();
  await first.locator("[data-sortable-control=handle]").focus();
  await page.keyboard.press("ArrowDown");
  await expect.poll(keys).toEqual([before[1], before[0], ...before.slice(2)]);
  await expect(main(page).locator("[data-sortable-said]")).toContainText(`${before[0]}`);
  await expect(main(page).locator("[data-sortable-said]")).toContainText("2");
  // Focus followed the rule it moved.
  await expect(main(page).locator(`[data-sortable-key] [data-rule-card="${before[0]}"]`).locator("xpath=ancestor::li").locator("[data-sortable-control=handle]")).toBeFocused();
  // And back with the step button, by Enter.
  await main(page).locator(`[data-sortable-key]:nth-child(2) [data-sortable-control=up]`).focus();
  await page.keyboard.press("Enter");
  await expect.poll(keys).toEqual(before);

  // Add: the button by keyboard opens the new rule's editor.
  await main(page).locator("[data-visual-add]").focus();
  await page.keyboard.press("Enter");
  await expect(said(page)).toContainText("rule-");
  const editor = page.locator("dialog[open] [data-rule-editor]");
  await expect(editor).toBeVisible();
  // Edit: a choice from the vocabulary, by keyboard.
  await editor.locator("[data-visual-field=effect]").focus();
  await editor.locator("[data-visual-field=effect]").selectOption("allow");
  await expect(said(page)).toContainText("Effect");
  // A chip from a closed set, by keyboard, and its removal.
  const channels = editor.locator("[data-chip-editor='route.channels']");
  await channels.locator("[data-chip-add]").focus();
  await channels.locator("[data-chip-add]").selectOption("session");
  await expect(channels.locator('[data-chip="session"]')).toBeVisible();
  await channels.locator('[data-chip="session"] button').focus();
  await page.keyboard.press("Enter");
  await expect(channels.locator('[data-chip="session"]')).toHaveCount(0);
  await expect(channels.locator("[data-chip-add]")).toBeFocused();
  // Done, by keyboard, closes the editor.
  await editor.locator("[data-visual-done]").focus();
  await page.keyboard.press("Enter");
  await expect(page.locator("dialog[open]")).toHaveCount(0);
  // Remove: by keyboard, announced.
  const added = main(page).locator("[data-sortable-key]").last();
  await added.locator("[data-rule-remove]").focus();
  await page.keyboard.press("Enter");
  await expect(said(page)).toContainText("Removed rule");
  await expect.poll(keys).toEqual(before);
  await page.context().close();
});

test("a typed value is checked where the server checks it, and refused with its code's sentence", async ({ browser }) => {
  const page = await openScreen(browser, screen("policy-candidate-visual-editor"));
  const editor = page.locator("dialog[open] [data-rule-editor=ops-db-shell]");
  const hosts = editor.locator("[data-chip-editor='target.hostnames']");
  await hosts.locator("[data-chip-add]").fill("db*.example");
  await hosts.locator("[data-chip-add]").press("Enter");
  await expect(hosts.locator("[data-chip-error]")).toContainText("db*.example");
  await expect(hosts.locator("[data-chip]")).toHaveCount(0);
  await editor.locator("[data-visual-field=id]").fill("vault-through-dmz");
  await editor.locator("[data-visual-field=id]").press("Enter");
  await expect(editor.getByText("vault-through-dmz", { exact: false }).first()).toBeVisible();
  await expect(editor.locator("[data-visual-field=id]")).toHaveAttribute("aria-invalid", "true");
  await page.context().close();
});

test("a trace marks the rule that answers, the terms that matched and the effect, or the default deny", async ({ browser }) => {
  const page = await openScreen(browser, screen("policy-candidate-visual"));
  const sim = page.waitForRequest((r) => r.url().endsWith(`${API}/simulate`));
  await traceRequest(page);
  const body = (await sim).postDataJSON() as { cases: { now: string; subject: { groups: string[] } }[] };
  // The instant as the author gave it, in their zone (UTC in these checks).
  expect(Date.parse(body.cases[0]?.now ?? "")).toBe(Date.parse("2026-11-02T10:00:00Z"));
  expect(body.cases[0]?.subject.groups).toEqual(["sre"]);
  const hit = card(page, "ops-db-shell");
  await expect(hit).toHaveAttribute("data-trace", "matched");
  await expect(hit.locator('[data-term="groups=sre"][data-matched]')).toBeVisible();
  await expect(hit.locator('[data-term="labels.env=prod"][data-matched]')).toBeVisible();
  await expect(main(page).locator("[data-trace-result=allow]")).toBeVisible();
  // Nobody's: a host with no label any rule names lands on the default deny.
  const trace = main(page).locator("[data-visual-trace]");
  await trace.locator("[data-trace-label=env]").selectOption("");
  await trace.locator("[data-trace-label=kind]").selectOption("");
  await trace.locator("[data-trace-field=hostname]").fill("nobody.example.internal");
  await trace.locator("[data-trace-run]").click();
  await expect(main(page).locator("[data-default-deny][data-trace=matched]")).toBeVisible();
  await expect(main(page).locator("[data-trace-result=deny]")).toBeVisible();
  await expect(main(page).locator("[data-rule-card][data-trace=matched]")).toHaveCount(0);
  await page.context().close();
});

test("an auditor builds, checks and traces visually, is offered no upload, and the API refuses one", async ({ browser }) => {
  const page = await openScreen(browser, { ...screen("policy-candidate-visual"), role: "auditor" });
  // A rule added last, which matches what no rule above it does, with the reason a deny needs.
  await main(page).locator("[data-visual-add]").click();
  await expect(said(page)).toContainText("rule-");
  const editor = page.locator("dialog[open] [data-rule-editor]");
  await editor.locator("[data-visual-field=reason]").fill("nothing else is reached");
  await editor.locator("[data-visual-field=reason]").press("Enter");
  await page.keyboard.press("Escape");
  await expect(page.locator("dialog[open]")).toHaveCount(0);
  await expect(main(page).locator("[data-visual-check=ok]")).toBeVisible();
  await expect(main(page).locator("[data-rules] [data-rejection], [data-card-notes] [data-rejection]")).toHaveCount(0);
  await traceRequest(page);
  await expect(main(page).locator("[data-rule-card=ops-db-shell][data-trace=matched]")).toBeVisible();
  await expect(main(page).locator("[data-trace-result]:not([data-trace-result=''])").first()).toBeVisible();
  await expect(main(page).locator("[data-upload]")).toHaveCount(0);
  const refused = await page.request.post(`${API}/bundles`, { data: { source: await activeSource(page) } });
  expect(refused.status()).toBe(403);
  await page.context().close();
});

test("a version opens visually, and a rule's editor is a deep link", async ({ browser }) => {
  const page = await openScreen(browser, screen("policy-version"));
  await main(page).locator("[data-edit-visual]").click();
  await expect(page).toHaveURL(/\/console\/policy\/candidate\?from=3&view=visual$/);
  await main(page).locator("[data-visual-check=ok]").waitFor();
  await card(page, "ops-db-shell").locator("[data-rule-open]").click();
  await expect(page).toHaveURL(/rule=ops-db-shell/);
  await expect(page.locator("dialog[open] [data-rule-editor=ops-db-shell]")).toBeVisible();
  await page.context().close();
});
