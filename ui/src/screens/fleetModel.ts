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

/**
 * What the health bar is laid out in, in CSS pixels: its width as drawn, and
 * the three layout tokens (DESIGN.md §3.6) as the bar resolves them.
 */
export interface BarGeometry {
  /** The bar's width. */
  track: number;
  /** `--chart-segment-gap`: the surface left between two segments. */
  gap: number;
  /** `--chart-segment-min`: the narrowest a segment is drawn, so one proxy of hundreds is seen. */
  minWidth: number;
  /** `--chart-label-min`: the narrowest segment that carries its glyph and count. */
  labelMin: number;
}

/** One segment of the bar, as drawn. */
export interface BarSegment {
  segment: ChartSegment;
  count: number;
  /** From the bar's inline start: a right-to-left page measures it from the right. */
  start: number;
  width: number;
  /** Whether it carries its glyph and count; a narrower one is named by the legend alone. */
  labelled: boolean;
}

/**
 * The health bar's layout: one segment per state with a proxy in it, in
 * CHART_SEGMENTS' order, each as wide as its share of the fleet, with a gap
 * between consecutive segments. A segment whose share is narrower than the
 * minimum is drawn at the minimum, and the width it takes comes out of the
 * segments sized by their share — so the bar still ends at its track, and a
 * segment the minimum squeezes below the minimum in turn is held there too.
 * Where the track cannot hold every segment at the minimum, every segment
 * shares the room equally, and where it cannot hold the gaps, the gaps
 * shrink: the bar never runs past its track.
 */
export function layoutBar(counts: Readonly<Record<ChartSegment, number>>, g: BarGeometry): BarSegment[] {
  const shown = CHART_SEGMENTS.filter((s) => counts[s] > 0);
  if (shown.length === 0 || !(g.track > 0)) return [];
  const gaps = shown.length - 1;
  const gap = gaps > 0 ? Math.min(Math.max(0, g.gap), g.track / gaps) : 0;
  const room = Math.max(0, g.track - gap * gaps);
  const floor = Math.min(Math.max(0, g.minWidth), room / shown.length);
  const width = new Map<ChartSegment, number>();
  let free: ChartSegment[] = shown;
  let left = room;
  while (free.length > 0) {
    const total = free.reduce((n, s) => n + counts[s], 0);
    const share = (s: ChartSegment) => (counts[s] / total) * left;
    const under = free.filter((s) => share(s) < floor);
    if (under.length === 0) {
      for (const s of free) width.set(s, share(s));
      break;
    }
    if (under.length === free.length) {
      // Only reached by rounding: the room left is the minimum for each.
      for (const s of free) width.set(s, left / free.length);
      break;
    }
    for (const s of under) width.set(s, floor);
    left -= floor * under.length;
    free = free.filter((s) => !under.includes(s));
  }
  let at = 0;
  return shown.map((s) => {
    const w = width.get(s) ?? 0;
    const out: BarSegment = { segment: s, count: counts[s], start: at, width: w, labelled: w >= g.labelMin };
    at += w + gap;
    return out;
  });
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
