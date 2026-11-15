// THE EMERGENCY RUNBOOK (0058; PLAN §5.2, M11, M26; Hoplock/proxy#69;
// upstream api/README.md, "Banned algorithms", which states the steps): the
// workflow for the day an advisory lands. Three steps, in order, each a call
// 0019 serves and audits, each destructive and confirmed as one, each showing
// what its call reported covering (runbookModel.ts):
//
//   1. ban the identifier on every route and activate it — POST …/policy/apply,
//      after the candidate's publish-time report is read and its warnings
//      accepted by id; or, where the active version already bans it
//      everywhere (a GitOps pipeline applied it), go on from that version;
//   2. `cache_invalidate` with `all` — POST …/revocations — because a cached
//      decision keeps the old policy until its hint runs out;
//   3. find the running sessions by what they negotiated — GET
//      …/audit/negotiated, read to its last page (M26) — and end every one
//      listed with `session_kill`, whose `reason` the user is shown, so the
//      workflow asks for it and shows it before sending.
//
// Each step is offered only once the one before it has answered, and only to
// a role holding its call's permission; the API refuses it to anyone else.
// The screen reads the active version on opening (its loading, empty and
// error states) and the fleet's declarations, which say which builds offer
// the identifier; that read failing is its partial state, the gap named.

import { useEffect, useState, type ReactNode } from "react";
import { offered } from "../api/permit";
import type {
  AlgorithmAxis,
  Finding,
  NegotiatedPage,
  NegotiatedSession,
  PolicyApplied,
  PolicyBundle,
  PolicyReport,
  RevocationReceipt,
} from "../api/types";
import { Button, ButtonLink } from "../components/Button";
import { CopyableId } from "../components/CopyableId";
import { ConfirmDialog, type ConfirmFailure } from "../components/Dialog";
import { Field, Input, Select, Textarea } from "../components/Field";
import { PageHeader } from "../components/PageHeader";
import { EmptyState, ErrorMessage, ScreenFrame, Skeleton } from "../components/States";
import { StatusOf, type StatusName } from "../components/Status";
import { readRange, rangeQuery, resolveRange, TimeRangePicker } from "../components/TimeRange";
import type { ApiError } from "../i18n/errors";
import { useI18n } from "../i18n/I18n";
import { navigate, useLocation } from "../router/router";
import { useLoad } from "../shell/load";
import { useDeployment } from "../shell/Shell";
import { AXIS_LABEL, useBuilds } from "./AlgorithmParts";
import { readAll, type Build } from "./algorithmModel";
import { FindingCard } from "./PolicyParts";
import { inReportOrder, severityCounts, severityStatus, unaccepted } from "./policyModel";
import {
  batches,
  finished,
  NEGOTIATED_AXES,
  remaining,
  START,
  stepStates,
  validIdentifier,
  withBan,
  type Progress,
  type StepState,
} from "./runbookModel";
import styles from "./Algorithms.module.css";

const DEFAULT_LIMIT = 500;

function readAxis(search: URLSearchParams): AlgorithmAxis {
  const a = search.get("axis") as AlgorithmAxis | null;
  return a && NEGOTIATED_AXES.includes(a) ? a : "ciphers";
}

function readLimit(search: URLSearchParams): number {
  const n = Number(search.get("limit"));
  return Number.isInteger(n) && n >= 1 && n <= 10000 ? n : DEFAULT_LIMIT;
}

const STEP_STATUS: Readonly<Record<StepState, StatusName>> = { done: "done", ready: "pending", waiting: "pending" };

function failure(a: { kind: "refused" | "outage"; error: ApiError }): ConfirmFailure {
  return { error: a.error, kind: a.kind };
}

export function RunbookScreen() {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const location = useLocation();
  const axis = readAxis(location.search);
  const name = (location.search.get("name") ?? "").trim();
  const [load, refresh] = useLoad<PolicyBundle | null>(tenant ? `runbook|${tenant}` : null, async (signal) => {
    const a = await api.call<PolicyBundle>("policyActive", { params: { tenant: tenant as string }, signal });
    // No active version is a fact, not an error: there is nothing to ban in.
    if (a.kind === "refused" && a.status === 404) return { kind: "ok", status: 404, data: null };
    return a;
  });
  const builds = useBuilds(true);
  const gaps = builds && (builds.state === "outage" || builds.state === "refused") ? [t("runbook.gap.builds")] : [];

  return (
    <div className={styles.screen}>
      <PageHeader title={t("runbook.title")} lede={t("runbook.lede")} />
      <ScreenFrame
        load={load}
        onRetry={refresh}
        gaps={gaps}
        isEmpty={(d) => d === null}
        loading={<RunbookSkeleton />}
        empty={
          <EmptyState
            title={t("runbook.empty.title")}
            body={t("runbook.empty.body")}
            action={<ButtonLink to="policy/candidate">{t("runbook.empty.action")}</ButtonLink>}
          />
        }
      >
        {(active) =>
          active ? <Runbook key={`${axis}|${name}`} active={active} axis={axis} name={name} builds={builds?.state === "ok" ? builds.data : null} /> : null
        }
      </ScreenFrame>
    </div>
  );
}

function RunbookSkeleton() {
  return (
    <div className={styles.stack} aria-hidden="true">
      <Skeleton shape="block" size="full" />
      <Skeleton shape="block" size="full" />
      <Skeleton shape="block" size="full" />
    </div>
  );
}

/** The advisory: which identifier, on which axis. Written to the URL, so a link carries it. */
function Advisory({ axis, name }: { axis: AlgorithmAxis; name: string }) {
  const { t } = useI18n();
  const location = useLocation();
  const [a, setA] = useState<AlgorithmAxis>(axis);
  const [n, setN] = useState(name);
  const bad = n.trim() !== "" && !validIdentifier(n.trim());
  return (
    <form
      className={styles.form}
      data-runbook-advisory=""
      onSubmit={(e) => {
        e.preventDefault();
        if (bad) return;
        const q: Record<string, string> = {};
        for (const [k, v] of location.search) q[k] = v;
        q.axis = a;
        if (n.trim()) q.name = n.trim();
        else delete q.name;
        navigate(location.path, { replace: true, query: q });
      }}
    >
      <Field label={t("runbook.advisory.axis")}>
        <Select value={a} onChange={(e) => setA(e.target.value as AlgorithmAxis)} data-runbook-axis="">
          {NEGOTIATED_AXES.map((x) => (
            <option key={x} value={x}>
              {t(AXIS_LABEL[x])}
            </option>
          ))}
        </Select>
      </Field>
      <Field label={t("runbook.advisory.name")} hint={t("runbook.advisory.nameHint")} error={bad ? t("runbook.advisory.nameInvalid") : undefined}>
        <Input mono value={n} onChange={(e) => setN(e.target.value)} spellCheck={false} data-runbook-name="" />
      </Field>
      <div className={styles.actions}>
        <Button type="submit" variant="secondary">
          {t("runbook.advisory.prepare")}
        </Button>
      </div>
      <p className={`${styles.meta} ${styles.wide}`}>{t("runbook.advisory.public_key_auth")}</p>
    </form>
  );
}

function Step({ n, title, state, children }: { n: number; title: string; state: StepState; children: ReactNode }) {
  const { t } = useI18n();
  return (
    <li className={styles.step} data-step={n} data-step-state={state} aria-labelledby={`runbook-step-${n}`}>
      <div className={styles.stepHead}>
        <span className={styles.stepNumber}>{t("runbook.step.number", { n })}</span>
        <h2 id={`runbook-step-${n}`} className={styles.sectionTitle}>
          {title}
        </h2>
        <StatusOf name={STEP_STATUS[state]} pill />
        {state === "waiting" ? <span className={styles.meta}>{t("runbook.step.waiting", { n: n - 1 })}</span> : null}
      </div>
      {children}
    </li>
  );
}

/** A control the role is not offered: said in words, never a disabled button that would be refused. */
function NotOffered({ permission }: { permission: string }) {
  const { t } = useI18n();
  return <p className={styles.meta}>{t("runbook.notOffered", { permission: `⁨${permission}⁩` })}</p>;
}

function Receipt({ receipt, children }: { receipt: RevocationReceipt; children?: ReactNode }) {
  const { t } = useI18n();
  return (
    <div className={styles.receipt} data-receipt={receipt.type}>
      {children}
      <p className={styles.para}>{t("runbook.receipt.delivered", { count: receipt.delivered })}</p>
      <p className={styles.meta}>
        {t(receipt.covers_host_key_decisions ? "runbook.receipt.hostKeys.yes" : "runbook.receipt.hostKeys.no")}
      </p>
      <p className={styles.meta}>
        <span>{t("runbook.receipt.event")}</span> <CopyableId value={receipt.event_id} volatile />
      </p>
    </div>
  );
}

function Runbook({ active, axis, name, builds }: { active: PolicyBundle; axis: AlgorithmAxis; name: string; builds: Build[] | null }) {
  const { t } = useI18n();
  const { tenant, session } = useDeployment();
  const permissions = (tenant && session.scopes[tenant]?.permissions) || [];
  const [progress, setProgress] = useState<Progress>(START);
  const [one, two, three] = stepStates(progress);
  const ready = name !== "" && validIdentifier(name);
  return (
    <div className={styles.stack}>
      <section className={styles.section} aria-labelledby="runbook-advisory">
        <h2 id="runbook-advisory" className={styles.sectionTitle}>
          {t("runbook.advisory.title")}
        </h2>
        <p className={styles.para}>{t("runbook.advisory.lede")}</p>
        <Advisory axis={axis} name={name} />
      </section>
      {!ready ? (
        <p className={styles.note}>{t("runbook.advisory.first")}</p>
      ) : (
        <>
          {finished(progress) ? (
            <p className={styles.unreported} role="status" data-runbook-finished="">
              <StatusOf name="done" pill /> <span>{t("runbook.finished", { count: progress.killed.ended.length })}</span>
            </p>
          ) : null}
          <ol className={styles.steps}>
            <Step n={1} title={t("runbook.ban.title")} state={one}>
              <BanStep
                active={active}
                axis={axis}
                name={name}
                builds={builds}
                canApply={offered("policyApply", permissions)}
                done={progress.ban}
                onDone={(ban) => setProgress((p) => ({ ...p, ban }))}
              />
            </Step>
            <Step n={2} title={t("runbook.invalidate.title")} state={two}>
              <InvalidateStep
                state={two}
                canSend={offered("revocations", permissions)}
                version={progress.ban?.version}
                done={progress.invalidated}
                onDone={(invalidated) => setProgress((p) => ({ ...p, invalidated }))}
              />
            </Step>
            <Step n={3} title={t("runbook.kill.title")} state={three}>
              <KillStep
                state={three}
                axis={axis}
                name={name}
                canFind={offered("auditNegotiated", permissions)}
                canKill={offered("revocations", permissions)}
                progress={progress}
                onFound={(found) => setProgress((p) => ({ ...p, found }))}
                onKilled={(receipt, ended) =>
                  setProgress((p) => ({ ...p, killed: { receipts: [...p.killed.receipts, receipt], ended: [...p.killed.ended, ...ended] } }))
                }
              />
            </Step>
          </ol>
        </>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Step 1: the ban, everywhere, and its activation.
// ---------------------------------------------------------------------------

function BanStep({
  active,
  axis,
  name,
  builds,
  canApply,
  done,
  onDone,
}: {
  active: PolicyBundle;
  axis: AlgorithmAxis;
  name: string;
  builds: Build[] | null;
  canApply: boolean;
  done?: Progress["ban"];
  onDone(ban: NonNullable<Progress["ban"]>): void;
}) {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const banned = withBan(active.source, axis, name);
  const [draft, setDraft] = useState(banned.source);
  const [check, setCheck] = useState<{ source: string; report: PolicyReport } | { source: string; error: ConfirmFailure } | null>(null);
  const [checking, setChecking] = useState(false);
  const [accepted, setAccepted] = useState<Set<string>>(new Set());
  const [confirming, setConfirming] = useState(false);
  const [applied, setApplied] = useState<PolicyApplied | null>(null);
  const axisLabel = t(AXIS_LABEL[axis]);
  const offering = builds?.filter((b) => (b.declaration.algorithms?.[axis] ?? []).includes(name)) ?? null;

  const reach =
    offering === null ? null : (
      <p className={styles.meta} data-runbook-reach="">
        {offering.length
          ? t("runbook.ban.reach", { count: offering.reduce((n, b) => n + b.proxies.length, 0), builds: offering.length })
          : t("runbook.ban.reachNone")}
      </p>
    );

  if (done) {
    return (
      <div className={styles.stack}>
        <div className={styles.receipt} data-receipt="ban">
          <p className={styles.para}>
            {t(done.via === "applied" ? "runbook.ban.applied" : "runbook.ban.inForce", { version: done.version })}
          </p>
          {applied ? (
            <p className={styles.meta}>
              {t("runbook.ban.appliedFacts", { previous: applied.activate.previous, accepted: applied.activate.accepted.length })}
            </p>
          ) : null}
        </div>
        {reach}
      </div>
    );
  }

  if (banned.already) {
    return (
      <div className={styles.stack}>
        <p className={styles.para}>{t("runbook.ban.already", { version: active.version, axis: axisLabel })}</p>
        {reach}
        <div className={styles.actions}>
          <Button variant="secondary" onClick={() => onDone({ version: active.version, via: "active" })} data-runbook-continue="">
            {t("runbook.ban.continue", { version: active.version })}
          </Button>
        </div>
      </div>
    );
  }

  const current = check && check.source === draft ? check : null;
  const report = current && "report" in current ? current.report : null;
  const findings: Finding[] = report ? inReportOrder(report.findings) : [];
  const warnings = findings.filter((f) => f.severity === "warning");
  const blocking = findings.filter((f) => f.severity === "blocking");
  const open = unaccepted(warnings, accepted);
  const applicable = report !== null && report.valid && blocking.length === 0 && open.length === 0;

  return (
    <div className={styles.stack}>
      <p className={styles.para}>{t("runbook.ban.lede", { version: active.version, axis: axisLabel })}</p>
      {reach}
      {banned.edited ? (
        <p className={styles.meta} data-runbook-added="">
          {t("runbook.ban.added")}{" "}
          <code dir="ltr" translate="no" className={styles.added}>
            {`algorithm_bans: { ${axis}: [${name}] }`}
          </code>
        </p>
      ) : (
        <p className={styles.warnNote}>{t("runbook.ban.byHand")}</p>
      )}
      <Field label={t("runbook.ban.source")} hint={t("runbook.ban.sourceHint")}>
        <Textarea mono rows={14} value={draft} onChange={(e) => setDraft(e.target.value)} data-runbook-source="" />
      </Field>
      <div className={styles.actions}>
        <Button
          variant="secondary"
          loading={checking}
          data-runbook-check=""
          onClick={async () => {
            if (!tenant) return;
            setChecking(true);
            const source = draft;
            const r = await api.call<PolicyReport>("policyValidate", { params: { tenant }, body: { source } });
            setChecking(false);
            setAccepted(new Set());
            setCheck(r.kind === "ok" ? { source, report: r.data } : { source, error: failure(r) });
          }}
        >
          {t("runbook.ban.check")}
        </Button>
        {canApply ? (
          <Button variant="danger" disabled={!applicable} onClick={() => setConfirming(true)} data-runbook-apply="">
            {t("runbook.ban.apply")}
          </Button>
        ) : null}
      </div>
      {!canApply ? <NotOffered permission="policy:publish" /> : null}
      {current && "error" in current ? <ErrorMessage error={current.error.error} kind={current.error.kind} /> : null}
      {report ? (
        <div className={styles.stack} data-runbook-report="">
          <p className={styles.unreported}>
            <StatusOf name={report.valid ? "valid" : "invalid"} pill />
            <span className={styles.meta}>{t("policies.report.counts", severityCounts(report.findings))}</span>
          </p>
          {!report.valid ? <p className={styles.para}>{t("runbook.ban.invalid")}</p> : null}
          {findings.map((f) => (
            <FindingCard
              key={f.id}
              finding={f}
              builds={builds ? { state: "ok", data: builds } : undefined}
              accept={
                f.severity === "warning"
                  ? {
                      checked: accepted.has(f.id),
                      onChange: (on) =>
                        setAccepted((s) => {
                          const next = new Set(s);
                          if (on) next.add(f.id);
                          else next.delete(f.id);
                          return next;
                        }),
                    }
                  : undefined
              }
            />
          ))}
          {open.length ? <p className={styles.meta}>{t("runbook.ban.acceptEach", { count: open.length })}</p> : null}
          {blocking.length ? (
            <p className={styles.unreported}>
              <StatusOf name={severityStatus("blocking")} /> <span>{t("runbook.ban.blocked")}</span>
            </p>
          ) : null}
        </div>
      ) : (
        <p className={styles.meta}>{t("runbook.ban.checkFirst")}</p>
      )}
      <ConfirmDialog
        open={confirming}
        title={t("runbook.ban.confirm.title")}
        body={
          <>
            <p className={styles.para}>{t("runbook.ban.confirm.body", { axis: axisLabel, name: `⁨${name}⁩` })}</p>
            {accepted.size ? (
              <p className={styles.meta}>{t("runbook.ban.confirm.accepting", { count: accepted.size })}</p>
            ) : null}
          </>
        }
        name={name}
        action={t("runbook.ban.apply")}
        hint={t("runbook.ban.confirm.hint")}
        onClose={() => setConfirming(false)}
        onConfirm={async () => {
          if (!tenant) return null;
          const r = await api.call<PolicyApplied>("policyApply", { params: { tenant }, body: { source: draft, accept: [...accepted] } });
          if (r.kind !== "ok") return failure(r);
          setApplied(r.data);
          setConfirming(false);
          onDone({ version: r.data.activate.version.version, via: "applied" });
          return null;
        }}
      />
    </div>
  );
}

// ---------------------------------------------------------------------------
// Step 2: every cached decision, dropped.
// ---------------------------------------------------------------------------

function InvalidateStep({
  state,
  canSend,
  version,
  done,
  onDone,
}: {
  state: StepState;
  canSend: boolean;
  version?: number;
  done?: RevocationReceipt;
  onDone(receipt: RevocationReceipt): void;
}) {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const [confirming, setConfirming] = useState(false);
  return (
    <div className={styles.stack}>
      <p className={styles.para}>{t("runbook.invalidate.lede")}</p>
      {done ? (
        <Receipt receipt={done}>
          <p className={styles.para}>{t("runbook.invalidate.done")}</p>
        </Receipt>
      ) : canSend ? (
        <div className={styles.actions}>
          <Button variant="danger" disabled={state !== "ready"} onClick={() => setConfirming(true)} data-runbook-invalidate="">
            {t("runbook.invalidate.action")}
          </Button>
        </div>
      ) : (
        <NotOffered permission="session:revoke" />
      )}
      <ConfirmDialog
        open={confirming}
        title={t("runbook.invalidate.confirm.title")}
        body={<p className={styles.para}>{t("runbook.invalidate.confirm.body", { tenant: `⁨${tenant ?? ""}⁩`, version: version ?? 0 })}</p>}
        name={tenant ?? ""}
        action={t("runbook.invalidate.action")}
        onClose={() => setConfirming(false)}
        onConfirm={async () => {
          if (!tenant) return null;
          const r = await api.call<RevocationReceipt>("revocations", { params: { tenant }, body: { type: "cache_invalidate", all: true } });
          if (r.kind !== "ok") return failure(r);
          setConfirming(false);
          onDone(r.data);
          return null;
        }}
      />
    </div>
  );
}

// ---------------------------------------------------------------------------
// Step 3: every session that negotiated it, found and ended.
// ---------------------------------------------------------------------------

function KillStep({
  state,
  axis,
  name,
  canFind,
  canKill,
  progress,
  onFound,
  onKilled,
}: {
  state: StepState;
  axis: AlgorithmAxis;
  name: string;
  canFind: boolean;
  canKill: boolean;
  progress: Progress;
  onFound(found: NonNullable<Progress["found"]>): void;
  onKilled(receipt: RevocationReceipt, ended: string[]): void;
}) {
  const { t, f } = useI18n();
  const { tenant, api } = useDeployment();
  const location = useLocation();
  const range = readRange(location.search);
  const limit = readLimit(location.search);
  const [finding, setFinding] = useState<{ pages: number; count: number } | null>(null);
  const [findError, setFindError] = useState<ConfirmFailure | null>(null);
  const [reason, setReason] = useState("");
  const [confirming, setConfirming] = useState(false);
  const axisLabel = t(AXIS_LABEL[axis]);
  const found = progress.found;
  const left = remaining(progress);
  const reasonReady = reason.trim() !== "";
  useEffect(() => setFindError(null), [name, axis]);

  const find = async () => {
    if (!tenant) return;
    setFindError(null);
    setFinding({ pages: 0, count: 0 });
    const window: Record<string, string> = {};
    for (const [k, v] of Object.entries(resolveRange(range, Date.now()))) if (v) window[k] = v;
    let pages = 0;
    const all = await readAll<NegotiatedPage, NegotiatedSession>(
      (after) =>
        api.call("auditNegotiated", {
          params: { tenant },
          query: { axis, value: name, limit: String(limit), ...window, ...(after ? { after } : {}) },
        }),
      (p) => p.sessions,
      (n, count) => {
        pages = n;
        setFinding({ pages: n, count });
      },
    );
    setFinding(null);
    if (all.kind !== "ok") return setFindError(failure(all));
    onFound({ sessions: all.data, pages });
  };

  return (
    <div className={styles.stack}>
      <p className={styles.para}>{t("runbook.kill.lede", { axis: axisLabel, name: `⁨${name}⁩` })}</p>
      <div className={styles.actions}>
        <TimeRangePicker
          label={t("runbook.kill.window")}
          value={range}
          disabled={state === "waiting" || finding !== null}
          onChange={(r) => {
            const q: Record<string, string> = {};
            for (const [k, v] of location.search) if (!["range", "from", "to"].includes(k)) q[k] = v;
            for (const [k, v] of Object.entries(rangeQuery(r))) if (v) q[k] = v;
            navigate(location.path, { replace: true, query: q });
          }}
        />
        {canFind ? (
          <Button variant="secondary" disabled={state === "waiting"} loading={finding !== null} onClick={find} data-runbook-find="">
            {t(found ? "runbook.kill.findAgain" : "runbook.kill.find")}
          </Button>
        ) : null}
      </div>
      {!canFind ? <NotOffered permission="audit:read" /> : null}
      {finding ? (
        <p className={styles.meta} role="status">
          {t("runbook.kill.reading", { pages: finding.pages, count: finding.count })}
        </p>
      ) : null}
      {findError ? <ErrorMessage error={findError.error} kind={findError.kind} /> : null}
      {found ? (
        <div className={styles.stack} data-runbook-found={found.sessions.length}>
          <p className={styles.para}>
            {found.sessions.length
              ? t("runbook.kill.found", { count: found.sessions.length, pages: found.pages })
              : t("runbook.kill.foundNone", { axis: axisLabel, name: `⁨${name}⁩` })}
          </p>
          {found.sessions.length ? (
            <div className={`${styles.tableWrap} ${styles.sessions}`} tabIndex={0} role="region" aria-label={t("runbook.kill.sessions")}>
              <table className={styles.table}>
                <caption className={styles.visuallyHidden}>{t("runbook.kill.sessions")}</caption>
                <thead>
                  <tr>
                    <th scope="col">{t("runbook.kill.col.session")}</th>
                    <th scope="col">{t("runbook.kill.col.latest")}</th>
                    <th scope="col">{t("runbook.kill.col.state")}</th>
                  </tr>
                </thead>
                <tbody>
                  {found.sessions.map((s) => (
                    <tr key={s.session_id} data-session={s.session_id}>
                      <th scope="row">
                        <code dir="ltr" translate="no">
                          {s.session_id}
                        </code>
                      </th>
                      <td>
                        <time dateTime={f.utc(s.latest_recorded_at)} title={f.utc(s.latest_recorded_at)}>
                          {f.dateTime(s.latest_recorded_at)}
                        </time>
                      </td>
                      <td>{progress.killed.ended.includes(s.session_id) ? <StatusOf name="done" /> : <StatusOf name="pending" />}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : null}
        </div>
      ) : null}
      {found && found.sessions.length ? (
        <>
          <Field label={t("runbook.kill.reason")} hint={t("runbook.kill.reasonHint")}>
            <Textarea rows={3} value={reason} onChange={(e) => setReason(e.target.value)} disabled={left.length === 0} data-runbook-reason="" />
          </Field>
          {reasonReady ? (
            <div className={styles.stack}>
              <p className={styles.meta}>{t("runbook.kill.reasonShown")}</p>
              <p className={styles.reason} data-runbook-reason-preview="">
                {reason}
              </p>
            </div>
          ) : null}
        </>
      ) : null}
      {canKill ? (
        left.length || !found ? (
          <div className={styles.actions}>
            <Button
              variant="danger"
              disabled={state === "waiting" || !found || left.length === 0 || !reasonReady}
              onClick={() => setConfirming(true)}
              data-runbook-kill=""
            >
              {found ? t("runbook.kill.action", { count: left.length }) : t("runbook.kill.actionUnfound")}
            </Button>
          </div>
        ) : null
      ) : (
        <NotOffered permission="session:revoke" />
      )}
      {progress.killed.receipts.map((r, i) => (
        <Receipt key={r.event_id || i} receipt={r}>
          <p className={styles.para}>{t("runbook.kill.batch", { n: i + 1 })}</p>
        </Receipt>
      ))}
      <ConfirmDialog
        open={confirming}
        title={t("runbook.kill.confirm.title", { count: left.length })}
        body={
          <>
            <p className={styles.para}>{t("runbook.kill.confirm.body", { count: left.length, axis: axisLabel, name: `⁨${name}⁩` })}</p>
            <p className={styles.meta}>{t("runbook.kill.reasonShown")}</p>
            <p className={styles.reason} data-runbook-reason-confirm="">
              {reason}
            </p>
          </>
        }
        name={name}
        action={t("runbook.kill.action", { count: left.length })}
        ready={reasonReady}
        onClose={() => setConfirming(false)}
        onConfirm={async () => {
          if (!tenant) return null;
          for (const batch of batches(left)) {
            const r = await api.call<RevocationReceipt>("revocations", {
              params: { tenant },
              body: { type: "session_kill", session_ids: batch, reason },
            });
            // A batch that fails stops the run: the ones before it are ended and
            // recorded, and the confirm offers the rest again.
            if (r.kind !== "ok") return failure(r);
            onKilled(r.data, batch);
          }
          setConfirming(false);
          return null;
        }}
      />
      {state === "waiting" ? <p className={styles.meta}>{t("runbook.kill.afterInvalidate")}</p> : null}
    </div>
  );
}
