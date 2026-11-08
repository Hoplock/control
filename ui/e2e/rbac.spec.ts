// THE RBAC HARNESS (0050; PLAN M2). RBAC is asserted at the API, not at the
// button: for every registered console screen and every role the fixture
// signs in as,
//   - each mutating call the screen offers is shown only to a role holding
//     its permission, and a role without it sees the read-only view;
//   - the API REFUSES that call to the role without it (403 forbidden) —
//     sent straight to the API with the role's own session, so a hidden
//     button is never what the assertion rests on;
//   - every API request the screen makes as that role names a route of the
//     server's table whose permission the role holds.
// A later phase adds its screen's mutating calls to its registry entry.

import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "@playwright/test";
import { OPERATORS, UI, type Role } from "./harness";
import { openScreen } from "./open";
import { SCREENS } from "./screens";

const ROLES = Object.keys(OPERATORS) as Role[];
const table = JSON.parse(readFileSync(join(UI, "test", "north-routes.json"), "utf8")) as {
  routes: { method: string; pattern: string; access: string; permission: string }[];
  roles: Record<string, string[]>;
};

function routeOf(method: string, path: string) {
  return table.routes.find((r) => {
    if (r.method !== method) return false;
    const re = new RegExp(`^${r.pattern.replace(/\{[a-z_]+\.{0,3}\}/g, "[^/]+")}$`);
    return re.test(path);
  });
}

for (const screen of SCREENS.filter((s) => s.kind === "console")) {
  for (const role of ROLES) {
    test(`${screen.id} as ${role}`, async ({ browser }) => {
      const permissions = table.roles[role] ?? [];
      const calls: { method: string; path: string }[] = [];
      const page = await openScreen(browser, { ...screen, role }, {
        before: async (context) => {
          context.on("request", (r) => {
            const url = new URL(r.url());
            if (url.pathname.startsWith("/api/")) calls.push({ method: r.method(), path: url.pathname });
          });
        },
      });

      for (const m of screen.mutations ?? []) {
        const holds = permissions.includes(m.permission);
        if (m.reveal) await m.reveal(page);
        await expect(m.control(page), `${role} ${holds ? "is" : "is not"} offered ${m.route}`).toHaveCount(holds ? 1 : 0);
        await page.keyboard.press("Escape");
        if (!holds) {
          const res = await page.request.fetch(m.path, { method: m.method, data: m.body ?? {} });
          expect(res.status(), `the API refuses ${m.route} to ${role}`).toBe(403);
          expect(((await res.json()) as { code: string }).code).toBe("forbidden");
        }
      }

      expect(calls.length, "the screen read something").toBeGreaterThan(0);
      for (const c of calls) {
        const r = routeOf(c.method, c.path);
        expect(r, `${c.method} ${c.path} is a route of the table`).toBeDefined();
        if (r?.permission) expect(permissions, `${role} holds ${r.permission} for ${c.method} ${c.path}`).toContain(r.permission);
      }
      await page.context().close();
    });
  }
}
