// EXTENSIONS (0056; PLAN M15, M8, M11, §7 "Export").
//
// /console/extensions: what is extending this deployment — each extension
// point, what is registered at it, whether a handler or a sync started, and
// how far each downstream consumer of the audit store is behind in this
// tenant. It reads GET …/extensions (extension:read) and changes nothing:
// registration is sealed before the server starts.
//
// Three rules from the plan decide how it reads:
//   - a point with nothing registered shows what Control does instead, in the
//     console's own words, and is never shown as broken (M15);
//   - a handler or sync whose Start failed is registered and not started,
//     with the error's kind, degraded and never an outage (M11);
//   - a failing sink is a downstream consumer failing, and a sink a retention
//     pass overtook missed what the pass's record explains — neither is an
//     outage or tampering, because the local store is the record (M8).

import type { ExtensionPoint, Extensions } from "../api/types";
import { Badge } from "../components/Badge";
import { Button } from "../components/Button";
import { FeedLagSkeleton, FeedLagView } from "../components/FeedLag";
import { Ident } from "../components/Ident";
import { PageHeader } from "../components/PageHeader";
import { EmptyState, ScreenFrame, Skeleton } from "../components/States";
import { StatusOf } from "../components/Status";
import { useI18n } from "../i18n/I18n";
import { useLoad } from "../shell/load";
import { useDeployment } from "../shell/Shell";
import { ARCHIVE_POINT, extendedBy, gaps, POINTS, pointStatus, SINK_POINT, START_ERRORS, type Gap } from "./extensionsModel";
import styles from "./Extensions.module.css";

export function ExtensionsScreen() {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const [load, refresh] = useLoad<Extensions>(tenant ? `extensions|${tenant}` : null, (signal) =>
    api.call("extensions", { params: { tenant: tenant as string }, signal }),
  );
  const pointName = usePointName();
  const gapText = (g: Gap): string => {
    switch (g.kind) {
      case "sinkLag":
        return t("extensions.partial.sinkLag", { provider: g.provider });
      case "archiveLag":
        return t("extensions.partial.archiveLag");
      case "unknownPoint":
        return t("extensions.partial.unknownPoint", { point: g.point });
    }
  };

  return (
    <div className={styles.screen}>
      <PageHeader
        title={t("extensions.title")}
        lede={t("extensions.lede")}
        actions={
          <Button onClick={refresh} data-refresh="">
            {t("extensions.refresh")}
          </Button>
        }
      />
      <ScreenFrame
        load={load}
        onRetry={refresh}
        gaps={load.state === "ok" ? gaps(load.data.points).map(gapText) : []}
        isEmpty={(d) => d.points.length === 0}
        empty={
          <EmptyState
            title={t("extensions.empty.title")}
            body={t("extensions.empty.body")}
            action={<Button onClick={refresh}>{t("extensions.refresh")}</Button>}
          />
        }
        loading={<ExtensionsSkeleton />}
      >
        {(d) => (
          <>
            <InPlay points={d.points} pointName={pointName} />
            <ul className={styles.points} aria-label={t("extensions.points.label")}>
              {d.points.map((p) => (
                <Point key={p.point} point={p} name={pointName(p.point)} />
              ))}
            </ul>
          </>
        )}
      </ScreenFrame>
    </div>
  );
}

/** A point's name from the catalogue, or its code when this console does not know it. */
function usePointName(): (code: string) => string {
  const { t } = useI18n();
  return (code) => {
    const known = POINTS[code];
    return known ? t(known.name) : code;
  };
}

/** Whether anything from outside Control is in play, and which: the first thing an operator debugging behaviour needs. */
function InPlay({ points, pointName }: { points: readonly ExtensionPoint[]; pointName(code: string): string }) {
  const { t } = useI18n();
  const extended = extendedBy(points);
  const registered = points.filter((p) => p.registered).length;
  return (
    <section className={styles.panel} aria-labelledby="extensions-in-play" data-in-play={extended.length}>
      <h2 id="extensions-in-play" className={styles.panelTitle}>
        {t("extensions.inPlay.title")}
      </h2>
      <p className={styles.text}>{t("extensions.inPlay.count", { registered, total: points.length })}</p>
      {extended.length === 0 ? (
        <p className={styles.quiet}>{t("extensions.inPlay.none")}</p>
      ) : (
        <>
          <p className={styles.text}>{t("extensions.inPlay.extended")}</p>
          <ul className={styles.inPlay}>
            {extended.map((e) => (
              <li key={`${e.point}|${e.provider}`} data-extension={e.provider}>
                <Ident value={e.provider} />
                <span className={styles.quiet}>{t("extensions.inPlay.at", { point: pointName(e.point) })}</span>
              </li>
            ))}
          </ul>
        </>
      )}
    </section>
  );
}

export function Point({ point: p, name }: { point: ExtensionPoint; name: string }) {
  const { t } = useI18n();
  const status = pointStatus(p);
  const known = POINTS[p.point];
  const headingId = `point-${p.point}`;
  return (
    <li className={styles.point} data-point={p.point} data-point-status={status} aria-labelledby={headingId}>
      <div className={styles.pointHead}>
        <h2 id={headingId} className={styles.pointTitle}>
          {name}
        </h2>
        <Ident value={p.interface} />
        <span className={styles.pointStatus}>
          <StatusOf name={status} pill />
        </span>
      </div>
      {status === "notRegistered" ? (
        <div className={styles.absent} data-absent={p.when_absent}>
          <p className={styles.text}>{known ? t(known.absent) : t("extensions.absent.unknown")}</p>
          <p className={styles.quiet}>
            {p.when_absent === "disabled" ? t("extensions.absent.disabled") : t("extensions.absent.core")}
          </p>
        </div>
      ) : (
        <>
          {status === "running" ? <p className={styles.text}>{t("extensions.running")}</p> : null}
          {status === "notStarted" ? <StartError kind={p.start_error ?? ""} /> : null}
          <ul className={styles.registrations}>
            {p.registrations.map((r) => (
              <li key={r.provider} className={styles.registration} data-registration={r.provider} data-sink={p.point === SINK_POINT ? r.provider : undefined}>
                <div className={styles.registrationHead}>
                  <Ident value={r.provider} />
                  {r.version ? <span className={styles.quiet}>{t("extensions.registration.version", { version: r.version })}</span> : null}
                  <Badge>{r.default ? t("extensions.registration.default") : t("extensions.registration.extension")}</Badge>
                </div>
                {p.point === SINK_POINT ? (
                  <FeedLagView lag={r.lag ?? null} label={t("extensions.sinkLag.label", { provider: r.provider })} />
                ) : null}
              </li>
            ))}
          </ul>
          {p.point === ARCHIVE_POINT ? <FeedLagView lag={p.lag ?? null} label={t("extensions.archiveLag.label")} /> : null}
        </>
      )}
    </li>
  );
}

/** Why a registered handler or sync is not running: the error's kind, in a sentence and as its code. */
function StartError({ kind }: { kind: string }) {
  const { t } = useI18n();
  const key = START_ERRORS[kind];
  return (
    <div className={styles.startError} data-start-error={kind}>
      <p className={styles.text}>{t("extensions.notStarted")}</p>
      <p className={styles.quiet}>
        <span>{key ? t(key) : t("extensions.startError.unknown")}</span> <Ident value={kind || "-"} />
      </p>
    </div>
  );
}

function ExtensionsSkeleton() {
  return (
    <div className={styles.stack}>
      <section className={styles.panel}>
        <Skeleton shape="title" />
        <Skeleton size="l" />
      </section>
      <ul className={styles.points}>
        {[0, 1, 2].map((i) => (
          <li key={i} className={styles.point}>
            <div className={styles.pointHead}>
              <Skeleton shape="title" />
              <Skeleton size="s" />
            </div>
            {i === 0 ? <FeedLagSkeleton /> : <Skeleton size="full" />}
          </li>
        ))}
      </ul>
    </div>
  );
}
