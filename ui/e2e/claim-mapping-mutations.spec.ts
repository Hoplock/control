// THE VISUAL CLAIM-MAPPING EDITOR'S WRITE (0068), headless, after every
// screenshot (the `mutations` project). An administrator, in the visual view
// and nowhere else:
//   1. adds a claim that becomes an attribute with two permitted values;
//   2. scopes a new entry to a directory chosen from the tenant's sources —
//      `Platform Guild` from `corp`, which the seeded mapping does not map
//      and the fixture's dana carries (fixture/control.yaml);
//   3. previews dana, a provisioned identity of that directory, and sees her
//      gain the new group under the draft and not under the active version;
//   4. reads the change summary;
//   5. writes it through the typed confirm;
// and the screen's active version and `group_map` show it. The seeded mapping
// is written back at the end, so a later run starts where this one did.

import { expect, test, type Page } from "@playwright/test";
import { DIRECTORY, fixtureSubject, MAPPING } from "./fixture/standing";
import { TENANT } from "./harness";
import { openScreen } from "./open";
import { SCREENS } from "./screens";

const API = `/api/v1/tenants/${TENANT}/identity/claim-mapping`;
const main = (page: Page) => page.locator("main#content");
const screen = SCREENS.find((s) => s.id === "inventory-claim-mapping");

test("an administrator scopes an entry, previews it, reads what it changes and writes it typed", async ({ browser }) => {
  if (!screen) throw new Error("inventory-claim-mapping is not registered");
  const page = await openScreen(browser, screen);
  const before = (await (await page.request.get(API)).json()) as { version: number };

  // 1. A claim that becomes an attribute, trusted for two values.
  const addClaim = main(page).locator("form[data-mapping-add=claim]");
  await addClaim.locator("[data-mapping-add-field=claim]").fill("title");
  await addClaim.locator("[data-mapping-add-field=attribute]").fill("job_title");
  await addClaim.locator("button[type=submit]").click();
  const values = main(page).locator('[data-chip-editor="claims[1].values"]');
  for (const v of ["staff", "lead"]) {
    await values.locator("input").fill(v);
    await values.locator("[data-chip-add-button]").click();
  }
  await expect(values).toContainText("staff");
  await expect(values).toContainText("lead");

  // 2. An entry for a name the mapping does not read, scoped to a directory chosen from the tenant's.
  const addEntry = main(page).locator("form[data-mapping-add=entry]");
  await addEntry.locator("[data-mapping-add-field=external]").fill("Platform Guild");
  await addEntry.locator("[data-mapping-add-field=group]").fill("platform-guild");
  const directories = addEntry.locator('[data-chip-editor="add.directories"]');
  await directories.locator("select").selectOption("corp");
  await expect(directories).toContainText("corp");
  await addEntry.locator("button[type=submit]").click();
  await expect(main(page).locator('[data-entry-name="Platform Guild"] [data-entry-row]')).toHaveCount(1);
  await main(page).locator("[data-mapping-draft=edited][data-mapping-check=valid]").waitFor();

  // 3. dana, provisioned by corp, gains the group under the draft and not under the active version.
  const dana = fixtureSubject(DIRECTORY.dana);
  const preview = main(page).locator("[data-mapping-preview]");
  await preview.locator("[data-preview-field=through]").selectOption(`identity:${dana}`);
  await preview.locator("[data-preview-run]").click();
  await expect(preview.locator("[data-preview-side=draft] [data-preview-group=platform-guild][data-mark=gained]")).toHaveCount(1);
  await expect(preview.locator("[data-preview-side=active] [data-preview-group=platform-guild]")).toHaveCount(0);
  await expect(preview.locator("[data-preview-side=draft] [data-preview-group=payments-eng]")).toHaveCount(1);

  // 4. What it changes, row by row.
  const changes = main(page).locator("[data-mapping-changes]");
  await expect(changes.locator("[data-change=claimAdded]")).toContainText("job_title");
  await expect(changes.locator("[data-change=entryAdded]")).toContainText("Platform Guild");

  // 5. Written through the typed confirm, which shows the same summary.
  await main(page).locator("[data-mapping-write]").click();
  const confirm = page.locator("dialog[open]");
  await expect(confirm.locator("[data-mapping-confirm] [data-change=entryAdded]")).toHaveCount(1);
  await confirm.locator("input").fill(TENANT);
  const written = page.waitForResponse((r) => r.url().endsWith(API) && r.request().method() === "PUT");
  await confirm.locator("button[type=submit]").click();
  expect((await written).status()).toBe(200);

  // The active version and its entries say so, on the screen and over the API.
  await expect(main(page).locator("[data-mapping-version]")).toHaveAttribute("data-mapping-version", String(before.version + 1));
  await expect(main(page).locator("[data-group-map]")).toHaveAttribute("data-group-map", /Platform Guild>platform-guild@corp/);
  await expect(main(page).locator("[data-mapping-draft=unchanged]")).toHaveCount(1);
  const after = (await (await page.request.get(API)).json()) as { version: number; attributes: string[]; group_map: { external: string; group: string; directories?: string[] }[] };
  expect(after.attributes).toContain("job_title");
  expect(after.group_map).toContainEqual({ external: "Platform Guild", group: "platform-guild", directories: ["corp"] });

  // The seeded mapping again, for the next run.
  const restored = await page.request.put(API, { data: { document: MAPPING } });
  expect(restored.status()).toBe(200);
  await page.context().close();
});
