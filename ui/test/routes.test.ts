// THE ROUTE TEST (0050; PLAN M2): no API route is reachable from the console
// that RBAC would deny. Every call the console makes names an entry of
// src/api/routes.ts, so it is enough that
//   1. every entry is a route of the server's north-bound table, with that
//      route's method, pattern and permission exactly; and
//   2. for every role, the console offers exactly the routes whose
//      permission that role holds (src/api/permit.ts), and nothing else.
// The table is ./north-routes.json, which a Go test keeps equal to the
// server's (internal/httpapi/north/console_routes_test.go).

import { describe, expect, it } from "vitest";
import { offered, offeredRoutes } from "../src/api/permit";
import { ROUTES, type RouteName } from "../src/api/routes";
import table from "./north-routes.json";

const names = Object.keys(ROUTES) as RouteName[];
const serverRoute = (method: string, pattern: string) =>
  table.routes.find((r) => r.method === method && r.pattern === pattern);

describe("the console's routes", () => {
  it.each(names)("%s is a route of the server's table, with its permission", (name) => {
    const route = ROUTES[name];
    const served = serverRoute(route.method, route.pattern);
    expect(served, `${route.method} ${route.pattern} is not served`).toBeDefined();
    expect(route.permission ?? "", `${name}'s permission`).toBe(served?.permission);
  });

  it("calls no route a host serves or one that reads across tenants", () => {
    for (const name of names) {
      const route = ROUTES[name];
      expect(serverRoute(route.method, route.pattern)?.access, name).not.toBe("cross-tenant");
    }
  });
});

describe.each(Object.entries(table.roles))("the %s role", (role, permissions) => {
  it("is offered exactly the routes it holds the permission for", () => {
    const want = names.filter((n) => {
      const p = serverRoute(ROUTES[n].method, ROUTES[n].pattern)?.permission ?? "";
      return p === "" || permissions.includes(p);
    });
    expect(offeredRoutes(permissions).sort()).toEqual(want.sort());
  });

  it("is never offered a route its permissions do not include", () => {
    for (const name of names) {
      const p = ROUTES[name].permission;
      if (p !== null && !permissions.includes(p)) expect(offered(name, permissions), `${role}: ${name}`).toBe(false);
    }
  });
});

it("knows the roles the server has, so a new role is checked here too", () => {
  expect(Object.keys(table.roles).sort()).toEqual(
    ["admin", "auditor", "fleet-admin", "grant-admin", "integration", "policy-author", "requester"].sort(),
  );
});
