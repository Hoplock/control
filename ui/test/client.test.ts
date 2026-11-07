import { describe, expect, it } from "vitest";
import { ApiClient, type Fetch } from "../src/api/client";
import { ROUTES, wildcards } from "../src/api/routes";

function respond(status: number, body?: unknown): Fetch {
  return async () => new Response(body === undefined ? null : JSON.stringify(body), { status });
}

describe("the API client (M2, M11)", () => {
  it("calls only the routes it names, each under /api/v1/", () => {
    for (const [name, r] of Object.entries(ROUTES)) {
      expect(r.pattern.startsWith("/api/v1/"), name).toBe(true);
      expect(r.pattern.startsWith("/api/v1/hosts/"), name).toBe(false);
    }
    expect(ApiClient.url("instance", { params: { tenant: "a b" } })).toBe("/api/v1/tenants/a%20b/instance");
    expect(wildcards(ROUTES.federatedStart.pattern)).toEqual(["connector"]);
    expect(() => ApiClient.url("instance")).toThrow();
  });

  it("sends the call as the route's method with the session cookie and no other credential", async () => {
    let seen: RequestInit | undefined;
    const api = new ApiClient(async (_, init) => {
      seen = init;
      return new Response(null, { status: 204 });
    });
    await api.call("endSession");
    expect(seen?.method).toBe("DELETE");
    expect(seen?.credentials).toBe("same-origin");
    expect(JSON.stringify(seen?.headers)).not.toMatch(/authorization/i);
  });

  it("keeps a refusal and an outage apart", async () => {
    const refused = await new ApiClient(respond(403, { code: "forbidden", correlation_id: "c" })).call("session");
    expect(refused).toMatchObject({ kind: "refused", status: 403, error: { code: "forbidden" } });
    const outage = await new ApiClient(respond(503, { code: "unavailable" })).call("session");
    expect(outage.kind).toBe("outage");
    const unreadable = await new ApiClient(respond(502)).call("session");
    expect(unreadable).toMatchObject({ kind: "outage", error: { code: "internal" } });
    const down = await new ApiClient(async () => {
      throw new TypeError("failed to fetch");
    }).call("session");
    expect(down).toMatchObject({ kind: "outage", status: 0, error: { code: "network" } });
  });

  it("signs the operator out on an unauthenticated answer, except where it is a failed sign-in", async () => {
    let out = 0;
    const api = new ApiClient(respond(401, { code: "unauthenticated" }), () => out++);
    await api.call("session");
    expect(out).toBe(1);
    await api.call("localLogin", { body: {} });
    expect(out).toBe(1);
  });
});
