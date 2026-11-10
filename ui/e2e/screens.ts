// THE SCREEN REGISTRY THE BROWSER CHECKS RUN OVER (0050; DESIGN.md §10).
//
// A later phase's screen is checked by adding ONE entry here, and nothing
// else: every check below iterates this list.
//   - `path` is where it is (a console screen, or a gallery page);
//   - `ready` waits for what it renders when loaded;
//   - `states` are how its loading, empty, error and partial states are made:
//     an API answer substituted in the browser (docs/PROTOCOL.md §8), or
//     `seeded` when the seeded database itself produces that state;
//   - `densities` says it holds a table, so it is captured in both;
//   - `mutations` are the calls it offers that change something: the RBAC
//     harness asserts the console hides each from a role without the route's
//     permission AND that the API refuses it (rbac.spec.ts);
//   - its data goes in fixture/seed.yaml, or a fixture function in SEEDS.
// The screenshot baseline for every entry lives in e2e/baseline/, and
// `npm run e2e:update` rewrites it after a deliberate change.

import type { APIRequestContext, Page } from "@playwright/test";
import { seedAudit } from "./fixture/audit";
import { seedFleet } from "./fixture/fleet";
import { fixtureGrant, seedInventory } from "./fixture/inventory";
import { candidates, seedPolicy } from "./fixture/policy";
import { DIRECTORY, fixtureSubject, seedStanding } from "./fixture/standing";
import { substitute, TENANT, type Role, type Substitute } from "./harness";

export type StateName = "loading" | "empty" | "error" | "partial";

export interface StateDef {
  /** API paths (from the origin, a prefix) and what each answers instead. */
  substitute?: [string, Substitute][];
  /** The seeded data already shows this state. */
  seeded?: true;
  /** Waits for the state to render. */
  ready(page: Page): Promise<void>;
}

export interface Mutation {
  /** The route's name in src/api/routes.ts. */
  route: string;
  method: "POST" | "PUT" | "DELETE";
  path: string;
  body?: unknown;
  /** The permission the route requires: a role without it is refused. */
  permission: string;
  /** Brings the control into view (opens the menu it sits in). */
  reveal?(page: Page): Promise<void>;
  /** The control that offers it, found in the screen once revealed. */
  control(page: Page): ReturnType<Page["locator"]>;
}

export interface E2EScreen {
  id: string;
  /** console: under /console/, signed in. anonymous: under /console/, signed out. gallery: a component page. */
  kind: "console" | "anonymous" | "gallery";
  path: string;
  /** The role it is viewed as in the design checks (default admin). */
  role?: Role;
  densities: boolean;
  ready(page: Page): Promise<void>;
  states: Partial<Record<StateName, StateDef>>;
  mutations?: readonly Mutation[];
  /** Interactions to run before the gallery page is captured (open a menu). */
  prepare?(page: Page): Promise<void>;
}

const API = `/api/v1/tenants/${TENANT}`;
const outage = (correlation: string): Substitute => ({
  kind: "answer",
  status: 503,
  body: { code: "unavailable", message: "substituted outage", correlation_id: correlation },
});
const main = (page: Page) => page.locator("main#content");

/** Fixture steps that run after the seed, before any check (global-setup.ts). */
export const SEEDS: readonly ((admin: APIRequestContext) => Promise<void>)[] = [seedFleet, seedAudit, seedPolicy, seedInventory, seedStanding];

const GALLERY_PAGES = [
  "buttons",
  "fields",
  "status",
  "labels",
  "feedback",
  "navigation",
  "data",
  "time",
  "diff",
  "overlay-dialog",
  "overlay-confirm",
  "overlay-confirm-failed",
  "overlay-drawer",
  "overlay-menu",
  "overlay-palette",
  "overlay-toast",
] as const;

const GALLERY: E2EScreen[] = GALLERY_PAGES.map((section) => ({
  id: `gallery-${section}`,
  kind: "gallery",
  path: `/console/gallery?section=${section}`,
  densities: section === "data",
  ready: (page) => page.locator(`[data-gallery="${section}"]`).waitFor(),
  states: {},
  prepare: async (page) => {
    if (section === "overlay-confirm-failed") {
      await page.locator("[data-confirm-input]").fill("px-spare-09");
      await page.locator("dialog form button[type=submit]").click();
      await page.locator("dialog [role=alert]").waitFor();
    }
    if (section === "overlay-confirm") await page.locator("[data-confirm-input]").fill("px-spare");
    if (section === "time") {
      // An absolute range whose start is after its end, applied: refused at the field.
      await page.locator("[data-range-invalid] form button[type=submit]").click();
      await page.locator("[data-range-invalid] [role=alert]").waitFor();
    }
    if (section === "overlay-menu") {
      await page.locator('button[aria-haspopup="menu"]').click();
      await page.locator("[role=menu]:popover-open").waitFor();
    }
    if (section === "overlay-palette") {
      await page.keyboard.press("Control+k");
      await page.locator("dialog[open] [role=combobox]").fill("go");
    }
  },
}));


// ---------------------------------------------------------------------------
// Inventory (0054). A list's states: loading held open, empty and error
// substituted, and partial as the server's own page marked cut (M26) — one
// page of a longer list, said beside its rows. A detail's partial is a second
// read failing beside a first that answered.
// ---------------------------------------------------------------------------

const rows = (page: Page) => main(page).locator("table tbody tr[data-row]").first().waitFor();
const cut: Substitute = { kind: "patch", patch: (b) => ({ ...(b as object), truncated: true, next: "e2e-cut" }) };
const notFound = (correlation: string): Substitute => ({
  kind: "answer",
  status: 404,
  body: { code: "not_found", message: "substituted", correlation_id: correlation },
});
const rowMenu = (row: string) => async (page: Page) => {
  await main(page).locator(`tr[data-row="${row}"] button[aria-haspopup="menu"]`).first().click();
  await page.locator("[role=menu]:popover-open").waitFor();
};
const dangerItem = (page: Page) => page.locator("[role=menu]:popover-open [role=menuitem][data-danger]");

function listStates(route: string, member: string, extra: Record<string, unknown> = {}): E2EScreen["states"] {
  return {
    loading: { substitute: [[`${API}/${route}`, { kind: "hang" }]], ready: (page) => main(page).locator("[data-state=loading]").waitFor() },
    empty: {
      substitute: [[`${API}/${route}`, { kind: "answer", status: 200, body: { [member]: [], truncated: false, ...extra } }]],
      ready: (page) => main(page).locator("[data-state=empty]").waitFor(),
    },
    error: { substitute: [[`${API}/${route}`, outage(`corr-e2e-${route}`)]], ready: (page) => main(page).locator("[data-state=error]").waitFor() },
    partial: { substitute: [[`${API}/${route}`, cut]], ready: (page) => main(page).locator("[data-partial-note]").first().waitFor() },
  };
}

function detailStates(route: string, gap: [string, string], ready: string): E2EScreen["states"] {
  return {
    loading: { substitute: [[`${API}/${route}`, { kind: "hang" }]], ready: (page) => main(page).locator("[data-state=loading]").waitFor() },
    empty: { substitute: [[`${API}/${route}`, notFound(`corr-e2e-${route}-404`)]], ready: (page) => main(page).locator("[data-state=empty]").waitFor() },
    error: { substitute: [[`${API}/${route}`, outage(`corr-e2e-${route}`)]], ready: (page) => main(page).locator("[data-state=error]").waitFor() },
    partial: {
      substitute: [[`${API}/${gap[0]}`, outage(`corr-e2e-${gap[1]}`)]],
      ready: (page) => main(page).locator(`[data-state=partial] ${ready}`).first().waitFor(),
    },
  };
}

/** A create form's one call, offered as the form itself: a role without its permission is told so instead. */
const formMutation = (route: string, method: "POST" | "PUT", path: string, permission: string, form: string): Mutation => ({
  route,
  method,
  path,
  permission,
  control: (page) => main(page).locator(`form[${form}]`),
});

/** Sorts a list by its name column, when it has rows (a role offered nothing still has the column). */
const byName = async (page: Page) => {
  const sort = main(page).locator("table th[data-column=name] button").first();
  if (await sort.count()) await sort.click();
};

/** Opens a detail's control, if the role is offered it, and waits for its dialog. */
const openDialog = async (page: Page, control: string) => {
  const button = main(page).locator(control);
  if (!(await button.count())) return false;
  await button.click();
  await page.locator("dialog[open]").waitFor();
  return true;
};

const STANDING: E2EScreen[] = [
  // A quarantined target (M28): the quarantine's facts, release offered to a
  // holder of session:revoke, and removal still offered to target:write —
  // whose refusal says release it first.
  {
    id: "inventory-target-quarantined",
    kind: "console",
    path: "/console/inventory/targets/cache-01",
    densities: false,
    ready: (page) => main(page).locator("[data-denial=quarantined]").waitFor(),
    states: {},
    mutations: [
      { route: "targetRelease", method: "POST", path: `${API}/targets/cache-01/release`, permission: "session:revoke", control: (page) => main(page).locator("[data-release]") },
      { route: "targetDelete", method: "DELETE", path: `${API}/targets/cache-01`, permission: "target:write", control: (page) => main(page).locator("[data-remove]") },
    ],
  },
  // Removing a quarantined target, typed and sent: the server refuses it
  // (409 target_quarantined), and the dialog says release it first.
  {
    id: "inventory-target-remove-refused",
    kind: "console",
    path: "/console/inventory/targets/cache-01",
    densities: false,
    ready: (page) => main(page).locator("[data-denial=quarantined]").waitFor(),
    states: {},
    prepare: async (page) => {
      if (!(await openDialog(page, "[data-remove]"))) return;
      const dialog = page.locator("dialog[open]");
      await dialog.locator("[data-confirm-input]").fill("cache-01");
      await dialog.locator("button[type=submit]").click();
      await dialog.locator("[role=alert][data-error-code=target_quarantined]").waitFor();
    },
  },
  // Quarantining asks for nothing the holder sees, and says it ends no session.
  {
    id: "inventory-target-quarantine-dialog",
    kind: "console",
    path: "/console/inventory/targets/db-01",
    densities: false,
    ready: (page) => main(page).locator("[data-standing=none]").waitFor(),
    states: {},
    prepare: async (page) => {
      await openDialog(page, "[data-quarantine]");
    },
  },
  // An operator's lock: its facts, the text its holder was shown, and unlock.
  {
    id: "inventory-subject-locked",
    kind: "console",
    path: "/console/inventory/subjects/noor",
    densities: false,
    ready: (page) => main(page).locator("[data-denial=locked]").waitFor(),
    states: {},
    mutations: [
      { route: "subjectUnlock", method: "POST", path: `${API}/subjects/noor/unlock`, permission: "session:revoke", control: (page) => main(page).locator("[data-unlock]") },
    ],
  },
  // The lock dialog, with the text the holder will be shown, before sending.
  {
    id: "inventory-subject-lock-dialog",
    kind: "console",
    path: "/console/inventory/subjects/lin",
    densities: false,
    ready: (page) => main(page).locator("[data-standing=none]").waitFor(),
    states: {},
    prepare: async (page) => {
      if (!(await openDialog(page, "[data-lock]"))) return;
      const dialog = page.locator("dialog[open]");
      await dialog.locator("input[name=reason]").fill("Your access is paused while we check a lost laptop. Call the SOC.");
      await dialog.locator("input[name=reason_code]").fill("INC-4900");
      await dialog.locator("[data-confirm-input]").fill("lin");
    },
  },
  // A lock the server placed and could not deliver (503
  // standing_denial_undelivered, substituted in the browser: one server does
  // not fail its own stream on demand): in force, with a retry.
  {
    id: "inventory-subject-undelivered",
    kind: "console",
    path: "/console/inventory/subjects/lin",
    densities: false,
    ready: (page) => main(page).locator("[data-standing=none]").waitFor(),
    states: {},
    prepare: async (page) => {
      await substitute(page, `${API}/subjects/lin/lock`, UNDELIVERED);
      if (!(await openDialog(page, "[data-lock]"))) return;
      const dialog = page.locator("dialog[open]");
      await dialog.locator("[data-confirm-input]").fill("lin");
      await dialog.locator("button[type=submit]").click();
      await main(page).locator("[data-undelivered]").waitFor();
    },
  },
  // A subject the directory manages (0040, 0042): whose it is, and no edit
  // or removal; the lock and its roles stay offered.
  {
    id: "inventory-subject-directory",
    kind: "console",
    get path() {
      return `/console/inventory/subjects/${fixtureSubject(DIRECTORY.dana)}`;
    },
    densities: false,
    ready: (page) => main(page).locator("[data-directory-panel]").waitFor(),
    states: {},
    mutations: [
      {
        route: "subjectLock",
        method: "POST",
        get path() {
          return `${API}/subjects/${fixtureSubject(DIRECTORY.dana)}/lock`;
        },
        permission: "session:revoke",
        control: (page) => main(page).locator("[data-lock]"),
      },
      {
        route: "roleBind",
        method: "POST",
        path: `${API}/role-bindings`,
        get body() {
          return { subject_id: fixtureSubject(DIRECTORY.dana), role: "auditor" };
        },
        permission: "identity:write",
        control: (page) => main(page).locator("form[data-bind-form]"),
      },
    ],
  },
  // The directory's deactivation alone: a deny of its own, with no lever.
  {
    id: "inventory-subject-deactivated",
    kind: "console",
    get path() {
      return `/console/inventory/subjects/${fixtureSubject(DIRECTORY.eli)}`;
    },
    densities: false,
    ready: (page) => main(page).locator("[data-denial=deactivated]").waitFor(),
    states: {},
  },
  // Locked by an operator AND deactivated by the directory: both, the lock first.
  {
    id: "inventory-subject-locked-deactivated",
    kind: "console",
    get path() {
      return `/console/inventory/subjects/${fixtureSubject(DIRECTORY.sam)}`;
    },
    densities: false,
    ready: (page) => main(page).locator("[data-denial=deactivated]").waitFor(),
    states: {},
    mutations: [
      {
        route: "subjectUnlock",
        method: "POST",
        get path() {
          return `${API}/subjects/${fixtureSubject(DIRECTORY.sam)}/unlock`;
        },
        permission: "session:revoke",
        control: (page) => main(page).locator("[data-unlock]"),
      },
    ],
  },
  // Its unlock dialog: lifting the lock does not re-admit a deactivated person.
  {
    id: "inventory-subject-unlock-deactivated",
    kind: "console",
    get path() {
      return `/console/inventory/subjects/${fixtureSubject(DIRECTORY.sam)}`;
    },
    densities: false,
    ready: (page) => main(page).locator("[data-denial=deactivated]").waitFor(),
    states: {},
    prepare: async (page) => {
      await openDialog(page, "[data-unlock]");
    },
  },
  // Removed while locked: listed by the lock alone, and still unlockable.
  {
    id: "inventory-subject-record-missing",
    kind: "console",
    path: "/console/inventory/subjects/ex-contractor",
    densities: false,
    ready: (page) => main(page).locator("[data-denial=locked]").waitFor(),
    states: {},
    mutations: [
      { route: "subjectUnlock", method: "POST", path: `${API}/subjects/ex-contractor/unlock`, permission: "session:revoke", control: (page) => main(page).locator("[data-unlock]") },
    ],
  },
  // Deactivated by the directory before it created the person.
  {
    id: "inventory-subject-deactivated-early",
    kind: "console",
    get path() {
      return `/console/inventory/subjects/${fixtureSubject(DIRECTORY.early)}`;
    },
    densities: false,
    ready: (page) => main(page).locator("[data-denial=deactivated]").waitFor(),
    states: {},
  },
  // The claim mapping (0043): its facts, each group entry with its
  // directories, the document, and the connectors' ties. Partial: some
  // directory identities not yet on this version (the server's answer, its
  // count changed) and the connectors unreadable.
  {
    id: "inventory-claim-mapping",
    kind: "console",
    path: "/console/inventory/claim-mapping",
    densities: false,
    ready: (page) => main(page).locator("[data-connectors] table tbody tr[data-row]").first().waitFor(),
    states: {
      loading: { substitute: [[`${API}/identity/claim-mapping`, { kind: "hang" }]], ready: (page) => main(page).locator("[data-state=loading]").waitFor() },
      error: { substitute: [[`${API}/identity/claim-mapping`, outage("corr-e2e-claim-mapping")]], ready: (page) => main(page).locator("[data-state=error]").waitFor() },
      partial: {
        substitute: [
          [`${API}/identity/claim-mapping`, { kind: "patch", patch: (b) => ({ ...(b as object), stale_directory_subjects: 3 }) }],
          ["/api/v1/session/methods", outage("corr-e2e-methods")],
        ],
        ready: (page) => main(page).locator("[data-state=partial] [data-stale]").waitFor(),
      },
    },
    mutations: [
      { route: "claimMappingPut", method: "PUT", path: `${API}/identity/claim-mapping`, body: { document: "schema_version: 1\n" }, permission: "identity:write", control: (page) => main(page).locator("[data-edit]") },
    ],
  },
  // A write refused for the three directory faults (0043), each at its field.
  {
    id: "inventory-claim-mapping-refused",
    kind: "console",
    path: "/console/inventory/claim-mapping?edit=1",
    densities: false,
    ready: (page) => main(page).locator("[data-mapping-version]").waitFor(),
    states: {},
    prepare: async (page) => {
      const form = main(page).locator("form[data-mapping-form]");
      if (!(await form.count())) return;
      await form.locator("textarea[name=document]").fill(REFUSED_MAPPING);
      await form.locator("button[type=submit]").click();
      await form.locator("[data-rejections]").waitFor();
    },
  },
  // Explain for a subject the directory provisioned: its groups' directory beside the mapping version.
  {
    id: "explain-directory",
    kind: "console",
    path: "/console/explain/sessions/sess-e2e-directory",
    densities: false,
    ready: (page) => main(page).locator("[data-mapping-directory]").waitFor(),
    states: {},
  },
];

/** The answer to a lock the server placed and could not deliver (M28). */
export const UNDELIVERED: Substitute = {
  kind: "answer",
  status: 503,
  body: {
    code: "standing_denial_undelivered",
    message: "substituted",
    parameters: { subject: "lin", locked: true, stands: true },
    correlation_id: "corr-e2e-undelivered",
  },
};

/** A mapping with one of each directory fault (0043): a bad name, an empty list, a source listed twice. */
export const REFUSED_MAPPING = `schema_version: 1
groups:
  claim: groups
  map:
    - external: Payments Engineering
      directories: [Corp]
      group: payments-eng
    - external: Auditors
      directories: []
      group: auditors
    - external: sre
      directories: [corp, lab]
      group: sre
    - external: sre
      directories: [lab]
      group: sre-lab
`;

const INVENTORY: E2EScreen[] = [
  {
    id: "inventory-targets",
    kind: "console",
    path: "/console/inventory/targets",
    densities: true,
    ready: rows,
    states: listStates("targets", "targets"),
    mutations: [
      { route: "targetPut", method: "PUT", path: `${API}/targets/api-01`, permission: "target:write", control: (page) => main(page).locator("[data-create]") },
      { route: "targetDelete", method: "DELETE", path: `${API}/targets/scratch-01`, permission: "target:write", reveal: rowMenu("scratch-01"), control: dangerItem },
    ],
  },
  {
    id: "inventory-targets-filtered",
    kind: "console",
    path: "/console/inventory/targets?label=env%3Dprod",
    densities: false,
    ready: rows,
    // A label no target carries: the filter's own empty state, which says how to clear it.
    states: {
      empty: {
        substitute: [[`${API}/targets`, { kind: "answer", status: 200, body: { targets: [], truncated: false } }]],
        ready: (page) => main(page).locator("[data-state=empty]").waitFor(),
      },
    },
  },
  {
    id: "inventory-target",
    kind: "console",
    path: "/console/inventory/targets/db-01",
    densities: true,
    ready: (page) => main(page).locator("[data-activity=ok] table tbody tr[data-row]").first().waitFor(),
    states: detailStates("targets/db-01", ["audit/records", "target-activity"], "[data-activity=outage]"),
    mutations: [
      { route: "targetPut", method: "PUT", path: `${API}/targets/db-01`, permission: "target:write", control: (page) => main(page).locator("[data-edit]") },
      { route: "targetDelete", method: "DELETE", path: `${API}/targets/db-01`, permission: "target:write", control: (page) => main(page).locator("[data-remove]") },
      { route: "targetQuarantine", method: "POST", path: `${API}/targets/db-01/quarantine`, permission: "session:revoke", control: (page) => main(page).locator("[data-quarantine]") },
    ],
  },
  {
    id: "inventory-target-edit",
    kind: "console",
    path: "/console/inventory/targets/api-01?edit=1",
    densities: false,
    ready: (page) => main(page).locator("form[data-target-form], [data-activity]").first().waitFor(),
    states: {},
  },
  {
    id: "inventory-target-new",
    kind: "console",
    path: "/console/inventory/new/targets",
    densities: false,
    ready: (page) => main(page).locator("form[data-target-form], [role=alert]").first().waitFor(),
    states: {},
    mutations: [formMutation("targetPut", "PUT", `${API}/targets/new-from-e2e`, "target:write", "data-target-form")],
  },
  {
    id: "inventory-subjects",
    kind: "console",
    path: "/console/inventory/subjects",
    densities: true,
    ready: rows,
    states: listStates("subjects", "subjects"),
    // A directory subject's id is Control's random choice (0055), so in id
    // order the directory's rows change places from run to run: the list is
    // captured sorted by name, which is the same every run.
    prepare: byName,
    mutations: [
      { route: "subjectPut", method: "PUT", path: `${API}/subjects/lin`, permission: "identity:write", control: (page) => main(page).locator("[data-create]") },
      { route: "subjectDelete", method: "DELETE", path: `${API}/subjects/temp-intern`, permission: "identity:write", reveal: rowMenu("temp-intern"), control: dangerItem },
    ],
  },
  {
    id: "inventory-subject",
    kind: "console",
    path: "/console/inventory/subjects/lin",
    densities: true,
    ready: async (page) => {
      await main(page).locator("[data-bindings=ok] table tbody tr[data-row]").first().waitFor();
      await main(page).locator("[data-grants=ok] table tbody tr[data-row]").first().waitFor();
    },
    states: detailStates("subjects/lin", ["grants", "subject-grants"], "[data-grants=outage]"),
    mutations: [
      { route: "subjectLock", method: "POST", path: `${API}/subjects/lin/lock`, permission: "session:revoke", control: (page) => main(page).locator("[data-lock]") },
      { route: "subjectPut", method: "PUT", path: `${API}/subjects/lin`, permission: "identity:write", control: (page) => main(page).locator("[data-edit]") },
      { route: "subjectDelete", method: "DELETE", path: `${API}/subjects/lin`, permission: "identity:write", control: (page) => main(page).locator("[data-remove]") },
      {
        route: "roleBind",
        method: "POST",
        path: `${API}/role-bindings`,
        body: { subject_id: "lin", role: "auditor" },
        permission: "identity:write",
        control: (page) => main(page).locator("form[data-bind-form]"),
      },
      {
        route: "roleUnbind",
        method: "POST",
        path: `${API}/role-bindings/remove`,
        body: { subject_id: "lin", role: "policy-author" },
        permission: "identity:write",
        reveal: rowMenu("policy-author/subject/lin"),
        control: dangerItem,
      },
    ],
  },
  {
    id: "inventory-subject-new",
    kind: "console",
    path: "/console/inventory/new/subjects",
    densities: false,
    ready: (page) => main(page).locator("form[data-subject-form], [role=alert]").first().waitFor(),
    states: {},
    mutations: [formMutation("subjectPut", "PUT", `${API}/subjects/new-from-e2e`, "identity:write", "data-subject-form")],
  },
  {
    id: "inventory-groups",
    kind: "console",
    path: "/console/inventory/groups",
    densities: true,
    ready: rows,
    states: listStates("groups", "groups"),
  },
  {
    id: "inventory-group",
    kind: "console",
    path: "/console/inventory/groups/sre",
    densities: true,
    ready: async (page) => {
      await main(page).locator("[data-bindings=ok] table tbody tr[data-row]").first().waitFor();
      await main(page).locator("[data-members=ok] table tbody tr[data-row]").first().waitFor();
    },
    states: detailStates("groups/sre", ["subjects", "group-members"], "[data-members=outage]"),
    mutations: [
      {
        route: "roleBind",
        method: "POST",
        path: `${API}/role-bindings`,
        body: { group_id: "sre", role: "admin" },
        permission: "identity:write",
        control: (page) => main(page).locator("form[data-bind-form]"),
      },
      {
        route: "roleUnbind",
        method: "POST",
        path: `${API}/role-bindings/remove`,
        body: { group_id: "sre", role: "auditor" },
        permission: "identity:write",
        reveal: rowMenu("auditor/group/sre"),
        control: dangerItem,
      },
    ],
  },
  {
    id: "inventory-roles",
    kind: "console",
    path: "/console/inventory/roles",
    densities: true,
    ready: rows,
    states: {
      loading: { substitute: [[`${API}/role-bindings`, { kind: "hang" }]], ready: (page) => main(page).locator("[data-state=loading]").waitFor() },
      empty: {
        substitute: [[`${API}/role-bindings`, { kind: "answer", status: 200, body: { bindings: [], roles: [], truncated: false } }]],
        ready: (page) => main(page).locator("[data-state=empty]").waitFor(),
      },
      error: { substitute: [[`${API}/role-bindings`, outage("corr-e2e-roles")]], ready: (page) => main(page).locator("[data-state=error]").waitFor() },
      // A newer server names a role this console has no words for: listed by its code, and named as a gap.
      partial: {
        substitute: [[`${API}/role-bindings`, { kind: "patch", patch: (b) => ({ ...(b as object), roles: [...(b as { roles: string[] }).roles, "auditor-plus"] }) }]],
        ready: (page) => main(page).locator("[data-state=partial] table tbody tr[data-row]").first().waitFor(),
      },
    },
  },
  {
    id: "inventory-role",
    kind: "console",
    path: "/console/inventory/roles/fleet-admin",
    densities: true,
    ready: (page) => main(page).locator("[data-bindings=ok] table tbody tr[data-row]").first().waitFor(),
    states: {
      loading: { substitute: [[`${API}/role-bindings`, { kind: "hang" }]], ready: (page) => main(page).locator("[data-state=loading]").waitFor() },
      // A role this release does not define: the API refuses the filter, and there is no such role.
      empty: {
        substitute: [
          [
            `${API}/role-bindings`,
            {
              kind: "answer",
              status: 400,
              body: { code: "invalid_request", parameters: { field: "role", problem: "invalid" }, message: "substituted", correlation_id: "corr-e2e-role-400" },
            },
          ],
        ],
        ready: (page) => main(page).locator("[data-state=empty]").waitFor(),
      },
      error: { substitute: [[`${API}/role-bindings`, outage("corr-e2e-role")]], ready: (page) => main(page).locator("[data-state=error]").waitFor() },
      partial: { substitute: [[`${API}/role-bindings`, cut]], ready: (page) => main(page).locator("[data-partial-note]").first().waitFor() },
    },
    mutations: [
      {
        route: "roleBind",
        method: "POST",
        path: `${API}/role-bindings`,
        body: { subject_id: "mallory", role: "fleet-admin" },
        permission: "identity:write",
        control: (page) => main(page).locator("form[data-bind-form]"),
      },
      {
        route: "roleUnbind",
        method: "POST",
        path: `${API}/role-bindings/remove`,
        body: { subject_id: "fenn", role: "fleet-admin" },
        permission: "identity:write",
        reveal: rowMenu("fleet-admin/subject/fenn"),
        control: dangerItem,
      },
    ],
  },
  {
    id: "inventory-grants",
    kind: "console",
    path: "/console/inventory/grants",
    densities: true,
    ready: rows,
    states: listStates("grants", "grants", { tenant: "default" }),
    mutations: [
      { route: "grantCreate", method: "POST", path: `${API}/grants`, permission: "grant:write", control: (page) => main(page).locator("[data-create]") },
    ],
  },
  {
    id: "inventory-grant",
    kind: "console",
    get path() {
      return `/console/inventory/grants/${fixtureGrant("prod-db")}`;
    },
    densities: false,
    ready: (page) => main(page).locator("[data-grant-state=active]").waitFor(),
    states: {
      loading: { substitute: [[`${API}/grants/`, { kind: "hang" }]], ready: (page) => main(page).locator("[data-state=loading]").waitFor() },
      empty: { substitute: [[`${API}/grants/`, notFound("corr-e2e-grant-404")]], ready: (page) => main(page).locator("[data-state=empty]").waitFor() },
      error: { substitute: [[`${API}/grants/`, outage("corr-e2e-grant")]], ready: (page) => main(page).locator("[data-state=error]").waitFor() },
      // More decisions used it than the server samples: the sample, and the gap named (M26.3).
      partial: {
        substitute: [[`${API}/grants/`, { kind: "patch", patch: (b) => ({ ...(b as object), decisions_truncated: true }) }]],
        ready: (page) => main(page).locator("[data-state=partial] [data-grant-state]").waitFor(),
      },
    },
    mutations: [
      {
        route: "grantRevoke",
        method: "POST",
        get path() {
          return `${API}/grants/${fixtureGrant("prod-db")}/revoke`;
        },
        body: { reason: "refused before it is read" },
        permission: "grant:write",
        control: (page) => main(page).locator("[data-revoke]"),
      },
    ],
  },
  {
    id: "inventory-grant-new",
    kind: "console",
    path: "/console/inventory/new/grants",
    densities: false,
    ready: (page) => main(page).locator("form[data-grant-form], [role=alert]").first().waitFor(),
    states: {},
    mutations: [formMutation("grantCreate", "POST", `${API}/grants`, "grant:write", "data-grant-form")],
  },
  // A create the API refused: each problem at the field it names, never a toast alone (DESIGN.md §7).
  {
    id: "inventory-grant-refused",
    kind: "console",
    path: "/console/inventory/new/grants",
    densities: false,
    ready: (page) => main(page).locator("form[data-grant-form], [role=alert]").first().waitFor(),
    states: {},
    prepare: async (page) => {
      const form = main(page).locator("form[data-grant-form]");
      if (!(await form.count())) return;
      await form.locator("button[type=submit]").click();
      await form.locator("[data-invalid]").first().waitFor();
    },
  },
];

export const SCREENS: readonly E2EScreen[] = [
  {
    id: "sign-in",
    kind: "anonymous",
    path: "/console/sign-in",
    densities: false,
    ready: (page) => page.locator("main#content form, main#content h1").first().waitFor(),
    states: {},
  },
  {
    id: "deployment",
    kind: "console",
    path: "/console/deployment",
    densities: true,
    ready: (page) => main(page).locator(":is([data-state=ok], [data-state=partial]) table tbody tr").first().waitFor(),
    states: {
      loading: {
        substitute: [[`${API}/instance`, { kind: "hang" }]],
        ready: (page) => main(page).locator("[data-state=loading]").waitFor(),
      },
      error: {
        substitute: [[`${API}/instance`, outage("corr-e2e-deployment")]],
        ready: (page) => main(page).locator("[data-state=error]").waitFor(),
      },
      empty: {
        substitute: [
          [
            `${API}/instance/health`,
            {
              kind: "answer",
              status: 200,
              body: {
                state: "unknown",
                reasons: [{ code: "node_not_reported" }],
                served_at: "2026-11-01T08:00:00Z",
                nodes: { state: "unknown", members: [], upgrade: { in_progress: false, versions: [] } },
                fleet: [],
              },
            },
          ],
        ],
        ready: (page) => main(page).locator("[data-state=empty]").waitFor(),
      },
      partial: {
        substitute: [[`${API}/instance/health`, outage("corr-e2e-health")]],
        ready: (page) => main(page).locator("[data-state=partial]").waitFor(),
      },
    },
  },
  {
    id: "fleet",
    kind: "console",
    path: "/console/fleet",
    densities: true,
    ready: (page) => main(page).locator("table tbody tr[data-row]").first().waitFor(),
    states: {
      loading: {
        substitute: [[`${API}/proxies`, { kind: "hang" }]],
        ready: (page) => main(page).locator("[data-state=loading]").waitFor(),
      },
      empty: {
        substitute: [
          [
            `${API}/proxies`,
            {
              kind: "answer",
              status: 200,
              body: {
                proxies: [],
                truncated: false,
                liveness: { heartbeat_ttl_seconds: 90, relay_registration_ttl_seconds: 60, target_capability_ttl_seconds: 86400, report_after_seconds: 21600 },
              },
            },
          ],
        ],
        ready: (page) => main(page).locator("[data-state=empty]").waitFor(),
      },
      error: {
        substitute: [[`${API}/proxies`, outage("corr-e2e-fleet")]],
        ready: (page) => main(page).locator("[data-state=error]").waitFor(),
      },
      // The seed holds a proxy that has never been heard from: the fleet is
      // partial by its own data, and the gap is named.
      partial: { seeded: true, ready: (page) => main(page).locator("[data-state=partial]").waitFor() },
    },
    mutations: [
      {
        route: "proxyWithdraw",
        method: "POST",
        path: `${API}/proxies/px-spare-09/withdraw`,
        permission: "fleet:write",
        reveal: async (page) => {
          await page.locator('tr[data-row="px-spare-09"] button[aria-haspopup="menu"]').click();
          await page.locator("[role=menu]:popover-open").waitFor();
        },
        control: (page) => page.locator("[role=menu]:popover-open [role=menuitem][data-danger]"),
      },
    ],
  },
  // Policy (0053): the stored versions (v3 a candidate, v2 active, v1 older),
  // one version's four views, the candidate editor, a refused candidate's
  // rejections at their lines, and the activation dialog — each a deep link
  // or a step from one, so each is in the baseline and every check.
  {
    id: "policy",
    kind: "console",
    path: "/console/policy",
    densities: true,
    ready: async (page) => {
      await main(page).locator("table tbody tr[data-row]").first().waitFor();
      await main(page).locator("[data-active-panel=ok]").waitFor();
    },
    states: {
      loading: {
        substitute: [[`${API}/policy/bundles`, { kind: "hang" }]],
        ready: (page) => main(page).locator("[data-state=loading]").waitFor(),
      },
      // Nothing uploaded: no version stored and none active.
      empty: {
        substitute: [
          [`${API}/policy/bundles`, { kind: "answer", status: 200, body: { versions: [], truncated: false } }],
          [`${API}/policy/active`, { kind: "answer", status: 404, body: { code: "not_found", message: "substituted", correlation_id: "corr-e2e-policy-404" } }],
        ],
        ready: (page) => main(page).locator("[data-state=empty]").waitFor(),
      },
      error: {
        substitute: [[`${API}/policy/bundles`, outage("corr-e2e-policy")]],
        ready: (page) => main(page).locator("[data-state=error]").waitFor(),
      },
      // The versions read, the active one's details not: the gap is named.
      partial: {
        substitute: [[`${API}/policy/active`, outage("corr-e2e-policy-active")]],
        ready: (page) => main(page).locator("[data-state=partial] table tbody tr[data-row]").first().waitFor(),
      },
    },
    mutations: [
      {
        route: "policyActivate",
        method: "POST",
        path: `${API}/policy/bundles/3/activate`,
        permission: "policy:publish",
        reveal: async (page) => {
          await page.locator('tr[data-row="3"] button[aria-haspopup="menu"]').click();
          await page.locator("[role=menu]:popover-open").waitFor();
        },
        control: (page) => page.locator("[role=menu]:popover-open [role=menuitem][data-danger]"),
      },
      // A rollback is an activation of an older version (§5.6): the same route.
      {
        route: "policyActivate",
        method: "POST",
        path: `${API}/policy/bundles/1/activate`,
        permission: "policy:publish",
        reveal: async (page) => {
          await page.locator('tr[data-row="1"] button[aria-haspopup="menu"]').click();
          await page.locator("[role=menu]:popover-open").waitFor();
        },
        control: (page) => page.locator("[role=menu]:popover-open [role=menuitem][data-danger]"),
      },
    ],
  },
  {
    id: "policy-version",
    kind: "console",
    path: "/console/policy/versions/3",
    densities: false,
    ready: (page) => main(page).locator("[data-source-view] tr[data-noted]").first().waitFor(),
    states: {
      loading: {
        substitute: [[`${API}/policy/bundles/3`, { kind: "hang" }]],
        ready: (page) => main(page).locator("[data-state=loading]").waitFor(),
      },
      // A version this tenant does not store.
      empty: {
        substitute: [
          [`${API}/policy/bundles/3`, { kind: "answer", status: 404, body: { code: "not_found", message: "substituted", correlation_id: "corr-e2e-version-404" } }],
        ],
        ready: (page) => main(page).locator("[data-state=empty]").waitFor(),
      },
      error: {
        substitute: [[`${API}/policy/bundles/3`, outage("corr-e2e-version")]],
        ready: (page) => main(page).locator("[data-state=error]").waitFor(),
      },
      // The source read, its publish-time report not.
      partial: {
        substitute: [[`${API}/policy/validate`, outage("corr-e2e-version-report")]],
        ready: (page) => main(page).locator("[data-state=partial] [data-source-view]").waitFor(),
      },
    },
    mutations: [
      {
        route: "policyActivate",
        method: "POST",
        path: `${API}/policy/bundles/3/activate`,
        permission: "policy:publish",
        control: (page) => main(page).locator("[data-activate=activate]"),
      },
    ],
  },
  {
    id: "policy-version-changes",
    kind: "console",
    path: "/console/policy/versions/3?tab=changes",
    densities: false,
    ready: (page) => main(page).locator("[data-changes] [data-diff=lines]").waitFor(),
    states: {
      loading: {
        substitute: [[`${API}/policy/bundles/3/diff`, { kind: "hang" }]],
        ready: (page) => main(page).locator("[role=tabpanel] [data-state=loading]").waitFor(),
      },
      error: {
        substitute: [[`${API}/policy/bundles/3/diff`, outage("corr-e2e-diff")]],
        ready: (page) => main(page).locator("[role=tabpanel] [data-state=error]").waitFor(),
      },
    },
  },
  {
    id: "policy-version-report",
    kind: "console",
    path: "/console/policy/versions/3?tab=report",
    densities: false,
    ready: (page) => main(page).locator("[data-report] [data-finding]").first().waitFor(),
    states: {},
  },
  {
    id: "policy-version-simulate",
    kind: "console",
    path: "/console/policy/versions/3?tab=simulate",
    densities: false,
    ready: (page) => main(page).locator("[data-simulate] [data-run-replay]").waitFor(),
    states: {},
    // A replay of the last seven days' recorded traffic: one allow flips to a deny, one deny to an allow.
    prepare: async (page) => {
      await main(page).locator("[data-run-replay]").click();
      await main(page).locator("[data-replay]").waitFor();
    },
  },
  {
    id: "policy-activate",
    kind: "console",
    path: "/console/policy/versions/3",
    densities: false,
    ready: (page) => main(page).locator("[data-source-view] tr[data-noted]").first().waitFor(),
    states: {},
    // The dialog, its one warning listed to accept by id, and a refusal: the
    // name typed, the warning not accepted, so the server answers 409 and the
    // dialog says so where the author is (nothing is activated).
    prepare: async (page) => {
      // A role that cannot activate is offered no button (rbac.spec.ts holds that).
      if ((await main(page).locator("[data-activate]").count()) === 0) return;
      await main(page).locator("[data-activate]").click();
      const dialog = page.locator("dialog[open]");
      await dialog.locator("[data-activation-gate=ok] [data-finding]").waitFor();
      await dialog.locator("[data-confirm-input]").fill("v3");
      await dialog.locator("button[type=submit]").click();
      await dialog.locator("[role=alert][data-error-code=findings_not_accepted]").waitFor();
    },
  },
  {
    id: "policy-candidate",
    kind: "console",
    path: "/console/policy/candidate",
    densities: false,
    ready: (page) => main(page).locator("[data-start=version]").waitFor(),
    states: {
      loading: {
        substitute: [[`${API}/policy/active`, { kind: "hang" }]],
        ready: (page) => main(page).locator("[data-start=loading]").waitFor(),
      },
      // No version active: an empty editor that says there is nothing to start from.
      empty: {
        substitute: [[`${API}/policy/active`, { kind: "answer", status: 404, body: { code: "not_found", message: "substituted", correlation_id: "corr-e2e-candidate-404" } }]],
        ready: (page) => main(page).locator("[data-start=none]").waitFor(),
      },
      error: {
        substitute: [[`${API}/policy/active`, outage("corr-e2e-candidate")]],
        ready: (page) => main(page).locator("[data-start=outage] [role=alert]").waitFor(),
      },
    },
    mutations: [
      {
        route: "policyUpload",
        method: "POST",
        path: `${API}/policy/bundles`,
        body: { source: "schema_version: 1\n" },
        permission: "policy:write",
        control: (page) => main(page).locator("[data-upload]"),
      },
    ],
  },
  // A candidate the compiler refuses: each rejection rendered from its code
  // and parameters, beneath the line it names.
  {
    id: "policy-candidate-rejected",
    kind: "console",
    path: "/console/policy/candidate",
    densities: false,
    ready: (page) => main(page).locator("[data-start=version]").waitFor(),
    states: {},
    prepare: async (page) => {
      await main(page).locator("[data-candidate-source]").fill(candidates().invalid);
      await main(page).locator("[data-validate]").click();
      await main(page).locator("[data-rejections] [data-source-view]").waitFor();
    },
  },
  // Explain (0051): a session crossing two hops under a grant, opened on its
  // innermost decision; the start (no id yet) and a deny are entries of their
  // own, so each is in the baseline and every check.
  {
    id: "explain",
    kind: "console",
    path: "/console/explain/sessions/sess-e2e-hops",
    densities: false,
    ready: (page) => main(page).locator("[data-story-section=explain-hops-path] [role=tree]").waitFor(),
    states: {
      loading: {
        substitute: [[`${API}/sessions/sess-e2e-hops/decisions`, { kind: "hang" }]],
        ready: (page) => main(page).locator("[data-state=loading]").waitFor(),
      },
      // A 404 is the answer "this server took no decision under that id".
      empty: {
        substitute: [
          [
            `${API}/sessions/sess-e2e-hops/decisions`,
            { kind: "answer", status: 404, body: { code: "not_found", message: "substituted", correlation_id: "corr-e2e-explain-404" } },
          ],
        ],
        ready: (page) => main(page).locator("[data-state=empty]").waitFor(),
      },
      error: {
        substitute: [[`${API}/sessions/sess-e2e-hops/decisions`, outage("corr-e2e-explain")]],
        ready: (page) => main(page).locator("[data-state=error]").waitFor(),
      },
      // More decisions than one answer holds: the server's own answer, cut.
      partial: {
        substitute: [[`${API}/sessions/sess-e2e-hops/decisions`, { kind: "patch", patch: (b) => ({ ...(b as object), truncated: true }) }]],
        ready: (page) => main(page).locator("[data-state=partial] [data-story-section]").first().waitFor(),
      },
    },
  },
  {
    id: "explain-deny",
    kind: "console",
    path: "/console/explain/sessions/sess-e2e-rule-deny",
    densities: false,
    ready: (page) => main(page).locator("article[data-effect=deny]").waitFor(),
    states: {},
  },
  {
    id: "explain-start",
    kind: "console",
    path: "/console/explain",
    densities: false,
    ready: (page) => main(page).locator("[data-state=empty]").waitFor(),
    states: {},
  },
  // Audit (0051): the records, a supervisor's acts, the blocked commands and
  // a recorded session's replay — four views of one screen, each a deep link.
  {
    id: "audit-records",
    kind: "console",
    path: "/console/audit/records",
    densities: true,
    ready: (page) => main(page).locator("table tbody tr[data-row]").first().waitFor(),
    states: {
      loading: {
        substitute: [[`${API}/audit/records`, { kind: "hang" }]],
        ready: (page) => main(page).locator("[data-state=loading]").waitFor(),
      },
      empty: {
        substitute: [[`${API}/audit/records`, { kind: "answer", status: 200, body: { records: [], truncated: false } }]],
        ready: (page) => main(page).locator("[data-state=empty]").waitFor(),
      },
      error: {
        substitute: [[`${API}/audit/records`, outage("corr-e2e-audit")]],
        ready: (page) => main(page).locator("[data-state=error]").waitFor(),
      },
    },
  },
  {
    id: "audit-supervisor",
    kind: "console",
    path: "/console/audit/records?principal=sup-acme-ops",
    densities: false,
    ready: (page) => main(page).locator("[data-acting-operator]").first().waitFor(),
    states: {},
  },
  {
    id: "audit-blocked",
    kind: "console",
    path: "/console/audit/blocked-commands?label=env%3Dprod",
    densities: true,
    ready: (page) => main(page).locator("table tbody tr[data-row]").first().waitFor(),
    states: {
      loading: {
        substitute: [[`${API}/audit/blocked-commands`, { kind: "hang" }]],
        ready: (page) => main(page).locator("[data-state=loading]").waitFor(),
      },
      empty: {
        substitute: [[`${API}/audit/blocked-commands`, { kind: "answer", status: 200, body: { blocked: [], truncated: false } }]],
        ready: (page) => main(page).locator("[data-state=empty]").waitFor(),
      },
      error: {
        substitute: [[`${API}/audit/blocked-commands`, outage("corr-e2e-blocked")]],
        ready: (page) => main(page).locator("[data-state=error]").waitFor(),
      },
      // A decision retention took from the store: the command stays, its access is a named gap.
      partial: {
        substitute: [
          [
            `${API}/audit/blocked-commands`,
            {
              kind: "patch",
              patch: (b) => {
                const body = b as { blocked: Record<string, unknown>[] };
                return {
                  ...body,
                  blocked: body.blocked.map((x) => ({ ...x, decision_found: false, matched_rule: undefined, route_type: undefined, decided_at: undefined })),
                };
              },
            },
          ],
        ],
        ready: (page) => main(page).locator("[data-state=partial] table tbody tr[data-row]").first().waitFor(),
      },
    },
  },
  {
    id: "audit-replay",
    kind: "console",
    path: "/console/audit/sessions/sess-e2e-allow/replay",
    densities: false,
    // Ready once every capture up to the step has arrived: none still busy,
    // however many were (a locator's waitFor takes exactly one element).
    ready: async (page) => {
      await main(page).locator("pre").first().waitFor();
      await page.waitForFunction(() => document.querySelectorAll("main#content [aria-busy=true]").length === 0);
    },
    states: {
      loading: {
        substitute: [[`${API}/audit/records`, { kind: "hang" }]],
        ready: (page) => main(page).locator("[data-state=loading]").waitFor(),
      },
      empty: {
        substitute: [[`${API}/audit/records`, { kind: "answer", status: 200, body: { records: [], truncated: false } }]],
        ready: (page) => main(page).locator("[data-state=empty]").waitFor(),
      },
      error: {
        substitute: [[`${API}/audit/records`, outage("corr-e2e-replay")]],
        ready: (page) => main(page).locator("[data-state=error]").waitFor(),
      },
      // A capture a retention pass emptied: shown where it falls, nothing joined across it.
      partial: {
        substitute: [
          [
            `${API}/audit/records/rec-e2e-allow-05/capture`,
            { kind: "patch", patch: (b) => ({ ...(b as object), retained: true, payload: undefined }) },
          ],
        ],
        ready: (page) => main(page).locator("[data-state=partial] [data-capture-gap=retained]").waitFor(),
      },
    },
  },
  ...INVENTORY,
  ...STANDING,
  ...GALLERY,
];
