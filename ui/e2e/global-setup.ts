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
 * Waits for the deployment's health to settle — every node reported and the
 * fleet counted — so no screenshot catches the first seconds after start.
 */
async function settled(admin: Awaited<ReturnType<typeof signIn>>): Promise<void> {
  for (let i = 0; i < 120; i++) {
    const res = await admin.get(`/api/v1/tenants/${TENANT}/instance/health`);
    if (res.ok()) {
      const h = (await res.json()) as { nodes: { members: { reported_at: string | null }[] }; fleet?: { summary: unknown }[] };
      const reported = h.nodes.members.length > 0 && h.nodes.members.every((m) => m.reported_at);
      if (reported && h.fleet?.every((f) => f.summary)) return;
    }
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error("the deployment's health did not settle in a minute");
}

export default async function globalSetup(_: FullConfig): Promise<void> {
  const admin = await signIn("admin");
  for (const seed of SEEDS) await seed(admin);
  await settled(admin);
  await admin.dispose();
  for (const role of Object.keys(OPERATORS) as Role[]) {
    const ctx = await signIn(role);
    mkdirSync(dirname(stateFile(role)), { recursive: true });
    await ctx.storageState({ path: stateFile(role) });
    await ctx.dispose();
  }
}
