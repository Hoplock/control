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
import { TENANT, type Role, type Substitute } from "./harness";

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
export const SEEDS: readonly ((admin: APIRequestContext) => Promise<void>)[] = [seedFleet, seedAudit];

const GALLERY_PAGES = [
  "buttons",
  "fields",
  "status",
  "labels",
  "feedback",
  "navigation",
  "data",
  "time",
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
  ...GALLERY,
];
