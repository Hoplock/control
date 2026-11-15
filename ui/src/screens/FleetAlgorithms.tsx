// FLEET'S ALGORITHM POSTURE (0058; PLAN M17, M19, M26, §5.2;
// Hoplock/proxy#69, #72): what the fleet can enforce before an operator asks
// it to — which `algorithm_floor` levels each build declares, with the key
// exchanges each level accepts IN THAT BUILD, and what each profile the build
// accepts offers per axis, before any floor or ban.
//
// It reads two lists to their end (M26): GET …/fleet/algorithms, each proxy's
// latest declaration, and GET …/proxies, so that a proxy that declared nothing
// is named as `Not reported` rather than left out. The posture is the whole
// fleet's, so a page of it would say something false; both lists are bounded
// by the fleet. The declarations failing is the screen's error state (an
// outage, never a denial: M11); the proxies failing is its partial state, the
// builds shown and the gap named. Builds that disagree are a rolling upgrade
// IN PROGRESS (M19), never a fault.

import type { ReactNode } from "react";
import type { Answer } from "../api/client";
import type { DeclarationPage, DeclaredAlgorithms, ProfileCapability, Proxy, ProxyPage } from "../api/types";
import { Button } from "../components/Button";
import { PageHeader } from "../components/PageHeader";
import { EmptyState, ScreenFrame, Skeleton } from "../components/States";
import { StatusOf, type StatusName } from "../components/Status";
import { ViewLinks } from "../components/ViewLinks";
import { useI18n } from "../i18n/I18n";
import { useLoad } from "../shell/load";
import { useDeployment } from "../shell/Shell";
import {
  AXES,
  buildsOf,
  levelPosture,
  profilePosture,
  readAll,
  undeclaredProxies,
  type Build,
  type PostureState,
} from "./algorithmModel";
import { AXIS_LABEL, Idents, PqHybridNote, ProxyChips, READ_LIMIT } from "./AlgorithmParts";
import styles from "./Algorithms.module.css";


/** Fleet's views (0050's proxies, 0058's algorithms): each its own deep link. */
export function FleetViews({ current }: { current: "proxies" | "algorithms" }) {
  const { t } = useI18n();
  return (
    <ViewLinks
      label={t("fleet.views.label")}
      current={current}
      views={[
        { id: "proxies", to: "fleet", label: t("fleet.views.proxies") },
        { id: "algorithms", to: "fleet/algorithms", label: t("fleet.views.algorithms") },
      ]}
    />
  );
}

export const POSTURE: Readonly<Record<PostureState, StatusName>> = {
  declared: "declared",
  inProgress: "inProgress",
  notDeclared: "notDeclared",
  notReported: "notReported",
};

function BuildNames({ builds }: { builds: readonly number[] }) {
  const { t, f } = useI18n();
  return <>{f.list(builds.map((n) => t("algorithms.build.name", { n })))}</>;
}

interface Posture {
  declarations: DeclaredAlgorithms[];
  /** Every proxy, or the read's failure: the partial state's gap. */
  proxies: Answer<Proxy[]>;
}

export function FleetAlgorithmsScreen() {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const [load, refresh] = useLoad<Posture>(tenant ? `algorithms|${tenant}` : null, async (signal) => {
    const params = { tenant: tenant as string };
    const declarations = await readAll<DeclarationPage, DeclaredAlgorithms>(
      (after) =>
        api.call("fleetAlgorithms", { params, query: after ? { limit: READ_LIMIT, after } : { limit: READ_LIMIT }, signal }),
      (p) => p.declarations,
    );
    if (declarations.kind !== "ok") return declarations;
    const proxies = await readAll<ProxyPage, Proxy>(
      (after) => api.call("proxies", { params, query: after ? { limit: READ_LIMIT, after } : { limit: READ_LIMIT }, signal }),
      (p) => p.proxies,
    );
    return { kind: "ok", status: 200, data: { declarations: declarations.data, proxies } };
  });

  const gaps = load.state === "ok" && load.data.proxies.kind !== "ok" ? [t("algorithms.gap.proxies")] : [];

  return (
    <div className={styles.screen}>
      <PageHeader
        title={t("fleet.title")}
        lede={t("algorithms.lede")}
        actions={<Button onClick={refresh}>{t("fleet.refresh")}</Button>}
      />
      <FleetViews current="algorithms" />
      <ScreenFrame
        load={load}
        onRetry={refresh}
        gaps={gaps}
        isEmpty={(d) => d.declarations.length === 0 && d.proxies.kind === "ok" && d.proxies.data.length === 0}
        loading={<AlgorithmsSkeleton />}
        empty={
          <EmptyState
            title={t("algorithms.empty.title")}
            body={t("algorithms.empty.body")}
            action={<code dir="ltr" translate="no">{`POST /api/v1/tenants/${tenant ?? ""}/proxies/enrollments`}</code>}
          />
        }
      >
        {(d) => <Posture data={d} />}
      </ScreenFrame>
    </div>
  );
}

function AlgorithmsSkeleton() {
  return (
    <div className={styles.stack} aria-hidden="true">
      <Skeleton shape="title" />
      <Skeleton shape="block" size="full" />
      <Skeleton shape="block" size="full" />
    </div>
  );
}

function Posture({ data }: { data: Posture }) {
  const { t } = useI18n();
  const builds = buildsOf(data.declarations);
  const levels = levelPosture(builds);
  const profiles = profilePosture(builds);
  const nothing = data.proxies.kind === "ok" ? undeclaredProxies(data.proxies.data.map((p) => p.proxy_id), data.declarations) : null;
  return (
    <div className={styles.stack}>
      <section className={styles.section} aria-labelledby="algorithms-levels" data-levels="">
        <h2 id="algorithms-levels" className={styles.sectionTitle}>
          {t("algorithms.levels.title")}
        </h2>
        <p className={styles.para}>{t("algorithms.levels.lede")}</p>
        <div className={styles.tableWrap}>
          <table className={styles.table}>
            <caption className={styles.visuallyHidden}>{t("algorithms.levels.title")}</caption>
            <thead>
              <tr>
                <th scope="col">{t("algorithms.col.level")}</th>
                <th scope="col">{t("algorithms.col.state")}</th>
                <th scope="col">{t("algorithms.col.accepts")}</th>
                <th scope="col">{t("algorithms.col.cannot")}</th>
              </tr>
            </thead>
            <tbody>
              {levels.map((l) => (
                <tr key={l.level} data-level={l.level} data-posture={l.state}>
                  <th scope="row">
                    <code dir="ltr" translate="no">
                      {l.level}
                    </code>
                  </th>
                  <td>
                    <StatusOf name={POSTURE[l.state]} />
                  </td>
                  <td>
                    {l.variants.length === 0 ? (
                      <span className={styles.quiet}>{t(l.state === "notReported" ? "algorithms.levels.unreported" : "algorithms.levels.nobody")}</span>
                    ) : (
                      <div className={styles.variants}>
                        {l.variants.map((v) => (
                          <div key={v.builds.join(",")} className={styles.variant} data-variant={v.builds.join(",")}>
                            <span className={styles.meta}>
                              <BuildNames builds={v.builds} /> · {t("algorithms.proxies", { count: v.proxies.length })}
                            </span>
                            <Idents names={v.value} />
                          </div>
                        ))}
                      </div>
                    )}
                  </td>
                  <td>
                    {l.cannot.length ? <ProxyChips ids={l.cannot} /> : null}
                    {l.unreported.length ? (
                      <span className={styles.unreported}>
                        <StatusOf name="notReported" /> <ProxyChips ids={l.unreported} />
                      </span>
                    ) : null}
                    {!l.cannot.length && !l.unreported.length ? <span className={styles.quiet}>{t("algorithms.none")}</span> : null}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {levels.some((l) => l.state === "inProgress") ? <p className={styles.note}>{t("algorithms.levels.inProgress")}</p> : null}
        <PqHybridNote />
      </section>

      <section className={styles.section} aria-labelledby="algorithms-profiles" data-profiles="">
        <h2 id="algorithms-profiles" className={styles.sectionTitle}>
          {t("algorithms.profiles.title")}
        </h2>
        <p className={styles.para}>{t("algorithms.profiles.lede")}</p>
        {profiles.length === 0 ? (
          <p className={styles.unreported}>
            <StatusOf name="notReported" /> <span>{t("algorithms.profiles.unreported")}</span>
          </p>
        ) : (
          <ul className={styles.list}>
            {profiles.map((p) => (
              <li key={p.profile} className={styles.row} data-profile={p.profile} data-posture={p.state}>
                <code dir="ltr" translate="no">
                  {p.profile}
                </code>
                <StatusOf name={POSTURE[p.state]} />
                <span className={styles.meta}>
                  {p.variants.length > 1
                    ? t("algorithms.profiles.differ", { count: p.variants.length })
                    : p.variants.length === 1
                      ? <BuildNames builds={p.variants[0]?.builds ?? []} />
                      : null}
                </span>
                {p.without.length ? (
                  <span className={styles.meta}>
                    {t("algorithms.profiles.without")} <ProxyChips ids={p.without} />
                  </span>
                ) : null}
                {p.unreported.length ? (
                  <span className={styles.unreported}>
                    <StatusOf name="notReported" /> <ProxyChips ids={p.unreported} />
                  </span>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className={styles.section} aria-labelledby="algorithms-builds" data-builds="">
        <h2 id="algorithms-builds" className={styles.sectionTitle}>
          {t("algorithms.builds.title", { count: builds.length })}
        </h2>
        <p className={styles.para}>{t("algorithms.builds.lede")}</p>
        {builds.length === 0 ? (
          <p className={styles.unreported}>
            <StatusOf name="notReported" /> <span>{t("algorithms.builds.none")}</span>
          </p>
        ) : (
          builds.map((b) => <BuildCard key={b.n} build={b} />)
        )}
      </section>

      <section className={styles.section} aria-labelledby="algorithms-nothing" data-undeclared="">
        <h2 id="algorithms-nothing" className={styles.sectionTitle}>
          {t("algorithms.nothing.title")}
        </h2>
        {nothing === null ? (
          <p className={styles.para}>{t("algorithms.nothing.unread")}</p>
        ) : nothing.length === 0 ? (
          <p className={styles.para}>{t("algorithms.nothing.none")}</p>
        ) : (
          <>
            <p className={styles.para}>{t("algorithms.nothing.lede", { count: nothing.length })}</p>
            <p className={styles.unreported}>
              <StatusOf name="notReported" /> <ProxyChips ids={nothing} />
            </p>
          </>
        )}
      </section>
    </div>
  );
}

function BuildCard({ build: b }: { build: Build }) {
  const { t, f } = useI18n();
  const floors = b.declaration.algorithm_floors;
  const profiles = b.declaration.algorithm_profiles;
  return (
    <article className={styles.build} data-build={b.n} aria-labelledby={`build-${b.n}`}>
      <header className={styles.buildHead}>
        <h3 id={`build-${b.n}`} className={styles.subTitle}>
          {t("algorithms.build.name", { n: b.n })}
        </h3>
        <span className={styles.meta}>
          {t("algorithms.proxies", { count: b.proxies.length })} · {t("algorithms.build.declaredAt")}{" "}
          <time dateTime={f.utc(b.declaredAt)} title={f.utc(b.declaredAt)}>
            {f.dateTime(b.declaredAt)}
          </time>
        </span>
      </header>
      <ProxyChips ids={b.proxies} />
      <dl className={styles.facts}>
        <dt>{t("algorithms.build.floors")}</dt>
        <dd>
          {floors === undefined ? (
            <StatusOf name="notReported" />
          ) : floors.length === 0 ? (
            <span className={styles.quiet}>{t("algorithms.build.noLevel")}</span>
          ) : (
            <ul className={styles.list}>
              {floors.map((l) => (
                <li key={l.level} className={styles.row}>
                  <code dir="ltr" translate="no">
                    {l.level}
                  </code>
                  <Idents names={l.key_exchanges} />
                </li>
              ))}
            </ul>
          )}
        </dd>
        <dt>{t("algorithms.build.profiles")}</dt>
        <dd>{profiles === undefined ? <StatusOf name="notReported" /> : <ProfileTable profiles={profiles} label={t("algorithms.build.name", { n: b.n })} />}</dd>
      </dl>
    </article>
  );
}

/** What each profile offers per axis: profiles down, axes across. */
export function ProfileTable({ profiles, label }: { profiles: readonly ProfileCapability[]; label: ReactNode }) {
  const { t } = useI18n();
  if (profiles.length === 0) return <span className={styles.quiet}>{t("algorithms.build.noProfile")}</span>;
  return (
    <div className={styles.tableWrap}>
      <table className={styles.table}>
        <caption className={styles.visuallyHidden}>
          {t("algorithms.profiles.caption")} {label}
        </caption>
        <thead>
          <tr>
            <th scope="col">{t("algorithms.col.profile")}</th>
            {AXES.map((a) => (
              <th key={a} scope="col">
                {t(AXIS_LABEL[a])}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {profiles.map((p) => (
            <tr key={p.profile} data-offer={p.profile}>
              <th scope="row">
                <code dir="ltr" translate="no">
                  {p.profile}
                </code>
              </th>
              {AXES.map((a) => (
                <td key={a}>
                  <Idents names={p[a] ?? []} />
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
