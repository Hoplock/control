// THE ACCOUNT MENU: the operator's name at the end of the top bar opens their
// display preferences — theme and density, each a group of choices — and
// signing out. Choosing changes only this browser, so it is checked here; a
// sign-in or sign-out writes the audit trail, so those are in mutations.spec.

import { expect, test } from "@playwright/test";
import { stateFile } from "./harness";

test("the account menu sets the theme and the density, and they hold across a reload", async ({ browser }) => {
  const context = await browser.newContext({ storageState: stateFile("admin"), viewport: { width: 1280, height: 800 }, timezoneId: "UTC" });
  const page = await context.newPage();
  await page.goto("/console/deployment");
  const account = page.locator("[data-testid=account] button[aria-haspopup=menu]");
  await expect(account).toContainText("Ada Admin");
  await expect(page.locator("#console-nav select")).toHaveCount(0);

  await account.click();
  const menu = page.locator("[role=menu]:popover-open");
  await expect(menu).toContainText("Signed in as Ada Admin");
  await menu.getByRole("menuitemradio", { name: "Dark" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  // A choice keeps the menu open, so the second is one more click.
  await menu.getByRole("menuitemradio", { name: "Compact" }).click();
  await expect(page.locator("[data-density]").first()).toHaveAttribute("data-density", "compact");
  await expect(menu.getByRole("menuitemradio", { name: "Dark" })).toHaveAttribute("aria-checked", "true");
  await page.keyboard.press("Escape");
  await expect(menu).toHaveCount(0);

  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect(page.locator("[data-density]").first()).toHaveAttribute("data-density", "compact");
  await context.close();
});
