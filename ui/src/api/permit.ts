// WHICH ROUTES THE CONSOLE OFFERS (PLAN M2; 0050's route test).
//
// RBAC is the API's: the console holds no privilege, and a hidden button is a
// courtesy, never the control. But the console must never offer an operator
// a call RBAC would refuse them, so every control that makes a call asks
// here, with the route's name, and the answer is the route's own permission
// against the session's permissions in the tenant. test/routes.test.ts holds
// this against the server's route table and every role.

import { ROUTES, type RouteName } from "./routes";

/** Whether a route is offered to a holder of these permissions. */
export function offered(name: RouteName, permissions: readonly string[]): boolean {
  const permission: string | null = ROUTES[name].permission;
  return permission === null || permissions.includes(permission);
}

/** Every route a holder of these permissions is offered. */
export function offeredRoutes(permissions: readonly string[]): RouteName[] {
  return (Object.keys(ROUTES) as RouteName[]).filter((n) => offered(n, permissions));
}
