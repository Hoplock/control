// ALGORITHM POSTURE'S ACCEPTANCE CHECKS THAT CHANGE NOTHING (0058). Against
// the seeded fixture (fixture/algorithms.ts: three builds mid-upgrade, a
// target below pq-hybrid-kex, one stale and one never observed):
//   - Fleet shows each proxy's declared levels and profile offers per build;
//     builds that disagree are in progress, never a fault; a proxy that
//     declared nothing is Not reported;
//   - the floor's impact preview names the target below a candidate floor,
//     shows the stale and the unobserved one as Not reported, and names the
//     proxy that cannot enforce the level;
//   - a ban that empties an axis on one of two builds is a per-build warning
//     naming that proxy, in progress, and it does not block the publish.
// The runbook, which changes the data, is runbook.spec.ts, in mutations.

import { expect, test, type Page } from "@playwright/test";
import { BANNED, floorCandidate } from "./fixture/algorithms";
import { TENANT } from "./harness";
import { openScreen } from "./open";
import { SCREENS } from "./screens";

const API = `/api/v1/tenants/${TENANT}`;
const screen = (id: string) => {
  const s = SCREENS.find((x) => x.id === id);
  if (!s) throw new Error(`${id} is not registered`);
  return s;
};
const main = (page: Page) => page.locator("main#content");

test("Fleet shows each build's levels and profile offers; disagreement is in progress; nothing declared is Not reported", async ({ browser }) => {
  const page = await openScreen(browser, screen("fleet-algorithms"));
  const levels = main(page).locator("[data-levels]");
  // Builds 1 and 3 accept ML-KEM under modern-kex; build 2 does not.
  const modern = levels.locator('tr[data-level="modern-kex"]');
  await expect(modern).toHaveAttribute("data-posture", "inProgress");
  await expect(modern).toContainText("In progress");
  await expect(modern.locator("[data-variant]")).toHaveCount(2);
  // pq-hybrid-kex: px-core-02 cannot enforce it yet, named.
  const pq = levels.locator('tr[data-level="pq-hybrid-kex"]');
  await expect(pq).toHaveAttribute("data-posture", "inProgress");
  await expect(pq.locator("[data-proxies]").first()).toHaveAttribute("data-proxies", "px-core-02");
  await expect(levels.locator("[data-pq-note]")).toContainText("OpenSSH 9.9");
  // Never "fault" anywhere on it.
  await expect(levels.locator("[data-tone=deny], [data-tone=outage]")).toHaveCount(0);

  // Per build: what each profile offers per axis.
  const builds = main(page).locator("[data-builds] [data-build]");
  await expect(builds).toHaveCount(3);
  const old = main(page).locator('[data-build]:has([data-proxies="px-core-02"])');
  await expect(old.locator('tr[data-offer="default"]')).toContainText(BANNED);
  // A build that declared no profiles: Not reported, never "offers nothing".
  const floorsOnly = main(page).locator('[data-build]:has([data-proxies="px-dmz-03"])');
  await expect(floorsOnly).toContainText("Not reported");
  await expect(floorsOnly.locator("[data-offer]")).toHaveCount(0);

  // A proxy that declared nothing is Not reported, never "no floor".
  const nothing = main(page).locator("[data-undeclared]");
  await expect(nothing).toContainText("Not reported");
  await expect(nothing.locator("[data-proxies]")).toHaveAttribute("data-proxies", /px-dmz-04/);
  await page.context().close();
});

test("the floor's impact preview names the target below it, the unreported ones and the proxy that cannot enforce it", async ({ browser }) => {
  const page = await openScreen(browser, screen("policy-candidate"));
  await main(page).locator("[data-candidate-source]").fill(floorCandidate());
  await main(page).locator("[data-validate]").click();

  const below = main(page).locator('[data-finding^="algorithm.target_below_floor"]');
  await expect(below).toHaveAttribute("data-severity", "warning");
  const db = below.locator('tr[data-target="db-01.core.internal"]');
  await expect(db).toContainText("modern-kex");
  await expect(db).toContainText("curve25519-sha256");
  await expect(db.locator("time")).toHaveCount(1);
  await expect(below).toContainText("never blocks");
  await expect(below.locator("[data-pq-note]")).toContainText("mlkem768x25519-sha256");
  await expect(below.locator("[data-pq-note]")).toContainText("Does not meet it");
  await expect(below.locator("[data-pq-note]")).toContainText("sntrup761x25519-sha512");

  // The stale observation and the absent one: Not reported, never met.
  const unreported = main(page).locator('[data-finding^="algorithm.target_not_reported"]');
  await expect(unreported.locator('[data-target="api-01.core.internal"]')).toContainText("Not reported");
  await expect(unreported.locator('[data-target="vault-01.dmz.internal"]')).toContainText("Not reported");
  await expect(main(page).locator('[data-finding^="algorithm.target_below_floor"] [data-target="api-01.core.internal"]')).toHaveCount(0);

  // The proxy whose build does not declare the level.
  const cannot = main(page).locator('[data-finding^="algorithm.floor_undeclared"]');
  await expect(cannot.locator("[data-proxies]")).toHaveAttribute("data-proxies", "px-core-02");
  await page.context().close();
});

test("a ban that empties an axis on one of two builds warns per build, in progress, and does not block", async ({ browser }) => {
  const page = await openScreen(browser, screen("policy-candidate"));
  await main(page).locator("[data-candidate-source]").fill(floorCandidate());
  await main(page).locator("[data-validate]").click();

  const emptied = main(page).locator('[data-finding^="algorithm.axis_emptied/prod-pq-floor/ciphers"]');
  await expect(emptied).toHaveAttribute("data-severity", "warning");
  await expect(emptied.locator("tr[data-proxy]")).toHaveCount(1);
  const row = emptied.locator('tr[data-proxy="px-core-02"]');
  await expect(row).toContainText(BANNED);
  await expect(emptied.locator("[data-ban-progress]")).toHaveAttribute("data-ban-progress", "inProgress");
  await expect(emptied).toContainText("never blocks");
  // The build that cannot be judged is shown as such, never as fine.
  await expect(main(page).locator('[data-finding^="algorithm.declaration_unknown/prod-pq-floor"]')).toContainText("Not judged");

  // It does not block: the server's report holds it as a warning to accept,
  // and nothing blocking.
  const res = await page.request.post(`${API}/policy/validate`, { data: { source: floorCandidate() } });
  expect(res.ok()).toBe(true);
  const report = (await res.json()) as { valid: boolean; findings: { code: string; severity: string; parameters?: { proxies?: { proxy_id: string }[] } }[] };
  expect(report.valid).toBe(true);
  expect(report.findings.filter((f) => f.severity === "blocking")).toEqual([]);
  const axis = report.findings.filter((f) => f.code === "algorithm.axis_emptied");
  expect(axis.length).toBeGreaterThan(0);
  for (const f of axis) {
    expect(f.severity).toBe("warning");
    expect(f.parameters?.proxies?.map((p) => p.proxy_id)).toEqual(["px-core-02"]);
  }
  await page.context().close();
});
