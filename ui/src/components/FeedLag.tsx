// THE LAG OF A DOWNSTREAM CONSUMER (DESIGN.md §7; 0056, and 0057's archive).
//
// How far one consumer of the audit store is behind in the caller's tenant —
// the registered archive (0025), or a sink (0026) — from the scalars the
// extension listing carries under one set of names: streams_behind,
// oldest_unconfirmed_at, failing, and the archive's captures_waiting or a
// sink's skipped_by_retention. One component renders both, so the archive's
// lag and a sink's read alike.
//
// Three readings, and the plan decides each (M8, M11):
//   - caught up is said plainly: nothing in this tenant is unconfirmed;
//   - behind is delivery in progress, information and not a fault;
//   - failing is degraded — delivery is retried by itself, and the local
//     store is still the record — and never an outage or a deny.
// A retention pass that overtook a sink is a count with its explanation —
// the pass's own operator.audit.retained record — and never tampering.
// A lag the server did not send is Not reported (null), never caught up.

import type { FeedLag } from "../api/types";
import { useI18n } from "../i18n/I18n";
import { Link } from "../router/router";
import { Ident } from "./Ident";
import { Skeleton } from "./States";
import { StatusOf, type StatusName } from "./Status";
import styles from "./FeedLag.module.css";

/** The record kind a retention pass writes on the control stream (M8). */
export const RETAINED_KIND = "operator.audit.retained";

/** Which reading a lag is: failing before behind before caught up. */
export function lagStatus(lag: FeedLag): "failing" | "behind" | "caughtUp" {
  if (lag.failing) return "failing";
  if (lag.streams_behind > 0) return "behind";
  return "caughtUp";
}

/**
 * A consumer's lag. `label` names the consumer, for a reader moving between
 * several on one page. `lag` null is a lag the server did not report.
 */
export function FeedLagView({ lag, label }: { lag: FeedLag | null; label: string }) {
  const { t, f } = useI18n();
  if (lag === null) {
    return (
      <section className={styles.lag} data-lag-state="notReported" aria-label={label}>
        <div className={styles.head}>
          <StatusOf name="notReported" pill />
          <p className={styles.summary}>{t("lag.notReported")}</p>
        </div>
      </section>
    );
  }
  const status: StatusName = lagStatus(lag);
  const skipped = lag.skipped_by_retention ?? 0;
  return (
    <section className={styles.lag} data-lag-state={status} data-skipped={skipped > 0 ? skipped : undefined} aria-label={label}>
      <div className={styles.head}>
        <StatusOf name={status} pill />
        <p className={styles.summary}>
          {status === "failing"
            ? t("lag.failing")
            : status === "behind"
              ? t("lag.behind", { count: lag.streams_behind })
              : t("lag.caughtUp")}
        </p>
      </div>
      <dl className={styles.facts}>
        <div className={styles.fact}>
          <dt>{t("lag.streamsBehind")}</dt>
          <dd>{f.number(lag.streams_behind)}</dd>
        </div>
        <div className={styles.fact}>
          <dt>{t("lag.oldest")}</dt>
          <dd>
            {lag.oldest_unconfirmed_at ? (
              <time dateTime={f.utc(lag.oldest_unconfirmed_at)} title={f.utc(lag.oldest_unconfirmed_at)}>
                {f.dateTime(lag.oldest_unconfirmed_at)}
              </time>
            ) : (
              t("lag.oldest.none")
            )}
          </dd>
        </div>
        {lag.captures_waiting !== undefined ? (
          <div className={styles.fact}>
            <dt>{t("lag.capturesWaiting")}</dt>
            <dd>{f.number(lag.captures_waiting)}</dd>
          </div>
        ) : null}
        {lag.skipped_by_retention !== undefined ? (
          <div className={styles.fact} data-fact="skipped">
            <dt>{t("lag.skipped")}</dt>
            <dd>{f.number(lag.skipped_by_retention)}</dd>
          </div>
        ) : null}
      </dl>
      {skipped > 0 ? (
        <p className={styles.gap} data-retention-gap="">
          <span>{t("lag.skipped.explain", { count: skipped })}</span>{" "}
          <Link to="audit/records" query={{ kind: RETAINED_KIND }} className={styles.link}>
            {t("lag.skipped.link")}
          </Link>{" "}
          <Ident value={RETAINED_KIND} />
        </p>
      ) : null}
    </section>
  );
}

/** A lag being read: the same frame, so nothing shifts when it arrives. */
export function FeedLagSkeleton() {
  return (
    <section className={styles.lag} data-lag-state="loading" aria-hidden="true">
      <div className={styles.head}>
        <Skeleton size="s" />
        <Skeleton size="l" />
      </div>
      <div className={styles.skeletonFacts}>
        <Skeleton size="m" />
        <Skeleton size="m" />
      </div>
    </section>
  );
}
