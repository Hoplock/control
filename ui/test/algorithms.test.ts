// Algorithm posture's rules (0058; PLAN M17, M19, M26): builds are distinct
// declarations; builds that disagree are in progress, never a fault; a build
// that declared nothing there is not reported, never "no floor"; and a list is
// read to its end or not at all.

import { describe, expect, it } from "vitest";
import type { Answer } from "../src/api/client";
import type { DeclaredAlgorithms, Paged, ProfileCapability } from "../src/api/types";
import { buildsOf, levelPosture, profilePosture, readAll, undeclaredProxies } from "../src/screens/algorithmModel";

const offer = (profile: string, ciphers: string[]): ProfileCapability => ({
  profile,
  key_exchanges: ["curve25519-sha256"],
  ciphers,
  macs: ["hmac-sha2-256"],
  host_keys: ["ssh-ed25519"],
  public_key_auth: ["ssh-ed25519"],
});

const decl = (proxy: string, d: DeclaredAlgorithms["declaration"], at = "2026-11-01T08:00:00Z"): DeclaredAlgorithms => ({
  proxy_id: proxy,
  declaration: d,
  declared_at: at,
});

const OLD = {
  algorithm_floors: [{ level: "modern-kex", key_exchanges: ["curve25519-sha256", "curve25519-sha256@libssh.org"] }],
  algorithm_profiles: [offer("default", ["aes128-ctr", "aes256-gcm@openssh.com"])],
};
const NEW = {
  algorithm_floors: [
    { level: "modern-kex", key_exchanges: ["curve25519-sha256", "curve25519-sha256@libssh.org", "mlkem768x25519-sha256"] },
    { level: "pq-hybrid-kex", key_exchanges: ["mlkem768x25519-sha256"] },
  ],
  algorithm_profiles: [offer("default", ["aes256-gcm@openssh.com"])],
};

describe("builds", () => {
  it("groups proxies that declared the same thing, whatever the list order", () => {
    const b = buildsOf([
      decl("px-b", OLD),
      decl("px-a", { ...OLD, algorithm_floors: [{ level: "modern-kex", key_exchanges: ["curve25519-sha256@libssh.org", "curve25519-sha256"] }] }, "2026-11-02T08:00:00Z"),
      decl("px-c", NEW),
    ]);
    expect(b.map((x) => [x.n, x.proxies])).toEqual([
      [1, ["px-a", "px-b"]],
      [2, ["px-c"]],
    ]);
    expect(b[0]?.declaredAt).toBe("2026-11-02T08:00:00Z");
  });
});

describe("level posture", () => {
  it("is declared when every build accepts one set", () => {
    const [modern, pq] = levelPosture(buildsOf([decl("px-a", OLD), decl("px-b", OLD)]));
    expect(modern?.state).toBe("declared");
    expect(pq?.state).toBe("notDeclared");
    expect(pq?.cannot).toEqual(["px-a", "px-b"]);
  });

  it("is in progress, naming who cannot enforce it, while builds disagree (M19)", () => {
    const [modern, pq] = levelPosture(buildsOf([decl("px-a", OLD), decl("px-c", NEW)]));
    expect(modern?.state).toBe("inProgress");
    expect(modern?.variants.map((v) => v.builds)).toEqual([[1], [2]]);
    expect(pq?.state).toBe("inProgress");
    expect(pq?.cannot).toEqual(["px-a"]);
    expect(pq?.variants[0]?.value).toEqual(["mlkem768x25519-sha256"]);
  });

  it("is not reported, never 'no floor', for a build that declared no floors (M17)", () => {
    const levels = levelPosture(buildsOf([decl("px-a", { algorithm_profiles: OLD.algorithm_profiles })]));
    expect(levels.map((l) => l.state)).toEqual(["notReported", "notReported"]);
    expect(levels[0]?.unreported).toEqual(["px-a"]);
    expect(levels[0]?.cannot).toEqual([]);
    expect(levelPosture([]).map((l) => l.state)).toEqual(["notReported", "notReported"]);
  });

  it("shows a level a newer build declares after the known ladder", () => {
    const levels = levelPosture(buildsOf([decl("px-a", { algorithm_floors: [{ level: "future-kex", key_exchanges: ["x"] }] })]));
    expect(levels.map((l) => l.level)).toEqual(["modern-kex", "pq-hybrid-kex", "future-kex"]);
  });
});

describe("profile posture", () => {
  it("is in progress when builds offer different lists under one profile", () => {
    const [p] = profilePosture(buildsOf([decl("px-a", OLD), decl("px-c", NEW)]));
    expect(p?.profile).toBe("default");
    expect(p?.state).toBe("inProgress");
    expect(p?.variants).toHaveLength(2);
  });

  it("names a build that declared no profiles as not reported, never 'offers nothing'", () => {
    const [p] = profilePosture(buildsOf([decl("px-a", OLD), decl("px-z", { algorithm_floors: OLD.algorithm_floors })]));
    expect(p?.state).toBe("declared");
    expect(p?.unreported).toEqual(["px-z"]);
  });
});

it("names the proxies that declared nothing", () => {
  expect(undeclaredProxies(["px-a", "px-b", "px-c"], [decl("px-b", OLD)])).toEqual(["px-a", "px-c"]);
});

describe("readAll (M26)", () => {
  type P = Paged & { items: string[] };
  const ok = (data: P): Answer<P> => ({ kind: "ok", status: 200, data });

  it("follows next until truncated is false", async () => {
    const pages: Record<string, P> = {
      "": { items: ["a", "b"], truncated: true, next: "c1" },
      c1: { items: ["c"], truncated: true, next: "c2" },
      c2: { items: ["d"], truncated: false },
    };
    const asked: (string | undefined)[] = [];
    const got = await readAll<P, string>(async (after) => (asked.push(after), ok(pages[after ?? ""] as P)), (p) => p.items);
    expect(got).toMatchObject({ kind: "ok", data: ["a", "b", "c", "d"] });
    expect(asked).toEqual([undefined, "c1", "c2"]);
  });

  it("fails whole on a failed page, never answering a short list", async () => {
    const got = await readAll<P, string>(
      async (after) =>
        after
          ? { kind: "outage", status: 503, error: { code: "unavailable" } }
          : ok({ items: ["a"], truncated: true, next: "c1" }),
      (p) => p.items,
    );
    expect(got.kind).toBe("outage");
  });

  it("fails on a page that continues without a new position", async () => {
    const got = await readAll<P, string>(async () => ok({ items: ["a"], truncated: true }), (p) => p.items);
    expect(got).toMatchObject({ kind: "outage", error: { code: "list_unterminated" } });
  });
});
