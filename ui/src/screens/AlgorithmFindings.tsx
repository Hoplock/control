// THE FLOOR'S IMPACT PREVIEW AND A BAN'S PER-BUILD WARNINGS (0058; PLAN
// §5.2, §5.6, M17, M19; Hoplock/proxy#69, #72). These are findings of the
// publish-time report 0053 renders, and they render INSIDE its finding card,
// in the report's order — never as a panel beside it. What this file adds is
// the evidence each one carries, set out so an author can act on it:
//
//   - a target a raised floor breaks: the level it was seen to meet, what it
//     negotiated and offered, and when — a past fact about a target that may
//     have been upgraded since, so it WARNS and never blocks;
//   - a target with no observation, or a stale one: `Not reported`, never
//     shown as meeting the floor;
//   - a proxy that cannot enforce the level yet;
//   - a ban that empties an axis on some builds: per build, the proxies that
//     will refuse the route and what each build offers there; builds that
//     disagree are a rolling upgrade IN PROGRESS (M19), never a fault; and it
//     warns, never blocks, even when every proxy would refuse the route;
//   - a build whose declaration cannot judge the ban: `Not judged`, never fine.

import type { Finding } from "../api/types";
import { StatusOf } from "../components/Status";
import { useI18n } from "../i18n/I18n";
import type { Load } from "../shell/load";
import { AXIS_LABEL, Idents, PqHybridNote, ProxyChips } from "./AlgorithmParts";
import { PQ_HYBRID, type Build } from "./algorithmModel";
import styles from "./Algorithms.module.css";

/** The codes whose evidence this file sets out; the rest keep the card's own lists. */
export const ALGORITHM_DETAIL: ReadonlySet<string> = new Set([
  "algorithm.target_below_floor",
  "algorithm.target_not_reported",
  "algorithm.floor_undeclared",
  "algorithm.axis_emptied",
  "algorithm.declaration_unknown",
]);

/** Whether the report needs the fleet's builds to say whether builds disagree. */
export function needsBuilds(findings: readonly Finding[]): boolean {
  return findings.some((f) => f.source === "control" && f.code === "algorithm.axis_emptied");
}

interface KexTarget {
  hostname: string;
  port?: number;
  floor_met: string;
  negotiated?: string;
  offered?: string[];
  observed_at?: string;
}

interface EmptiedOn {
  proxy_id: string;
  offered: string[];
}

function str(v: unknown): string {
  return typeof v === "string" ? v : "";
}

function objects<T>(v: unknown, ok: (o: Record<string, unknown>) => boolean): T[] {
  return Array.isArray(v) ? (v.filter((x) => x && typeof x === "object" && ok(x as Record<string, unknown>)) as T[]) : [];
}

function strings(v: unknown): string[] {
  return Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : [];
}

/** "3 of 140 shown" when the server cut the list at its first 100 (M26.3). */
function Cut({ finding, name, shown }: { finding: Finding; name: string; shown: number }) {
  const { t, f } = useI18n();
  const p = finding.parameters ?? {};
  if (p[`${name}_truncated`] !== true) return null;
  const count = typeof p[`${name}_count`] === "number" ? (p[`${name}_count`] as number) : shown;
  return <p className={styles.meta}>{t("policies.finding.list.cut", { shown: f.number(shown), count: f.number(count) })}</p>;
}

function Instant({ at }: { at?: string }) {
  const { f } = useI18n();
  if (!at) return <StatusOf name="notReported" />;
  return (
    <time dateTime={f.utc(at)} title={f.utc(at)}>
      {f.dateTime(at)}
    </time>
  );
}

/** The evidence one algorithm finding carries, set out inside its card. */
export function AlgorithmDetail({ finding: fd, builds }: { finding: Finding; builds?: Load<Build[]> }) {
  const { t } = useI18n();
  const p = fd.parameters ?? {};
  const floor = str(p.floor);
  const pq = floor === PQ_HYBRID ? <PqHybridNote /> : null;

  switch (fd.code) {
    case "algorithm.target_below_floor": {
      const targets = objects<KexTarget>(p.targets, (o) => typeof o.hostname === "string");
      return (
        <div className={styles.stack} data-floor-impact="below">
          <div className={styles.tableWrap} tabIndex={0} role="region" aria-label={t("policies.floor.below.caption")}>
            <table className={styles.table}>
              <caption className={styles.visuallyHidden}>{t("policies.floor.below.caption")}</caption>
              <thead>
                <tr>
                  <th scope="col">{t("policies.floor.col.target")}</th>
                  <th scope="col">{t("policies.floor.col.met")}</th>
                  <th scope="col">{t("policies.floor.col.negotiated")}</th>
                  <th scope="col">{t("policies.floor.col.offered")}</th>
                  <th scope="col">{t("policies.floor.col.observed")}</th>
                </tr>
              </thead>
              <tbody>
                {targets.map((x) => (
                  <tr key={`${x.hostname}:${x.port ?? 0}`} data-target={x.hostname}>
                    <th scope="row">
                      <code dir="ltr" translate="no">
                        {x.port ? `${x.hostname}:${x.port}` : x.hostname}
                      </code>
                    </th>
                    <td>
                      <code dir="ltr" translate="no">
                        {x.floor_met}
                      </code>
                    </td>
                    <td>{x.negotiated ? <Idents names={[x.negotiated]} /> : <span className={styles.quiet}>{t("algorithms.none")}</span>}</td>
                    <td>
                      <Idents names={x.offered ?? []} />
                    </td>
                    <td>
                      <Instant at={x.observed_at} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <Cut finding={fd} name="targets" shown={targets.length} />
          <p className={styles.warnNote}>{t("policies.floor.warnsOnly")}</p>
          {pq}
        </div>
      );
    }
    case "algorithm.target_not_reported": {
      const targets = strings(p.targets);
      return (
        <div className={styles.stack} data-floor-impact="unreported">
          <ul className={styles.list}>
            {targets.map((h) => (
              <li key={h} className={styles.row} data-target={h}>
                <code dir="ltr" translate="no">
                  {h}
                </code>
                <StatusOf name="notReported" />
              </li>
            ))}
          </ul>
          <Cut finding={fd} name="targets" shown={targets.length} />
          <p className={styles.meta}>{t("policies.floor.unreported")}</p>
          {pq}
        </div>
      );
    }
    case "algorithm.floor_undeclared": {
      const proxies = strings(p.proxies);
      return (
        <div className={styles.stack} data-floor-impact="cannot">
          <p className={styles.meta}>{t("policies.floor.cannot", { floor: `⁨${floor}⁩` })}</p>
          <ProxyChips ids={proxies} />
          <Cut finding={fd} name="proxies" shown={proxies.length} />
          {pq}
        </div>
      );
    }
    case "algorithm.axis_emptied": {
      const on = objects<EmptiedOn>(p.proxies, (o) => typeof o.proxy_id === "string");
      const axis = str(p.axis);
      const axisLabel = axis in AXIS_LABEL ? t(AXIS_LABEL[axis as keyof typeof AXIS_LABEL]) : axis;
      const emptied = typeof p.proxies_count === "number" ? p.proxies_count : on.length;
      return (
        <div className={styles.stack} data-ban-emptied={axis}>
          <p className={styles.meta}>{t("policies.ban.refuse", { axis: axisLabel })}</p>
          <div className={styles.tableWrap} tabIndex={0} role="region" aria-label={t("policies.ban.refuse", { axis: axisLabel })}>
            <table className={styles.table}>
              <caption className={styles.visuallyHidden}>{t("policies.ban.refuse", { axis: axisLabel })}</caption>
              <thead>
                <tr>
                  <th scope="col">{t("policies.ban.col.proxy")}</th>
                  <th scope="col">{t("policies.ban.col.offers", { axis: axisLabel })}</th>
                </tr>
              </thead>
              <tbody>
                {on.map((e) => (
                  <tr key={e.proxy_id} data-proxy={e.proxy_id}>
                    <th scope="row">
                      <code dir="ltr" translate="no">
                        {e.proxy_id}
                      </code>
                    </th>
                    <td>
                      <Idents names={e.offered ?? []} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <Cut finding={fd} name="proxies" shown={on.length} />
          <Disagreement builds={builds} emptied={emptied} />
          <p className={styles.warnNote}>{t("policies.ban.warnsOnly")}</p>
        </div>
      );
    }
    case "algorithm.declaration_unknown": {
      const proxies = strings(p.proxies);
      if (proxies.length === 0) return null;
      return (
        <div className={styles.stack} data-ban-unjudged="">
          <p className={styles.unreported}>
            <StatusOf name="notJudged" /> <ProxyChips ids={proxies} />
          </p>
          <Cut finding={fd} name="proxies" shown={proxies.length} />
          <p className={styles.meta}>{t("policies.ban.unjudged")}</p>
        </div>
      );
    }
    default:
      return null;
  }
}

/**
 * Whether the fleet's builds disagree about the route: some refuse it and the
 * rest do not, which is a rolling upgrade in progress (M19), never a fault.
 * Said only where the fleet's declarations could be read.
 */
function Disagreement({ builds, emptied }: { builds?: Load<Build[]>; emptied: number }) {
  const { t } = useI18n();
  if (!builds || builds.state !== "ok") return null;
  const declaring = builds.data.reduce((n, b) => n + b.proxies.length, 0);
  if (declaring === 0) return null;
  if (emptied < declaring) {
    return (
      <p className={styles.unreported} data-ban-progress="inProgress">
        <StatusOf name="inProgress" /> <span>{t("policies.ban.disagree", { emptied, declaring })}</span>
      </p>
    );
  }
  return (
    <p className={styles.meta} data-ban-progress="every">
      {t("policies.ban.every", { declaring })}
    </p>
  );
}
