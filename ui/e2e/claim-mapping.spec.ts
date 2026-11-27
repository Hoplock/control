// THE VISUAL CLAIM-MAPPING EDITOR'S CHECKS THAT CHANGE NOTHING STORED (0068).
// Against the seeded database (fixture/standing.ts: MAPPING is version 1), in
// the browser:
//   - every `mapping.*` code the server answers renders on the row and field
//     its `field` path names, and one about the whole document heads the view;
//   - a write the server refuses writes nothing, and its refusals land on the
//     rows they name;
//   - an edit made visually is the smallest change to the document, and no
//     comment of it is lost;
//   - adding, removing, reordering and editing are each done by keyboard
//     alone, and each announces what it did;
//   - an auditor, who reads identities and cannot write the mapping, edits a
//     draft visually, has the server check it and previews it, is not offered
//     the write, and is refused it by the API.
// The operator's whole flow — a claim with permitted values, an entry scoped
// to a directory, the preview, the summary and the typed write — is in
// claim-mapping-mutations.spec.ts, which runs after every screenshot.

import { expect, test, type Page } from "@playwright/test";
import { commentsOf } from "../src/screens/yamlDocument";
import { MAPPING } from "./fixture/standing";
import { TENANT } from "./harness";
import { openScreen } from "./open";
import { SCREENS } from "./screens";

const API = `/api/v1/tenants/${TENANT}/identity`;
const screen = (id: string) => {
  const s = SCREENS.find((x) => x.id === id);
  if (!s) throw new Error(`${id} is not registered`);
  return s;
};
const main = (page: Page) => page.locator("main#content");
const said = (page: Page) => main(page).locator("[data-mapping-said]");
const field = (page: Page, name: string) => main(page).locator(`[data-mapping-field="${name}"]`);

/** Writes a draft in the source view and waits for the server's check of it. */
async function draft(page: Page, source: string, verdict: "valid" | "invalid"): Promise<void> {
  await page.locator('nav a[href*="view=source"]').click();
  await main(page).locator("textarea[data-mapping-source]").fill(source);
  await main(page).locator(`[data-mapping-check=${verdict}]`).waitFor();
}

async function visual(page: Page): Promise<void> {
  await main(page).locator("[data-mapping-draft] nav a").first().click();
  await main(page).locator("[data-mapping-visual]").first().waitFor();
}

async function source(page: Page): Promise<string> {
  await page.locator('nav a[href*="view=source"]').click();
  return main(page).locator("textarea[data-mapping-source]").inputValue();
}

async function activeVersion(page: Page): Promise<number> {
  const res = await page.request.get(`${API}/claim-mapping`);
  return ((await res.json()) as { version: number }).version;
}

// One document with a fault at every field the server can name: each code at its row and field.
const FAULTS = `schema_version: 1
claims:
  - claim: ""
    attribute: hoplock.proxy
  - claim: title
    attribute: Title
  - claim: dept
    attribute: department
    values: [eng, " "]
  - claim: dept2
    attribute: department
groups:
  claim: groups
  map:
    - external: sre
      group: sre
    - external: sre
      group: -bad
    - external: Payments Engineering
      directories: [Corp]
      group: payments-eng
    - external: Auditors
      directories: []
      group: auditors
    - external: Payments Engineering
      directories: [lab]
      group: a
    - external: Payments Engineering
      directories: [lab]
      group: b
`;

test("every mapping code the server answers renders on the row and field it names", async ({ browser }) => {
  const page = await openScreen(browser, screen("inventory-claim-mapping"));
  const answer = page.waitForResponse((r) => r.url().endsWith(`${API}/claim-mapping/validate`) && r.status() === 400);
  await draft(page, FAULTS, "invalid");
  const rejections = ((await (await answer).json()) as { parameters: { rejections: { code: string; field: string }[] } }).parameters.rejections;
  expect(new Set(rejections.map((r) => r.code))).toEqual(
    new Set([
      "mapping.claim_empty",
      "mapping.attribute_reserved",
      "mapping.attribute_name",
      "mapping.value_empty",
      "mapping.attribute_duplicate",
      "mapping.group_name",
      "mapping.directory_name",
      "mapping.directories_empty",
      "mapping.directory_duplicate",
    ]),
  );
  await visual(page);
  for (const r of rejections) {
    const target = /\.values\[\d+\]$|\.directories(\[\d+\])?$/.test(r.field)
      ? main(page).locator(`[data-chip-editor="${r.field.replace(/\[\d+\]$/, "")}"][data-invalid]`)
      : /\.(claim|attribute|group)$/.test(r.field)
        ? field(page, r.field).and(page.locator("[aria-invalid=true]"))
        : null;
    if (target) await expect(target, `${r.code} at ${r.field}`).toHaveCount(1);
  }
  // A row-level fault (an entry whose group is refused while its name is listed twice) is on its row, never lost.
  await expect(main(page).locator("[data-entry-row='1'] [aria-invalid=true]")).toHaveCount(1);

  // A fault about the whole document heads the view.
  await draft(page, "schema_version: 2\n", "invalid");
  await visual(page);
  await expect(main(page).locator("[data-mapping-visual=on] > [data-rejections] [data-rejection='mapping.schema_version']")).toHaveCount(1);
  await page.context().close();
});

test("a write the server refuses writes nothing, and its refusals land on their rows", async ({ browser }) => {
  const page = await openScreen(browser, screen("inventory-claim-mapping"));
  const before = await activeVersion(page);
  // The dry run is answered as if it passed, in the browser, so the write is offered and the server alone refuses it.
  await page.route(`**${API}/claim-mapping/validate`, (route) =>
    route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ digest: "x", attributes: [], groups: [], directories: [], require_directories: false, group_map: [], samples: [] }) }),
  );
  await draft(page, MAPPING.replace("directories: [lab]", "directories: [Lab]"), "valid");
  await visual(page);
  await main(page).locator("[data-mapping-write]").click();
  const confirm = page.locator("dialog[open]");
  await confirm.locator("input").fill(TENANT);
  const refused = page.waitForResponse((r) => r.url().endsWith(`${API}/claim-mapping`) && r.request().method() === "PUT");
  await confirm.locator("button[type=submit]").click();
  expect((await refused).status()).toBe(400);
  await main(page).locator("[data-mapping-write-refused='1']").waitFor();
  await expect(main(page).locator('[data-chip-editor="groups.map[1].directories"][data-invalid]')).toHaveCount(1);
  expect(await activeVersion(page)).toBe(before);
  await page.context().close();
});

test("an edit made visually changes only what it names and loses no comment", async ({ browser }) => {
  const page = await openScreen(browser, screen("inventory-claim-mapping"));
  const commented = MAPPING.replace("claims:\n", "claims:\n  # Who someone works for.\n").replace("  - external: sre\n", "  # Sign-ins and both directories.\n    - external: sre\n");
  await draft(page, commented, "valid");
  await visual(page);
  const group = field(page, "groups.map[2].group");
  await group.fill("site-reliability");
  await group.press("Enter");
  await expect(said(page)).toContainText("sre");
  const after = await source(page);
  expect(after).toBe(commented.replace("      group: sre\n", "      group: site-reliability\n"));
  for (const c of commentsOf(commented)) expect(commentsOf(after)).toContain(c);
  await page.context().close();
});

test("adding, reordering, editing and removing are each done by keyboard, and announced", async ({ browser }) => {
  const page = await openScreen(browser, screen("inventory-claim-mapping"));
  // Add a claim: type into the add form and press Enter.
  const add = main(page).locator("form[data-mapping-add=claim]");
  await add.locator("[data-mapping-add-field=claim]").focus();
  await page.keyboard.type("title");
  await page.keyboard.press("Tab");
  await page.keyboard.type("job_title");
  await page.keyboard.press("Enter");
  await expect(said(page)).toContainText("job_title");
  await expect(main(page).locator("[data-claim-row]")).toHaveCount(2);

  // Reorder: the new claim's handle, then the up arrow; the list announces it and focus stays on it.
  const handle = main(page).locator("[data-mapping-section=claims] [data-sortable-control=handle]").nth(1);
  await handle.focus();
  await page.keyboard.press("ArrowUp");
  await expect(main(page).locator("[data-mapping-section=claims] [data-sortable-said]")).toContainText("job_title");
  await expect(main(page).locator("[data-claim-row]").first()).toHaveAttribute("data-claim-attribute", "job_title");

  // Edit: its permitted values, typed and added with Enter.
  const values = main(page).locator('[data-chip-editor="claims[0].values"] input');
  await values.focus();
  await page.keyboard.type("staff");
  await page.keyboard.press("Enter");
  await expect(said(page)).toContainText(/claim 1/i);
  await expect(main(page).locator('[data-chip-editor="claims[0].values"]')).toContainText("staff");

  // Edit: its attribute, applied with Enter.
  await field(page, "claims[0].attribute").focus();
  await page.keyboard.press("ControlOrMeta+a");
  await page.keyboard.type("role_title");
  await page.keyboard.press("Enter");
  await expect(main(page).locator("[data-claim-row]").first()).toHaveAttribute("data-claim-attribute", "role_title");

  // Remove it: the row's own button, pressed with Enter.
  await main(page).locator("[data-claim-remove='0']").focus();
  await page.keyboard.press("Enter");
  await expect(said(page)).toContainText("role_title");
  await expect(main(page).locator("[data-claim-row]")).toHaveCount(1);

  // Add an entry for a directory by keyboard alone: open, choose, type, Enter.
  await main(page).locator('[data-mapping-open-scoped="sre"]').focus();
  await page.keyboard.press("Enter");
  const scoped = main(page).locator('form[data-mapping-add=scoped][data-mapping-add-name="sre"]');
  await scoped.locator("[data-mapping-add-field=directory]").selectOption("lab");
  await scoped.locator("[data-mapping-add-field=group]").focus();
  await page.keyboard.type("lab-sre");
  await page.keyboard.press("Enter");
  await expect(said(page)).toContainText("lab-sre");
  await expect(main(page).locator('[data-entry-name="sre"] [data-entry-row]')).toHaveCount(2);
  await page.context().close();
});

test("an auditor edits, checks and previews a draft, is not offered the write, and is refused it by the API", async ({ browser }) => {
  const page = await openScreen(browser, { ...screen("inventory-claim-mapping"), role: "auditor" });
  await expect(main(page).locator("[data-mapping-write]")).toHaveCount(0);
  const add = main(page).locator("form[data-mapping-add=claim]");
  await add.locator("[data-mapping-add-field=claim]").fill("title");
  await add.locator("[data-mapping-add-field=attribute]").fill("title");
  await add.locator("button[type=submit]").click();
  await main(page).locator("[data-mapping-draft=edited][data-mapping-check=valid]").waitFor();

  const preview = main(page).locator("[data-mapping-preview]");
  await expect(preview.locator("[data-preview-field=through] option[value^='identity:']")).toHaveCount(0);
  const chips = preview.locator('[data-chip-editor="preview.claims"]');
  await chips.locator("input").fill("title=staff");
  await chips.locator("[data-chip-add-button]").click();
  await preview.locator("[data-preview-run]").click();
  await expect(preview.locator("[data-preview-side=draft] [data-preview-attribute=title][data-mark=gained]")).toHaveCount(1);

  const res = await page.request.put(`${API}/claim-mapping`, { data: { document: MAPPING } });
  expect(res.status()).toBe(403);
  expect(((await res.json()) as { code: string }).code).toBe("forbidden");
  await page.context().close();
});
