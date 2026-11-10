// AUDIT (0051; PLAN M8, M11, M19, M26, §7): the trail, queried.
//
// Two views under /console/audit/, each a deep link whose every filter is in
// its URL (DESIGN.md §4):
//   - records, /console/audit/records?subject=…: the general query
//     (`GET …/audit/records`) by subject, target, session, decision, the
//     acting principal, kind, severity and time, newest first unless
//     `order=asc`; a record opens in a drawer at `?record=<id>`, read by id
//     (`GET …/audit/records/{record}`), and links to its explanation;
//   - blocked commands, /console/audit/blocked-commands?label=env%3Dprod: the
//     showcase join (`GET …/audit/blocked-commands`), each blocked command with
//     the decision that let the session in.
// A session's replay is Replay.tsx. Every list is one page of a bounded list
// (M26): it moves by the answer's own `next`, says when the answer was cut,
// and never reads as the whole tenant.
//
// A supervisor's acts (M19) are the records naming its `sup-` principal —
// `?principal=`, which the chrome's supervised badge opens. Where a record
// carries `acting_operator` (0048), the name is shown beside the principal as
// the supervisor's word: not a subject, linked to nothing, filtered on by
// nothing.

import { useState, type ReactNode } from "react";
import { offered } from "../api/permit";
import type { AuditRecord, AuditRecordPage, BlockedPage, BlockedCommand } from "../api/types";
import { Chip } from "../components/Badge";
import { Button } from "../components/Button";
import { JsonViewer } from "../components/Code";
import { CopyableId } from "../components/CopyableId";
import { DataTable, TableSkeleton, type Column, type TableSort } from "../components/DataTable";
import { Drawer } from "../components/Dialog";
import { Select } from "../components/Field";
import { FilterBar, type FilterField, type SavedView } from "../components/FilterBar";
import { PageHeader } from "../components/PageHeader";
import { Pagination, type PageState } from "../components/Pagination";
import { EmptyState, ErrorMessage, ScreenFrame, Skeleton } from "../components/States";
import { rangeQuery, readRange, resolveRange, TimeRangePicker, type TimeRange } from "../components/TimeRange";
import { useI18n } from "../i18n/I18n";
import { loadViews, operatorKey, saveViews } from "../prefs/prefs";
import { Link, navigate, useLocation } from "../router/router";
import { useLoad, type Load } from "../shell/load";
import { useDeployment } from "../shell/Shell";
import { AuditCounts } from "./AuditCounts";
import { useKindLabel, useSeverityLabel } from "./auditLabels";
import {
  actorOf,
  KINDS,
  knownAttributes,
  readLimit,
  readRecordFilters,
  recordsQuery,
  SEVERITIES,
  type RecordFilters,
} from "./auditModel";
import { ViewLinks } from "../components/ViewLinks";
import styles from "./Audit.module.css";

const FORBIDDEN: Load<never> = { state: "refused", status: 403, error: { code: "forbidden" } };

/** An identifier: mono, left-to-right, never translated. */
export function Id({ children, volatile = false }: { children: string; volatile?: boolean }) {
  return (
    <code className={styles.id} dir="ltr" translate="no" data-volatile={volatile || undefined}>
      {children}
    </code>
  );
}

export function When({ at }: { at?: string }) {
  const { f } = useI18n();
  if (!at) return null;
  return (
    <time dateTime={f.utc(at)} title={f.utc(at)}>
      {f.dateTime(at)}
    </time>
  );
}

/** The views of the audit screen, as links: each is its own deep link. */
export function AuditViews({ current }: { current: "records" | "blocked" | "replay" }) {
  const { t } = useI18n();
  return (
    <ViewLinks
      label={t("audit.views")}
      current={current}
      views={[
        { id: "records", to: "audit/records", label: t("audit.view.records") },
        { id: "blocked", to: "audit/blocked-commands", label: t("audit.view.blocked") },
      ]}
    />
  );
}

/**
 * Who acted: the principal this server attributes the act to, and — beside
 * it, never in its place — a supervisor's word for whom it acted (M19).
 */
export function Actor({ record }: { record: AuditRecord }) {
  const { t } = useI18n();
  const a = actorOf(record);
  if (!a.principal) return <span className={styles.quiet}>{t("audit.actor.none")}</span>;
  return (
    <span className={styles.actor}>
      <Id>{a.principal}</Id>
      {a.acting ? (
        <span className={styles.word} data-acting-operator>
          <span className={styles.wordLabel}>{t("audit.actor.word")}</span>
          <span className={styles.wordName} translate="no">
            {a.acting}
          </span>
        </span>
      ) : null}
    </span>
  );
}

/** The query keys a screen's URL owns, set or cleared, the rest kept. */
function setQuery(location: ReturnType<typeof useLocation>, next: Record<string, string | undefined>) {
  const q: Record<string, string> = {};
  for (const [k, v] of location.search) q[k] = v;
  for (const [k, v] of Object.entries(next)) {
    if (v) q[k] = v;
    else delete q[k];
  }
  navigate(location.path, { replace: true, query: q });
}

/**
 * The page trail and the instant a relative range resolves against, both
 * reset whenever the selection changes: a cursor binds the selection it was
 * minted under (M26.2), and "the last hour" means the hour before you asked.
 */
function useSelectionState(selection: string, limit: number) {
  const [state, setState] = useState(() => ({ selection, trail: [] as string[], at: Date.now() }));
  let current = state;
  if (state.selection !== selection) {
    current = { selection, trail: [], at: Date.now() };
    setState(current);
  }
  const page: PageState = { trail: current.trail, limit };
  return {
    page,
    at: current.at,
    onPage: (p: PageState) => setState((s) => ({ ...s, trail: [...p.trail] })),
    reanchor: () => setState((s) => ({ ...s, trail: [], at: Date.now() })),
  };
}

export function AuditRecordsScreen() {
  const { t } = useI18n();
  const { tenant, session, api, supervisors } = useDeployment();
  const location = useLocation();
  const kindLabel = useKindLabel();
  const severityLabel = useSeverityLabel();
  const filters = readRecordFilters(location.search);
  const range = readRange(location.search);
  const ascending = location.search.get("order") === "asc";
  const limit = readLimit(location.search);
  const selection = JSON.stringify([filters, range, ascending, limit]);
  const { page, at, onPage, reanchor } = useSelectionState(selection, limit);
  const [sort, setSort] = useState<TableSort | null>(null);
  const [hidden, setHidden] = useState<string[]>([]);
  const [views, setViews] = useState<SavedView[]>(() => loadViews(operatorKey(session), "audit"));
  const permissions = (tenant && session.scopes[tenant]?.permissions) || [];
  const may = offered("auditRecords", permissions);
  const after = page.trail[page.trail.length - 1];
  const query = recordsQuery(filters, resolveRange(range, at), { limit, after, ascending });
  const key = tenant && may ? `${tenant}|${query.toString()}` : null;
  const [load, refresh] = useLoad<AuditRecordPage>(key, (signal) =>
    api.call("auditRecords", { params: { tenant: tenant as string }, query, signal }),
  );
  const shown = may ? load : FORBIDDEN;
  const open = location.search.get("record");
  const supervisor = filters.principal ? supervisors.find((s) => s.principal_id === filters.principal) : undefined;
  const filtered = Object.keys(filters).length > 0 || range.kind !== "all";

  const fields: FilterField[] = [
    { id: "subject", label: t("audit.col.subject"), kind: "text", mono: true },
    { id: "target", label: t("audit.col.target"), kind: "text", mono: true },
    { id: "session", label: t("audit.col.session"), kind: "text", mono: true },
    { id: "decision", label: t("audit.col.decision"), kind: "text", mono: true },
    { id: "principal", label: t("audit.col.actor"), kind: "text", mono: true },
    { id: "kind", label: t("audit.col.kind"), options: KINDS.map((k) => ({ value: k, label: kindLabel(k) })) },
    { id: "severity", label: t("audit.col.severity"), options: SEVERITIES.map((s) => ({ value: s, label: severityLabel(s) })) },
  ];

  const headers = [
    t("audit.col.recorded"),
    t("audit.col.kind"),
    t("audit.col.severity"),
    t("audit.col.actor"),
    t("audit.col.what"),
    t("audit.col.subject"),
    t("audit.col.target"),
    t("audit.col.session"),
    t("audit.col.decision"),
  ];

  const columns: Column<AuditRecord>[] = [
    {
      id: "recorded",
      header: headers[0] as string,
      sortValue: (r) => r.recorded_at,
      cell: (r) => (
        <button type="button" className={styles.open} onClick={() => setQuery(location, { record: r.record_id })}>
          <When at={r.recorded_at} />
        </button>
      ),
    },
    { id: "kind", header: headers[1] as string, sortValue: (r) => r.kind, cell: (r) => kindLabel(r.kind) },
    {
      id: "severity",
      header: headers[2] as string,
      sortValue: (r) => SEVERITIES.indexOf(r.severity as (typeof SEVERITIES)[number]),
      cell: (r) => (
        <span className={styles.severity} data-severity={r.severity}>
          {severityLabel(r.severity)}
        </span>
      ),
    },
    { id: "actor", header: headers[3] as string, cell: (r) => <Actor record={r} /> },
    {
      id: "what",
      header: headers[4] as string,
      cell: (r) => (
        <span className={styles.what}>
          {r.event ? <Id>{r.event}</Id> : null}
          {r.message ? (
            <span className={styles.message} translate="no">
              {r.message}
            </span>
          ) : null}
        </span>
      ),
    },
    { id: "subject", header: headers[5] as string, sortValue: (r) => r.subject ?? "", cell: (r) => (r.subject ? <Id>{r.subject}</Id> : null) },
    { id: "target", header: headers[6] as string, sortValue: (r) => r.target ?? "", cell: (r) => (r.target ? <Id>{r.target}</Id> : null) },
    {
      id: "session",
      header: headers[7] as string,
      cell: (r) =>
        r.session_id ? (
          <Link to={`explain/sessions/${encodeURIComponent(r.session_id)}`} className={styles.link} data-explain="session">
            <Id>{r.session_id}</Id>
          </Link>
        ) : (
          <span className={styles.quiet}>{t("audit.session.none")}</span>
        ),
    },
    {
      id: "decision",
      header: headers[8] as string,
      cell: (r) =>
        r.decision_id ? (
          <Link to={`explain/decisions/${encodeURIComponent(r.decision_id)}`} className={styles.link} data-explain="decision">
            <Id volatile>{r.decision_id}</Id>
          </Link>
        ) : null,
    },
  ];

  return (
    <div className={styles.screen}>
      <PageHeader
        title={t("audit.title")}
        lede={t("audit.lede")}
        actions={
          <Button
            onClick={() => {
              reanchor();
              refresh();
            }}
          >
            {t("audit.refresh")}
          </Button>
        }
      />
      <AuditViews current="records" />
      <FilterBar
        label={t("audit.filters")}
        fields={fields}
        values={filters as Record<string, string>}
        onChange={(v) => {
          const next: Record<string, string | undefined> = {};
          for (const f of fields) next[f.id] = v[f.id];
          setQuery(location, next);
        }}
        views={{
          saved: views,
          onApply: (v) => navigate(location.path, { replace: true, query: Object.fromEntries(new URLSearchParams(v.query)) }),
          onSave: () => {
            const q = new URLSearchParams(location.search);
            q.delete("record");
            const query = q.toString();
            const name = query ? decodeURIComponent(query.replaceAll("&", " · ")) : t("audit.view.all");
            const next = [...views.filter((x) => x.name !== name), { name, query }];
            setViews(next);
            saveViews(operatorKey(session), "audit", next);
          },
          onForget: () => {
            setViews([]);
            saveViews(operatorKey(session), "audit", []);
          },
        }}
        extra={
          <>
            <TimeRangePicker
              key={JSON.stringify(range)}
              label={t("audit.filter.time")}
              value={range}
              onChange={(r: TimeRange) => setQuery(location, rangeQuery(r))}
            />
            <Order ascending={ascending} onChange={(asc) => setQuery(location, { order: asc ? "asc" : undefined })} />
          </>
        }
      />
      {filters.principal ? <PrincipalNotice principal={filters.principal} name={supervisor?.display_name} /> : null}
      <ScreenFrame
        load={shown}
        onRetry={refresh}
        isEmpty={(d) => d.records.length === 0 && page.trail.length === 0}
        loading={
          <>
            <Skeleton shape="block" size="full" />
            <TableSkeleton headers={headers} />
          </>
        }
        empty={
          filtered ? (
            <EmptyState
              title={t("audit.records.filteredEmpty.title")}
              body={t("audit.records.filteredEmpty.body")}
              action={
                <Link to="audit/records" className={styles.link}>
                  {t("audit.records.clear")}
                </Link>
              }
            />
          ) : (
            <EmptyState title={t("audit.records.empty.title")} body={t("audit.records.empty.body")} />
          )
        }
      >
        {(data) => (
          <>
            <AuditCounts records={data.records} truncated={data.truncated} />
            <DataTable
              caption={t("audit.records.caption")}
              columns={columns}
              rows={data.records}
              rowKey={(r) => r.record_id}
              rowLabel={(r) => r.record_id}
              sort={sort}
              onSort={setSort}
              hidden={hidden}
              onHidden={setHidden}
              partialNote={data.truncated ? t("audit.onePage") : undefined}
              empty={<EmptyState title={t("audit.records.filteredEmpty.title")} body={t("audit.records.filteredEmpty.body")} />}
              rowActions={(r) => [
                { id: "open", label: t("audit.action.open"), onSelect: () => setQuery(location, { record: r.record_id }) },
                ...(r.session_id
                  ? [
                      { id: "explain", label: t("audit.action.explainSession"), onSelect: () => navigate(`explain/sessions/${encodeURIComponent(r.session_id)}`) },
                      { id: "replay", label: t("audit.action.replay"), onSelect: () => navigate(`audit/sessions/${encodeURIComponent(r.session_id)}/replay`) },
                      { id: "only", label: t("audit.action.onlySession"), onSelect: () => setQuery(location, { session: r.session_id }) },
                    ]
                  : []),
              ]}
            />
            <Pagination page={page} shown={data.records.length} truncated={data.truncated} next={data.next} onPage={onPage} />
          </>
        )}
      </ScreenFrame>
      <RecordDrawer id={open} onClose={() => setQuery(location, { record: undefined })} />
    </div>
  );
}

function Order({ ascending, onChange }: { ascending: boolean; onChange(asc: boolean): void }) {
  const { t } = useI18n();
  return (
    <span className={styles.control}>
      <label className={styles.controlLabel} htmlFor="audit-order">
        {t("audit.filter.order")}
      </label>
      <Select id="audit-order" value={ascending ? "asc" : "desc"} onChange={(e) => onChange(e.target.value === "asc")}>
        <option value="desc">{t("audit.order.newest")}</option>
        <option value="asc">{t("audit.order.oldest")}</option>
      </Select>
    </span>
  );
}

/** What the list is when it is one actor's acts: said in words, with what the supervisor's word is and is not. */
function PrincipalNotice({ principal, name }: { principal: string; name?: string }) {
  const { t } = useI18n();
  const supervisor = principal.startsWith("sup-");
  return (
    <section className={styles.notice} aria-labelledby="audit-principal" data-principal-notice>
      <h2 id="audit-principal" className={styles.noticeTitle}>
        {supervisor ? t("audit.principal.supervisor.title") : t("audit.principal.title")}
      </h2>
      <p className={styles.noticeBody}>
        <Id>{principal}</Id>
        {name ? <span translate="no">{name}</span> : null}
      </p>
      {supervisor ? <p className={styles.noticeBody}>{t("audit.principal.supervisor.body")}</p> : null}
    </section>
  );
}

/** One record, read by id: every field, its place in its chain, and the body the hash covers. */
function RecordDrawer({ id, onClose }: { id: string | null; onClose(): void }) {
  const { t } = useI18n();
  const { tenant, session, api } = useDeployment();
  const permissions = (tenant && session.scopes[tenant]?.permissions) || [];
  const key = tenant && id && offered("auditRecord", permissions) ? `${tenant}|${id}` : null;
  const [load] = useLoad<AuditRecord>(key, (signal) =>
    api.call("auditRecord", { params: { tenant: tenant as string, record: id as string }, signal }),
  );
  return (
    <Drawer open={Boolean(id)} title={t("audit.record.title")} onClose={onClose}>
      {!id ? null : load.state === "ok" ? (
        <RecordDetail record={load.data} />
      ) : load.state === "loading" ? (
        <div className={styles.drawerBody} aria-busy="true">
          <Skeleton shape="title" size="l" />
          <Skeleton shape="block" size="full" />
        </div>
      ) : (
        <ErrorMessage error={load.error} kind={load.state} />
      )}
    </Drawer>
  );
}

function Detail({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt>{label}</dt>
      <dd>{children}</dd>
    </>
  );
}

function RecordDetail({ record: r }: { record: AuditRecord }) {
  const { t, f } = useI18n();
  const kindLabel = useKindLabel();
  const severityLabel = useSeverityLabel();
  let body: unknown = r.body;
  try {
    body = JSON.parse(r.body);
  } catch {
    // A body that is not JSON is shown as the text it is.
  }
  return (
    <div className={styles.drawerBody} data-record={r.record_id}>
      <dl className={styles.fields}>
        <Detail label={t("audit.record.id")}>
          <CopyableId value={r.record_id} />
        </Detail>
        <Detail label={t("audit.col.recorded")}>
          <When at={r.recorded_at} />
        </Detail>
        <Detail label={t("audit.record.received")}>
          <When at={r.received_at} />
        </Detail>
        <Detail label={t("audit.col.kind")}>
          <span className={styles.what}>
            {kindLabel(r.kind)}
            <Id>{r.kind}</Id>
          </span>
        </Detail>
        <Detail label={t("audit.col.severity")}>
          <span className={styles.severity} data-severity={r.severity}>
            {severityLabel(r.severity)}
          </span>
        </Detail>
        {r.event ? (
          <Detail label={t("audit.record.event")}>
            <Id>{r.event}</Id>
          </Detail>
        ) : null}
        {r.message ? (
          <Detail label={t("audit.record.message")}>
            <span translate="no">{r.message}</span>
          </Detail>
        ) : null}
        <Detail label={t("audit.col.actor")}>
          <Actor record={r} />
        </Detail>
        {r.subject ? (
          <Detail label={t("audit.col.subject")}>
            <Id>{r.subject}</Id>
          </Detail>
        ) : null}
        {r.target ? (
          <Detail label={t("audit.col.target")}>
            <Id>{r.target}</Id>
          </Detail>
        ) : null}
        <Detail label={t("audit.col.session")}>
          {r.session_id ? (
            <span className={styles.links}>
              <Link to={`explain/sessions/${encodeURIComponent(r.session_id)}`} className={styles.link}>
                <Id>{r.session_id}</Id>
              </Link>
              <Link to={`audit/sessions/${encodeURIComponent(r.session_id)}/replay`} className={styles.link}>
                {t("audit.action.replay")}
              </Link>
            </span>
          ) : (
            <span className={styles.quiet}>{t("audit.session.none")}</span>
          )}
        </Detail>
        {r.decision_id ? (
          <Detail label={t("audit.col.decision")}>
            <Link to={`explain/decisions/${encodeURIComponent(r.decision_id)}`} className={styles.link} data-explain="decision">
              {t("audit.record.explain")}
            </Link>
            <Id volatile>{r.decision_id}</Id>
          </Detail>
        ) : null}
        {r.proxy_id ? (
          <Detail label={t("audit.record.proxy")}>
            <Id>{r.proxy_id}</Id>
          </Detail>
        ) : null}
        <Detail label={t("audit.record.chain")}>
          <span className={styles.what}>
            <Id>{r.stream}</Id>
            <span>{t("audit.record.seq", { seq: f.number(r.chain_seq) })}</span>
          </span>
        </Detail>
        <Detail label={t("audit.record.hash")}>
          <Id volatile>{r.hash}</Id>
        </Detail>
      </dl>
      {knownAttributes(r).length ? (
        <section aria-labelledby="audit-record-attributes" className={styles.drawerSection}>
          <h3 id="audit-record-attributes" className={styles.drawerTitle}>
            {t("audit.record.attributes")}
          </h3>
          <dl className={styles.fields}>
            {knownAttributes(r).map(([k, v]) => (
              <Detail key={k} label={k}>
                {k === "acting_operator" ? (
                  <span className={styles.word}>
                    <span className={styles.wordLabel}>{t("audit.actor.word")}</span>
                    <span className={styles.wordName} translate="no">
                      {v}
                    </span>
                  </span>
                ) : (
                  <Id>{v}</Id>
                )}
              </Detail>
            ))}
          </dl>
        </section>
      ) : null}
      <JsonViewer label={t("audit.record.body")} value={body} />
    </div>
  );
}

// ---------------------------------------------------------------------------
// blocked commands
// ---------------------------------------------------------------------------

export function BlockedCommandsScreen() {
  const { t, f } = useI18n();
  const { tenant, session, api } = useDeployment();
  const location = useLocation();
  const label = location.search.get("label")?.trim() || "";
  const range = readRange(location.search);
  const ascending = location.search.get("order") === "asc";
  const limit = readLimit(location.search);
  const valid = /^[^=]+=.*$/.test(label);
  const selection = JSON.stringify([label, range, ascending, limit]);
  const { page, at, onPage, reanchor } = useSelectionState(selection, limit);
  const permissions = (tenant && session.scopes[tenant]?.permissions) || [];
  const may = offered("blockedCommands", permissions);
  const after = page.trail[page.trail.length - 1];
  const window = resolveRange(range, at);
  const query = new URLSearchParams({ label, limit: String(limit) });
  if (window.from) query.set("from", window.from);
  if (window.to) query.set("to", window.to);
  if (ascending) query.set("order", "asc");
  if (after) query.set("after", after);
  const key = tenant && may && valid ? `${tenant}|${query.toString()}` : null;
  const [load, refresh] = useLoad<BlockedPage>(key, (signal) =>
    api.call("blockedCommands", { params: { tenant: tenant as string }, query, signal }),
  );
  const shown = may ? load : FORBIDDEN;
  const headers = [
    t("audit.col.recorded"),
    t("audit.blocked.col.command"),
    t("audit.col.subject"),
    t("audit.col.target"),
    t("audit.blocked.col.access"),
    t("audit.col.session"),
  ];
  const gone = (rows: readonly BlockedCommand[]) => rows.filter((b) => !b.decision_found).length;

  const columns: Column<BlockedCommand>[] = [
    { id: "recorded", header: headers[0] as string, sortValue: (b) => b.record.recorded_at, cell: (b) => <When at={b.record.recorded_at} /> },
    { id: "command", header: headers[1] as string, sortValue: (b) => b.command, cell: (b) => <Id>{b.command}</Id> },
    { id: "subject", header: headers[2] as string, sortValue: (b) => b.record.subject ?? "", cell: (b) => (b.record.subject ? <Id>{b.record.subject}</Id> : null) },
    {
      id: "target",
      header: headers[3] as string,
      sortValue: (b) => b.record.target ?? "",
      cell: (b) => (
        <span className={styles.what}>
          {b.record.target ? <Id>{b.record.target}</Id> : null}
          <span className={styles.chips}>
            {Object.entries(b.target_labels)
              .sort(([x], [y]) => x.localeCompare(y))
              .map(([k, v]) => (
                <Chip key={k} data>{`${k}=${v}`}</Chip>
              ))}
          </span>
        </span>
      ),
    },
    {
      id: "access",
      header: headers[4] as string,
      cell: (b) =>
        b.decision_found ? (
          <span className={styles.what}>
            {b.matched_rule ? <Id>{b.matched_rule}</Id> : <span>{t("explain.basis.default")}</span>}
            {b.route_type ? <Id>{b.route_type}</Id> : null}
            {b.record.decision_id ? (
              <Link to={`explain/decisions/${encodeURIComponent(b.record.decision_id)}`} className={styles.link} data-explain="decision">
                {t("audit.record.explain")}
              </Link>
            ) : null}
          </span>
        ) : (
          <span className={styles.quiet}>{t("audit.blocked.decisionGone")}</span>
        ),
    },
    {
      id: "session",
      header: headers[5] as string,
      cell: (b) =>
        b.record.session_id ? (
          <Link to={`explain/sessions/${encodeURIComponent(b.record.session_id)}`} className={styles.link}>
            <Id>{b.record.session_id}</Id>
          </Link>
        ) : null,
    },
  ];

  return (
    <div className={styles.screen}>
      <PageHeader
        title={t("audit.title")}
        lede={t("audit.blocked.lede")}
        actions={
          <Button
            onClick={() => {
              reanchor();
              refresh();
            }}
          >
            {t("audit.refresh")}
          </Button>
        }
      />
      <AuditViews current="blocked" />
      <FilterBar
        label={t("audit.filters")}
        fields={[{ id: "label", label: t("audit.blocked.label"), kind: "text", mono: true }]}
        values={label ? { label } : {}}
        onChange={(v) => setQuery(location, { label: v.label })}
        extra={
          <>
            <TimeRangePicker
              key={JSON.stringify(range)}
              label={t("audit.filter.time")}
              value={range}
              onChange={(r: TimeRange) => setQuery(location, rangeQuery(r))}
            />
            <Order ascending={ascending} onChange={(asc) => setQuery(location, { order: asc ? "asc" : undefined })} />
          </>
        }
      />
      {!valid ? (
        <EmptyState
          title={t("audit.blocked.start.title")}
          body={t("audit.blocked.start.body")}
          action={
            <Link to="audit/blocked-commands" query={{ label: "env=prod" }} className={styles.link}>
              {t("audit.blocked.start.example")}
            </Link>
          }
        />
      ) : (
        <ScreenFrame
          load={shown}
          onRetry={refresh}
          isEmpty={(d) => d.blocked.length === 0 && page.trail.length === 0}
          gaps={shown.state === "ok" && gone(shown.data.blocked) ? [t("audit.blocked.partial", { count: gone(shown.data.blocked) })] : []}
          loading={<TableSkeleton headers={headers} />}
          empty={<EmptyState title={t("audit.blocked.empty.title")} body={t("audit.blocked.empty.body", { label })} />}
        >
          {(data) => (
            <>
              <p className={styles.quiet} role="status">
                {t("audit.blocked.count", { count: f.number(data.blocked.length), label })}
              </p>
              <DataTable
                caption={t("audit.blocked.caption")}
                columns={columns}
                rows={data.blocked}
                rowKey={(b) => b.record.record_id}
                rowLabel={(b) => b.record.record_id}
                partialNote={data.truncated ? t("audit.onePage") : undefined}
              />
              <Pagination page={page} shown={data.blocked.length} truncated={data.truncated} next={data.next} onPage={onPage} />
            </>
          )}
        </ScreenFrame>
      )}
    </div>
  );
}

export type { RecordFilters };
