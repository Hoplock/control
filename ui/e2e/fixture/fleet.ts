// Fleet's fixture (0050): moves the seeded proxies into every configuration
// state the screen must show, through the two real surfaces — publishing
// each proxy's document north-bound as an admin, and reporting what it runs
// south-bound as the proxy itself, with its own credential (proxy D18). The
// seed (seed.yaml) already holds health, relays and enrollment.

import type { APIRequestContext } from "@playwright/test";
import { SOUTH, TENANT } from "../harness";

interface Published {
  changed: { proxy_id: string; version: string; hash: string }[];
}

async function publish(api: APIRequestContext, proxy: string, batch: number): Promise<{ version: string; hash: string }> {
  const res = await api.put(`/api/v1/tenants/${TENANT}/fleet/config/proxy/${proxy}`, {
    data: { settings: { "logging.batch_size": batch } },
  });
  if (!res.ok()) throw new Error(`publish ${proxy}: ${res.status()} ${await res.text()}`);
  const body = (await res.json()) as Published;
  const doc = body.changed.find((c) => c.proxy_id === proxy);
  if (!doc) throw new Error(`publish ${proxy}: no document announced`);
  return doc;
}

async function report(api: APIRequestContext, proxy: string, body: Record<string, unknown>): Promise<void> {
  const res = await api.post(`${SOUTH}/v1/proxies/${proxy}/config/report`, {
    headers: { authorization: `Bearer ${TENANT}.e2e-${proxy}` },
    data: { reported_at: "2026-11-01T08:00:00Z", ...body },
  });
  if (!res.ok()) throw new Error(`report ${proxy}: ${res.status()} ${await res.text()}`);
}

/** `admin` is an admin's signed-in API context. */
export async function seedFleet(admin: APIRequestContext): Promise<void> {
  // Once per server: a reused local server already holds these documents.
  const seen = await admin.get(`/api/v1/tenants/${TENANT}/proxies?limit=1`);
  const first = (await seen.json()) as { proxies: { config: { desired_version?: string } }[] };
  if (first.proxies[0]?.config.desired_version) return;
  // Applied, and caught up.
  const a = await publish(admin, "px-core-01", 200);
  await report(admin, "px-core-01", {
    desired_version: a.version, desired_hash: a.hash, running_version: a.version, running_hash: a.hash, state: "applied",
  });
  // Pending a restart: nothing of the new document is in force yet.
  const b0 = await publish(admin, "px-core-02", 200);
  await report(admin, "px-core-02", {
    desired_version: b0.version, desired_hash: b0.hash, running_version: b0.version, running_hash: b0.hash, state: "applied",
  });
  const b = await publish(admin, "px-core-02", 400);
  await report(admin, "px-core-02", {
    desired_version: b.version, desired_hash: b.hash, running_version: b0.version, running_hash: b0.hash,
    state: "pending_restart", restart_required: ["logging.batch_size", "logging.queue_size"],
  });
  // Rejected: the proxy refused the document and kept its own.
  const c = await publish(admin, "px-dmz-03", 300);
  await report(admin, "px-dmz-03", {
    desired_version: c.version, desired_hash: c.hash, state: "rejected",
    last_error: "logging.batch_size: value out of range for this build",
  });
  // Fetch failed: the proxy could not reach Control for the document.
  const d = await publish(admin, "px-dmz-04", 300);
  await report(admin, "px-dmz-04", {
    desired_version: d.version, desired_hash: d.hash, state: "fetch_failed",
    last_error: "fetch: connection reset before the document arrived",
  });
  // A rollout in progress: it runs v1 and v2 is desired.
  const e1 = await publish(admin, "px-edge-05", 100);
  await report(admin, "px-edge-05", {
    desired_version: e1.version, desired_hash: e1.hash, running_version: e1.version, running_hash: e1.hash, state: "applied",
  });
  await publish(admin, "px-edge-05", 150);
  // Never reported: a document is desired and the proxy has said nothing.
  await publish(admin, "px-edge-06", 100);
}
