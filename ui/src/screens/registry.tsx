// THE CONSOLE'S SCREENS. A later phase adds a screen by adding one entry
// here — its path inside /console/, its navigation label (a catalogue key),
// its density default (DESIGN.md §3.7) and the permission its main read
// needs — and its catalogue keys to en.json, and its routes to api/routes.ts.
// The navigation lists exactly these, in this order, so it never names a
// screen that does not exist.

import type { ReactNode } from "react";
import type { MessageKey } from "../i18n/catalogue";
import { AuditRecordsScreen, BlockedCommandsScreen } from "./Audit";
import { ClaimMappingScreen } from "./ClaimMapping";
import { DeploymentScreen } from "./Deployment";
import { ExplainScreen } from "./Explain";
import { FleetScreen } from "./Fleet";
import { GrantNewScreen, GrantScreen, GrantsScreen } from "./Grants";
import { GroupScreen, GroupsScreen, RoleScreen, RolesScreen, SubjectNewScreen, SubjectScreen, SubjectsScreen } from "./Identities";
import { PolicyCandidateScreen, PolicyScreen, PolicyVersionScreen } from "./Policy";
import { ReplayScreen } from "./Replay";
import { TargetNewScreen, TargetScreen, TargetsScreen } from "./Targets";

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
  // Inventory (0054): five lists, each a view of one navigation entry, and a
  // detail for each. The create forms live under inventory/new/, a segment no
  // list uses, so an object of any id has a detail path.
  {
    id: "inventory-targets",
    path: "inventory/targets",
    nav: "nav.inventory",
    section: "inventory",
    density: "comfortable",
    permission: "fleet:read",
    render: () => <TargetsScreen />,
  },
  {
    id: "inventory-target",
    path: "inventory/targets/:target",
    density: "comfortable",
    permission: "fleet:read",
    render: (p) => <TargetScreen key={p.target} id={p.target as string} />,
  },
  {
    id: "inventory-target-new",
    path: "inventory/new/targets",
    density: "comfortable",
    permission: "target:write",
    render: () => <TargetNewScreen />,
  },
  {
    id: "inventory-subjects",
    path: "inventory/subjects",
    density: "comfortable",
    permission: "identity:read",
    render: () => <SubjectsScreen />,
  },
  {
    id: "inventory-subject",
    path: "inventory/subjects/:subject",
    density: "comfortable",
    permission: "identity:read",
    render: (p) => <SubjectScreen key={p.subject} id={p.subject as string} />,
  },
  {
    id: "inventory-subject-new",
    path: "inventory/new/subjects",
    density: "comfortable",
    permission: "identity:write",
    render: () => <SubjectNewScreen />,
  },
  {
    id: "inventory-groups",
    path: "inventory/groups",
    density: "comfortable",
    permission: "identity:read",
    render: () => <GroupsScreen />,
  },
  {
    id: "inventory-group",
    path: "inventory/groups/:group",
    density: "comfortable",
    permission: "identity:read",
    render: (p) => <GroupScreen key={p.group} id={p.group as string} />,
  },
  {
    id: "inventory-roles",
    path: "inventory/roles",
    density: "comfortable",
    permission: "identity:read",
    render: () => <RolesScreen />,
  },
  {
    id: "inventory-role",
    path: "inventory/roles/:role",
    density: "comfortable",
    permission: "identity:read",
    render: (p) => <RoleScreen key={p.role} role={p.role as string} />,
  },
  // The claim mapping (0055): one view of the inventory, beside its lists.
  {
    id: "inventory-claim-mapping",
    path: "inventory/claim-mapping",
    density: "comfortable",
    permission: "identity:read",
    render: () => <ClaimMappingScreen />,
  },
  {
    id: "inventory-grants",
    path: "inventory/grants",
    density: "comfortable",
    permission: "grant:read",
    render: () => <GrantsScreen />,
  },
  {
    id: "inventory-grant",
    path: "inventory/grants/:grant",
    density: "comfortable",
    permission: "grant:read",
    render: (p) => <GrantScreen key={p.grant} id={p.grant as string} />,
  },
  {
    id: "inventory-grant-new",
    path: "inventory/new/grants",
    density: "comfortable",
    permission: "grant:write",
    render: () => <GrantNewScreen />,
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
