// STANDING DENIALS AND THE DIRECTORY'S FACTS (0055; PLAN M28, M11, M7, M2).
//
// A subject lock, a target quarantine and the directory's deactivation, shown
// wherever their subject or target is, and the four levers over the first two
// — lock, unlock, quarantine, release — offered to a holder of session:revoke
// and to nobody else (the API refuses anyone else, M2). Each status is a
// deny's, through the one status component, never an outage's (M11). A
// deactivation has no lever here: only the directory lifts it, and nothing on
// this screen claims to.
//
// A lock or a quarantine the server placed but could not deliver (503
// standing_denial_undelivered) is IN FORCE: the console says so, and offers
// to ask again, which is the only thing that delivers it. It is never shown
// as a failure to place it.

import { useState, type ReactNode } from "react";
import type { Answer } from "../api/client";
import type { Deactivation, DirectoryFacts, Lock, LockAnswer, Quarantine, QuarantineAnswer, ReleaseAnswer, Subject, Target, UnlockAnswer } from "../api/types";
import { Badge, Chip } from "../components/Badge";
import { Button } from "../components/Button";
import { CopyableId } from "../components/CopyableId";
import { ConfirmDialog, type ConfirmFailure } from "../components/Dialog";
import { Field, Input, Textarea } from "../components/Field";
import { Ident } from "../components/Ident";
import { StatusOf } from "../components/Status";
import { useToast } from "../components/Toast";
import type { ApiError } from "../i18n/errors";
import { useI18n } from "../i18n/I18n";
import { Link } from "../router/router";
import { useDeployment } from "../shell/Shell";
import { Instant } from "./InventoryParts";
import {
  holderText,
  isUndelivered,
  lineProblem,
  MAX_HOLDER_TEXT_BYTES,
  MAX_REASON_CODE_BYTES,
  placerOf,
  subjectDenials,
  targetDenials,
  utf8Bytes,
  type LineProblem,
} from "./standingModel";
import styles from "./Inventory.module.css";

const subjectPath = (id: string) => `inventory/subjects/${encodeURIComponent(id)}`;

// ---------------------------------------------------------------------------
// In a list: the denials as statuses, beside what else is known
// ---------------------------------------------------------------------------

/** A subject's access column: each denial standing, the lock first; nothing when none does. */
export function SubjectAccess({ subject }: { subject: Subject }) {
  const { t } = useI18n();
  const denials = subjectDenials(subject);
  if (!denials.length && !subject.record_missing) return null;
  return (
    <span className={styles.denials} data-denials={denials.join(" ")}>
      {denials.map((d) => (
        <StatusOf key={d} name={d} />
      ))}
      {subject.record_missing ? <Badge>{t("inventory.standing.recordMissing")}</Badge> : null}
    </span>
  );
}

export function TargetAccess({ target }: { target: Target }) {
  const denials = targetDenials(target);
  if (!denials.length) return null;
  return (
    <span className={styles.denials} data-denials={denials.join(" ")}>
      {denials.map((d) => (
        <StatusOf key={d} name={d} />
      ))}
    </span>
  );
}

/** Whose a directory subject is, in a cell: the directory it comes from beside that directory's id for the person. */
export function DirectoryRef({ directory }: { directory?: DirectoryFacts }) {
  if (!directory) return null;
  return (
    <span className={styles.denials} data-directory={`${directory.source}/${directory.external_id}`}>
      <Chip data>{directory.source}</Chip>
      <Ident value={directory.external_id} />
    </span>
  );
}

// ---------------------------------------------------------------------------
// Who placed it
// ---------------------------------------------------------------------------

function Placed({ by }: { by: Lock["locked_by"] }) {
  const { t } = useI18n();
  const p = placerOf(by);
  switch (p.kind) {
    case "handler":
      return <span>{t("inventory.standing.actor.handler", { provider: `⁨${p.provider}⁩` })}</span>;
    case "sync":
      return <span>{t("inventory.standing.actor.sync", { provider: `⁨${p.provider}⁩` })}</span>;
    case "person":
      return (
        <span className={styles.chips}>
          <Link to={subjectPath(p.subject)} className={styles.link}>
            <Ident value={p.subject} />
          </Link>
          {p.breakGlass ? <Badge tone="info">{t("inventory.subjects.breakGlass")}</Badge> : null}
        </span>
      );
    case "credential":
      return (
        <span className={styles.chips}>
          <Ident value={p.principal} />
          {p.breakGlass ? <Badge tone="info">{t("inventory.subjects.breakGlass")}</Badge> : null}
        </span>
      );
  }
}

/** The facts every standing denial an operator or an action places carries (0039). */
function DenialFacts({
  at,
  by,
  reasonCode,
  reason,
  reasonLabel,
  externalRef,
  actionId,
}: {
  at: string;
  by: Lock["locked_by"];
  reasonCode?: string;
  reason?: string;
  reasonLabel: string;
  externalRef?: string;
  actionId?: string;
}) {
  const { t } = useI18n();
  return (
    <dl className={styles.facts}>
      <dt>{t("inventory.standing.placedAt")}</dt>
      <dd>
        <Instant at={at} />
      </dd>
      <dt>{t("inventory.standing.placedBy")}</dt>
      <dd>
        <Placed by={by} />
      </dd>
      <dt>{t("inventory.standing.reasonCode")}</dt>
      <dd>{reasonCode ? <Ident value={reasonCode} /> : <span className={styles.quiet}>{t("inventory.none")}</span>}</dd>
      {reason ? (
        <>
          <dt>{reasonLabel}</dt>
          <dd>
            <span className={styles.operator} translate="no">
              {reason}
            </span>
          </dd>
        </>
      ) : null}
      {externalRef ? (
        <>
          <dt>{t("inventory.standing.externalRef")}</dt>
          <dd>
            <Ident value={externalRef} />
          </dd>
        </>
      ) : null}
      {actionId ? (
        <>
          <dt>{t("inventory.standing.action")}</dt>
          <dd>
            <Ident value={actionId} />
          </dd>
        </>
      ) : null}
    </dl>
  );
}

function DeactivationFacts({ deactivation }: { deactivation: Deactivation }) {
  const { t } = useI18n();
  return (
    <dl className={styles.facts}>
      <dt>{t("inventory.deactivation.at")}</dt>
      <dd>
        <Instant at={deactivation.deactivated_at} />
      </dd>
      <dt>{t("inventory.deactivation.reasonCode")}</dt>
      <dd>{deactivation.reason_code ? <Ident value={deactivation.reason_code} /> : <span className={styles.quiet}>{t("inventory.none")}</span>}</dd>
    </dl>
  );
}

function Denial({ name, title, children, actions }: { name: "locked" | "quarantined" | "deactivated"; title: string; children: ReactNode; actions?: ReactNode }) {
  return (
    <div className={styles.denial} data-denial={name}>
      <div className={styles.panelHead}>
        <h3 className={styles.denialTitle}>
          <StatusOf name={name} pill />
          <span>{title}</span>
        </h3>
        {actions ? <div className={styles.actions}>{actions}</div> : null}
      </div>
      {children}
    </div>
  );
}

// ---------------------------------------------------------------------------
// In force, not yet delivered
// ---------------------------------------------------------------------------

/**
 * A denial that stands and whose publication could not be sent: pending
 * delivery, never failed. Asking again is what sends it (M28: a publication
 * that failed is only ever repaired by asking).
 */
function Undelivered({ text, error, onRetry, busy }: { text: string; error: ApiError; onRetry(): void; busy: boolean }) {
  const { t } = useI18n();
  return (
    <div className={styles.notice} role="status" data-undelivered="">
      <div className={styles.panelHead}>
        <span className={styles.denials}>
          <StatusOf name="pending" />
          <span>{text}</span>
        </span>
        <Button onClick={onRetry} loading={busy} data-retry-delivery="">
          {t("inventory.standing.undelivered.retry")}
        </Button>
      </div>
      {error.correlation_id ? <CopyableId value={error.correlation_id} /> : null}
    </div>
  );
}

/** A lever's call, and what its answer says: done, in force and undelivered, or refused. */
function useLever<T>() {
  const [undelivered, setUndelivered] = useState<{ error: ApiError; retry(): Promise<void> } | null>(null);
  const [busy, setBusy] = useState(false);
  return {
    undelivered,
    busy,
    clear: () => setUndelivered(null),
    /**
     * Runs a placement. Placed, or in force and undelivered, it is done with
     * (`after`: the dialog closes and the page reads again); undelivered also
     * leaves the notice, with a retry that sends the same call. A refusal is
     * returned, for the dialog to show where the operator is.
     */
    place: async (send: () => Promise<Answer<T>>, done: (data: T) => void, after: () => void): Promise<ConfirmFailure | null> => {
      const a = await send();
      if (a.kind === "ok") {
        setUndelivered(null);
        done(a.data);
        after();
        return null;
      }
      if (isUndelivered(a.error)) {
        const retry = async () => {
          setBusy(true);
          const again = await send();
          setBusy(false);
          if (again.kind === "ok") {
            setUndelivered(null);
            done(again.data);
          } else if (isUndelivered(again.error)) setUndelivered({ error: again.error, retry });
          else setUndelivered(null);
          after();
        };
        setUndelivered({ error: a.error, retry });
        after();
        return null;
      }
      return { error: a.error, kind: a.kind };
    },
  };
}

// ---------------------------------------------------------------------------
// A subject's standing denials: the lock, and the directory's deactivation
// ---------------------------------------------------------------------------

export function SubjectStanding({ subject, canRevoke, onChanged }: { subject: Subject; canRevoke: boolean; onChanged(): void }) {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const toast = useToast();
  const [dialog, setDialog] = useState<"lock" | "unlock" | null>(null);
  const lever = useLever<LockAnswer>();
  const params = { tenant: tenant as string, subject: subject.id };

  const lock = (body: { reason: string; reason_code: string }) =>
    lever.place(
      () => api.call<LockAnswer>("subjectLock", { params, body }),
      (a) => toast.success(t(a.placed ? "inventory.lock.placed" : "inventory.lock.already", { id: subject.id })),
      () => {
        setDialog(null);
        onChanged();
      },
    );
  const unlock = async (): Promise<ConfirmFailure | null> => {
    const a = await api.call<UnlockAnswer>("subjectUnlock", { params, body: {} });
    if (a.kind !== "ok") return { error: a.error, kind: a.kind };
    setDialog(null);
    toast.success(t(a.data.unlocked ? "inventory.unlock.done" : "inventory.unlock.none", { id: subject.id }));
    onChanged();
    return null;
  };

  const none = !subject.locked && !subject.deactivated;
  return (
    <section className={styles.panel} aria-labelledby="subject-standing" data-standing={subjectDenials(subject).join(" ") || "none"}>
      <div className={styles.panelHead}>
        <h2 id="subject-standing" className={styles.panelTitle}>
          {t("inventory.standing.title")}
        </h2>
        {canRevoke && !subject.locked ? (
          <Button variant="danger" onClick={() => setDialog("lock")} data-lock="">
            {t("inventory.lock.action")}
          </Button>
        ) : null}
      </div>
      <p className={styles.quiet}>{t("inventory.standing.lede.subject")}</p>
      {lever.undelivered ? (
        <Undelivered
          text={t("inventory.standing.undelivered.lock", { id: `⁨${subject.id}⁩` })}
          error={lever.undelivered.error}
          onRetry={() => void lever.undelivered?.retry()}
          busy={lever.busy}
        />
      ) : null}
      {subject.record_missing ? <p className={styles.notice}>{t("inventory.standing.recordMissing.body")}</p> : null}
      {none ? <p className={styles.para}>{t("inventory.standing.none.subject")}</p> : null}
      {subject.lock ? (
        <Denial
          name="locked"
          title={t("inventory.lock.heading")}
          actions={
            canRevoke ? (
              <Button onClick={() => setDialog("unlock")} data-unlock="">
                {t("inventory.unlock.action")}
              </Button>
            ) : null
          }
        >
          <DenialFacts
            at={subject.lock.locked_at}
            by={subject.lock.locked_by}
            reasonCode={subject.lock.reason_code}
            reason={subject.lock.reason}
            reasonLabel={t("inventory.standing.holderText")}
            externalRef={subject.lock.external_ref}
            actionId={subject.lock.action_id}
          />
        </Denial>
      ) : null}
      {subject.deactivation ? (
        <Denial name="deactivated" title={t("inventory.deactivation.heading")}>
          <DeactivationFacts deactivation={subject.deactivation} />
          <p className={styles.quiet} data-deactivation-only="">
            {t("inventory.deactivation.only")}
          </p>
        </Denial>
      ) : null}
      <LockDialog open={dialog === "lock"} subject={subject} onClose={() => setDialog(null)} onConfirm={lock} />
      <ConfirmDialog
        open={dialog === "unlock"}
        title={t("inventory.unlock.title")}
        body={
          <>
            <p className={styles.para}>{t("inventory.unlock.body", { id: subject.id })}</p>
            {subject.deactivated ? (
              <p className={styles.para} data-unlock-deactivated="">
                {t("inventory.unlock.deactivated")}
              </p>
            ) : null}
          </>
        }
        name={subject.id}
        action={t("inventory.unlock.confirm")}
        hint={t("inventory.unlock.hint")}
        onConfirm={unlock}
        onClose={() => setDialog(null)}
      />
    </section>
  );
}

function problemText(t: ReturnType<typeof useI18n>["t"], problem: LineProblem | null, bytes: number, limit: number): string | undefined {
  if (problem === "control") return t("inventory.lock.problem.control");
  if (problem === "too_long") return t("inventory.lock.problem.tooLong", { bytes, limit });
  return undefined;
}

/**
 * Locking asks for the text the holder is shown and shows it before it is
 * sent: it is displayed verbatim as their sessions end, so what the operator
 * types is what a person reads in their terminal (0039).
 */
function LockDialog({
  open,
  subject,
  onClose,
  onConfirm,
}: {
  open: boolean;
  subject: Subject;
  onClose(): void;
  onConfirm(body: { reason: string; reason_code: string }): Promise<ConfirmFailure | null>;
}) {
  const { t } = useI18n();
  const [reason, setReason] = useState("");
  const [code, setCode] = useState("");
  const reasonProblem = lineProblem(reason, MAX_HOLDER_TEXT_BYTES);
  const codeProblem = lineProblem(code, MAX_REASON_CODE_BYTES);
  const shown = holderText(reason);
  const confirm = async (): Promise<ConfirmFailure | null> => {
    const failure = await onConfirm({ reason: reason.trim(), reason_code: code.trim() });
    if (!failure) {
      setReason("");
      setCode("");
    }
    return failure;
  };
  return (
    <ConfirmDialog
      open={open}
      title={t("inventory.lock.title")}
      body={
        <>
          <p className={styles.para}>{t("inventory.lock.body", { id: subject.id })}</p>
          {subject.break_glass ? <p className={styles.para}>{t("inventory.lock.breakGlass")}</p> : null}
          <Field
            label={t("inventory.lock.field.reason")}
            hint={t("inventory.lock.field.reason.hint", { limit: MAX_HOLDER_TEXT_BYTES })}
            error={problemText(t, reasonProblem, utf8Bytes(reason.trim()), MAX_HOLDER_TEXT_BYTES)}
          >
            <Input value={reason} onChange={(e) => setReason(e.target.value)} name="reason" translate="no" autoComplete="off" />
          </Field>
          <Field
            label={t("inventory.lock.field.reasonCode")}
            hint={t("inventory.lock.field.reasonCode.hint")}
            error={problemText(t, codeProblem, utf8Bytes(code.trim()), MAX_REASON_CODE_BYTES)}
          >
            <Input mono value={code} onChange={(e) => setCode(e.target.value)} name="reason_code" autoComplete="off" />
          </Field>
          <figure className={styles.preview} data-holder-preview="">
            <figcaption className={styles.previewLabel}>{shown.isDefault ? t("inventory.lock.preview.default") : t("inventory.lock.preview")}</figcaption>
            <blockquote className={styles.previewText} translate="no">
              {shown.text}
            </blockquote>
          </figure>
        </>
      }
      name={subject.id}
      action={t("inventory.lock.confirm")}
      hint={t("inventory.lock.hint")}
      ready={!reasonProblem && !codeProblem}
      onConfirm={confirm}
      onClose={onClose}
    />
  );
}

// ---------------------------------------------------------------------------
// A target's quarantine
// ---------------------------------------------------------------------------

export function TargetStanding({ target, canRevoke, onChanged }: { target: Target; canRevoke: boolean; onChanged(): void }) {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const toast = useToast();
  const [dialog, setDialog] = useState<"quarantine" | "release" | null>(null);
  const [reason, setReason] = useState("");
  const [code, setCode] = useState("");
  const lever = useLever<QuarantineAnswer>();
  const params = { tenant: tenant as string, target: target.id };

  const quarantine = () =>
    lever.place(
      () => api.call<QuarantineAnswer>("targetQuarantine", { params, body: { reason: reason.trim(), reason_code: code.trim() } }),
      (a) => {
        setReason("");
        setCode("");
        toast.success(t(a.placed ? "inventory.quarantine.placed" : "inventory.quarantine.already", { id: target.id }));
      },
      () => {
        setDialog(null);
        onChanged();
      },
    );
  const release = async (): Promise<ConfirmFailure | null> => {
    const a = await api.call<ReleaseAnswer>("targetRelease", { params, body: {} });
    if (a.kind !== "ok") return { error: a.error, kind: a.kind };
    setDialog(null);
    toast.success(t(a.data.released ? "inventory.release.done" : "inventory.release.none", { id: target.id }));
    onChanged();
    return null;
  };

  const q: Quarantine | undefined = target.quarantine;
  return (
    <section className={styles.panel} aria-labelledby="target-standing" data-standing={target.quarantined ? "quarantined" : "none"}>
      <div className={styles.panelHead}>
        <h2 id="target-standing" className={styles.panelTitle}>
          {t("inventory.standing.title")}
        </h2>
        {canRevoke && !target.quarantined ? (
          <Button variant="danger" onClick={() => setDialog("quarantine")} data-quarantine="">
            {t("inventory.quarantine.action")}
          </Button>
        ) : null}
      </div>
      <p className={styles.quiet}>{t("inventory.standing.lede.target")}</p>
      {lever.undelivered ? (
        <Undelivered
          text={t("inventory.standing.undelivered.quarantine", { id: `⁨${target.id}⁩` })}
          error={lever.undelivered.error}
          onRetry={() => void lever.undelivered?.retry()}
          busy={lever.busy}
        />
      ) : null}
      {q ? (
        <Denial
          name="quarantined"
          title={t("inventory.quarantine.heading")}
          actions={
            canRevoke ? (
              <Button onClick={() => setDialog("release")} data-release="">
                {t("inventory.release.action")}
              </Button>
            ) : null
          }
        >
          <DenialFacts
            at={q.quarantined_at}
            by={q.quarantined_by}
            reasonCode={q.reason_code}
            reason={q.reason}
            reasonLabel={t("inventory.standing.reason")}
            externalRef={q.external_ref}
            actionId={q.action_id}
          />
          <p className={styles.quiet}>{t("inventory.quarantine.sessionsRunning")}</p>
        </Denial>
      ) : (
        <p className={styles.para}>{t("inventory.standing.none.target")}</p>
      )}
      <ConfirmDialog
        open={dialog === "quarantine"}
        title={t("inventory.quarantine.title")}
        body={
          <>
            <p className={styles.para}>{t("inventory.quarantine.body", { id: target.id, hostname: target.hostname })}</p>
            <p className={styles.para} data-quarantine-sessions="">
              {t("inventory.quarantine.sessions")}
            </p>
            <CopyableId value={`POST /api/v1/tenants/${tenant ?? ""}/revocations`} />
            <Field label={t("inventory.quarantine.field.reason")} hint={t("inventory.quarantine.field.reason.hint")}>
              <Textarea rows={2} value={reason} onChange={(e) => setReason(e.target.value)} name="reason" translate="no" />
            </Field>
            <Field label={t("inventory.lock.field.reasonCode")} hint={t("inventory.lock.field.reasonCode.hint")}>
              <Input mono value={code} onChange={(e) => setCode(e.target.value)} name="reason_code" autoComplete="off" />
            </Field>
          </>
        }
        name={target.id}
        action={t("inventory.quarantine.confirm")}
        hint={t("inventory.quarantine.hint")}
        onConfirm={quarantine}
        onClose={() => setDialog(null)}
      />
      <ConfirmDialog
        open={dialog === "release"}
        title={t("inventory.release.title")}
        body={<p className={styles.para}>{t("inventory.release.body", { id: target.id })}</p>}
        name={target.id}
        action={t("inventory.release.confirm")}
        hint={t("inventory.release.hint")}
        onConfirm={release}
        onClose={() => setDialog(null)}
      />
    </section>
  );
}

// ---------------------------------------------------------------------------
// The directory's facts about a subject it manages
// ---------------------------------------------------------------------------

export function DirectoryPanel({ subject }: { subject: Subject }) {
  const { t } = useI18n();
  const d = subject.directory;
  if (!d) return null;
  return (
    <section className={styles.panel} aria-labelledby="subject-directory" data-directory-panel="">
      <h2 id="subject-directory" className={styles.panelTitle}>
        {t("inventory.directory.title")}
      </h2>
      <p className={styles.quiet}>{t("inventory.directory.lede")}</p>
      <dl className={styles.facts}>
        <dt>{t("inventory.directory.provider")}</dt>
        <dd>
          <Ident value={d.provider} />
        </dd>
        <dt>{t("inventory.directory.source")}</dt>
        <dd>
          <Ident value={d.source} />
        </dd>
        <dt>{t("inventory.directory.externalId")}</dt>
        <dd>
          <Ident value={d.external_id} />
        </dd>
      </dl>
      <p className={styles.notice} data-directory-managed="">
        {t("inventory.directory.noEdit")}
      </p>
    </section>
  );
}
