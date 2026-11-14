// SESSION REPLAY (0051; PLAN M8, M26, §7): a recorded session, stepped
// through in the order it happened. /console/audit/sessions/<id>/replay.
//
// It reads the session's records oldest first, one bounded page at a time
// (`GET …/audit/records?session=…&order=asc`), as a timeline the operator
// steps along — by keyboard, by the step buttons, or played — and, for every
// `stream` record up to the step, that record's capture
// (`GET …/audit/records/{record}/capture`, north-bound version 3), so the
// terminal pane shows what the session's terminal showed at that point. The
// bytes are the proxy's; the pane is a reading of them (auditModel's
// terminalText), and the record's digest is beside each chunk.
//
// A capture a retention pass removed, or one that could not be read, is a
// named gap in the pane and in the partial notice: never spliced over, never
// shown as an empty terminal, and an unreadable one is an outage (M11).
//
// Where the proxy's stream has a hole (0057, `Hoplock/proxy#71`), the
// session's `logging.gap` reports (`GET …/audit/gaps?session=…`) are steps of
// their own, each at the start of its SPAN and never at its report's
// timestamp, which is often after the session ended; the report's own record
// is not a step. Its cause is its status: `Evicted` (never received — an
// outage, M11) or `Refused at ingest` (received and refused here, with this
// server's answer — neither a deny nor an outage). The terminal shows the
// hole where it falls and never joins the capture on either side of it. A
// session with a hole is partial, and its loss is the SUM of its reports per
// cause (`GET …/audit/sessions/{session}/loss`), never the latest report. A
// session with no report is never called complete: the chain says nothing
// about a record that never arrived (PLAN §7).

import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { offered } from "../api/permit";
import type { AuditRecord, AuditRecordPage, Capture, Gap, GapPage, SessionLoss } from "../api/types";
import { Button } from "../components/Button";
import { CopyableId } from "../components/CopyableId";
import { PageHeader } from "../components/PageHeader";
import { Pagination } from "../components/Pagination";
import { EmptyState, ScreenFrame, Skeleton } from "../components/States";
import { Status } from "../components/Status";
import { Timeline, TimelineSkeleton } from "../components/Timeline";
import { useI18n } from "../i18n/I18n";
import { Link, navigate, useLocation } from "../router/router";
import { fromAnswer, useLoad, type Load } from "../shell/load";
import { useDeployment } from "../shell/Shell";
import { AuditViews, Id } from "./Audit";
import { useKindLabel } from "./auditLabels";
import { captureBytes, readLimit, recordsQuery, terminalText } from "./auditModel";
import { GapFacts, GapStatus, gapEvent, Unplaced } from "./Gaps";
import { lossByCause, placeGaps, type Entry } from "./gapModel";
import styles from "./Audit.module.css";

const FORBIDDEN: Load<never> = { state: "refused", status: 403, error: { code: "forbidden" } };
/** A session's page of records: larger than a list's, because a replay reads it whole. */
const REPLAY_LIMIT = 500;
/** How long a played step stays on screen. */
const PLAY_MS = 700;
/** A session's gap reports, read in one page: one per cause and loss, so a handful. */
const GAPS_LIMIT = 500;
const NO_GAPS: readonly Gap[] = [];
const NO_RECORDS: readonly AuditRecord[] = [];

export function ReplayScreen({ session: sessionID }: { session: string }) {
  const { t } = useI18n();
  const { tenant, session, api } = useDeployment();
  const location = useLocation();
  const limit = readLimit(location.search, REPLAY_LIMIT);
  const [trail, setTrail] = useState<string[]>([]);
  const permissions = (tenant && session.scopes[tenant]?.permissions) || [];
  const may = offered("auditRecords", permissions) && offered("auditCapture", permissions);
  const after = trail[trail.length - 1];
  const query = recordsQuery({ session: sessionID }, {}, { limit, after, ascending: true });
  const key = tenant && may ? `${tenant}|${query.toString()}` : null;
  const [load, refresh] = useLoad<AuditRecordPage>(key, (signal) =>
    api.call("auditRecords", { params: { tenant: tenant as string }, query, signal }),
  );
  const shown = may ? load : FORBIDDEN;
  const holes = useHoles(sessionID);
  const records = shown.state === "ok" ? shown.data.records : NO_RECORDS;
  // The page's window: a gap is placed on the page its span starts in.
  const from = trail.length && records.length ? (records[0] as AuditRecord).recorded_at : undefined;
  const to = shown.state === "ok" && shown.data.truncated && records.length ? (records[records.length - 1] as AuditRecord).recorded_at : undefined;
  const reports = holes.gaps.state === "ok" ? holes.gaps.data.gaps : NO_GAPS;
  // One order for the records and the holes, kept while neither answer changes.
  const { entries, unplaced } = useMemo(() => placeGaps(records, reports, { from, to }), [records, reports, from, to]);
  // A replay opens on its last step, the whole session shown; ?step= names another.
  const step = location.search.get("step");
  const found = entries.findIndex((e) => e.id === step);
  const at = found >= 0 ? found : Math.max(0, entries.length - 1);
  const captures = useCaptures(streamsOf(entries.slice(0, at + 1)));
  const missingCaptures = Array.from(captures.values()).filter((c) => c.state !== "ok" || c.data.retained).length;
  const partial = [...(missingCaptures ? [t("replay.partial", { count: missingCaptures })] : []), ...holes.lines];

  return (
    <div className={styles.screen}>
      <PageHeader
        title={t("replay.title")}
        lede={t("replay.lede")}
        id={<CopyableId value={sessionID} />}
        actions={
          <Link to={`explain/sessions/${encodeURIComponent(sessionID)}`} className={styles.link}>
            {t("replay.explain")}
          </Link>
        }
      />
      <AuditViews current="replay" />
      <ScreenFrame
        load={shown}
        onRetry={refresh}
        isEmpty={(d) => d.records.length === 0 && trail.length === 0}
        gaps={partial}
        loading={
          <div className={styles.replay}>
            <div className={styles.replayRail}>
              <TimelineSkeleton rows={5} />
            </div>
            <Skeleton shape="block" size="full" />
          </div>
        }
        empty={<EmptyState title={t("replay.empty.title")} body={t("replay.empty.body")} />}
      >
        {(data) => (
          <Stepper
            entries={entries}
            unplaced={unplaced}
            reported={holes.reported}
            at={at}
            captures={captures}
            onStep={(id) => navigate(location.path, { replace: true, query: { step: id } })}
            pager={
              data.truncated || trail.length ? (
                <Pagination
                  page={{ trail, limit }}
                  shown={data.records.length}
                  truncated={data.truncated}
                  next={data.next}
                  onPage={(p) => setTrail([...p.trail])}
                />
              ) : null
            }
          />
        )}
      </ScreenFrame>
    </div>
  );
}

/** The stream records of these steps: the ones that carry a capture. */
function streamsOf(entries: readonly Entry[]): AuditRecord[] {
  return entries.flatMap((e) => (e.kind === "record" && e.record.kind === "stream" ? [e.record] : []));
}

/**
 * The session's holes: its gap reports, and its loss summed per cause. Each
 * is its own read, and one that fails is a named gap of the partial notice —
 * never a session shown as whole. `reported` is false only when both answered
 * and the proxy reported nothing missing.
 */
function useHoles(sessionID: string): { gaps: Load<GapPage>; lines: string[]; reported: boolean | null } {
  const { t } = useI18n();
  const { tenant, session, api } = useDeployment();
  const permissions = (tenant && session.scopes[tenant]?.permissions) || [];
  const mayGaps = offered("auditGaps", permissions);
  const mayLoss = offered("auditSessionLoss", permissions);
  const gq = new URLSearchParams({ session: sessionID, order: "asc", limit: String(GAPS_LIMIT) });
  const [gaps] = useLoad<GapPage>(tenant && mayGaps ? `${tenant}|gaps|${gq.toString()}` : null, (signal) =>
    api.call("auditGaps", { params: { tenant: tenant as string }, query: gq, signal }),
  );
  const [loss] = useLoad<SessionLoss>(tenant && mayLoss ? `${tenant}|loss|${sessionID}` : null, (signal) =>
    api.call("auditSessionLoss", { params: { tenant: tenant as string, session: sessionID }, signal }),
  );
  const shownGaps = mayGaps ? gaps : FORBIDDEN;
  const lines: string[] = [];
  if (!mayLoss || loss.state === "refused" || loss.state === "outage") lines.push(t("replay.loss.unread"));
  else if (loss.state === "ok") {
    const all = shownGaps.state === "ok" && !shownGaps.data.truncated ? shownGaps.data.gaps : null;
    for (const l of lossByCause(loss.data.loss, all)) {
      const args = { records: l.records, reports: l.reports };
      const line =
        l.cause === "evicted"
          ? t("replay.loss.evicted", args)
          : l.cause === "refused"
            ? t("replay.loss.refused", args)
            : t("replay.loss.other", { ...args, cause: l.cause });
      lines.push(l.critical ? `${line} ${t("replay.loss.critical", { count: l.critical })}` : line);
    }
  }
  if (shownGaps.state === "refused" || shownGaps.state === "outage") lines.push(t("replay.gaps.unread"));
  const reported =
    loss.state === "ok" && shownGaps.state === "ok" ? loss.data.loss.length > 0 || shownGaps.data.gaps.length > 0 : null;
  return { gaps: shownGaps, lines, reported };
}

/**
 * The captures of these records, each read once, in order, and kept: a step
 * back reads nothing again. Each answer is its own Load, so one that fails
 * names its own gap and stops nothing else.
 */
function useCaptures(records: readonly AuditRecord[]): Map<string, Load<Capture>> {
  const { tenant, api } = useDeployment();
  const [cache, setCache] = useState<Map<string, Load<Capture>>>(new Map());
  const asked = useRef(new Set<string>());
  const want = records.map((r) => r.record_id).join(",");
  useEffect(() => {
    if (!tenant) return;
    let stopped = false;
    (async () => {
      for (const id of want ? want.split(",") : []) {
        if (stopped) return;
        if (asked.current.has(id)) continue;
        asked.current.add(id);
        setCache((m) => new Map(m).set(id, { state: "loading" }));
        const a = await api.call<Capture>("auditCapture", { params: { tenant, record: id } });
        setCache((m) => new Map(m).set(id, fromAnswer(a)));
      }
    })();
    return () => {
      stopped = true;
    };
  }, [tenant, api, want]);
  const out = new Map<string, Load<Capture>>();
  for (const r of records) {
    const c = cache.get(r.record_id);
    if (c) out.set(r.record_id, c);
  }
  return out;
}

function Stepper({
  entries: records,
  unplaced,
  reported,
  at,
  captures,
  onStep,
  pager,
}: {
  entries: readonly Entry[];
  unplaced: readonly Gap[];
  reported: boolean | null;
  at: number;
  captures: Map<string, Load<Capture>>;
  onStep(id: string): void;
  pager: ReactNode;
}) {
  const { t, f } = useI18n();
  const kindLabel = useKindLabel();
  const [playing, setPlaying] = useState(false);
  const current = records[at] as Entry | undefined;
  const streams = streamsOf(records);

  useEffect(() => {
    if (!playing) return;
    if (at >= records.length - 1) {
      const id = window.setTimeout(() => setPlaying(false), PLAY_MS);
      return () => window.clearTimeout(id);
    }
    const id = window.setTimeout(() => onStep((records[at + 1] as Entry).id), PLAY_MS);
    return () => window.clearTimeout(id);
  }, [playing, at, records, onStep]);

  // A page whose only records are gap reports, while their gaps are read.
  if (!current) return <TimelineSkeleton rows={3} />;

  return (
    <div className={styles.replay}>
      <div className={styles.replayRail}>
        <div className={styles.stepButtons}>
          <Button disabled={at === 0} onClick={() => onStep((records[at - 1] as Entry).id)}>
            {t("replay.previous")}
          </Button>
          <Button
            variant={playing ? "secondary" : "primary"}
            disabled={records.length < 2}
            onClick={() => {
              // Played from the end, it starts again from the first step.
              if (!playing && at >= records.length - 1) onStep((records[0] as Entry).id);
              setPlaying(!playing);
            }}
          >
            {playing ? t("replay.pause") : t("replay.play")}
          </Button>
          <Button disabled={at >= records.length - 1} onClick={() => onStep((records[at + 1] as Entry).id)}>
            {t("replay.next")}
          </Button>
        </div>
        <p className={styles.quiet} aria-live="polite">
          {t("replay.position", { step: f.number(at + 1), count: f.number(records.length) })}
        </p>
        <Timeline
          label={t("replay.timeline")}
          selected={current.id}
          onSelect={onStep}
          events={records.map((e) => {
            if (e.kind === "gap") return gapEvent(t, e.gap);
            const r = e.record;
            return {
              id: r.record_id,
              at: r.recorded_at,
              title: <span>{kindLabel(r.kind)}</span>,
              meta:
                r.attributes?.command || r.event || r.message ? (
                  <>
                    {r.attributes?.command ? <Id>{r.attributes.command}</Id> : r.event ? <Id>{r.event}</Id> : null}
                    {r.message ? <span translate="no">{r.message}</span> : null}
                  </>
                ) : undefined,
            };
          })}
        />
        {unplaced.length ? <Unplaced gaps={unplaced} /> : null}
        {reported === false ? (
          <p className={styles.quiet} data-loss="none">
            {t("replay.loss.none")}
          </p>
        ) : null}
        {pager}
      </div>
      <section className={styles.terminalPanel} aria-labelledby="replay-terminal">
        {current.kind === "gap" ? (
          <section className={styles.gapPanel} aria-labelledby="replay-gap" data-gap-detail={current.id}>
            <h2 id="replay-gap" className={styles.drawerTitle}>
              {t("replay.gap.title")}
            </h2>
            <GapFacts gap={current.gap} session={false} />
          </section>
        ) : null}
        <h2 id="replay-terminal" className={styles.drawerTitle}>
          {t("replay.terminal")}
        </h2>
        {streams.length === 0 ? (
          <p className={styles.quiet}>{t("replay.noCapture")}</p>
        ) : (
          <Terminal
            entries={records.slice(0, at + 1).filter((e) => e.kind === "gap" || e.record.kind === "stream")}
            captures={captures}
          />
        )}
      </section>
    </div>
  );
}

/**
 * The terminal at this step: every chunk so far, joined, as text. A chunk that
 * is gone or unreadable, and a hole in the proxy's stream (a gap, at its
 * span), each ends the run before it and is shown where it falls, so nothing
 * on either side of a hole is joined across it.
 */
function Terminal({ entries, captures }: { entries: readonly Entry[]; captures: Map<string, Load<Capture>> }) {
  const { t } = useI18n();
  const pieces: ReactNode[] = [];
  let run: Uint8Array[] = [];
  let runStart = "";
  const flush = (key: string) => {
    if (run.length) {
      pieces.push(
        <pre key={`text-${runStart}`} className={styles.terminal} dir="ltr" translate="no">
          {terminalText(run)}
        </pre>,
      );
    }
    run = [];
    runStart = key;
  };
  for (const e of entries) {
    if (e.kind === "gap") {
      flush(e.id);
      pieces.push(
        <div key={e.id} className={styles.terminalGap} data-stream-gap={e.gap.gap_cause}>
          <GapStatus gap={e.gap} />
          <span>
            {e.gap.gap_records === undefined
              ? t("replay.hole.unknown")
              : t("replay.hole", { count: e.gap.gap_records })}
          </span>
        </div>,
      );
      continue;
    }
    const r = e.record;
    const c = captures.get(r.record_id);
    if (!c || c.state === "loading") {
      flush(r.record_id);
      pieces.push(
        <div key={r.record_id} className={styles.terminalGap} aria-busy="true">
          <Skeleton size="m" />
        </div>,
      );
      continue;
    }
    if (c.state === "ok" && !c.data.retained && c.data.payload !== undefined) {
      if (!run.length) runStart = r.record_id;
      run.push(captureBytes(c.data.payload));
      continue;
    }
    flush(r.record_id);
    pieces.push(
      <div key={r.record_id} className={styles.terminalGap} data-capture-gap={c.state === "ok" ? "retained" : c.state}>
        {c.state === "ok" ? (
          <Status tone="unknown" label={t("replay.gap.retained")} />
        ) : c.state === "outage" ? (
          <Status tone="outage" label={t("replay.gap.outage")} />
        ) : (
          <Status tone="deny" label={t("replay.gap.refused")} />
        )}
        <Id>{r.record_id}</Id>
      </div>,
    );
  }
  flush("end");
  return (
    <div className={styles.terminalScroll} tabIndex={0} role="region" aria-label={t("replay.terminal")}>
      {pieces}
    </div>
  );
}
