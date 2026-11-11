// 0055's acceptance criteria, against the seeded fixture (fixture/standing.ts,
// and the fixture host's directory): standing denials and the directory's
// facts wherever their subject or target is shown, the levers offered to a
// holder of session:revoke and to nobody else, and the claim mapping's
// directory-scoped entries. Nothing here changes the seeded data: what does
// runs last, in mutations.spec.ts. RBAC at the API for every lever is
// rbac.spec.ts's, over the registry's mutations.

import { expect, test, type Page } from "@playwright/test";
import { DIRECTORY, fixtureSubject } from "./fixture/standing";
import { substitute, TENANT, type Role } from "./harness";
import { openScreen } from "./open";
import { SCREENS, UNDELIVERED, type E2EScreen } from "./screens";

const API = `/api/v1/tenants/${TENANT}`;

function screen(id: string): E2EScreen {
  const s = SCREENS.find((x) => x.id === id);
  if (!s) throw new Error(`${id} is not registered`);
  return s;
}

const main = (page: Page) => page.locator("main#content");
const row = (page: Page, id: string) => main(page).locator(`tr[data-row="${id}"]`);

/** A status as the one component draws it: its tone and its word. */
async function expectDeny(scope: ReturnType<Page["locator"]>, word: string) {
  const status = scope.locator('[data-tone="deny"]').filter({ hasText: word });
  await expect(status.first()).toBeVisible();
  await expect(scope.locator('[data-tone="outage"]').filter({ hasText: word })).toHaveCount(0);
}

test.describe("standing denials in the inventory", () => {
  test("the list shows a lock, a deactivation, both, and the denials with no record", async ({ browser }) => {
    const page = await openScreen(browser, screen("inventory-subjects"));
    await expectDeny(row(page, "noor"), "Locked");
    await expectDeny(row(page, fixtureSubject(DIRECTORY.eli)), "Deactivated");
    const sam = row(page, fixtureSubject(DIRECTORY.sam));
    await expect(sam.locator("[data-denials]")).toHaveAttribute("data-denials", "locked deactivated");
    await expect(row(page, "lin").locator("[data-denials]")).toHaveCount(0);

    // Removed while locked, and deactivated before created: listed by the denial alone.
    const missing = main(page).locator("[data-record-missing=ok]");
    await expect(missing.locator('tr[data-row="ex-contractor"] [data-denials="locked"]')).toHaveCount(1);
    const early = missing.locator(`tr[data-row="${fixtureSubject(DIRECTORY.early)}"]`);
    await expect(early.locator('[data-denials="deactivated"]')).toHaveCount(1);
    await expect(early.locator("[data-directory]")).toHaveAttribute("data-directory", DIRECTORY.early);
    await page.context().close();
  });

  test("a directory subject shows its provider, its directory and the directory's id, in the list and the detail", async ({ browser }) => {
    const list = await openScreen(browser, screen("inventory-subjects"));
    for (const ref of [DIRECTORY.dana, DIRECTORY.danaLab]) {
      const r = row(list, fixtureSubject(ref));
      await expect(r.locator("[data-directory]")).toHaveAttribute("data-directory", ref);
      await expect(r).toContainText("e2e-directory");
    }
    // One id, two directories, two people.
    expect(fixtureSubject(DIRECTORY.dana)).not.toBe(fixtureSubject(DIRECTORY.danaLab));
    await list.context().close();

    const page = await openScreen(browser, screen("inventory-subject-directory"));
    const panel = main(page).locator("[data-directory-panel]");
    for (const fact of ["e2e-directory", "corp", "00u1"]) await expect(panel).toContainText(fact);
    await page.context().close();
  });

  test("a directory subject is offered no edit and no removal, and the API refuses both as managed by the directory", async ({ browser }) => {
    const page = await openScreen(browser, screen("inventory-subject-directory"));
    await expect(main(page).locator("[data-edit], [data-remove]")).toHaveCount(0);
    // The lock and the role bindings stay an operator's.
    await expect(main(page).locator("[data-lock]")).toHaveCount(1);
    await expect(main(page).locator("form[data-bind-form]")).toHaveCount(1);
    const id = fixtureSubject(DIRECTORY.dana);
    for (const method of ["PUT", "DELETE"] as const) {
      const res = await page.request.fetch(`${API}/subjects/${id}`, { method, data: method === "PUT" ? { source: "local", display_name: "x" } : undefined });
      expect(res.status(), `${method} of a directory subject`).toBe(409);
      expect(((await res.json()) as { code: string }).code).toBe("subject_directory_managed");
    }
    await page.context().close();
  });

  test("a lock's facts are shown, and the holder's text", async ({ browser }) => {
    const page = await openScreen(browser, screen("inventory-subject-locked"));
    const lock = main(page).locator("[data-denial=locked]");
    await expectDeny(lock, "Locked");
    await expect(lock).toContainText("INC-4821");
    await expect(lock).toContainText("Call the SOC on 4821.");
    await expect(lock.locator('a[href="/console/inventory/subjects/ada"]')).toHaveCount(1);
    await expect(lock.locator("time")).toHaveCount(1);
    await page.context().close();
  });

  test("a deactivation is its own deny, with when and its code, and no control claims to lift it", async ({ browser }) => {
    const page = await openScreen(browser, screen("inventory-subject-deactivated"));
    const d = main(page).locator("[data-denial=deactivated]");
    await expectDeny(d, "Deactivated");
    await expect(d).toContainText("offboarded");
    await expect(d.locator("time")).toHaveCount(1);
    await expect(main(page).locator("[data-denial=locked], [data-unlock]")).toHaveCount(0);
    await expect(main(page).locator("button").filter({ hasText: /activat/i })).toHaveCount(0);
    await page.context().close();
  });

  test("locked and deactivated shows both, the lock first, and unlocking says the deactivation still stands", async ({ browser }) => {
    const page = await openScreen(browser, screen("inventory-subject-unlock-deactivated"));
    const denials = main(page).locator("[data-denial]");
    await expect(denials).toHaveCount(2);
    await expect(denials.nth(0)).toHaveAttribute("data-denial", "locked");
    await expect(denials.nth(1)).toHaveAttribute("data-denial", "deactivated");
    const dialog = page.locator("dialog[open]");
    await expect(dialog.locator("[data-unlock-deactivated]")).toHaveCount(1);
    await expect(dialog.locator("button[type=submit]")).toHaveText("Lift lock");
    await page.context().close();
  });

  test("a subject the directory deactivated before creating it is read as record_missing", async ({ browser }) => {
    const page = await openScreen(browser, screen("inventory-subject-deactivated-early"));
    await expect(main(page).locator("[data-denials]").first()).toContainText("No record");
    await expect(main(page).locator("[data-edit], [data-remove]")).toHaveCount(0);
    await page.context().close();
  });

  test("a subject removed while locked can still be unlocked", async ({ browser }) => {
    const page = await openScreen(browser, screen("inventory-subject-record-missing"));
    await expectDeny(main(page).locator("[data-denial=locked]"), "Locked");
    await expect(main(page).locator("[data-unlock]")).toHaveCount(1);
    await page.context().close();
  });

  test("a quarantined target says so in the list and the detail, with its facts", async ({ browser }) => {
    const list = await openScreen(browser, screen("inventory-targets"));
    await expectDeny(row(list, "cache-01"), "Quarantined");
    await expect(row(list, "db-01").locator("[data-denials]")).toHaveCount(0);
    await list.context().close();
    const page = await openScreen(browser, screen("inventory-target-quarantined"));
    const q = main(page).locator("[data-denial=quarantined]");
    await expectDeny(q, "Quarantined");
    await expect(q).toContainText("INC-4821");
    await expect(q).toContainText("forensics under way");
    await page.context().close();
  });

  test("removing a quarantined target renders target_quarantined as release it first", async ({ browser }) => {
    const page = await openScreen(browser, screen("inventory-target-remove-refused"));
    await expect(page.locator("dialog[open] [role=alert]")).toContainText("Release it first");
    await page.context().close();
  });
});

test.describe("the levers are a session:revoke holder's", () => {
  for (const role of ["admin", "grant-admin", "auditor", "fleet-admin"] as Role[]) {
    const holds = role === "admin" || role === "grant-admin";
    test(`${role} ${holds ? "is" : "is not"} offered lock, unlock, quarantine and release`, async ({ browser }) => {
      for (const [id, control] of [
        ["inventory-subject", "[data-lock]"],
        ["inventory-subject-locked", "[data-unlock]"],
        ["inventory-target", "[data-quarantine]"],
        ["inventory-target-quarantined", "[data-release]"],
      ] as const) {
        const page = await openScreen(browser, { ...screen(id), role });
        await expect(main(page).locator(control), `${id} ${control}`).toHaveCount(holds ? 1 : 0);
        await page.context().close();
      }
    });
  }

  test("a quarantine says it ends no session and points to a revocation", async ({ browser }) => {
    const page = await openScreen(browser, screen("inventory-target-quarantine-dialog"));
    const dialog = page.locator("dialog[open]");
    await expect(dialog.locator("[data-quarantine-sessions]")).toContainText("ends no session");
    await expect(dialog).toContainText(`POST /api/v1/tenants/${TENANT}/revocations`);
    await page.context().close();
  });

  test("locking shows the holder's text before it is sent, and refuses one it would", async ({ browser }) => {
    const page = await openScreen(browser, { ...screen("inventory-subject"), role: "grant-admin" });
    const sent: string[] = [];
    page.on("request", (r) => {
      if (r.url().endsWith("/subjects/lin/lock")) sent.push(r.method());
    });
    await main(page).locator("[data-lock]").click();
    const dialog = page.locator("dialog[open]");
    const preview = dialog.locator("[data-holder-preview]");
    // Empty: Control's words, verbatim.
    await expect(preview).toContainText("Your access has been suspended by an administrator.");
    await dialog.locator("input[name=reason]").fill("Paused while we check your laptop.");
    await expect(preview).toContainText("Paused while we check your laptop.");
    await dialog.locator("[data-confirm-input]").fill("lin");
    const confirm = dialog.locator("button[type=submit]");
    await expect(confirm).toBeEnabled();
    // Longer than the 512 bytes a holder can be shown: refused where it is typed.
    await dialog.locator("input[name=reason]").fill("é".repeat(257));
    await expect(dialog.locator("[aria-invalid=true]")).toHaveCount(1);
    await expect(confirm).toBeDisabled();
    expect(sent).toEqual([]);
    await page.context().close();
  });

  test("an undelivered lock reads as in force, not yet delivered, and its retry asks again", async ({ browser }) => {
    const page = await openScreen(browser, screen("inventory-subject"));
    let asked = 0;
    await substitute(page, `${API}/subjects/lin/lock`, UNDELIVERED);
    page.on("request", (r) => {
      if (r.url().endsWith("/subjects/lin/lock") && r.method() === "POST") asked++;
    });
    await main(page).locator("[data-lock]").click();
    const dialog = page.locator("dialog[open]");
    await dialog.locator("[data-confirm-input]").fill("lin");
    await dialog.locator("button[type=submit]").click();
    const notice = main(page).locator("[data-undelivered]");
    await expect(notice).toContainText("in force, not yet delivered");
    await expect(notice.locator('span[data-tone="info"]')).toHaveCount(1);
    await expect(main(page).locator('[data-tone="outage"]')).toHaveCount(0);
    await expect(page.locator("dialog[open]")).toHaveCount(0);
    await notice.locator("[data-retry-delivery]").click();
    await expect.poll(() => asked).toBe(2);
    await expect(main(page).locator("[data-undelivered]")).toHaveCount(1);
    await page.context().close();
  });
});

test.describe("the directory's door", () => {
  test("the sign-in page shows a tied connector's claim beside its directory", async ({ browser }) => {
    const page = await openScreen(browser, screen("sign-in"));
    const tied = page.locator('[data-connector="corp-sso"] [data-directory-tie]');
    await expect(tied).toContainText("employee_number");
    await expect(tied).toContainText("corp");
    await expect(page.locator('[data-connector="partner-sso"] [data-directory-tie]')).toHaveCount(0);
    await page.context().close();
  });

  test("the claim mapping shows each entry's directories, require_directories and the connectors' ties", async ({ browser }) => {
    const page = await openScreen(browser, screen("inventory-claim-mapping"));
    const entries = main(page).locator("[data-entries] tbody tr");
    await expect(entries).toHaveCount(3);
    await expect(entries.nth(0)).toContainText("payments-eng");
    await expect(entries.nth(0)).toContainText("corp");
    await expect(entries.nth(1)).toContainText("lab");
    await expect(entries.nth(2)).toContainText("every directory not listed for this name");
    await expect(main(page).locator("[data-require-directories]")).toHaveAttribute("data-require-directories", "false");
    await expect(main(page).locator("[data-directories]")).toHaveAttribute("data-directories", "corp lab");
    await expect(main(page).locator("[data-stale]")).toHaveCount(0);
    const corp = main(page).locator('[data-connectors] tr[data-row="corp-sso"]');
    await expect(corp).toContainText("employee_number");
    await expect(corp).toContainText("corp");
    await page.context().close();
  });

  test("the claim mapping says how many directory identities are not yet on it, while any are not", async ({ browser }) => {
    const page = await openScreen(browser, screen("inventory-claim-mapping"), { state: "partial" });
    await expect(main(page).locator("[data-stale]")).toContainText("3 directory identities are still being brought onto version 1");
    await page.context().close();
  });

  test("each of the three directory rejections renders with its field", async ({ browser }) => {
    const page = await openScreen(browser, screen("inventory-claim-mapping-refused"));
    const list = main(page).locator("[data-rejections]");
    for (const [code, field] of [
      ["mapping.directory_name", "groups.map[0].directories[0]"],
      ["mapping.directories_empty", "groups.map[1].directories"],
      ["mapping.directory_duplicate", "groups.map[3].directories[0]"],
    ] as const) {
      const item = list.locator(`li[data-rejection="${code}"][data-field="${field}"]`);
      await expect(item, code).toHaveCount(1);
      await expect(item).toContainText(field);
      await expect(item).not.toContainText(code);
    }
    await page.context().close();
  });

  test("Explain shows the directory a provisioned subject's groups came from, and nothing for one that is not", async ({ browser }) => {
    const page = await openScreen(browser, screen("explain-directory"));
    await expect(main(page).locator("[data-mapping-directory]")).toContainText("corp");
    await page.context().close();
    const other = await openScreen(browser, screen("explain-deny"));
    await expect(main(other).locator("[data-story-section=explain-mapping]")).toHaveCount(1);
    await expect(main(other).locator("[data-mapping-directory]")).toHaveCount(0);
    await other.context().close();
  });
});
