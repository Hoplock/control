// STREAM GAPS (0057; PLAN M8, M11, M26, §7): where a proxy's stream has a
// hole, and why — `logging.gap`, a proxy's own report of records this server
// does not hold (`Hoplock/proxy#71`, stored and served by 0019).
//
// The pieces every view of a gap uses, so a gap reads the same in a session's
// replay, in the record drawer and on the per-proxy view:
//   - its status is its cause: `Evicted` (never received — outage-class, M11)
//     or `Refused at ingest` (received and refused here — degraded, neither a
//     deny nor an outage; DESIGN.md §6);
//   - its place is its span, `gap_first_at` to `gap_last_at`, never the
//     report's own timestamp;
//   - a gap that hid something critical is marked critical;
//   - a refused gap shows this server's own answer and the refused record ids.
// A gap is never tampering, and nothing here says "nothing missing": the chain
// says nothing about a record that never arrived (PLAN §7).
//
// The per-proxy view, /console/audit/gaps?proxy=…&cause=…&scope=sessionless,
// is one page of the reports (`GET …/audit/gaps`), each proxy's stream a
// timeline placed by span. It is where a gap that belongs to no session is
// shown: it has no session to be shown in.

import { useState, type ReactNode } from "react";
import { offered } from "../api/permit";
import type { Gap, GapPage } from "../api/types";
import { Button } from "../components/Button";
import { FilterBar, type FilterField } from "../components/FilterBar";
import { PageHeader } from "../components/PageHeader";
import { Pagination, type PageState } from "../components/Pagination";
import { EmptyState, ScreenFrame } from "../components/States";
import { STATUS, StatusOf } from "../components/Status";
import { Timeline, TimelineSkeleton, type TimelineEvent } from "../components/Timeline";
import { useI18n } from "../i18n/I18n";
import type { Translate } from "../i18n/messages";
import { Link, navigate, useLocation } from "../router/router";
import { useLoad, type Load } from "../shell/load";
import { useDeployment } from "../shell/Shell";
import { AuditViews, Id, When } from "./Audit";
import { useKindLabel, useSeverityLabel } from "./auditLabels";
import { readLimit } from "./auditModel";
import { GAP_CAUSES, gapCritical, gapStatus, gapsByStream } from "./gapModel";
import styles from "./Audit.module.css";

const FORBIDDEN: Load<never> = { state: "refused", status: 403, error: { code: "forbidden" } };

/** A gap's status: its cause, from the vocabulary. */
export function GapStatus({ gap, pill = false }: { gap: Gap; pill?: boolean }) {
  return <StatusOf name={gapStatus(gap.gap_cause)} pill={pill} />;
}

/** The critical marker: set like a critical severity, and said in words. */
export function GapCritical({ gap }: { gap: Gap }) {
  const { t } = useI18n();
  const severityLabel = useSeverityLabel();
  if (!gapCritical(gap)) return null;
  return (
    <span className={styles.what} data-gap-critical="">
      <span className={styles.severity} data-severity="critical">
        {severityLabel("critical")}
      </span>
      {gap.gap_critical ? <span>{t("gaps.critical.count", { count: gap.gap_critical })}</span> : null}
    </span>
  );
}

/** How many records a gap is missing, in words; a count the proxy did not send is said to be unreported. */
function missing(t: Translate, g: Gap): string {
  return g.gap_records === undefined ? t("gaps.missing.unknown") : t("gaps.missing", { count: g.gap_records });
}

/** A gap's span: from its first missing record to its last. */
export function GapSpan({ gap }: { gap: Gap }) {
  const { t } = useI18n();
  if (!gap.gap_first_at) return <span className={styles.quiet}>{t("gaps.span.none")}</span>;
  return (
    <span className={styles.what} data-gap-span="">
      <When at={gap.gap_first_at} />
      <span aria-hidden="true">–</span>
      <span className={styles.hidden}>{t("gaps.span.to")}</span>
      <When at={gap.gap_last_at ?? gap.gap_first_at} />
    </span>
  );
}

/**
 * A gap as a timeline event, at the start of its span: its cause as the
 * marker, how many records are missing as the title, and beneath it the span's
 * end, the critical marker and, for a refusal, this server's answer's code.
 */
export function gapEvent(t: Translate, g: Gap, extra?: ReactNode): TimelineEvent {
  const s = STATUS[gapStatus(g.gap_cause)];
  return {
    id: g.record_id,
    at: g.gap_first_at ?? g.reported_at,
    title: (
      <span data-gap={g.record_id} data-gap-cause={g.gap_cause} data-gap-status={gapStatus(g.gap_cause)}>
        {missing(t, g)}
      </span>
    ),
    status: { tone: s.tone, label: t(s.label) },
    meta: (
      <>
        {g.gap_last_at && g.gap_last_at !== g.gap_first_at ? (
          <span className={styles.what}>
            <span>{t("gaps.span.until")}</span>
            <When at={g.gap_last_at} />
          </span>
        ) : null}
        <GapCritical gap={g} />
        {g.refusal_code ? <Id>{g.refusal_code}</Id> : null}
        {extra}
      </>
    ),
  };
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt>{label}</dt>
      <dd>{children}</dd>
    </>
  );
}

/**
 * Everything a gap report says, as facts: why (its cause, in a sentence), when
 * (its span), how much, of which kinds, whether something critical is
 * missing, and — for a refusal — this server's own answer and the record ids
 * it refused.
 */
export function GapFacts({ gap: g, session = true }: { gap: Gap; session?: boolean }) {
  const { t, f } = useI18n();
  const kindLabel = useKindLabel();
  const status = gapStatus(g.gap_cause);
  return (
    <div className={styles.drawerSection} data-gap-facts={g.record_id} data-gap-status={status}>
      <div className={styles.what}>
        <GapStatus gap={g} pill />
        <GapCritical gap={g} />
      </div>
      <p className={styles.gapWhy}>
        {status === "evicted"
          ? t("gaps.why.evicted")
          : status === "refusedAtIngest"
            ? t("gaps.why.refused")
            : t("gaps.why.unknown", { cause: g.gap_cause || "—" })}
      </p>
      <dl className={styles.fields}>
        <Fact label={t("gaps.fact.span")}>
          <GapSpan gap={g} />
        </Fact>
        <Fact label={t("gaps.fact.missing")}>
          <span>{missing(t, g)}</span>
          {g.gap_bytes !== undefined ? <span className={styles.quiet}>{t("gaps.bytes", { n: f.number(g.gap_bytes) })}</span> : null}
        </Fact>
        {g.gap_kinds?.length ? (
          <Fact label={t("gaps.fact.kinds")}>
            {g.gap_kinds.map((k) => (
              <span key={k} className={styles.what}>
                <span>{kindLabel(k)}</span>
                <Id>{k}</Id>
              </span>
            ))}
          </Fact>
        ) : null}
        {g.refusal_code || g.refusal_message ? (
          <Fact label={t("gaps.fact.answer")}>
            <span className={styles.refusal} data-gap-answer="">
              {g.refusal_code ? <Id>{g.refusal_code}</Id> : null}
              {g.refusal_message ? <span translate="no">{g.refusal_message}</span> : null}
            </span>
          </Fact>
        ) : null}
        {g.gap_record_ids?.length ? (
          <Fact label={t("gaps.fact.refused")}>
            {g.gap_record_ids.map((id) => (
              <Id key={id}>{id}</Id>
            ))}
            {g.gap_record_ids_truncated ? <span className={styles.quiet}>{t("gaps.refused.more")}</span> : null}
          </Fact>
        ) : null}
        {session ? (
          <Fact label={t("audit.col.session")}>
            {g.session_id ? (
              <Link to={`audit/sessions/${encodeURIComponent(g.session_id)}/replay`} className={styles.link} data-gap-session="">
                <Id>{g.session_id}</Id>
              </Link>
            ) : (
              <span className={styles.quiet}>{t("gaps.session.none")}</span>
            )}
          </Fact>
        ) : null}
        <Fact label={t("gaps.fact.stream")}>
          <Id>{g.stream}</Id>
        </Fact>
        <Fact label={t("gaps.fact.reported")}>
          <When at={g.reported_at} />
          <Link to="audit/records" query={{ record: g.record_id }} className={styles.link}>
            <Id>{g.record_id}</Id>
          </Link>
        </Fact>
      </dl>
    </div>
  );
}

const SCOPE_SESSIONLESS = "sessionless";

/** The per-proxy view's filters, from its URL: each the gap query's own parameter but `scope`. */
export function readGapFilters(search: URLSearchParams): { proxy?: string; cause?: string; scope?: string } {
  const out: { proxy?: string; cause?: string; scope?: string } = {};
  const proxy = search.get("proxy")?.trim();
  if (proxy) out.proxy = proxy;
  const cause = search.get("cause")?.trim();
  if (cause && (GAP_CAUSES as readonly string[]).includes(cause)) out.cause = cause;
  if (search.get("scope") === SCOPE_SESSIONLESS) out.scope = SCOPE_SESSIONLESS;
  return out;
}

export function AuditGapsScreen() {
  const { t } = useI18n();
  const { tenant, session, api } = useDeployment();
  const location = useLocation();
  const filters = readGapFilters(location.search);
  const limit = readLimit(location.search);
  const selection = JSON.stringify([filters, limit]);
  const [state, setState] = useState({ selection, trail: [] as string[] });
  let trail = state.trail;
  if (state.selection !== selection) {
    trail = [];
    setState({ selection, trail });
  }
  const page: PageState = { trail, limit };
  const permissions = (tenant && session.scopes[tenant]?.permissions) || [];
  const may = offered("auditGaps", permissions);
  const query = new URLSearchParams({ limit: String(limit) });
  if (filters.proxy) query.set("proxy", filters.proxy);
  if (filters.cause) query.set("cause", filters.cause);
  if (filters.scope) query.set("no_session", "true");
  const after = trail[trail.length - 1];
  if (after) query.set("after", after);
  const key = tenant && may ? `${tenant}|${query.toString()}` : null;
  const [load, refresh] = useLoad<GapPage>(key, (signal) => api.call("auditGaps", { params: { tenant: tenant as string }, query, signal }));
  const shown = may ? load : FORBIDDEN;
  const filtered = Object.keys(filters).length > 0;

  const fields: FilterField[] = [
    { id: "proxy", label: t("gaps.filter.proxy"), kind: "text", mono: true },
    { id: "cause", label: t("gaps.filter.cause"), options: GAP_CAUSES.map((c) => ({ value: c, label: t(STATUS[gapStatus(c)].label) })) },
    { id: "scope", label: t("gaps.filter.scope"), options: [{ value: SCOPE_SESSIONLESS, label: t("gaps.scope.sessionless") }] },
  ];

  return (
    <div className={styles.screen}>
      <PageHeader title={t("audit.title")} lede={t("gaps.lede")} actions={<Button onClick={refresh}>{t("audit.refresh")}</Button>} />
      <AuditViews current="gaps" />
      <FilterBar
        label={t("audit.filters")}
        fields={fields}
        values={filters as Record<string, string>}
        onChange={(v) => {
          const q: Record<string, string> = {};
          for (const [k, val] of location.search) q[k] = val;
          for (const fl of fields) {
            const val = v[fl.id];
            if (val) q[fl.id] = val;
            else delete q[fl.id];
          }
          navigate(location.path, { replace: true, query: q });
        }}
      />
      <ScreenFrame
        load={shown}
        onRetry={refresh}
        isEmpty={(d) => d.gaps.length === 0 && trail.length === 0}
        gaps={shown.state === "ok" && shown.data.truncated ? [t("gaps.onePage")] : []}
        loading={<TimelineSkeleton rows={4} />}
        empty={
          filtered ? (
            <EmptyState
              title={t("gaps.filteredEmpty.title")}
              body={t("gaps.filteredEmpty.body")}
              action={
                <Link to="audit/gaps" className={styles.link}>
                  {t("audit.records.clear")}
                </Link>
              }
            />
          ) : (
            <EmptyState title={t("gaps.empty.title")} body={t("gaps.empty.body")} />
          )
        }
      >
        {(data) => (
          <>
            <p className={styles.quiet} role="status">
              {t("gaps.count", { count: data.gaps.length })}
            </p>
            {gapsByStream(data.gaps).map((s) => (
              <section key={s.stream} className={styles.streamPanel} aria-labelledby={`stream-${s.stream}`} data-stream={s.stream}>
                <h2 id={`stream-${s.stream}`} className={styles.drawerTitle}>
                  <span className={styles.what}>
                    <span>{t("gaps.stream.title")}</span>
                    <Id>{s.proxy ?? s.stream}</Id>
                  </span>
                </h2>
                <Timeline
                  label={t("gaps.stream.timeline", { stream: s.stream })}
                  events={s.placed.map((g) =>
                    gapEvent(
                      t,
                      g,
                      g.session_id ? (
                        <Link to={`audit/sessions/${encodeURIComponent(g.session_id)}/replay`} className={styles.link} data-gap-session="">
                          <Id>{g.session_id}</Id>
                        </Link>
                      ) : (
                        <span className={styles.quiet} data-gap-sessionless="">
                          {t("gaps.session.none")}
                        </span>
                      ),
                    ),
                  )}
                  empty={<p className={styles.quiet}>{t("gaps.stream.allUnplaced")}</p>}
                />
                {s.unplaced.length ? <Unplaced gaps={s.unplaced} /> : null}
                <details className={styles.gapDetails}>
                  <summary>{t("gaps.stream.details", { count: s.placed.length + s.unplaced.length })}</summary>
                  {[...s.placed, ...s.unplaced].map((g) => (
                    <GapFacts key={g.record_id} gap={g} />
                  ))}
                </details>
              </section>
            ))}
            <Pagination page={page} shown={data.gaps.length} truncated={data.truncated} next={data.next} onPage={(p) => setState({ selection, trail: [...p.trail] })} />
          </>
        )}
      </ScreenFrame>
    </div>
  );
}

/** Reports with no span: listed, never placed at a time they do not have. */
export function Unplaced({ gaps }: { gaps: readonly Gap[] }) {
  const { t } = useI18n();
  return (
    <div className={styles.drawerSection} data-gaps-unplaced="">
      <p className={styles.quiet}>{t("gaps.unplaced", { count: gaps.length })}</p>
      <ul className={styles.unplaced}>
        {gaps.map((g) => (
          <li key={g.record_id} className={styles.what}>
            <GapStatus gap={g} />
            <span>{missing(t, g)}</span>
            <span className={styles.quiet}>{t("gaps.fact.reported")}</span>
            <When at={g.reported_at} />
          </li>
        ))}
      </ul>
    </div>
  );
}
