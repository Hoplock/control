// What the chrome shows, decided from the API's answers and nothing else
// (DESIGN.md §5, PLAN M18, M19). Pure, so each rule is asserted directly.

import type { MessageKey } from "../i18n/catalogue";
import type { HealthState, Session, Supervisor } from "../api/types";
import type { Tone } from "../components/Status";
import type { Load } from "./load";

/**
 * The tenants this session may act in: the scopes the API reported for it.
 * The console never widens it — a tenant is a selector within this set (M18).
 */
export function tenantsInScope(session: Session): string[] {
  return Object.keys(session.scopes).sort();
}

/** The tenant the console acts in: the operator's last, while it is in scope. */
export function currentTenant(tenants: readonly string[], preferred: string | undefined): string | null {
  if (preferred && tenants.includes(preferred)) return preferred;
  return tenants[0] ?? null;
}

/**
 * Whether the chrome shows a tenant at all. With one tenant in scope it does
 * not: tenancy is invisible to a deployment that does not use it (M18).
 */
export function showsTenant(tenants: readonly string[]): boolean {
  return tenants.length > 1;
}

/** Whether a permission is held in a tenant. A courtesy: the API is the control. */
export function holds(session: Session, tenant: string | null, permission: string): boolean {
  return tenant !== null && (session.scopes[tenant]?.permissions ?? []).includes(permission);
}

export interface StatusView {
  tone: Tone;
  label: MessageKey;
}

/**
 * A health state in the status vocabulary (DESIGN.md §6). An upgrade in
 * progress is info, never a fault (M19); unknown is Not reported, never
 * healthy (M17); and a health that could not be read is an outage, never a
 * denial (M11).
 */
export const HEALTH_STATUS: Readonly<Record<HealthState | "unreachable", StatusView>> = {
  healthy: { tone: "allow", label: "status.healthy" },
  degraded: { tone: "warn", label: "status.degraded" },
  in_progress: { tone: "info", label: "status.inProgress" },
  unknown: { tone: "unknown", label: "status.notReported" },
  stale: { tone: "warn", label: "status.stale" },
  left: { tone: "unknown", label: "status.left" },
  unreachable: { tone: "outage", label: "status.unreachable" },
};

export function healthStatus(health: Load<{ state: HealthState }>): StatusView {
  switch (health.state) {
    case "ok":
      return HEALTH_STATUS[health.data.state] ?? HEALTH_STATUS.unknown;
    case "outage":
      return HEALTH_STATUS.unreachable;
    case "loading":
    case "refused":
      return HEALTH_STATUS.unknown;
  }
}

/**
 * The supervisors the badge names: the live supervisor credentials in this
 * tenant (M19). Being supervised is a fact, not a fault, so the badge is
 * info, never warn (DESIGN.md §5).
 */
export function liveSupervisors(load: Load<{ supervisors: Supervisor[] }>): Supervisor[] {
  return load.state === "ok" ? load.data.supervisors.filter((s) => s.live) : [];
}
