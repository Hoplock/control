// ALGORITHM POSTURE, AS DATA (0058; PLAN M17, M19, M26, §5.2). Pure: what the
// fleet's declarations say each floor level and each profile is, build by
// build, and the list follower the screens use to read a bounded list to its
// end. Unit-tested in test/algorithms.test.ts.
//
// A BUILD is a distinct declaration. The contract carries no build id, so the
// proxies that declared the same thing are one build, and two that declared
// differently are two — which is exactly the per-build truth M17 asks a fleet
// view to show. During a rolling upgrade two builds may accept different
// exchanges for one level, or offer different lists under one profile: that
// is IN PROGRESS (M19), never a fault. A build that declared no floors or no
// profiles is NOT REPORTED there, never "no floor" or "offers nothing" (M17:
// stale, undated and absent are one case).

import type { Answer } from "../api/client";
import type { AlgorithmAxis, AlgorithmDeclaration, DeclaredAlgorithms, Paged, ProfileCapability } from "../api/types";

/**
 * The floor's ladder, lowest first: the contract's levels
 * (internal/contract/enums.go). A level a newer build declares that this
 * console does not know is shown after them, as declared.
 */
export const FLOOR_LEVELS: readonly string[] = ["modern-kex", "pq-hybrid-kex"];

/** The level whose meaning the console explains (PLAN §5.2): ML-KEM-768 hybrid only. */
export const PQ_HYBRID = "pq-hybrid-kex";
/** The exchange that meets it, and the other hybrid, which the proxy does not implement and never does. */
export const PQ_HYBRID_KEX = "mlkem768x25519-sha256";
export const SNTRUP_KEX = "sntrup761x25519-sha512";

/** The five axes, in the contract's order (contract.Axes). */
export const AXES: readonly AlgorithmAxis[] = ["key_exchanges", "ciphers", "macs", "host_keys", "public_key_auth"];

export interface Build {
  /** 1-based, in the order of each build's first proxy id. A label, not an identity. */
  n: number;
  declaration: AlgorithmDeclaration;
  /** Its proxies, by id. */
  proxies: string[];
  /** The newest of its proxies' declarations. */
  declaredAt: string;
}

function canonical(d: AlgorithmDeclaration): string {
  const sorted = (xs: readonly string[] | undefined) => [...(xs ?? [])].sort();
  return JSON.stringify({
    f: [...(d.algorithm_floors ?? [])]
      .map((l) => [l.level, sorted(l.key_exchanges)])
      .sort((a, b) => String(a[0]).localeCompare(String(b[0]))),
    p: [...(d.algorithm_profiles ?? [])]
      .map((p) => [p.profile, ...AXES.map((a) => sorted(p[a]))])
      .sort((a, b) => String(a[0]).localeCompare(String(b[0]))),
    a: AXES.map((a) => sorted(d.algorithms?.[a])),
    has: [d.algorithm_floors !== undefined, d.algorithm_profiles !== undefined, d.algorithms !== undefined],
  });
}

/** Groups the declarations into builds. */
export function buildsOf(decls: readonly DeclaredAlgorithms[]): Build[] {
  const byKey = new Map<string, Build>();
  for (const d of [...decls].sort((a, b) => a.proxy_id.localeCompare(b.proxy_id))) {
    const key = canonical(d.declaration);
    const b = byKey.get(key);
    if (b) {
      b.proxies.push(d.proxy_id);
      if (d.declared_at > b.declaredAt) b.declaredAt = d.declared_at;
    } else {
      byKey.set(key, { n: byKey.size + 1, declaration: d.declaration, proxies: [d.proxy_id], declaredAt: d.declared_at });
    }
  }
  return [...byKey.values()];
}

/**
 * A level's or a profile's state across the fleet:
 *  - declared: every build that says anything there says the same;
 *  - inProgress: builds disagree, or some lack it (M19);
 *  - notDeclared: no build declares it — a fact, not an absence of proof;
 *  - notReported: no build said anything there at all (M17).
 */
export type PostureState = "declared" | "inProgress" | "notDeclared" | "notReported";

export interface Variant<T> {
  /** What these builds accept or offer. */
  value: T;
  builds: number[];
  proxies: string[];
}

export interface LevelPosture {
  level: string;
  state: PostureState;
  /** Each distinct accepted set, with the builds that accept it. */
  variants: Variant<string[]>[];
  /** Proxies whose build declared floors without this level: they cannot enforce it yet. */
  cannot: string[];
  /** Proxies whose build declared no floors: not reported, never "no floor". */
  unreported: string[];
}

function variantsOf<T>(entries: { build: Build; value: T }[], key: (v: T) => string): Variant<T>[] {
  const out = new Map<string, Variant<T>>();
  for (const e of entries) {
    const k = key(e.value);
    const v = out.get(k) ?? { value: e.value, builds: [], proxies: [] };
    v.builds.push(e.build.n);
    v.proxies.push(...e.build.proxies);
    out.set(k, v);
  }
  return [...out.values()];
}

function stateOf(builds: number, reporting: number, having: number, variants: number): PostureState {
  if (builds === 0 || reporting === 0) return "notReported";
  if (having === 0) return "notDeclared";
  if (having === reporting && variants === 1) return "declared";
  return "inProgress";
}

/** Each floor level's state across the builds: the known ladder, then any level a build adds. */
export function levelPosture(builds: readonly Build[]): LevelPosture[] {
  const levels = [...FLOOR_LEVELS];
  for (const b of builds) for (const l of b.declaration.algorithm_floors ?? []) if (!levels.includes(l.level)) levels.push(l.level);
  const reporting = builds.filter((b) => b.declaration.algorithm_floors !== undefined);
  const unreported = builds.filter((b) => b.declaration.algorithm_floors === undefined).flatMap((b) => b.proxies);
  return levels.map((level) => {
    const having: { build: Build; value: string[] }[] = [];
    const cannot: string[] = [];
    for (const b of reporting) {
      const l = b.declaration.algorithm_floors?.find((x) => x.level === level);
      if (l) having.push({ build: b, value: [...l.key_exchanges] });
      else cannot.push(...b.proxies);
    }
    const variants = variantsOf(having, (v) => [...v].sort().join(" "));
    return {
      level,
      state: stateOf(builds.length, reporting.length, having.length, variants.length),
      variants,
      cannot,
      unreported,
    };
  });
}

export interface ProfilePosture {
  profile: string;
  state: PostureState;
  variants: Variant<ProfileCapability>[];
  /** Proxies whose build declared profiles without this one: it does not accept it. */
  without: string[];
  /** Proxies whose build declared no profiles: not reported, never "offers nothing". */
  unreported: string[];
}

/** Each profile's state across the builds, in the order the builds name them. */
export function profilePosture(builds: readonly Build[]): ProfilePosture[] {
  const names: string[] = [];
  for (const b of builds) for (const p of b.declaration.algorithm_profiles ?? []) if (!names.includes(p.profile)) names.push(p.profile);
  const reporting = builds.filter((b) => b.declaration.algorithm_profiles !== undefined);
  const unreported = builds.filter((b) => b.declaration.algorithm_profiles === undefined).flatMap((b) => b.proxies);
  return names.map((profile) => {
    const having: { build: Build; value: ProfileCapability }[] = [];
    const without: string[] = [];
    for (const b of reporting) {
      const p = b.declaration.algorithm_profiles?.find((x) => x.profile === profile);
      if (p) having.push({ build: b, value: p });
      else without.push(...b.proxies);
    }
    const variants = variantsOf(having, (p) => AXES.map((a) => [...(p[a] ?? [])].sort().join(" ")).join("|"));
    return {
      profile,
      state: stateOf(builds.length, reporting.length, having.length, variants.length),
      variants,
      without,
      unreported,
    };
  });
}

/** The proxies that declared nothing at all: not reported, never "no floor" (M17). */
export function undeclaredProxies(proxyIds: readonly string[], decls: readonly DeclaredAlgorithms[]): string[] {
  const declared = new Set(decls.map((d) => d.proxy_id));
  return proxyIds.filter((id) => !declared.has(id));
}

/**
 * Reads a bounded list to its end (M26): every page, following `next` until
 * `truncated` is false. A page that fails fails the whole read — never a short
 * list answered as though it had ended (M26.6) — and so does a page that says
 * it continues without saying where, or names the position it was asked from.
 */
export async function readAll<P extends Paged, T>(
  read: (after: string | undefined) => Promise<Answer<P>>,
  items: (page: P) => readonly T[],
  onPage?: (pages: number, count: number) => void,
): Promise<Answer<T[]>> {
  const out: T[] = [];
  let after: string | undefined;
  let pages = 0;
  for (;;) {
    const a = await read(after);
    if (a.kind !== "ok") return a;
    out.push(...items(a.data));
    pages++;
    onPage?.(pages, out.length);
    if (!a.data.truncated) return { kind: "ok", status: a.status, data: out, correlationId: a.correlationId };
    if (!a.data.next || a.data.next === after) {
      return {
        kind: "outage",
        status: a.status,
        error: { code: "list_unterminated", message: "the list said it continues and named no new position", correlation_id: a.correlationId },
      };
    }
    after = a.data.next;
  }
}
