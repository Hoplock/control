// THE CONSOLE'S SCREENS. A later phase adds a screen by adding one entry
// here — its path inside /console/, its navigation label (a catalogue key),
// its density default (DESIGN.md §3.7) and the permission its main read
// needs — and its catalogue keys to en.json, and its routes to api/routes.ts.
// The navigation lists exactly these, in this order, so it never names a
// screen that does not exist.

import type { ReactNode } from "react";
import type { MessageKey } from "../i18n/catalogue";
import { AuditRecordsScreen, BlockedCommandsScreen } from "./Audit";
import { DeploymentScreen } from "./Deployment";
import { ExplainScreen } from "./Explain";
import { FleetScreen } from "./Fleet";
import { PolicyCandidateScreen, PolicyScreen, PolicyVersionScreen } from "./Policy";
import { ReplayScreen } from "./Replay";

export interface Screen {
  /** Its stable id, used in tests and in 0050's screen registry. */
  id: string;
  /** Its path inside the console, with :params: a deep link, so stable. */
  path: string;
  /** Its entry in the navigation, or none for a detail page. */
  nav?: MessageKey;
  /**
   * The paths its navigation entry stands for, when wider than its own: the
   * entry is current anywhere under it ("audit" covers the audit view's
   * records, blocked commands and replay).
   */
  section?: string;
  /** Compact on scanned screens (Fleet, Audit); comfortable on read ones. */
  density: "compact" | "comfortable";
  /** The permission the screen's main read needs, if any (a courtesy: RBAC is the API's). */
  permission?: string;
  render(params: Record<string, string>): ReactNode;
}

export const SCREENS: readonly Screen[] = [
  {
    id: "deployment",
    path: "deployment",
    nav: "nav.deployment",
    density: "comfortable",
    permission: "instance:read",
    render: () => <DeploymentScreen />,
  },
  {
    id: "fleet",
    path: "fleet",
    nav: "nav.fleet",
    density: "compact",
    permission: "fleet:read",
    render: () => <FleetScreen />,
  },
  {
    id: "policy",
    path: "policy",
    nav: "nav.policy",
    section: "policy",
    density: "comfortable",
    permission: "policy:read",
    render: () => <PolicyScreen />,
  },
  {
    id: "policy-version",
    path: "policy/versions/:version",
    density: "comfortable",
    permission: "policy:read",
    render: (p) => <PolicyVersionScreen key={p.version} version={p.version as string} />,
  },
  {
    id: "policy-candidate",
    path: "policy/candidate",
    density: "comfortable",
    permission: "policy:read",
    render: () => <PolicyCandidateScreen />,
  },
  {
    id: "explain",
    path: "explain",
    nav: "nav.explain",
    density: "comfortable",
    permission: "decision:read",
    render: () => <ExplainScreen />,
  },
  {
    id: "explain-session",
    path: "explain/sessions/:session",
    density: "comfortable",
    permission: "decision:read",
    render: (p) => <ExplainScreen key={`s:${p.session}`} by="sessions" id={p.session} />,
  },
  {
    id: "explain-decision",
    path: "explain/decisions/:decision",
    density: "comfortable",
    permission: "decision:read",
    render: (p) => <ExplainScreen key={`d:${p.decision}`} by="decisions" id={p.decision} />,
  },
  {
    id: "audit-records",
    path: "audit/records",
    nav: "nav.audit",
    section: "audit",
    density: "compact",
    permission: "audit:read",
    render: () => <AuditRecordsScreen />,
  },
  {
    id: "audit-blocked",
    path: "audit/blocked-commands",
    density: "compact",
    permission: "audit:read",
    render: () => <BlockedCommandsScreen />,
  },
  {
    id: "audit-replay",
    path: "audit/sessions/:session/replay",
    density: "compact",
    permission: "audit:read",
    render: (p) => <ReplayScreen key={p.session} session={p.session as string} />,
  },
];

/** Where /console/ itself goes. */
export const HOME = "deployment";
