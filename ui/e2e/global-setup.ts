// Runs once before every browser check: brings the seeded database into the
// states the registered screens need (each screen's `seed`, e2e/screens.ts),
// then signs each operator in and keeps their browser state, so a check
// starts signed in as the role it names.

import { mkdirSync } from "node:fs";
import { dirname } from "node:path";
import { request, type FullConfig } from "@playwright/test";
import { NORTH, OPERATORS, stateFile, TENANT, type Role } from "./harness";
import { SEEDS } from "./screens";

export async function signIn(role: Role) {
  const ctx = await request.newContext({ baseURL: NORTH });
  const res = await ctx.post("/api/v1/session/local", { data: OPERATORS[role] });
  if (!res.ok()) throw new Error(`sign in as ${role}: ${res.status()} ${await res.text()}`);
  return ctx;
}

/**
 * Waits for the deployment's health to settle AFTER the fixture: every node
 * reported, and the fleet counted, at an instant later than the fixture's
 * last write. The server computes both on its own interval, so a summary
 * read too early still describes the database before the fixture — and every
 * screen that shows it differs from the baseline. The settled answer is
 * printed on one line, so a CI log says what the screens were given.
 */
async function settled(admin: Awaited<ReturnType<typeof signIn>>, after: number): Promise<void> {
  for (let i = 0; i < 240; i++) {
    const res = await admin.get(`/api/v1/tenants/${TENANT}/instance/health`);
    if (res.ok()) {
      const h = (await res.json()) as {
        state: string;
        reasons: { code: string }[];
        nodes: { members: { reported_at: string | null }[] };
        fleet?: { state: string; observed_at: string | null; summary: Record<string, unknown> | null }[];
      };
      const fleet = h.fleet ?? [];
      const reported = h.nodes.members.length > 0 && h.nodes.members.every((m) => m.reported_at && Date.parse(m.reported_at) > after);
      const counted = fleet.length > 0 && fleet.every((f) => f.summary && f.observed_at && Date.parse(f.observed_at) > after);
      if (reported && counted) {
        console.log(`[fixture] health ${h.state} (${h.reasons.map((r) => r.code).join(",")}); fleet ${fleet[0]?.state} ${JSON.stringify(fleet[0]?.summary)}`);
        return;
      }
    }
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error("the deployment's health did not settle after the fixture in two minutes");
}

export default async function globalSetup(_: FullConfig): Promise<void> {
  const admin = await signIn("admin");
  for (const seed of SEEDS) await seed(admin);
  await settled(admin, Date.now());
  await admin.dispose();
  for (const role of Object.keys(OPERATORS) as Role[]) {
    const ctx = await signIn(role);
    mkdirSync(dirname(stateFile(role)), { recursive: true });
    await ctx.storageState({ path: stateFile(role) });
    await ctx.dispose();
  }
}
