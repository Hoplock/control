// What the extensions screen reads from GET …/extensions (0004, 0025, 0026,
// 0039, 0040), as rules a test can hold without a browser (0056).
//
// The server's `absent` is an English line for curl; the console says what
// Control does instead from its own catalogue, by the point's code (M21). A
// point this console does not know still renders, by its code, and is named
// as a gap rather than guessed at.

import type { ExtensionPoint, FeedLag } from "../api/types";
import type { MessageKey } from "../i18n/catalogue";

/** The point codes are the ext interfaces' names (ext.Point.String). */
export const POINTS: Readonly<Record<string, { name: MessageKey; absent: MessageKey }>> = {
  AuditSink: { name: "extensions.point.auditSink", absent: "extensions.absent.auditSink" },
  ArchiveStore: { name: "extensions.point.archiveStore", absent: "extensions.absent.archiveStore" },
  GrantWorkflow: { name: "extensions.point.grantWorkflow", absent: "extensions.absent.grantWorkflow" },
  IdentitySync: { name: "extensions.point.identitySync", absent: "extensions.absent.identitySync" },
  KeyStore: { name: "extensions.point.keyStore", absent: "extensions.absent.keyStore" },
  Notifier: { name: "extensions.point.notifier", absent: "extensions.absent.notifier" },
  ClusterCoordinator: { name: "extensions.point.clusterCoordinator", absent: "extensions.absent.clusterCoordinator" },
  ActionHandler: { name: "extensions.point.actionHandler", absent: "extensions.absent.actionHandler" },
  ReportProvider: { name: "extensions.point.reportProvider", absent: "extensions.absent.reportProvider" },
  PolicyValidator: { name: "extensions.point.policyValidator", absent: "extensions.absent.policyValidator" },
  AccessContextProvider: { name: "extensions.point.accessContextProvider", absent: "extensions.absent.accessContextProvider" },
};

/** The kinds a Start error is reported as (ext.Kind.String), each a sentence. */
export const START_ERRORS: Readonly<Record<string, MessageKey>> = {
  internal: "extensions.startError.internal",
  unavailable: "extensions.startError.unavailable",
  invalid: "extensions.startError.invalid",
  not_found: "extensions.startError.not_found",
  conflict: "extensions.startError.conflict",
  disabled: "extensions.startError.disabled",
  malformed: "extensions.startError.malformed",
  denied: "extensions.startError.denied",
};

/** The point whose registrations each carry a sink's lag. */
export const SINK_POINT = "AuditSink";
/** The point whose row carries the archive's lag. */
export const ARCHIVE_POINT = "ArchiveStore";

export type PointStatus = "running" | "notStarted" | "active" | "notRegistered";

/**
 * A point reads as what is in play there: nothing registered is Not
 * registered — Control's own answer runs, and that is not a fault (M15); a
 * handler or sync whose Start failed is Not started, degraded and never an
 * outage (M11); one that started is Running; anything else registered is
 * Active.
 */
export function pointStatus(p: ExtensionPoint): PointStatus {
  if (!p.registered) return "notRegistered";
  if (p.started === false) return "notStarted";
  if (p.started === true) return "running";
  return "active";
}

/** Every registration from outside Control, in the listing's order: what shows a host is in play. */
export function extendedBy(points: readonly ExtensionPoint[]): { provider: string; point: string }[] {
  const out: { provider: string; point: string }[] = [];
  for (const p of points) {
    for (const r of p.registrations) {
      if (!r.default) out.push({ provider: r.provider, point: p.point });
    }
  }
  return out;
}

/** The lag a registration or a point should carry and does not: each one a named gap. */
export type Gap = { kind: "sinkLag"; provider: string } | { kind: "archiveLag" } | { kind: "unknownPoint"; point: string };

export function gaps(points: readonly ExtensionPoint[]): Gap[] {
  const out: Gap[] = [];
  for (const p of points) {
    if (!(p.point in POINTS)) out.push({ kind: "unknownPoint", point: p.point });
    if (p.point === SINK_POINT) {
      for (const r of p.registrations) if (!r.lag) out.push({ kind: "sinkLag", provider: r.provider });
    }
    if (p.point === ARCHIVE_POINT && p.registered && !p.lag) out.push({ kind: "archiveLag" });
  }
  return out;
}

/** A registration's lag at the sink point, or null when it was not reported. */
export function sinkLag(p: ExtensionPoint, provider: string): FeedLag | null {
  return p.registrations.find((r) => r.provider === provider)?.lag ?? null;
}
