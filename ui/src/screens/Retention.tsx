// RETENTION IN THE AUDIT VIEW (0057; PLAN M8, M11, M26, §7):
// /console/audit/retention. What the archive holds, a retention pass, and the
// passes in the trail.
//
//   - The archive's lag (0025), with an archive registered: its row of
//     `GET …/extensions` (`lag`), through the lag component 0056 shipped, and
//     said plainly when it is caught up. A delivery fact — never an outage,
//     never tampering. With no archive registered none of it appears.
//   - A retention pass (`POST …/audit/retention`, `audit:retain`, the
//     administrator's alone): one stream's oldest records, by sequence or by
//     instant received, never the head. It deletes records, so it confirms by
//     typing the stream's name (DESIGN.md §2). With an archive registered a
//     pass never deletes what the archive has not confirmed: one by sequence
//     beyond it is refused whole — `409 retention_not_archived`, a refusal
//     that deleted nothing, naming the stream and how far the archive holds
//     it, which offers the pass by instant instead — and one by instant
//     deletes as far as the archive holds, and says it was held back.
//   - The passes in the trail: each `operator.audit.retained` record on the
//     control stream, with what it deleted and — with an archive registered —
//     whether the archive held it back (`operator.held_by_archive`).

import { useRef, useState, type FormEvent } from "react";
import { offered } from "../api/permit";
import type { AuditRecord, AuditRecordPage, Extensions, RetentionPass } from "../api/types";
import { Button } from "../components/Button";
import { DataTable, TableSkeleton, type Column } from "../components/DataTable";
import { ConfirmDialog, type ConfirmFailure } from "../components/Dialog";
import { FeedLagSkeleton, FeedLagView } from "../components/FeedLag";
import { Choices, Field, Input, Radio } from "../components/Field";
import { Ident } from "../components/Ident";
import { PageHeader } from "../components/PageHeader";
import { Pagination, type PageState } from "../components/Pagination";
import { EmptyState, ErrorMessage, ScreenFrame } from "../components/States";
import { StatusOf } from "../components/Status";
import { fromLocalInput, localInput } from "../i18n/format";
import { useI18n } from "../i18n/I18n";
import { Link } from "../router/router";
import { useLoad, type Load } from "../shell/load";
import { useDeployment } from "../shell/Shell";
import { Actor, AuditViews, Id, When } from "./Audit";
import { ARCHIVE_POINT } from "./extensionsModel";
import { RETAINED_EVENT, retainedOf } from "./gapModel";
import styles from "./Audit.module.css";

const FORBIDDEN: Load<never> = { state: "refused", status: 403, error: { code: "forbidden" } };
const TRAIL_LIMIT = 20;
/** This server's own stream: its operator records, retention's among them. */
export const CONTROL_STREAM = "control";

/** What the archive holds: its row, when one is registered; null when none is or it was not read. */
function useArchive(): { load: Load<Extensions> | null; row: Extensions["points"][number] | null } {
  const { tenant, session, api } = useDeployment();
  const permissions = (tenant && session.scopes[tenant]?.permissions) || [];
  const may = offered("extensions", permissions);
  const [load] = useLoad<Extensions>(tenant && may ? `${tenant}|extensions` : null, (signal) =>
    api.call("extensions", { params: { tenant: tenant as string }, signal }),
  );
  if (!may) return { load: null, row: null };
  const row = load.state === "ok" ? (load.data.points.find((p) => p.point === ARCHIVE_POINT && p.registered) ?? null) : null;
  return { load, row };
}

/** The archive's panel: its lag, or nothing at all when no archive is registered. */
function ArchivePanel({ archive }: { archive: ReturnType<typeof useArchive> }) {
  const { t } = useI18n();
  const { load, row } = archive;
  if (!load) return null;
  if (load.state === "loading") {
    return (
      <section className={styles.streamPanel} aria-busy="true" data-archive="loading">
        <FeedLagSkeleton />
      </section>
    );
  }
  if (load.state !== "ok") {
    return (
      <section className={styles.streamPanel} aria-labelledby="retention-archive" data-archive="unread">
        <h2 id="retention-archive" className={styles.drawerTitle}>
          {t("retention.archive.title")}
        </h2>
        <ErrorMessage error={load.error} kind={load.state} />
      </section>
    );
  }
  if (!row) return null;
  return (
    <section className={styles.streamPanel} aria-labelledby="retention-archive" data-archive="registered">
      <h2 id="retention-archive" className={styles.drawerTitle}>
        <span className={styles.what}>
          <span>{t("retention.archive.title")}</span>
          {row.registrations.map((r) => (
            <Ident key={r.provider} value={r.provider} />
          ))}
        </span>
      </h2>
      <FeedLagView lag={row.lag ?? null} label={t("retention.archive.lag")} />
      <p className={styles.quiet}>{t("retention.archive.rule")}</p>
    </section>
  );
}

type Mode = "sequence" | "instant";
type Outcome = { kind: "done"; pass: RetentionPass } | { kind: "notArchived"; failure: ConfirmFailure; stream: string };

/** A retention pass: the form, the typed-name confirmation, and what the server answered. */
function RetentionForm({ archived, onRan }: { archived: boolean; onRan(): void }) {
  const { t, f } = useI18n();
  const { tenant, api } = useDeployment();
  const [stream, setStream] = useState("");
  const [mode, setMode] = useState<Mode>("sequence");
  const [seq, setSeq] = useState("");
  const [before, setBefore] = useState("");
  const [touched, setTouched] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [outcome, setOutcome] = useState<Outcome | null>(null);
  const instantField = useRef<HTMLInputElement>(null);

  const name = stream.trim();
  const through = Number(seq);
  const seqOk = Number.isInteger(through) && through >= 1;
  const instant = before ? fromLocalInput(before) : undefined;
  const errors = {
    stream: name ? undefined : t("retention.field.stream.required"),
    seq: mode === "sequence" && !seqOk ? t("retention.field.seq.invalid") : undefined,
    before: mode === "instant" && !instant ? t("retention.field.before.invalid") : undefined,
  };
  const valid = !errors.stream && !errors.seq && !errors.before;

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (valid) setConfirming(true);
  };

  const run = async (): Promise<ConfirmFailure | null> => {
    const body = mode === "sequence" ? { stream: name, through_seq: through } : { stream: name, before: instant };
    const a = await api.call<RetentionPass>("auditRetention", { params: { tenant: tenant as string }, body });
    if (a.kind === "ok") {
      setOutcome({ kind: "done", pass: a.data });
      setConfirming(false);
      onRan();
      return null;
    }
    // A pass by sequence beyond the archive is refused whole: said beside the
    // form, with the pass by instant offered, rather than inside the dialog.
    if (a.kind === "refused" && a.error.code === "retention_not_archived") {
      setOutcome({ kind: "notArchived", failure: { error: a.error, kind: a.kind }, stream: name });
      setConfirming(false);
      return null;
    }
    return { error: a.error, kind: a.kind };
  };

  const byInstant = () => {
    setMode("instant");
    setBefore(localInput(new Date()));
    setOutcome(null);
    window.setTimeout(() => instantField.current?.focus(), 0);
  };

  return (
    <section className={styles.streamPanel} aria-labelledby="retention-pass" data-retention-form="">
      <h2 id="retention-pass" className={styles.drawerTitle}>
        {t("retention.pass.title")}
      </h2>
      <p className={styles.quiet}>{t("retention.pass.lede")}</p>
      <form className={styles.retentionForm} onSubmit={submit} noValidate>
        <Field label={t("retention.field.stream")} hint={t("retention.field.stream.hint", { example: "proxy:px-core-01", control: CONTROL_STREAM })} error={touched ? errors.stream : undefined}>
          <Input mono name="stream" value={stream} onChange={(e) => setStream(e.target.value)} autoComplete="off" spellCheck={false} />
        </Field>
        <Choices legend={t("retention.field.by")}>
          <Radio label={t("retention.by.sequence")} name="retention-by" value="sequence" checked={mode === "sequence"} onChange={() => setMode("sequence")} />
          <Radio label={t("retention.by.instant")} name="retention-by" value="instant" checked={mode === "instant"} onChange={() => setMode("instant")} />
        </Choices>
        {mode === "sequence" ? (
          <Field label={t("retention.field.seq")} hint={t("retention.field.seq.hint")} error={touched ? errors.seq : undefined}>
            <Input name="through_seq" inputMode="numeric" value={seq} onChange={(e) => setSeq(e.target.value)} />
          </Field>
        ) : (
          <Field label={t("retention.field.before")} hint={t("retention.field.before.hint")} error={touched ? errors.before : undefined}>
            {/* An instant the operator picks: masked in a screenshot, as a <time> is. */}
            <Input ref={instantField} name="before" type="datetime-local" step={1} value={before} onChange={(e) => setBefore(e.target.value)} data-volatile="" />
          </Field>
        )}
        <div className={styles.stepButtons}>
          <Button type="submit" variant="danger" data-retention-run="">
            {t("retention.run")}
          </Button>
        </div>
      </form>
      {outcome ? <PassOutcome outcome={outcome} archived={archived} onByInstant={byInstant} /> : null}
      <ConfirmDialog
        open={confirming}
        title={t("retention.confirm.title", { stream: name })}
        body={
          <>
            <p className={styles.para}>
              {mode === "sequence"
                ? t("retention.confirm.bySequence", { stream: name, seq: f.number(through || 0) })
                : t("retention.confirm.byInstant", { stream: name })}
            </p>
            {mode === "instant" && instant ? (
              <p className={styles.para}>
                <When at={instant} />
              </p>
            ) : null}
            <p className={styles.para}>{t("retention.confirm.chain")}</p>
            {archived ? <p className={styles.para}>{t("retention.confirm.archive")}</p> : null}
          </>
        }
        name={name}
        action={t("retention.confirm.action")}
        onConfirm={run}
        onClose={() => setConfirming(false)}
      />
    </section>
  );
}

/** What a pass did, or why it did nothing — beside the form, never only a toast (DESIGN.md §7). */
function PassOutcome({ outcome, archived, onByInstant }: { outcome: Outcome; archived: boolean; onByInstant(): void }) {
  const { t, f } = useI18n();
  if (outcome.kind === "notArchived") {
    return (
      <div className={styles.retentionResult} data-retention-result="refused" aria-live="polite">
        <h3 className={styles.drawerTitle}>{t("retention.result.refused")}</h3>
        <ErrorMessage error={outcome.failure.error} kind={outcome.failure.kind} />
        <p className={styles.quiet}>{t("retention.result.offer")}</p>
        <div className={styles.stepButtons}>
          <Button onClick={onByInstant} data-retention-by-instant="">
            {t("retention.result.byInstant")}
          </Button>
        </div>
      </div>
    );
  }
  const p = outcome.pass;
  const state = p.held_by_archive ? "held" : p.deleted > 0 ? "deleted" : "none";
  return (
    <div className={styles.retentionResult} data-retention-result={state} aria-live="polite">
      <h3 className={styles.drawerTitle}>
        <span className={styles.what}>
          {p.held_by_archive ? <StatusOf name="behind" /> : null}
          <span>{p.held_by_archive ? t("retention.result.held") : t("retention.result.done")}</span>
        </span>
      </h3>
      <p className={styles.para}>
        {p.deleted > 0
          ? t("retention.result.deleted", { count: p.deleted, stream: p.stream, seq: f.number(p.through_seq) })
          : t("retention.result.nothing", { stream: p.stream })}
      </p>
      {p.held_by_archive && p.archived_through_seq !== undefined ? (
        <p className={styles.para} data-archived-through={p.archived_through_seq}>
          {t("retention.result.heldBody", { stream: p.stream, seq: f.number(p.archived_through_seq) })}
        </p>
      ) : null}
      {archived && !p.held_by_archive && p.archived_through_seq !== undefined ? (
        <p className={styles.quiet}>{t("retention.result.archiveHolds", { seq: f.number(p.archived_through_seq) })}</p>
      ) : null}
      {p.deleted > 0 ? (
        <p className={styles.quiet}>
          <span>{t("retention.result.recorded")}</span> {p.key ? <Id>{p.key}</Id> : null}
        </p>
      ) : null}
    </div>
  );
}

/** A retention pass's record, in the record drawer: what it deleted, and whether the archive held it back. */
export function RetainedFactsOf({ record }: { record: AuditRecord }) {
  const { t, f } = useI18n();
  const r = retainedOf(record);
  return (
    <section aria-labelledby="audit-record-retained" className={styles.drawerSection} data-retained-facts="">
      <h3 id="audit-record-retained" className={styles.drawerTitle}>
        {t("retention.record.title")}
      </h3>
      <dl className={styles.fields}>
        {r.stream ? (
          <>
            <dt>{t("retention.col.stream")}</dt>
            <dd>
              <Id>{r.stream}</Id>
            </dd>
          </>
        ) : null}
        {r.through !== undefined ? (
          <>
            <dt>{t("retention.col.through")}</dt>
            <dd>{f.number(r.through)}</dd>
          </>
        ) : null}
        {r.deleted !== undefined ? (
          <>
            <dt>{t("retention.col.deleted")}</dt>
            <dd>{f.number(r.deleted)}</dd>
          </>
        ) : null}
        {r.heldByArchive !== undefined ? (
          <>
            <dt>{t("retention.col.archive")}</dt>
            <dd>
              <ArchiveCell facts={r} />
            </dd>
          </>
        ) : null}
      </dl>
    </section>
  );
}

function ArchiveCell({ facts: r }: { facts: ReturnType<typeof retainedOf> }) {
  const { t, f } = useI18n();
  if (r.heldByArchive === undefined) return null;
  return r.heldByArchive ? (
    <span className={styles.what} data-held-by-archive="true">
      <StatusOf name="behind" />
      <span>
        {r.archivedThrough !== undefined
          ? t("retention.trail.held", { seq: f.number(r.archivedThrough) })
          : t("retention.trail.heldNoSeq")}
      </span>
    </span>
  ) : (
    <span data-held-by-archive="false">{t("retention.trail.notHeld")}</span>
  );
}

export function AuditRetentionScreen() {
  const { t, f } = useI18n();
  const { tenant, session, api } = useDeployment();
  const permissions = (tenant && session.scopes[tenant]?.permissions) || [];
  const archive = useArchive();
  const [trail, setTrail] = useState<string[]>([]);
  const page: PageState = { trail, limit: TRAIL_LIMIT };
  const may = offered("auditRecords", permissions);
  const query = new URLSearchParams({ event: RETAINED_EVENT, limit: String(TRAIL_LIMIT) });
  const after = trail[trail.length - 1];
  if (after) query.set("after", after);
  const key = tenant && may ? `${tenant}|${query.toString()}` : null;
  const [load, refresh] = useLoad<AuditRecordPage>(key, (signal) => api.call("auditRecords", { params: { tenant: tenant as string }, query, signal }));
  const shown = may ? load : FORBIDDEN;
  const mayRun = offered("auditRetention", permissions);
  const unread = archive.load && (archive.load.state === "outage" || archive.load.state === "refused");

  const headers = [t("audit.col.recorded"), t("audit.col.actor"), t("retention.col.stream"), t("retention.col.through"), t("retention.col.deleted"), t("retention.col.archive")];
  const columns = (rows: readonly AuditRecord[]): Column<AuditRecord>[] => {
    const cols: Column<AuditRecord>[] = [
      {
        id: "recorded",
        header: headers[0] as string,
        sortValue: (r) => r.recorded_at,
        cell: (r) => (
          <Link to="audit/records" query={{ record: r.record_id }} className={styles.link}>
            <When at={r.recorded_at} />
          </Link>
        ),
      },
      { id: "actor", header: headers[1] as string, cell: (r) => <Actor record={r} /> },
      { id: "stream", header: headers[2] as string, sortValue: (r) => retainedOf(r).stream ?? "", cell: (r) => (retainedOf(r).stream ? <Id>{retainedOf(r).stream as string}</Id> : null) },
      { id: "through", header: headers[3] as string, sortValue: (r) => retainedOf(r).through ?? 0, cell: (r) => (retainedOf(r).through !== undefined ? f.number(retainedOf(r).through as number) : null) },
      { id: "deleted", header: headers[4] as string, sortValue: (r) => retainedOf(r).deleted ?? 0, cell: (r) => (retainedOf(r).deleted !== undefined ? f.number(retainedOf(r).deleted as number) : null) },
    ];
    // Whether the archive held a pass back is said only where an archive was registered when it ran.
    if (rows.some((r) => retainedOf(r).heldByArchive !== undefined)) {
      cols.push({ id: "archive", header: headers[5] as string, cell: (r) => <ArchiveCell facts={retainedOf(r)} /> });
    }
    return cols;
  };

  return (
    <div className={styles.screen}>
      <PageHeader title={t("audit.title")} lede={t("retention.lede")} actions={<Button onClick={refresh}>{t("audit.refresh")}</Button>} />
      <AuditViews current="retention" />
      <ArchivePanel archive={archive} />
      {mayRun ? (
        <RetentionForm
          archived={archive.row !== null}
          onRan={() => {
            setTrail([]);
            refresh();
          }}
        />
      ) : (
        <p className={styles.quiet} data-retention-readonly="">
          {t("retention.readonly", { permission: "audit:retain" })}
        </p>
      )}
      <section className={styles.drawerSection} aria-labelledby="retention-trail">
        <h2 id="retention-trail" className={styles.drawerTitle}>
          {t("retention.trail.title")}
        </h2>
        <ScreenFrame
          load={shown}
          onRetry={refresh}
          isEmpty={(d) => d.records.length === 0 && trail.length === 0}
          gaps={unread ? [t("retention.partial.archive")] : []}
          loading={<TableSkeleton headers={headers.slice(0, 5)} />}
          empty={<EmptyState title={t("retention.trail.empty.title")} body={t("retention.trail.empty.body", { event: RETAINED_EVENT })} />}
        >
          {(data) => (
            <>
              <DataTable
                caption={t("retention.trail.caption")}
                columns={columns(data.records)}
                rows={data.records}
                rowKey={(r) => r.record_id}
                rowLabel={(r) => r.record_id}
                partialNote={data.truncated ? t("audit.onePage") : undefined}
              />
              <Pagination page={page} shown={data.records.length} truncated={data.truncated} next={data.next} onPage={(p) => setTrail([...p.trail])} />
            </>
          )}
        </ScreenFrame>
      </section>
    </div>
  );
}
