// THE POLICY SCREENS' PARTS (0053; PLAN §5.6, M11, M21). What the version
// page and the candidate page share: the publish-time report, the compiler's
// rejections set at their lines, the changes between two versions, a replay
// against recorded traffic, and the one activation dialog — which a rollback
// uses too, because a rollback is an activation (§5.6).
//
// Every sentence is the console's own, built from a code and its parameters
// (policyCodes.ts); the English the server sends beside them is never shown.
// The two places the server sends only English — an unreplayable record's
// reason and a compiler parameter that is itself prose (`detail`) — are shown
// as the server's words, marked as such (docs/learnings/0053, raised).

import { useEffect, useState, type ReactNode } from "react";
import type { Answer } from "../api/client";
import type {
  Finding,
  FlipGroup,
  PolicyActivation,
  PolicyBundle,
  PolicyDiff,
  PolicyReport,
  Rejection,
  Replay,
  Simulation,
} from "../api/types";
import { Badge, Chip } from "../components/Badge";
import { Button } from "../components/Button";
import { JsonViewer, sourceLineId, SourceView, type SourceNote } from "../components/Code";
import { CopyableId } from "../components/CopyableId";
import { ConfirmDialog, type ConfirmFailure } from "../components/Dialog";
import { DiffSkeleton, DiffView } from "../components/Diff";
import { Checkbox } from "../components/Field";
import { EmptyState, ErrorMessage, Skeleton, type ErrorKind } from "../components/States";
import { StatusOf } from "../components/Status";
import { readRange, resolveRange, TimeRangePicker, rangeQuery, type TimeRange } from "../components/TimeRange";
import type { ApiError } from "../i18n/errors";
import { isoUTC } from "../i18n/format";
import { useI18n } from "../i18n/I18n";
import { Link } from "../router/router";
import { useDeployment } from "../shell/Shell";
import { findingText, rejectionText } from "./policyCodes";
import {
  findingLists,
  findingsOf,
  inReportOrder,
  isRollback,
  mergeFindings,
  replayWindow,
  severityCounts,
  severityStatus,
  unaccepted,
  versionName,
} from "./policyModel";
import styles from "./Policy.module.css";

/** A version's name, as data: mono, LTR, never translated (DESIGN.md §9). */
export function VersionName({ version, title = false }: { version: number; title?: boolean }) {
  return (
    <code className={title ? styles.titleVersion : styles.version} dir="ltr" translate="no">
      {versionName(version)}
    </code>
  );
}

/** An identifier inside a fact: a rule id, a code, a hostname. */
export function Ident({ children }: { children: ReactNode }) {
  return (
    <code className={styles.ident} dir="ltr" translate="no">
      {children}
    </code>
  );
}

/** Where a rejection or finding is: a rule and a line, or the whole bundle. */
function Place({ rule, line, onLine }: { rule?: string; line?: number; onLine?(line: number): void }) {
  const { t } = useI18n();
  if (!rule && !line) return <span className={styles.place}>{t("policies.place.document")}</span>;
  return (
    <span className={styles.place}>
      {line ? (
        onLine ? (
          <button type="button" className={styles.lineLink} onClick={() => onLine(line)}>
            {t("policies.place.line", { line })}
          </button>
        ) : (
          <span>{t("policies.place.line", { line })}</span>
        )
      ) : null}
      {rule ? (
        <span className={styles.placeRule}>
          {t("policies.place.rule")} <Ident>{rule}</Ident>
        </span>
      ) : null}
    </span>
  );
}

/** Moves to a line of a source view and gives it focus. */
export function goToLine(prefix: string, line: number): void {
  const row = document.getElementById(sourceLineId(prefix, line));
  if (row) {
    row.scrollIntoView({ block: "center" });
    row.focus({ preventScroll: true });
  }
}

// ---------------------------------------------------------------------------
// Rejections (0005): each at its rule and line, from its code.
// ---------------------------------------------------------------------------

/** One rejection, as a sentence and its code; the code alone when unknown, with the correlation id. */
export function RejectionNote({ rejection: r, correlationId }: { rejection: Rejection; correlationId?: string }) {
  const { t } = useI18n();
  const rendered = rejectionText(t, r);
  return (
    <div className={styles.note} data-rejection={r.code} data-known={rendered.known || undefined}>
      <StatusOf name="invalid" />
      <div className={styles.noteBody}>
        <p className={styles.noteText}>{rendered.text}</p>
        <p className={styles.noteMeta}>
          <span>{t("policies.code")}</span> <Ident>{r.code}</Ident>
          {r.rule ? (
            <>
              {" · "}
              <span>{t("policies.place.rule")}</span> <Ident>{r.rule}</Ident>
            </>
          ) : null}
        </p>
        {!rendered.known && correlationId ? (
          <p className={styles.noteMeta}>
            <span>{t("error.correlation.label")}</span> <CopyableId value={correlationId} volatile />
          </p>
        ) : null}
      </div>
    </div>
  );
}

/** The notes a source view sets at their lines: rejections first, then findings. */
export function sourceNotes(rejections: readonly Rejection[], findings: readonly Finding[], correlationId?: string): SourceNote[] {
  return [
    ...rejections.map((r, i) => ({
      line: r.line ?? 0,
      key: `r${i}`,
      tone: "deny" as const,
      content: <RejectionNote rejection={r} correlationId={correlationId} />,
    })),
    ...findings
      .filter((f) => f.line)
      .map((f) => ({
        line: f.line ?? 0,
        key: `f:${f.id}`,
        tone: f.severity === "advice" ? ("info" as const) : f.severity === "warning" ? ("warn" as const) : ("deny" as const),
        content: <FindingLine finding={f} />,
      })),
  ];
}

/** A finding set beneath its line: its severity and its sentence. The report holds the rest. */
function FindingLine({ finding: f }: { finding: Finding }) {
  const { t } = useI18n();
  return (
    <div className={styles.note} data-finding-line={f.id}>
      <StatusOf name={severityStatus(f.severity)} />
      <p className={styles.noteText}>{findingText(t, f).text}</p>
    </div>
  );
}

/** A refused bundle: how many rejections, each listed with its place, and the source with each at its line. */
export function Rejections({
  rejections,
  source,
  idPrefix,
  correlationId,
}: {
  rejections: readonly Rejection[];
  source: string;
  idPrefix: string;
  correlationId?: string;
}) {
  const { t } = useI18n();
  return (
    <section className={styles.panel} aria-labelledby={`${idPrefix}-rejections`} data-rejections={rejections.length}>
      <div className={styles.panelHead}>
        <h2 id={`${idPrefix}-rejections`} className={styles.panelTitle}>
          {t("policies.rejections.title", { count: rejections.length })}
        </h2>
        <StatusOf name="invalid" pill />
      </div>
      <p className={styles.para}>{t("policies.rejections.lede")}</p>
      <ol className={styles.rejectionList}>
        {rejections.map((r, i) => (
          <li key={i}>
            <Place rule={r.rule} line={r.line} onLine={(l) => goToLine(idPrefix, l)} />
            <span className={styles.rejectionCode}>
              <Ident>{r.code}</Ident>
            </span>
          </li>
        ))}
      </ol>
      <SourceView source={source} label={t("policies.source.label")} idPrefix={idPrefix} notes={sourceNotes(rejections, [], correlationId)} />
    </section>
  );
}

// ---------------------------------------------------------------------------
// The publish-time report (§5.6): every finding by severity, then id.
// ---------------------------------------------------------------------------

function ListOf({ list }: { list: ReturnType<typeof findingLists>[number] }) {
  const { t, f } = useI18n();
  const heading =
    list.name === "targets"
      ? t("policies.finding.list.targets", { count: list.count })
      : list.name === "proxies"
        ? t("policies.finding.list.proxies", { count: list.count })
        : list.name === "rules"
          ? t("policies.finding.list.rules", { count: list.count })
          : t("policies.finding.list.terms", { count: list.count });
  return (
    <div className={styles.findingList} data-finding-list={list.name}>
      <p className={styles.noteMeta}>
        {heading}
        {list.truncated ? ` · ${t("policies.finding.list.cut", { shown: f.number(list.items.length), count: f.number(list.count) })}` : ""}
      </p>
      <ul className={styles.chips}>
        {list.items.map((item, i) => (
          <li key={`${item.name}:${i}`} className={styles.item}>
            <Chip data>{item.name}</Chip>
            {item.missing.length ? (
              <span className={styles.itemDetail}>
                {t("policies.finding.item.missing")} <Ident>{item.missing.join(", ")}</Ident>
              </span>
            ) : null}
            {item.offered.length ? (
              <span className={styles.itemDetail}>
                {t("policies.finding.item.offered")} <Ident>{item.offered.join(", ")}</Ident>
              </span>
            ) : null}
          </li>
        ))}
      </ul>
    </div>
  );
}

/** One finding: severity, sentence, place, the lists it names, its id. */
export function FindingCard({
  finding: fd,
  onLine,
  correlationId,
  accept,
}: {
  finding: Finding;
  onLine?(line: number): void;
  correlationId?: string;
  /** Offered where an activation can accept it (a warning, by its id). */
  accept?: { checked: boolean; onChange(checked: boolean): void };
}) {
  const { t } = useI18n();
  const rendered = findingText(t, fd);
  return (
    <article className={styles.finding} data-finding={fd.id} data-severity={fd.severity} data-known={rendered.known || undefined}>
      <header className={styles.findingHead}>
        <StatusOf name={severityStatus(fd.severity)} pill />
        <Place rule={fd.rule} line={fd.line} onLine={onLine} />
        {fd.source !== "control" ? (
          <span className={styles.noteMeta}>
            {t("policies.finding.source")} <Ident>{fd.source}</Ident>
          </span>
        ) : null}
      </header>
      <p className={styles.noteText}>{rendered.text}</p>
      {findingLists(fd).map((l) => (
        <ListOf key={l.name} list={l} />
      ))}
      {!rendered.known && fd.parameters ? <JsonViewer label={t("policies.finding.parameters")} value={fd.parameters} /> : null}
      <p className={styles.noteMeta}>
        <span>{t("policies.finding.id")}</span> <CopyableId value={fd.id} />
      </p>
      {!rendered.known && correlationId ? (
        <p className={styles.noteMeta}>
          <span>{t("error.correlation.label")}</span> <CopyableId value={correlationId} volatile />
        </p>
      ) : null}
      {accept ? (
        <Checkbox
          label={t("policies.activate.accept", { id: `\u2068${fd.id}\u2069` })}
          checked={accept.checked}
          onChange={(e) => accept.onChange(e.target.checked)}
          data-accept={fd.id}
        />
      ) : null}
    </article>
  );
}

/** The publish-time report: valid or not, the counts, every finding in the report's order. */
export function Report({
  report,
  idPrefix,
  correlationId,
  onLine,
}: {
  report: PolicyReport;
  idPrefix: string;
  correlationId?: string;
  onLine?(line: number): void;
}) {
  const { t } = useI18n();
  const counts = severityCounts(report.findings);
  const findings = inReportOrder(report.findings);
  return (
    <section className={styles.panel} aria-labelledby={`${idPrefix}-report`} data-report="">
      <div className={styles.panelHead}>
        <h2 id={`${idPrefix}-report`} className={styles.panelTitle}>
          {t("policies.report.title")}
        </h2>
        <StatusOf name={report.valid ? "valid" : "invalid"} pill />
        <span className={styles.noteMeta}>{t("policies.report.counts", counts)}</span>
      </div>
      <p className={styles.para}>{t("policies.report.lede")}</p>
      {findings.length === 0 ? (
        <EmptyState title={t("policies.report.empty.title")} body={t("policies.report.empty.body")} />
      ) : (
        <div className={styles.findings}>
          {findings.map((fd) => (
            <FindingCard key={fd.id} finding={fd} onLine={onLine} correlationId={correlationId} />
          ))}
        </div>
      )}
    </section>
  );
}

/** The report's loading state. */
export function ReportSkeleton() {
  return (
    <div className={styles.panel} aria-hidden="true">
      <Skeleton shape="title" size="m" />
      <Skeleton size="full" />
      <Skeleton shape="block" size="full" />
      <Skeleton shape="block" size="full" />
    </div>
  );
}

// ---------------------------------------------------------------------------
// Changes (§5.6): by rule, then line by line.
// ---------------------------------------------------------------------------

function RuleChanges({ title, ids, kind }: { title: string; ids: readonly string[]; kind: string }) {
  if (ids.length === 0) return null;
  return (
    <div className={styles.ruleChange} data-rule-change={kind}>
      <dt>{title}</dt>
      <dd>
        <ul className={styles.chips}>
          {ids.map((id) => (
            <li key={id}>
              <Chip data>{id}</Chip>
            </li>
          ))}
        </ul>
      </dd>
    </div>
  );
}

/** What separates two versions: the rules, the document's keys, and the text. */
export function Changes({ diff, label }: { diff: PolicyDiff; label: string }) {
  const { t } = useI18n();
  const none = !diff.added.length && !diff.removed.length && !diff.changed.length && !diff.moved.length && !diff.document.length;
  return (
    <div className={styles.changes} data-changes="">
      <section className={styles.panel} aria-labelledby="policy-diff-rules">
        <h2 id="policy-diff-rules" className={styles.panelTitle}>
          {t("policies.diff.rules")}
        </h2>
        {none ? (
          <p className={styles.para}>{t("policies.diff.noRules")}</p>
        ) : (
          <dl className={styles.ruleChanges}>
            <RuleChanges title={t("policies.diff.added")} ids={diff.added} kind="added" />
            <RuleChanges title={t("policies.diff.removed")} ids={diff.removed} kind="removed" />
            <RuleChanges title={t("policies.diff.changed")} ids={diff.changed} kind="changed" />
            <RuleChanges title={t("policies.diff.moved")} ids={diff.moved} kind="moved" />
            <RuleChanges title={t("policies.diff.document")} ids={diff.document} kind="document" />
          </dl>
        )}
        {diff.moved.length ? <p className={styles.noteMeta}>{t("policies.diff.moved.note")}</p> : null}
      </section>
      <DiffView label={label} text={diff.text} omitted={diff.text_omitted} />
    </div>
  );
}

/** Changes' loading state. */
export function ChangesSkeleton() {
  return (
    <div className={styles.changes} aria-hidden="true">
      <div className={styles.panel}>
        <Skeleton shape="title" size="m" />
        <Skeleton size="l" />
      </div>
      <DiffSkeleton />
    </div>
  );
}

// ---------------------------------------------------------------------------
// Simulation (§5.6): replay a window of recorded decisions against a candidate.
// ---------------------------------------------------------------------------

type Run = { state: "idle" } | { state: "running" } | { state: "done"; replay: Replay } | { state: "failed"; error: ApiError; kind: ErrorKind };

function Effect({ effect }: { effect: string }) {
  return <StatusOf name={effect === "allow" ? "allowed" : effect === "deny" ? "denied" : "notReported"} />;
}

function RuleOrDefault({ rule }: { rule?: string }) {
  const { t } = useI18n();
  return rule ? <Ident>{rule}</Ident> : <span className={styles.quiet}>{t("policies.sim.defaultDeny")}</span>;
}

function FlipGroups({ title, groups, kind }: { title: string; groups: readonly FlipGroup[]; kind: string }) {
  const { t, f } = useI18n();
  if (groups.length === 0) return null;
  return (
    <section className={styles.flipSection} data-flips={kind} aria-label={title}>
      <h3 className={styles.subTitle}>{title}</h3>
      {groups.map((g, i) => (
        <div key={`${g.was_rule ?? ""}>${g.now_rule ?? ""}:${i}`} className={styles.flipGroup}>
          <p className={styles.flipHead}>
            <Effect effect={g.from} />
            <span className={styles.quiet}>{t("policies.sim.by")}</span>
            <RuleOrDefault rule={g.was_rule} />
            <svg className={styles.arrow} viewBox="0 0 12 12" aria-hidden="true" focusable="false">
              <path d="M2 6h8M7 3l3 3-3 3" />
            </svg>
            <span className={styles.visuallyHidden}>{t("policies.sim.becomes")}</span>
            <Effect effect={g.to} />
            <span className={styles.quiet}>{t("policies.sim.by")}</span>
            <RuleOrDefault rule={g.now_rule} />
            <Badge>{t("policies.sim.decisions", { count: g.count })}</Badge>
          </p>
          <table className={styles.flipTable}>
            <caption className={styles.visuallyHidden}>{title}</caption>
            <thead>
              <tr>
                <th scope="col">{t("policies.sim.col.decision")}</th>
                <th scope="col">{t("policies.sim.col.subject")}</th>
                <th scope="col">{t("policies.sim.col.target")}</th>
                <th scope="col">{t("policies.sim.col.at")}</th>
              </tr>
            </thead>
            <tbody>
              {g.flips.map((x) => (
                <tr key={x.decision_id} data-flip={x.decision_id}>
                  <td>
                    <Link to={`explain/decisions/${encodeURIComponent(x.decision_id)}`} className={styles.mono}>
                      <span dir="ltr" translate="no">
                        {x.decision_id}
                      </span>
                    </Link>
                  </td>
                  <td>
                    <Ident>{x.subject}</Ident>
                  </td>
                  <td>
                    <Ident>{x.target}</Ident>
                  </td>
                  <td>
                    <time dateTime={f.utc(x.decided_at)} title={f.utc(x.decided_at)}>
                      {f.dateTime(x.decided_at)}
                    </time>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ))}
    </section>
  );
}

/** A replay's answer: what was read, what flipped and how, and what could not be read. */
export function ReplayResult({ replay }: { replay: Replay }) {
  const { t, f } = useI18n();
  const flipped = replay.allow_to_deny.reduce((n, g) => n + g.count, 0) + replay.deny_to_allow.reduce((n, g) => n + g.count, 0);
  return (
    <div className={styles.replay} data-replay="" data-flipped={flipped}>
      <dl className={styles.facts}>
        <div>
          <dt>{t("policies.sim.replayed")}</dt>
          <dd>{f.number(replay.replayed)}</dd>
        </div>
        <div>
          <dt>{t("policies.sim.unchanged")}</dt>
          <dd>{f.number(replay.unchanged)}</dd>
        </div>
        <div>
          <dt>{t("policies.sim.flipped")}</dt>
          <dd>{f.number(flipped)}</dd>
        </div>
        <div>
          <dt>{t("policies.sim.window")}</dt>
          <dd>
            <time dateTime={f.utc(replay.from)} title={f.utc(replay.from)}>
              {f.dateTime(replay.from)}
            </time>
            {" – "}
            <time dateTime={f.utc(replay.to)} title={f.utc(replay.to)}>
              {f.dateTime(replay.to)}
            </time>
          </dd>
        </div>
      </dl>
      {replay.truncated ? <p className={styles.notice}>{t("policies.sim.truncated", { count: f.number(replay.replayed) })}</p> : null}
      {replay.replayed === 0 ? (
        <EmptyState title={t("policies.sim.empty.title")} body={t("policies.sim.empty.body")} />
      ) : flipped === 0 ? (
        <EmptyState title={t("policies.sim.none.title")} body={t("policies.sim.none.body")} />
      ) : null}
      <FlipGroups title={t("policies.sim.allowToDeny")} groups={replay.allow_to_deny} kind="allow_to_deny" />
      <FlipGroups title={t("policies.sim.denyToAllow")} groups={replay.deny_to_allow} kind="deny_to_allow" />
      {replay.unreplayable?.length ? (
        <section className={styles.flipSection} data-unreplayable={replay.unreplayable.length}>
          <h3 className={styles.subTitle}>{t("policies.sim.unreplayable.title", { count: replay.unreplayable.length })}</h3>
          <p className={styles.para}>{t("policies.sim.unreplayable.lede")}</p>
          <ul className={styles.unreplayable}>
            {replay.unreplayable.map((u) => (
              <li key={u.decision_id}>
                <Ident>{u.decision_id}</Ident>
                <span className={styles.noteMeta}>{t("policies.sim.unreplayable.reason")}</span>
                <span className={styles.serverWords} dir="ltr" translate="no">
                  {u.reason}
                </span>
              </li>
            ))}
          </ul>
        </section>
      ) : null}
    </div>
  );
}

/**
 * Replays a window of recorded traffic against a candidate: a stored version
 * by number, or a source not stored yet. The window is the time range
 * picker's, kept in the URL so a link reproduces the question.
 */
export function Simulate({
  candidate,
  search,
  onRange,
}: {
  candidate: { version: number } | { source: string };
  search: URLSearchParams;
  onRange(query: Record<string, string | undefined>): void;
}) {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const range: TimeRange = search.get("range") || search.get("from") || search.get("to") ? readRange(search) : { kind: "relative", last: "7d" };
  const [run, setRun] = useState<Run>({ state: "idle" });
  const go = async () => {
    setRun({ state: "running" });
    const now = isoUTC(new Date());
    const window = replayWindow(resolveRange(range, Date.now()), now);
    const a: Answer<Simulation> = await api.call<Simulation>("policySimulate", {
      params: { tenant: tenant as string },
      body: { ...candidate, replay: window },
    });
    if (a.kind === "ok" && a.data.replay) setRun({ state: "done", replay: a.data.replay });
    else if (a.kind === "ok") setRun({ state: "failed", error: { code: "internal", correlation_id: a.correlationId }, kind: "outage" });
    else setRun({ state: "failed", error: a.error, kind: a.kind });
  };
  return (
    <section className={styles.panel} aria-labelledby="policy-sim" data-simulate="">
      <h2 id="policy-sim" className={styles.panelTitle}>
        {t("policies.sim.title")}
      </h2>
      <p className={styles.para}>{t("policies.sim.lede")}</p>
      <div className={styles.simControls}>
        <TimeRangePicker label={t("policies.sim.range")} value={range} onChange={(r) => onRange(rangeQuery(r))} disabled={run.state === "running"} />
        <Button variant="primary" onClick={go} loading={run.state === "running"} data-run-replay="">
          {t("policies.sim.run")}
        </Button>
      </div>
      {run.state === "idle" ? <p className={styles.quiet}>{t("policies.sim.idle")}</p> : null}
      {run.state === "failed" ? <ErrorMessage error={run.error} kind={run.kind} /> : null}
      {run.state === "done" ? <ReplayResult replay={run.replay} /> : null}
    </section>
  );
}

// ---------------------------------------------------------------------------
// Activation (§5.6): one dialog for activating and rolling back.
// ---------------------------------------------------------------------------

type Gate = { state: "loading" } | { state: "ok"; findings: Finding[] } | { state: "unread" };

/**
 * Activates a stored version, confirmed by typing its name (DESIGN.md §2).
 * It reads the version's publish-time report against the fleet as it is now
 * and lists each warning for the author to accept by id — the override, which
 * the activation's record names (0022) — and each blocking finding. The
 * server checks again at activation: a `409` names the warnings it found
 * then, which join the list here, and every refusal reports in the dialog.
 */
export function ActivateDialog({
  version,
  active,
  onClose,
  onDone,
}: {
  /** The version to activate, or null when closed. */
  version: number | null;
  active: number | null;
  onClose(): void;
  onDone(result: PolicyActivation): void;
}) {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const [gate, setGate] = useState<Gate>({ state: "loading" });
  const [accepted, setAccepted] = useState<Set<string>>(new Set());
  useEffect(() => {
    if (version === null || !tenant) return;
    setGate({ state: "loading" });
    setAccepted(new Set());
    const abort = new AbortController();
    (async () => {
      const b = await api.call<PolicyBundle>("policyVersion", { params: { tenant, version: String(version) }, signal: abort.signal });
      if (b.kind !== "ok") return setGate({ state: "unread" });
      const r = await api.call<PolicyReport>("policyValidate", { params: { tenant }, body: { source: b.data.source }, signal: abort.signal });
      if (r.kind !== "ok") return setGate({ state: "unread" });
      setGate({ state: "ok", findings: inReportOrder(r.data.findings) });
    })().catch(() => {
      // Aborted: the dialog closed or moved to another version.
    });
    return () => abort.abort();
  }, [version, tenant, api]);

  const rollback = version !== null && isRollback(version, active);
  const name = version === null ? "" : versionName(version);
  const findings = gate.state === "ok" ? gate.findings : [];
  const blocking = findings.filter((f) => f.severity === "blocking");
  const warnings = findings.filter((f) => f.severity === "warning");

  const confirm = async (): Promise<ConfirmFailure | null> => {
    if (version === null || !tenant) return null;
    const a = await api.call<PolicyActivation>("policyActivate", {
      params: { tenant, version: String(version) },
      body: { accept: Array.from(accepted).sort() },
    });
    if (a.kind === "ok") {
      onDone(a.data);
      return null;
    }
    if (a.kind === "refused" && a.error.code === "findings_not_accepted") {
      const named = findingsOf(a.error.parameters);
      setGate((g) => ({ state: "ok", findings: inReportOrder(mergeFindings(g.state === "ok" ? g.findings : [], named)) }));
    }
    return { error: a.error, kind: a.kind };
  };

  const open = version !== null;
  const versionLabel = `\u2068${name}\u2069`;
  const activeLabel = active === null ? "" : `\u2068${versionName(active)}\u2069`;
  return (
    <ConfirmDialog
      open={open}
      title={rollback ? t("policies.rollback.title", { version: versionLabel }) : t("policies.activate.title", { version: versionLabel })}
      name={name}
      action={rollback ? t("policies.rollback.action") : t("policies.activate.action")}
      hint={t("policies.activate.hint")}
      onConfirm={confirm}
      onClose={onClose}
      body={
        <div className={styles.gate} data-activation-gate={gate.state}>
          <p className={styles.para}>
            {rollback
              ? t("policies.rollback.body", { version: versionLabel, active: activeLabel })
              : active === null
                ? t("policies.activate.bodyFirst", { version: versionLabel })
                : t("policies.activate.body", { version: versionLabel, active: activeLabel })}
          </p>
          {gate.state === "loading" ? (
            <p className={styles.quiet} role="status">
              {t("policies.activate.checking")}
            </p>
          ) : gate.state === "unread" ? (
            <p className={styles.para}>{t("policies.activate.unread")}</p>
          ) : blocking.length === 0 && warnings.length === 0 ? (
            <p className={styles.para}>{t("policies.activate.clear")}</p>
          ) : null}
          {blocking.length ? (
            <div className={styles.gateList}>
              <p className={styles.para}>{t("policies.activate.blocking", { count: blocking.length })}</p>
              {blocking.map((f) => (
                <FindingCard key={f.id} finding={f} />
              ))}
            </div>
          ) : null}
          {warnings.length ? (
            <div className={styles.gateList}>
              <p className={styles.para}>{t("policies.activate.warnings", { count: unaccepted(warnings, accepted).length, total: warnings.length })}</p>
              {warnings.map((f) => (
                <FindingCard
                  key={f.id}
                  finding={f}
                  accept={{
                    checked: accepted.has(f.id),
                    onChange: (on) =>
                      setAccepted((s) => {
                        const next = new Set(s);
                        if (on) next.add(f.id);
                        else next.delete(f.id);
                        return next;
                      }),
                  }}
                />
              ))}
            </div>
          ) : null}
        </div>
      }
    />
  );
}

/** The sentence an activation's answer is told in: activated, rolled back, or already active. */
export function activationMessage(t: ReturnType<typeof useI18n>["t"], r: PolicyActivation): string {
  const v = `\u2068${versionName(r.version.version)}\u2069`;
  if (r.unchanged) return t("policies.activate.unchanged", { version: v });
  if (r.rollback) return t("policies.rollback.done", { version: v, count: r.accepted.length });
  return t("policies.activate.done", { version: v, count: r.accepted.length });
}
