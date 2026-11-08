// What Fleet shows for a proxy, as the status vocabulary names it. Kept apart
// from the screen so a test can hold each rule without a browser.

import type { ConfigRollout, FleetSummary, Proxy } from "../api/types";
import type { StatusName } from "../components/Status";

/**
 * A proxy's health, in the server's own terms (fleet.FleetSummary), so the
 * table and the summary above it never disagree: live is healthy; enrolled
 * and heard from before but silent past its TTL is stale; enrolled and never
 * heard from at all is unreachable; an enrollment still pending is pending;
 * a withdrawn proxy is revoked, however loudly it reports.
 */
export function proxyHealth(p: Proxy): StatusName {
  switch (p.state) {
    case "revoked":
      return "revoked";
    case "pending":
      return "pending";
    case "enrolled":
      break;
    default:
      return "notReported";
  }
  if (p.live) return "healthy";
  if (!p.last_heartbeat_at && !p.subscription_seen_at) return "unreachable";
  return "stale";
}

/**
 * A proxy's configuration as it REPORTED it (proxy D18): never inferred from
 * what was published. A proxy that has never reported is its own state — Not
 * reported — and never `applied` (PLAN M17); an applied report of an older
 * document while a newer one is desired is a rollout in progress (M19), not
 * a fault; pending_restart is waiting, not failing; a rejected document is
 * the proxy refusing it and keeping its last one; a failed fetch is the proxy
 * not reaching this server — an outage, never a deny (M11).
 */
export function configStatus(c: ConfigRollout): StatusName {
  switch (c.state) {
    case undefined:
    case "":
      return "notReported";
    case "applied":
      return c.desired_version && !c.caught_up ? "inProgress" : "applied";
    case "pending_restart":
      return "pendingRestart";
    case "rejected":
      return "rejected";
    case "fetch_failed":
      return "fetchFailed";
    default:
      return "notReported";
  }
}

/** The proxies on this page Fleet cannot currently hear: the gap partial names. */
export function unreachable(proxies: readonly Proxy[]): Proxy[] {
  return proxies.filter((p) => {
    const h = proxyHealth(p);
    return h === "unreachable" || h === "stale";
  });
}

/** The chart's segments, in order, from the server's whole-tenant summary. */
export const CHART_SEGMENTS = ["live", "stale", "unreachable", "pending", "revoked"] as const;
export type ChartSegment = (typeof CHART_SEGMENTS)[number];

export const SEGMENT_STATUS: Readonly<Record<ChartSegment, StatusName>> = {
  live: "healthy",
  stale: "stale",
  unreachable: "unreachable",
  pending: "pending",
  revoked: "revoked",
};

export function segmentCounts(s: FleetSummary): Record<ChartSegment, number> {
  return { live: s.live, stale: s.stale, unreachable: s.unreachable, pending: s.pending, revoked: s.revoked };
}

/** The filters Fleet's URL carries, read back out of it. */
export interface FleetFilters {
  zone?: string;
  health?: StatusName;
  config?: StatusName;
  q?: string;
}

export function applyFilters(proxies: readonly Proxy[], f: FleetFilters, locale: string): Proxy[] {
  const q = f.q?.trim().toLocaleLowerCase(locale);
  return proxies.filter(
    (p) =>
      (!f.zone || p.zone === f.zone) &&
      (!f.health || proxyHealth(p) === f.health) &&
      (!f.config || configStatus(p.config) === f.config) &&
      (!q || p.proxy_id.toLocaleLowerCase(locale).includes(q) || p.zone.toLocaleLowerCase(locale).includes(q)),
  );
}
