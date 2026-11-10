// EXPLAIN (0051; PLAN M4, M11, M19, M28, §5.6): the one screen that sells the
// product. A user denied access is told "access denied" and a session id —
// deliberately vague (M4) — and an operator pastes that id here and gets the
// whole story: who the subject was, which rule answered or that none did, the
// claim mapping it was taken under, the grant that decided, the obligations,
// the route and every hop, and the audit trail it came from, one link away.
//
// It reads one of the two explain routes, as the operator (decision:read):
// `…/sessions/{session}/decisions`, a session's newest decisions bounded by
// the server (M26.3, `truncated` when cut), or `…/decisions/{decision}`.
// Both are deep links (DESIGN.md §4): /console/explain/sessions/<id> and
// /console/explain/decisions/<id>, and the decision a session opens on is
// `?decision=` on the first. A deny is a decision; an unserved allow and an
// unexplainable record are outages, and none of them renders like another
// (M11).

import { useState, type FormEvent, type ReactNode } from "react";
import type { Answer } from "../api/client";
import { offered } from "../api/permit";
import type { RecordedGrant, SessionStories, Story } from "../api/types";
import { Badge, Chip } from "../components/Badge";
import { Button } from "../components/Button";
import { JsonViewer } from "../components/Code";
import { CopyableId } from "../components/CopyableId";
import { Input } from "../components/Field";
import { PageHeader } from "../components/PageHeader";
import { EmptyState, ScreenFrame, Skeleton } from "../components/States";
import { STATUS, StatusOf } from "../components/Status";
import { Timeline, TimelineSkeleton } from "../components/Timeline";
import { Tree, type TreeNode } from "../components/Tree";
import { useI18n } from "../i18n/I18n";
import { Link, navigate, useLocation } from "../router/router";
import { useLoad, type Load } from "../shell/load";
import { useDeployment } from "../shell/Shell";
import { basis, effectStatus, hopPath, inHopOrder, lookupOf, openingDecision, sessionOutcome } from "./explainModel";
import styles from "./Explain.module.css";

type By = "sessions" | "decisions";

const FORBIDDEN: Load<never> = { state: "refused", status: 403, error: { code: "forbidden" } };

/** An identifier: mono, left-to-right, never translated; `volatile` when it is minted at random (a screenshot masks it). */
function Id({ children, volatile = false }: { children: string; volatile?: boolean }) {
  return (
    <code className={styles.id} dir="ltr" translate="no" data-volatile={volatile || undefined}>
      {children}
    </code>
  );
}

/** A decision id differs on every run of a fixture, so a screenshot masks it. */
function DecisionId({ id }: { id: string }) {
  return <CopyableId value={id} volatile />;
}

function When({ at }: { at?: string }) {
  const { f } = useI18n();
  if (!at) return null;
  return (
    <time dateTime={f.utc(at)} title={f.utc(at)}>
      {f.dateTime(at)}
    </time>
  );
}

function Lookup({ initial }: { initial: string }) {
  const { t } = useI18n();
  const [id, setId] = useState(initial);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    const v = id.trim();
    if (v) navigate(`explain/${lookupOf(v)}/${encodeURIComponent(v)}`);
  };
  return (
    <form className={styles.lookup} onSubmit={submit} role="search" aria-label={t("explain.lookup.label")}>
      <label className={styles.lookupLabel} htmlFor="explain-id">
        {t("explain.lookup.label")}
      </label>
      <div className={styles.lookupRow}>
        <Input
          id="explain-id"
          mono
          dir="ltr"
          spellCheck={false}
          autoComplete="off"
          value={id}
          onChange={(e) => setId(e.target.value)}
          aria-describedby="explain-id-hint"
          className={styles.lookupInput}
        />
        <Button type="submit" variant="primary">
          {t("explain.lookup.submit")}
        </Button>
      </div>
      <p id="explain-id-hint" className={styles.hint}>
        {t("explain.lookup.hint")}
      </p>
    </form>
  );
}

export function ExplainScreen({ by, id }: { by?: By; id?: string }) {
  const { t } = useI18n();
  const { tenant, session, api } = useDeployment();
  const location = useLocation();
  const permissions = (tenant && session.scopes[tenant]?.permissions) || [];
  const may = by ? offered(by === "sessions" ? "explainSession" : "explainDecision", permissions) : false;
  const key = tenant && by && id && may ? `${tenant}|${by}|${id}` : null;
  const [load, refresh] = useLoad<SessionStories>(key, async (signal): Promise<Answer<SessionStories>> => {
    const params = { tenant: tenant as string };
    const none: SessionStories = { session_id: by === "sessions" ? (id as string) : "", decisions: [], truncated: false };
    // A 404 is an answer — this server took no decision under that id in this
    // tenant — so it is the empty state, never an error.
    const found = <T,>(a: Answer<T>) => a.kind === "refused" && a.status === 404 && a.error.code === "not_found";
    if (by === "sessions") {
      const a = await api.call<SessionStories>("explainSession", { params: { ...params, session: id as string }, signal });
      return found(a) ? { kind: "ok", status: 404, data: none } : a;
    }
    const a = await api.call<Story>("explainDecision", { params: { ...params, decision: id as string }, signal });
    if (found(a)) return { kind: "ok", status: 404, data: none };
    if (a.kind !== "ok") return a;
    // One decision is a story of one: the screen reads both the same way.
    return { ...a, data: { session_id: a.data.session_id ?? "", decisions: [a.data], truncated: false } };
  });
  const shown: Load<SessionStories> = by && id && !may ? FORBIDDEN : load;

  return (
    <div className={styles.screen}>
      <PageHeader title={t("explain.title")} lede={t("explain.lede")} />
      <Lookup key={id ?? ""} initial={id ?? ""} />
      {!by || !id ? (
        <EmptyState title={t("explain.start.title")} body={t("explain.start.body")} />
      ) : (
        <ScreenFrame
          load={shown}
          onRetry={refresh}
          isEmpty={(d) => d.decisions.length === 0}
          gaps={shown.state === "ok" && shown.data.truncated ? [t("explain.partial.truncated")] : []}
          loading={<StorySkeleton />}
          empty={<NotFound by={by} id={id} />}
        >
          {(data) => (
            <Explained
              by={by}
              id={id}
              data={data}
              selected={location.search.get("decision")}
              onSelect={(d) => navigate(location.path, { replace: true, query: { decision: d } })}
            />
          )}
        </ScreenFrame>
      )}
    </div>
  );
}

/**
 * Nothing to explain: this server took no decision under that id in this
 * tenant. A 404 is that answer, not a failure, so it is the empty state; a
 * decision id that was not one is offered as a session id.
 */
function NotFound({ by, id }: { by: By; id: string }) {
  const { t } = useI18n();
  return (
    <EmptyState
      title={t("explain.notFound.title")}
      body={by === "sessions" ? t("explain.notFound.session", { id }) : t("explain.notFound.decision", { id })}
      action={
        by === "decisions" ? (
          <Link to={`explain/sessions/${encodeURIComponent(id)}`} className={styles.link}>
            {t("explain.notFound.asSession")}
          </Link>
        ) : undefined
      }
    />
  );
}

function StorySkeleton() {
  return (
    <div className={styles.layout}>
      <div className={styles.rail}>
        <TimelineSkeleton rows={2} />
      </div>
      <div className={styles.story}>
        <Skeleton shape="title" size="l" />
        <Skeleton shape="block" size="full" />
        <Skeleton shape="block" size="full" />
      </div>
    </div>
  );
}

function Explained({
  by,
  id,
  data,
  selected,
  onSelect,
}: {
  by: By;
  id: string;
  data: SessionStories;
  selected: string | null;
  onSelect(decision: string): void;
}) {
  const { t, f } = useI18n();
  const stories = inHopOrder(data.decisions);
  const current = stories.find((s) => s.decision_id === selected) ?? stories.find((s) => s.decision_id === openingDecision(stories));
  const sessionID = by === "sessions" ? id : data.session_id;
  const outcome = by === "sessions" ? sessionOutcome(stories) : effectStatus(stories[0]?.effect ?? "");

  return (
    <>
      <section className={styles.summary} aria-labelledby="explain-summary">
        <div className={styles.summaryHead}>
          <h2 id="explain-summary" className={styles.summaryTitle}>
            {by === "sessions" ? t("explain.summary.session") : t("explain.summary.decision")}
          </h2>
          <StatusOf name={outcome} pill />
        </div>
        <dl className={styles.facts}>
          {sessionID ? (
            <div>
              <dt>{t("explain.field.session")}</dt>
              <dd>
                <CopyableId value={sessionID} />
              </dd>
            </div>
          ) : null}
          <div>
            <dt>{t("explain.field.decisions")}</dt>
            <dd>{f.number(stories.length)}</dd>
          </div>
          <div>
            <dt>{t("explain.field.subject")}</dt>
            <dd>
              <Id>{stories[0]?.inputs.subject.id || t("explain.none")}</Id>
            </dd>
          </div>
          <div>
            <dt>{t("explain.field.target")}</dt>
            <dd>
              <Id>{stories[0]?.inputs.target.hostname ?? ""}</Id>
            </dd>
          </div>
        </dl>
        {sessionID ? (
          <p className={styles.links}>
            <Link to="audit/records" query={{ session: sessionID }} className={styles.link}>
              {t("explain.link.trail")}
            </Link>
            <Link to={`audit/sessions/${encodeURIComponent(sessionID)}/replay`} className={styles.link}>
              {t("explain.link.replay")}
            </Link>
          </p>
        ) : null}
      </section>
      <div className={styles.layout}>
        <nav className={styles.rail} aria-labelledby="explain-hops">
          <h2 id="explain-hops" className={styles.railTitle}>
            {t("explain.timeline")}
          </h2>
          <Timeline
            label={t("explain.timeline")}
            selected={current?.decision_id}
            onSelect={onSelect}
            events={stories.map((s) => ({
              id: s.decision_id,
              at: s.decided_at,
              title: <Id>{s.inputs.context.proxy_id || s.proxy_id || t("explain.none")}</Id>,
              status: { tone: STATUS[effectStatus(s.effect)].tone, label: t(STATUS[effectStatus(s.effect)].label) },
              meta: s.rule ? <Id>{`${s.rule}:${s.rule_line ?? ""}`}</Id> : <span>{t(basisLabel(basis(s)))}</span>,
            }))}
          />
        </nav>
        {current ? <StoryPanel story={current} stories={stories} /> : null}
      </div>
    </>
  );
}

function basisLabel(b: ReturnType<typeof basis>) {
  return (
    {
      rule: "explain.basis.rule",
      default: "explain.basis.default",
      standing: "explain.basis.standing",
      unserved: "explain.basis.unserved",
    } as const
  )[b];
}

function Section({ id, title, children }: { id: string; title: string; children: ReactNode }) {
  return (
    <section className={styles.section} aria-labelledby={id} data-story-section={id}>
      <h3 id={id} className={styles.sectionTitle}>
        {title}
      </h3>
      {children}
    </section>
  );
}

function Fields({ children }: { children: ReactNode }) {
  return <dl className={styles.fields}>{children}</dl>;
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt>{label}</dt>
      <dd>{children}</dd>
    </>
  );
}

function Chips({ values, none }: { values?: readonly string[]; none: string }) {
  if (!values?.length) return <span className={styles.quiet}>{none}</span>;
  return (
    <span className={styles.chips}>
      {values.map((v) => (
        <Chip key={v} data>
          {v}
        </Chip>
      ))}
    </span>
  );
}

function Labels({ labels, none }: { labels?: Record<string, string>; none: string }) {
  const entries = Object.entries(labels ?? {}).sort(([a], [b]) => a.localeCompare(b));
  return <Chips values={entries.map(([k, v]) => `${k}=${v}`)} none={none} />;
}

function StoryPanel({ story: s, stories }: { story: Story; stories: readonly Story[] }) {
  const { t, f } = useI18n();
  const status = effectStatus(s.effect);
  const b = basis(s);
  const subject = s.inputs.subject;
  const target = s.inputs.target;
  const route = s.snapshot?.route_type || s.explanation.route_type;
  return (
    <article className={styles.story} aria-labelledby="explain-story" data-effect={s.effect}>
      <header className={styles.storyHead}>
        <h2 id="explain-story" className={styles.storyTitle}>
          {t("explain.story.title", { proxy: s.inputs.context.proxy_id || s.proxy_id || "" })}
        </h2>
        <StatusOf name={status} pill />
        <DecisionId id={s.decision_id} />
        <When at={s.decided_at} />
      </header>

      <Section id="explain-outcome" title={t("explain.section.outcome")}>
        <p className={styles.sentence} data-basis={b}>
          {b === "rule"
            ? t(s.effect === "allow" ? "explain.outcome.allowedBy" : "explain.outcome.deniedBy", {
                rule: s.rule ?? "",
                line: s.rule_line ?? 0,
              })
            : b === "default"
              ? t("explain.outcome.default")
              : b === "standing"
                ? t("explain.outcome.standing", { count: s.standing?.length ?? s.explanation.standing?.length ?? 0 })
                : t("explain.outcome.unserved", { reason: s.unserved ?? s.explanation.unserved ?? "" })}
        </p>
        {s.explanation.deny_reason && b !== "unserved" ? (
          <Fields>
            <Field label={t("explain.field.denyReason")}>
              {/* The rule's own reason, as its author wrote it (M21: never translated). */}
              <span translate="no">{s.explanation.deny_reason}</span>
            </Field>
          </Fields>
        ) : null}
        {s.standing?.length ? (
          <ul className={styles.list}>
            {s.standing.map((d, i) => (
              <li key={`${d.code}-${i}`} className={styles.standing}>
                <Id>{d.code}</Id>
                <span>{t("explain.standing.placed", { by: d.placed_by?.subject || d.placed_by?.principal || "" })}</span>
                <When at={d.placed_at} />
                {d.reason ? <span translate="no">{d.reason}</span> : null}
              </li>
            ))}
          </ul>
        ) : null}
      </Section>

      <Section id="explain-identity" title={t("explain.section.identity")}>
        <Fields>
          <Field label={t("explain.field.subject")}>
            <Id>{subject.id || t("explain.none")}</Id>
            {subject.known ? null : <Badge>{t("explain.subject.unknown")}</Badge>}
          </Field>
          <Field label={t("explain.field.source")}>{subject.source ? <Id>{subject.source}</Id> : t("explain.none")}</Field>
          <Field label={t("explain.field.groups")}>
            <Chips values={subject.groups} none={t("explain.none")} />
          </Field>
          <Field label={t("explain.field.authMethod")}>
            {subject.auth_method ? <Id>{subject.auth_method}</Id> : t("explain.none")}
          </Field>
          <Field label={t("explain.field.mfa")}>{subject.mfa ? t("explain.yes") : t("explain.no")}</Field>
          {subject.break_glass ? (
            <Field label={t("explain.field.breakGlass")}>
              <Badge>{t("chrome.breakGlass")}</Badge>
            </Field>
          ) : null}
        </Fields>
      </Section>

      <Section id="explain-rule" title={t("explain.section.rule")}>
        <Fields>
          <Field label={t("explain.field.rule")}>
            {s.rule ? <Id>{`${s.rule}:${s.rule_line ?? ""}`}</Id> : <span>{t(basisLabel(b))}</span>}
          </Field>
          <Field label={t("explain.field.terms")}>
            <Chips values={s.explanation.terms} none={t("explain.none")} />
          </Field>
          {s.explanation.rules_considered ? (
            <Field label={t("explain.field.considered")}>{f.number(s.explanation.rules_considered)}</Field>
          ) : null}
          {s.explanation.bundle_digest ? (
            <Field label={t("explain.field.bundle")}>
              <CopyableId value={s.explanation.bundle_digest} short={s.explanation.bundle_digest.slice(0, 19)} />
            </Field>
          ) : null}
        </Fields>
      </Section>

      <Section id="explain-mapping" title={t("explain.section.mapping")}>
        <p className={styles.sentence}>
          {s.mapping.version === 0
            ? t("explain.mapping.none")
            : s.mapping.changed
              ? t("explain.mapping.changed", { version: s.mapping.version, active: s.mapping.active })
              : t("explain.mapping.current", { version: s.mapping.version })}
        </p>
        {s.mapping.directory_source ? (
          // A directory provisioned the subject (0043): its groups are that
          // directory's, through the entries scoped to it.
          <p className={styles.sentence} data-mapping-directory="">
            {t("explain.mapping.directory", { source: `⁨${s.mapping.directory_source}⁩` })}
          </p>
        ) : null}
      </Section>

      <Section id="explain-grant" title={t("explain.section.grant")}>
        {s.grant ? <Grant grant={s.grant} /> : <p className={styles.sentence}>{t("explain.grant.none")}</p>}
      </Section>

      <Section id="explain-obligations" title={t("explain.section.obligations")}>
        <Chips values={s.obligations} none={t("explain.obligations.none")} />
      </Section>

      <Section id="explain-route" title={t("explain.section.route")}>
        <Fields>
          <Field label={t("explain.field.routeType")}>{route ? <Id>{route}</Id> : t("explain.route.none")}</Field>
          <Field label={t("explain.field.target")}>
            <Id>{target.port ? `${target.hostname}:${target.port}` : target.hostname}</Id>
            {target.known ? null : <Badge>{t("explain.target.unknown")}</Badge>}
          </Field>
          <Field label={t("explain.field.zone")}>{target.zone ? <Id>{target.zone}</Id> : t("explain.none")}</Field>
          <Field label={t("explain.field.labels")}>
            <Labels labels={target.labels} none={t("explain.none")} />
          </Field>
          {s.explanation.next_proxy_id ? (
            <Field label={t("explain.field.next")}>
              <Id>{s.explanation.next_proxy_id}</Id>
            </Field>
          ) : null}
          {s.snapshot?.permitted_channels?.length ? (
            <Field label={t("explain.field.channels")}>
              <Chips values={s.snapshot.permitted_channels} none={t("explain.none")} />
            </Field>
          ) : null}
        </Fields>
      </Section>

      <Section id="explain-hops-path" title={t("explain.section.hops")}>
        <HopTree stories={stories} />
      </Section>

      <Section id="explain-documents" title={t("explain.section.documents")}>
        <p className={styles.quiet}>{t("explain.documents.body")}</p>
        {/* The record whole, behind one disclosure: the sections above are its reading. */}
        <details className={styles.documents}>
          <summary className={styles.summaryToggle}>
            <svg className={styles.chevron} viewBox="0 0 10 10" aria-hidden="true" focusable="false">
              <path d="M3.5 2 6.5 5 3.5 8" />
            </svg>
            {t("explain.documents.show")}
          </summary>
          <JsonViewer label={t("explain.documents.inputs")} value={s.inputs} />
          <JsonViewer label={t("explain.documents.explanation")} value={s.explanation} />
          {s.snapshot && Object.keys(s.snapshot).length ? (
            <JsonViewer label={t("explain.documents.snapshot")} value={s.snapshot} />
          ) : (
            <p className={styles.quiet}>{t("explain.documents.noSnapshot")}</p>
          )}
        </details>
      </Section>
    </article>
  );
}

function Grant({ grant: g }: { grant: RecordedGrant }) {
  const { t } = useI18n();
  return (
    <Fields>
      <Field label={t("explain.grant.id")}>
        <Id volatile>{g.id}</Id>
      </Field>
      <Field label={t("explain.grant.origin")}>
        <Id>{g.origin}</Id>
      </Field>
      <Field label={t("explain.grant.window")}>
        <span className={styles.window}>
          <When at={g.not_before} />
          <span aria-hidden="true">{t("explain.grant.until")}</span>
          <When at={g.expires_at} />
        </span>
      </Field>
      {g.reason ? (
        <Field label={t("explain.grant.reason")}>
          <span translate="no">{g.reason}</span>
        </Field>
      ) : null}
      {g.created_by?.subject || g.created_by?.principal ? (
        <Field label={t("explain.grant.createdBy")}>
          <Id>{g.created_by.subject || g.created_by.principal || ""}</Id>
        </Field>
      ) : null}
      {g.approvers?.length ? (
        <Field label={t("explain.grant.approvers")}>
          <Chips values={g.approvers} none={t("explain.none")} />
        </Field>
      ) : null}
      {g.external?.system ? (
        <Field label={t("explain.grant.external")}>
          <Id>{`${g.external.system} ${g.external.reference ?? ""}`.trim()}</Id>
        </Field>
      ) : null}
    </Fields>
  );
}

/** The hop path, edge to target, with each proxy's decision where this server holds one. */
function HopTree({ stories }: { stories: readonly Story[] }) {
  const { t } = useI18n();
  const hops = hopPath(stories);
  if (hops.length === 0) return <p className={styles.quiet}>{t("explain.none")}</p>;
  let tree: TreeNode | null = null;
  for (let i = hops.length - 1; i >= 0; i--) {
    const h = hops[i] as (typeof hops)[number];
    const label =
      h.kind === "target" ? (
        <span className={styles.hop}>
          <span className={styles.quiet}>{t("explain.hop.target")}</span>
          <Id>{h.id}</Id>
        </span>
      ) : (
        <span className={styles.hop}>
          <Id>{h.id}</Id>
        </span>
      );
    const meta = h.story ? (
      <StatusOf name={effectStatus(h.story.effect)} />
    ) : h.kind === "proxy" ? (
      <span className={styles.quiet}>{h.next ? t("explain.hop.next") : t("explain.hop.noDecision")}</span>
    ) : undefined;
    tree = { id: `${h.kind}:${h.id}`, label, meta, children: tree ? [tree] : undefined };
  }
  return <Tree label={t("explain.section.hops")} nodes={tree ? [tree] : []} expandAll />;
}
