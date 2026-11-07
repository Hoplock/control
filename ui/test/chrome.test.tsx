// The deployment and tenant chrome (DESIGN.md §5, PLAN M18, M19), asserted by
// rendering it: one tenant shows no tenant control, two show the current one
// and an explicit switch, a supervised deployment shows its supervisor as an
// info badge, and health reads in the status vocabulary — an upgrade is in
// progress, unknown is Not reported, an unread health is an outage.

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { Health, Identity, Session, Supervisor } from "../src/api/types";
import { I18nProvider } from "../src/i18n/I18n";
import { currentTenant, healthStatus, holds, liveSupervisors, showsTenant, tenantsInScope } from "../src/shell/chrome";
import type { Load } from "../src/shell/load";
import { TopBar, type TopBarProps } from "../src/shell/TopBar";

const scope = { roles: ["auditor"], permissions: ["instance:read"] };

function session(tenants: string[], extra: Partial<Session> = {}): Session {
  return {
    principal_id: "p-1",
    kind: "session",
    subject: "alice",
    display_name: "Alice",
    source: "oidc:corp",
    break_glass: false,
    scopes: Object.fromEntries(tenants.map((t) => [t, scope])),
    ...extra,
  };
}

const identity: Load<Identity> = {
  state: "ok",
  data: {
    instance_id: "inst-0123456789abcdef0123456789abcdef",
    display_name: "Production EU",
    created_at: "2026-01-01T00:00:00Z",
    versions: { software: "v0.37.0", contract: "1.4.0", contract_commit: "abcdef0123456789", north_api: 2, north_api_min: 1 },
    tenants: [],
  },
};

function health(state: Health["state"]): Load<Health> {
  return {
    state: "ok",
    data: {
      state,
      reasons: [],
      served_at: "2026-11-07T00:00:00Z",
      nodes: { state, members: [], upgrade: { in_progress: state === "in_progress", versions: [] } },
    },
  };
}

function render(p: Partial<TopBarProps> & { session: Session }): string {
  const tenants = tenantsInScope(p.session);
  const props: TopBarProps = {
    identity,
    health: health("healthy"),
    tenants,
    tenant: currentTenant(tenants, undefined),
    supervisors: [],
    navOpen: false,
    onNav: () => {},
    onTenant: () => {},
    onSignOut: () => {},
    ...p,
  };
  return renderToStaticMarkup(
    <I18nProvider locale="en">
      <TopBar {...props} />
    </I18nProvider>,
  );
}

describe("tenancy in the chrome (M18)", () => {
  it("shows no tenant control with one tenant in scope", () => {
    const html = render({ session: session(["acme"]) });
    expect(showsTenant(["acme"])).toBe(false);
    expect(html).not.toContain("tenant-switch");
    expect(html).not.toContain(">acme<");
  });

  it("shows the current tenant and an explicit switch with two", () => {
    const s = session(["globex", "acme"]);
    const html = render({ session: s, tenant: "globex" });
    expect(html).toContain('data-testid="tenant-switch"');
    expect(html).toMatch(/<option value="globex" translate="no" selected="">globex<\/option>/);
    expect(html).toContain('<option value="acme" translate="no">acme</option>');
  });

  it("acts in the operator's last tenant while it is in scope, and never outside the scope", () => {
    expect(currentTenant(["acme", "globex"], "globex")).toBe("globex");
    expect(currentTenant(["acme", "globex"], "initech")).toBe("acme");
    expect(currentTenant([], "initech")).toBeNull();
  });

  it("reads a permission only where the session holds it", () => {
    const s = session(["acme"]);
    expect(holds(s, "acme", "instance:read")).toBe(true);
    expect(holds(s, "globex", "instance:read")).toBe(false);
  });
});

describe("the deployment's identity (M19)", () => {
  it("names the deployment, with its short instance id in mono", () => {
    const html = render({ session: session(["acme"]) });
    expect(html).toContain("Production EU");
    expect(html).toMatch(/<code [^>]*dir="ltr" translate="no" title="inst-0123456789abcdef0123456789abcdef"/);
    expect(html).toContain(">inst-01234567</code>");
    expect(html).toContain("Version v0.37.0");
    expect(html).toContain("Contract 1.4.0");
  });

  it("says so when the identity cannot be read, rather than showing nothing", () => {
    const html = render({ session: session(["acme"]), identity: { state: "outage", error: { code: "internal" } } });
    expect(html).toContain("could not be read");
  });
});

describe("supervision (M19)", () => {
  const supervisors: Supervisor[] = [
    { principal_id: "sup-msp", display_name: "Example MSP", live: true },
    { principal_id: "sup-old", display_name: "Former MSP", live: false },
  ];

  it("shows a badge naming each live supervisor, as info", () => {
    const live = liveSupervisors({ state: "ok", data: { supervisors } });
    const html = render({ session: session(["acme"]), supervisors: live });
    expect(html).toContain("Supervised by Example MSP");
    expect(html).not.toContain("Former MSP");
    expect(html).toMatch(/data-tone="info" data-testid="supervisor-badge"/);
  });

  it("shows no badge on an unsupervised deployment", () => {
    expect(render({ session: session(["acme"]) })).not.toContain("supervisor-badge");
  });
});

describe("a break-glass session (M7)", () => {
  it("says so in the chrome", () => {
    expect(render({ session: session(["acme"], { break_glass: true }) })).toContain("Break-glass session");
    expect(render({ session: session(["acme"]) })).not.toContain("Break-glass session");
  });
});

describe("health in the status vocabulary (DESIGN.md §6)", () => {
  it("reads an upgrade as in progress, never a fault (M19)", () => {
    expect(healthStatus(health("in_progress"))).toEqual({ tone: "info", label: "status.inProgress" });
    expect(render({ session: session(["acme"]), health: health("in_progress") })).toContain("In progress");
  });
  it("reads unknown as Not reported, never healthy (M17)", () => {
    expect(healthStatus(health("unknown"))).toEqual({ tone: "unknown", label: "status.notReported" });
  });
  it("reads a health it could not read as an outage, never a denial (M11)", () => {
    expect(healthStatus({ state: "outage", error: { code: "network" } }).tone).toBe("outage");
    expect(healthStatus({ state: "outage", error: { code: "network" } }).tone).not.toBe("deny");
  });
});
