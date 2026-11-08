// What changes the seeded data runs last (the `mutations` project), so no
// screenshot sees it. Fleet's one mutation: a fleet-admin withdraws a proxy,
// and the console makes it look and act destructive — it confirms by typing
// the proxy's id, refuses a near miss, and then the API's answer is the row.

import { expect, test } from "@playwright/test";
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
