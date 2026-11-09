// POLICY (0053; PLAN M3, M4, M11, M21, M26, §5.6): author and simulate the
// tenant's policy, see what is active and what came before it, see what a
// candidate would change and would break, and activate or roll back
// deliberately. Three screens, each a deep link:
//   /console/policy                         the stored versions, newest first
//   /console/policy/versions/<n>[?tab=]     one version: its source, its
//                                           changes, its publish-time report,
//                                           a replay against recorded traffic
//   /console/policy/candidate[?from=<n>]    a source not stored yet: validate,
//                                           compare, simulate, upload
// Reading, validating, comparing and simulating are policy:read; uploading is
// policy:write; activating — and a rollback, which is an activation — is
// policy:publish and confirms by typing the version's name (DESIGN.md §2).
// The console offers each only to a role holding it, and the API refuses it to
// anyone else (M2).

import { useEffect, useMemo, useState } from "react";
import type { Answer } from "../api/client";
import { offered } from "../api/permit";
import type {
  PolicyActivation,
  PolicyBundle,
  PolicyDiff,
  PolicyReport,
  PolicyUpload,
  PolicyVersion,
  PolicyVersionPage,
  Rejection,
} from "../api/types";
import { Breadcrumb } from "../components/Badge";
import { Button, ButtonLink } from "../components/Button";
import { SourceView } from "../components/Code";
import { CopyableId } from "../components/CopyableId";
import { DataTable, TableSkeleton, type Column, type TableSort } from "../components/DataTable";
import { Field, Select, Textarea } from "../components/Field";
import { PageHeader } from "../components/PageHeader";
import { Pagination, type PageState } from "../components/Pagination";
import { EmptyState, ErrorMessage, ErrorState, ScreenFrame, Skeleton, type ErrorKind } from "../components/States";
import { StatusOf } from "../components/Status";
import { Tabs } from "../components/Tabs";
import { useToast } from "../components/Toast";
import type { ApiError } from "../i18n/errors";
import { useI18n } from "../i18n/I18n";
import { Link, navigate, useLocation } from "../router/router";
import { useLoad } from "../shell/load";
import { useDeployment } from "../shell/Shell";
import {
  activationMessage,
  ActivateDialog,
  Changes,
  ChangesSkeleton,
  goToLine,
  Ident,
  Rejections,
  Report,
  ReportSkeleton,
  Simulate,
  sourceNotes,
  VersionName,
} from "./PolicyParts";
import { isRollback, readVersion, rejectionsOf, versionName, versionStatus } from "./policyModel";
import styles from "./Policy.module.css";

const DEFAULT_LIMIT = 100;

function readLimit(search: URLSearchParams): number {
  const n = Number(search.get("limit"));
  return Number.isInteger(n) && n >= 1 && n <= 10000 ? n : DEFAULT_LIMIT;
}

function usePermissions(): readonly string[] {
  const { tenant, session } = useDeployment();
  return (tenant && session.scopes[tenant]?.permissions) || [];
}

/** Rewrites some of the query, keeping the rest: filters and tabs live in the URL (DESIGN.md §4). */
function useSetQuery(): (next: Record<string, string | undefined>) => void {
  const location = useLocation();
  return (next) => {
    const q: Record<string, string> = {};
    for (const [k, v] of location.search) q[k] = v;
    for (const [k, v] of Object.entries(next)) {
      if (v) q[k] = v;
      else delete q[k];
    }
    navigate(location.path, { replace: true, query: q });
  };
}

function Instant({ at }: { at: string }) {
  const { f } = useI18n();
  return (
    <time dateTime={f.utc(at)} title={f.utc(at)}>
      {f.dateTime(at)}
    </time>
  );
}

/** A digest, shortened where it is shown and whole where it is copied. */
function Digest({ digest }: { digest: string }) {
  return <CopyableId value={digest} short={digest.slice(0, 19)} />;
}

/** The active version's answer: the version, or none (404) — which is a fact, not an error. */
type Active = { kind: "none" } | { kind: "version"; bundle: PolicyBundle };

function useActive(): [ReturnType<typeof useLoad<Active>>[0], () => void] {
  const { tenant, api } = useDeployment();
  return useLoad<Active>(tenant ? `active|${tenant}` : null, async (signal) => {
    const a = await api.call<PolicyBundle>("policyActive", { params: { tenant: tenant as string }, signal });
    if (a.kind === "ok") return { ...a, data: { kind: "version", bundle: a.data } };
    if (a.kind === "refused" && a.status === 404) return { kind: "ok", status: 404, data: { kind: "none" } };
    return a;
  });
}

// ---------------------------------------------------------------------------
// /console/policy — the stored versions.
// ---------------------------------------------------------------------------

export function PolicyScreen() {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const toast = useToast();
  const location = useLocation();
  const limit = readLimit(location.search);
  const permissions = usePermissions();
  const canActivate = offered("policyActivate", permissions);
  const [page, setPage] = useState<PageState>({ trail: [], limit });
  const [sort, setSort] = useState<TableSort | null>(null);
  const [hidden, setHidden] = useState<string[]>([]);
  const [activating, setActivating] = useState<number | null>(null);
  const after = page.trail[page.trail.length - 1];
  const [load, refresh] = useLoad<PolicyVersionPage>(tenant ? `${tenant}|${limit}|${after ?? ""}` : null, (signal) =>
    api.call("policyVersions", {
      params: { tenant: tenant as string },
      query: after ? { limit: String(limit), after } : { limit: String(limit) },
      signal,
    }),
  );
  const [active, refreshActive] = useActive();
  const activeVersion = active.state === "ok" && active.data.kind === "version" ? active.data.bundle.version : null;

  const columns: Column<PolicyVersion>[] = [
    {
      id: "version",
      header: t("policies.col.version"),
      sortValue: (v) => v.version,
      cell: (v) => (
        <Link to={`policy/versions/${v.version}`} className={styles.open} aria-label={t("policies.open", { version: versionName(v.version) })}>
          <VersionName version={v.version} />
        </Link>
      ),
    },
    {
      id: "status",
      header: t("policies.col.status"),
      sortValue: (v) => (v.active ? 0 : 1),
      cell: (v) => <StatusOf name={versionStatus(v)} />,
    },
    { id: "digest", header: t("policies.col.digest"), cell: (v) => <Digest digest={v.digest} /> },
    {
      id: "by",
      header: t("policies.col.by"),
      sortValue: (v) => v.uploaded_by,
      cell: (v) => <Ident>{v.uploaded_by}</Ident>,
    },
    { id: "at", header: t("policies.col.at"), sortValue: (v) => v.uploaded_at, cell: (v) => <Instant at={v.uploaded_at} /> },
  ];

  const done = (r: PolicyActivation) => {
    setActivating(null);
    toast.success(activationMessage(t, r));
    refresh();
    refreshActive();
  };

  return (
    <div className={styles.screen}>
      <PageHeader
        title={t("policies.title")}
        lede={t("policies.lede")}
        actions={
          <>
            <ButtonLink to="policy/candidate" data-author="">
              {t("policies.author")}
            </ButtonLink>
            <Button
              onClick={() => {
                refresh();
                refreshActive();
              }}
            >
              {t("policies.refresh")}
            </Button>
          </>
        }
      />
      <ActivePanel active={active} />
      <ScreenFrame
        load={load}
        onRetry={refresh}
        gaps={active.state === "outage" ? [t("policies.partial.active")] : []}
        isEmpty={(d) => d.versions.length === 0 && page.trail.length === 0}
        loading={<TableSkeleton headers={columns.map((c) => c.header)} />}
        empty={
          <EmptyState
            title={t("policies.empty.title")}
            body={t("policies.empty.body")}
            action={
              <ButtonLink to="policy/candidate" variant="primary">
                {t("policies.author")}
              </ButtonLink>
            }
          />
        }
      >
        {(data) => (
          <>
            <DataTable
              caption={t("policies.table.caption")}
              columns={columns}
              rows={data.versions}
              rowKey={(v) => String(v.version)}
              rowLabel={(v) => versionName(v.version)}
              sort={sort}
              onSort={setSort}
              hidden={hidden}
              onHidden={setHidden}
              partialNote={data.truncated ? t("policies.table.onePage") : undefined}
              rowActions={(v) => [
                { id: "open", label: t("policies.action.open"), onSelect: () => navigate(`policy/versions/${v.version}`) },
                ...(!v.active && activeVersion !== null
                  ? [
                      {
                        id: "diff",
                        label: t("policies.action.diff"),
                        onSelect: () => navigate(`policy/versions/${v.version}`, { query: { tab: "changes" } }),
                      },
                    ]
                  : []),
                ...(canActivate && !v.active
                  ? [
                      {
                        id: isRollback(v.version, activeVersion) ? "rollback" : "activate",
                        label: isRollback(v.version, activeVersion) ? t("policies.action.rollback") : t("policies.action.activate"),
                        danger: true,
                        onSelect: () => setActivating(v.version),
                      },
                    ]
                  : []),
              ]}
            />
            <Pagination page={page} shown={data.versions.length} truncated={data.truncated} next={data.next} onPage={setPage} />
          </>
        )}
      </ScreenFrame>
      <ActivateDialog version={activating} active={activeVersion} onClose={() => setActivating(null)} onDone={done} />
    </div>
  );
}

/** The version every decision is taken under now, or the fact that there is none. */
function ActivePanel({ active }: { active: ReturnType<typeof useActive>[0] }) {
  const { t } = useI18n();
  return (
    <section className={styles.panel} aria-labelledby="policy-active" data-active-panel={active.state}>
      <h2 id="policy-active" className={styles.panelTitle}>
        {t("policies.active.title")}
      </h2>
      {active.state === "loading" ? (
        <div aria-hidden="true" className={styles.factsSkeleton}>
          <Skeleton size="l" />
          <Skeleton size="m" />
        </div>
      ) : active.state === "refused" || active.state === "outage" ? (
        <ErrorMessage error={active.error} kind={active.state} />
      ) : active.data.kind === "none" ? (
        <p className={styles.para} data-no-active="">
          {t("policies.active.none")}
        </p>
      ) : (
        <dl className={styles.facts}>
          <div>
            <dt>{t("policies.col.version")}</dt>
            <dd className={styles.activeVersion}>
              <Link to={`policy/versions/${active.data.bundle.version}`}>
                <VersionName version={active.data.bundle.version} />
              </Link>
              <StatusOf name="active" />
            </dd>
          </div>
          <div>
            <dt>{t("policies.col.digest")}</dt>
            <dd>
              <Digest digest={active.data.bundle.digest} />
            </dd>
          </div>
          <div>
            <dt>{t("policies.col.by")}</dt>
            <dd>
              <Ident>{active.data.bundle.uploaded_by}</Ident>
            </dd>
          </div>
          <div>
            <dt>{t("policies.col.at")}</dt>
            <dd>
              <Instant at={active.data.bundle.uploaded_at} />
            </dd>
          </div>
        </dl>
      )}
    </section>
  );
}

// ---------------------------------------------------------------------------
// /console/policy/versions/<n> — one version.
// ---------------------------------------------------------------------------

const TABS = ["source", "changes", "report", "simulate"] as const;
type VersionTab = (typeof TABS)[number];

function readTab(search: URLSearchParams): VersionTab {
  const v = search.get("tab") as VersionTab | null;
  return v && TABS.includes(v) ? v : "source";
}

export function PolicyVersionScreen({ version: raw }: { version: string }) {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const toast = useToast();
  const location = useLocation();
  const setQuery = useSetQuery();
  const version = readVersion(raw);
  const tab = readTab(location.search);
  const permissions = usePermissions();
  const canActivate = offered("policyActivate", permissions);
  const [activating, setActivating] = useState<number | null>(null);
  // A version this tenant does not store is an answer (404), shown as the
  // empty state that says so, never as an error.
  const [load, refresh] = useLoad<PolicyBundle | null>(tenant && version ? `${tenant}|${version}` : null, async (signal) => {
    const a = await api.call<PolicyBundle>("policyVersion", { params: { tenant: tenant as string, version: String(version) }, signal });
    if (a.kind === "refused" && a.status === 404) return { kind: "ok", status: 404, data: null };
    return a;
  });
  const [active, refreshActive] = useActive();
  const activeVersion = active.state === "ok" && active.data.kind === "version" ? active.data.bundle.version : null;
  const bundle = load.state === "ok" ? load.data : null;
  const source = bundle ? bundle.source : null;
  const [report] = useLoad<PolicyReport>(tenant && source !== null ? `${tenant}|${version}|report` : null, (signal) =>
    api.call("policyValidate", { params: { tenant: tenant as string }, body: { source }, signal }),
  );

  if (version === null) return <NotAVersion raw={raw} />;
  const name = versionName(version);

  const done = (r: PolicyActivation) => {
    setActivating(null);
    toast.success(activationMessage(t, r));
    refresh();
    refreshActive();
  };

  const idPrefix = `policy-v${version}`;
  const findings = report.state === "ok" ? report.data.findings : [];
  return (
    <div className={styles.screen}>
      <PageHeader
        breadcrumb={<Breadcrumb items={[{ label: t("policies.title"), to: "policy" }, { label: <VersionName version={version} /> }]} />}
        title={<VersionName version={version} title />}
        id={bundle ? <Digest digest={bundle.digest} /> : null}
        status={bundle ? <StatusOf name={versionStatus(bundle)} pill /> : null}
        actions={
          bundle ? (
            <>
              <ButtonLink to="policy/candidate" query={{ from: String(version) }} data-edit-candidate="">
                {t("policies.version.edit")}
              </ButtonLink>
              {canActivate && !bundle.active ? (
                <Button variant="danger" onClick={() => setActivating(version)} data-activate={isRollback(version, activeVersion) ? "rollback" : "activate"}>
                  {isRollback(version, activeVersion) ? t("policies.action.rollback") : t("policies.action.activate")}
                </Button>
              ) : null}
            </>
          ) : null
        }
      />
      <ScreenFrame
        load={load}
        onRetry={refresh}
        gaps={report.state === "outage" || report.state === "refused" ? [t("policies.partial.report")] : []}
        isEmpty={(b) => b === null}
        empty={
          <EmptyState
            title={t("policies.version.missing.title")}
            body={t("policies.version.missing.body", { version: `\u2068${name}\u2069` })}
            action={<Link to="policy">{t("policies.version.back")}</Link>}
          />
        }
        loading={
          <div className={styles.stack}>
            <Skeleton size="l" />
            <Skeleton shape="block" size="full" />
            <Skeleton shape="block" size="full" />
          </div>
        }
      >
        {(found) =>
          found === null ? null : (
          <>
            <dl className={styles.facts}>
              <div>
                <dt>{t("policies.col.by")}</dt>
                <dd>
                  <Ident>{found.uploaded_by}</Ident>
                </dd>
              </div>
              <div>
                <dt>{t("policies.col.at")}</dt>
                <dd>
                  <Instant at={found.uploaded_at} />
                </dd>
              </div>
              <div>
                <dt>{t("policies.version.activeNow")}</dt>
                <dd>{activeVersion === null ? t("policies.version.noneActive") : <VersionName version={activeVersion} />}</dd>
              </div>
            </dl>
            <Tabs
              label={t("policies.version.tabs")}
              selected={tab}
              onSelect={(id) => setQuery({ tab: id === "source" ? undefined : id })}
              tabs={[
                { id: "source", label: t("policies.tab.source") },
                { id: "changes", label: t("policies.tab.changes") },
                { id: "report", label: t("policies.tab.report") },
                { id: "simulate", label: t("policies.tab.simulate") },
              ]}
            >
              {tab === "source" ? (
                <SourceView source={found.source} label={t("policies.source.label")} idPrefix={idPrefix} notes={sourceNotes([], findings)} />
              ) : tab === "changes" ? (
                <VersionChanges version={version} active={activeVersion} />
              ) : tab === "report" ? (
                report.state === "loading" ? (
                  <ReportSkeleton />
                ) : report.state === "ok" ? (
                  <Report
                    report={report.data}
                    idPrefix={idPrefix}
                    onLine={(line) => {
                      setQuery({ tab: undefined });
                      window.setTimeout(() => goToLine(idPrefix, line), 0);
                    }}
                  />
                ) : (
                  <ErrorMessage error={report.error} kind={report.state} />
                )
              ) : (
                <Simulate candidate={{ version }} search={location.search} onRange={setQuery} />
              )}
            </Tabs>
          </>
          )
        }
      </ScreenFrame>
      <ActivateDialog version={activating} active={activeVersion} onClose={() => setActivating(null)} onDone={done} />
    </div>
  );
}

function NotAVersion({ raw }: { raw: string }) {
  const { t } = useI18n();
  return (
    <div className={styles.screen}>
      <PageHeader title={t("policies.title")} />
      <EmptyState
        title={t("policies.version.notFound.title")}
        body={t("policies.version.notFound.body", { version: `\u2068${raw}\u2069` })}
        action={<Link to="policy">{t("policies.version.back")}</Link>}
      />
    </div>
  );
}

/**
 * A version's changes against another: by default the active one — what
 * activating this would change — and for the active version itself, the one
 * before it. `?against=` names another, and 0 is nothing (the whole bundle).
 */
function VersionChanges({ version, active }: { version: number; active: number | null }) {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const location = useLocation();
  const setQuery = useSetQuery();
  const raw = location.search.get("against");
  const fallback = active !== null && active !== version ? active : version - 1;
  const against = raw !== null && /^\d+$/.test(raw) ? Number(raw) : fallback;
  const [versions] = useLoad<PolicyVersionPage>(tenant ? `${tenant}|versions-for-diff` : null, (signal) =>
    api.call("policyVersions", { params: { tenant: tenant as string }, query: { limit: String(DEFAULT_LIMIT) }, signal }),
  );
  const [diff, refresh] = useLoad<PolicyDiff>(tenant ? `${tenant}|${version}|diff|${against}` : null, (signal) =>
    api.call("policyVersionDiff", {
      params: { tenant: tenant as string, version: String(version) },
      query: { against: String(against) },
      signal,
    }),
  );
  const others = versions.state === "ok" ? versions.data.versions.filter((v) => v.version !== version) : [];
  const label =
    against === 0
      ? t("policies.diff.labelWhole", { to: `\u2068${versionName(version)}\u2069` })
      : t("policies.diff.label", { to: `\u2068${versionName(version)}\u2069`, from: `\u2068${versionName(against)}\u2069` });
  return (
    <div className={styles.stack}>
      <div className={styles.toolbar}>
        <Field label={t("policies.diff.against")}>
          <Select value={String(against)} onChange={(e) => setQuery({ against: e.target.value })} data-diff-against="">
            <option value="0">{t("policies.diff.againstNothing")}</option>
            {!others.some((v) => v.version === against) && against !== 0 ? (
              <option value={String(against)} translate="no">
                {versionName(against)}
              </option>
            ) : null}
            {others.map((v) => (
              <option key={v.version} value={String(v.version)} translate="no">
                {versionName(v.version)}
              </option>
            ))}
          </Select>
        </Field>
      </div>
      <ScreenFrame load={diff} onRetry={refresh} isEmpty={() => false} empty={null} loading={<ChangesSkeleton />}>
        {(d) => <Changes diff={d} label={label} />}
      </ScreenFrame>
    </div>
  );
}

// ---------------------------------------------------------------------------
// /console/policy/candidate — a source not stored yet.
// ---------------------------------------------------------------------------

const DRAFT_KEY = "hoplock.console.policy.draft";

function loadDraft(tenant: string | null): string | null {
  try {
    return tenant ? window.sessionStorage.getItem(`${DRAFT_KEY}.${tenant}`) : null;
  } catch {
    return null;
  }
}

function saveDraft(tenant: string | null, source: string): void {
  try {
    if (tenant) window.sessionStorage.setItem(`${DRAFT_KEY}.${tenant}`, source);
  } catch {
    // A draft is a courtesy: a tab that cannot keep one loses it on reload.
  }
}

type Check<T> = { state: "idle" } | { state: "busy" } | { state: "ok"; data: T; correlationId?: string } | { state: "failed"; error: ApiError; kind: ErrorKind };

function check<T>(a: Answer<T>): Check<T> {
  return a.kind === "ok" ? { state: "ok", data: a.data, correlationId: a.correlationId } : { state: "failed", error: a.error, kind: a.kind };
}

const CANDIDATE_TABS = ["validation", "changes", "simulate"] as const;
type CandidateTab = (typeof CANDIDATE_TABS)[number];

export function PolicyCandidateScreen() {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const toast = useToast();
  const location = useLocation();
  const setQuery = useSetQuery();
  const permissions = usePermissions();
  const canUpload = offered("policyUpload", permissions);
  const from = readVersion(location.search.get("from") ?? undefined);
  const tabParam = location.search.get("tab") as CandidateTab | null;
  const tab: CandidateTab = tabParam && CANDIDATE_TABS.includes(tabParam) ? tabParam : "validation";
  const [source, setSource] = useState<string | null>(() => (from === null ? loadDraft(tenant) : null));
  const [started, setStarted] = useState<number | null>(null);
  // Where the editor's text came from: this tab's kept draft, a stored version, or nothing (no version active).
  const [origin, setOrigin] = useState<"draft" | "version" | "none" | null>(() => (source === null ? null : "draft"));
  const [validation, setValidation] = useState<Check<PolicyReport>>({ state: "idle" });
  const [changes, setChanges] = useState<Check<PolicyDiff>>({ state: "idle" });
  const [upload, setUpload] = useState<{ state: "idle" | "busy" } | { state: "failed"; error: ApiError; kind: ErrorKind; rejections: Rejection[] }>({
    state: "idle",
  });
  // What the result panel shows was checked against: a later edit marks it as stale.
  const [checked, setChecked] = useState<string | null>(null);

  // Start from a named version, else the draft, else the active version, else nothing.
  const startKey = tenant && source === null ? `${tenant}|start|${from ?? "active"}` : null;
  const [start] = useLoad<{ source: string; version: number | null }>(startKey, async (signal) => {
    const name = from === null ? "policyActive" : "policyVersion";
    const a = await api.call<PolicyBundle>(name, {
      params: from === null ? { tenant: tenant as string } : { tenant: tenant as string, version: String(from) },
      signal,
    });
    if (a.kind === "ok") return { ...a, data: { source: a.data.source, version: a.data.version } };
    if (a.kind === "refused" && a.status === 404) return { kind: "ok", status: 404, data: { source: "", version: null } };
    return a;
  });
  useEffect(() => {
    if (source === null && start.state === "ok") {
      setSource(start.data.source);
      setStarted(start.data.version);
      setOrigin(start.data.version === null ? "none" : "version");
    }
  }, [source, start]);

  const text = source ?? "";
  const edit = (v: string) => {
    setSource(v);
    saveDraft(tenant, v);
    setUpload({ state: "idle" });
  };
  const empty = text.trim() === "";
  const stale = checked !== null && checked !== text;

  const validate = async () => {
    setQuery({ tab: undefined });
    setValidation({ state: "busy" });
    setChecked(text);
    setValidation(check(await api.call<PolicyReport>("policyValidate", { params: { tenant: tenant as string }, body: { source: text } })));
  };
  const compare = async () => {
    setQuery({ tab: "changes" });
    setChanges({ state: "busy" });
    setChecked(text);
    setChanges(check(await api.call<PolicyDiff>("policyDiff", { params: { tenant: tenant as string }, body: { source: text } })));
  };
  const send = async () => {
    setUpload({ state: "busy" });
    const a = await api.call<PolicyUpload>("policyUpload", { params: { tenant: tenant as string }, body: { source: text } });
    if (a.kind === "ok") {
      setUpload({ state: "idle" });
      const v = `\u2068${versionName(a.data.version.version)}\u2069`;
      toast.success(a.data.existing ? t("policies.candidate.existing", { version: v }) : t("policies.candidate.uploaded", { version: v }));
      navigate(`policy/versions/${a.data.version.version}`);
      return;
    }
    // A refusal reports here, at the form, and a bundle the compiler refused
    // shows each rejection at its line — never a toast alone (DESIGN.md §7).
    setUpload({ state: "failed", error: a.error, kind: a.kind, rejections: rejectionsOf(a.error.parameters) });
    setQuery({ tab: undefined });
    setChecked(text);
  };

  const idPrefix = "policy-candidate";
  const uploadRejections = upload.state === "failed" ? upload.rejections : [];
  return (
    <div className={styles.screen}>
      <PageHeader
        breadcrumb={<Breadcrumb items={[{ label: t("policies.title"), to: "policy" }, { label: t("policies.candidate.title") }]} />}
        title={t("policies.candidate.title")}
        lede={t("policies.candidate.lede")}
      />
      <section
        className={styles.panel}
        aria-labelledby="policy-candidate-editor"
        data-start={origin ?? start.state}
      >
        <h2 id="policy-candidate-editor" className={styles.panelTitle}>
          {t("policies.candidate.source")}
        </h2>
        {start.state === "loading" && source === null ? (
          <div aria-hidden="true" className={styles.stack}>
            <Skeleton shape="block" size="full" />
          </div>
        ) : start.state === "refused" || start.state === "outage" ? (
          <ErrorMessage error={start.error} kind={start.state} />
        ) : null}
        <Field
          label={t("policies.candidate.label")}
          hint={
            origin === "version" && started !== null
              ? t("policies.candidate.hint", { version: `\u2068${versionName(started)}\u2069` })
              : origin === "draft"
                ? t("policies.candidate.hintDraft")
                : t("policies.candidate.hintBlank")
          }
          error={upload.state === "failed" ? undefined : empty && validation.state !== "idle" ? t("policies.candidate.required") : undefined}
        >
          <Textarea
            mono
            wrap="off"
            rows={20}
            value={text}
            onChange={(e) => edit(e.target.value)}
            className={styles.editor}
            data-candidate-source=""
          />
        </Field>
        <div className={styles.actions}>
          <Button variant="primary" onClick={validate} disabled={empty} loading={validation.state === "busy"} data-validate="">
            {t("policies.candidate.validate")}
          </Button>
          <Button onClick={compare} disabled={empty} loading={changes.state === "busy"} data-compare="">
            {t("policies.candidate.diff")}
          </Button>
          <Button onClick={() => setQuery({ tab: "simulate" })} disabled={empty} data-simulate-tab="">
            {t("policies.candidate.simulate")}
          </Button>
          {canUpload ? (
            <Button onClick={send} disabled={empty} loading={upload.state === "busy"} data-upload="">
              {t("policies.candidate.upload")}
            </Button>
          ) : null}
        </div>
        {upload.state === "failed" ? <ErrorMessage error={upload.error} kind={upload.kind} /> : null}
        {stale ? (
          <p className={styles.quiet} role="status">
            {t("policies.candidate.stale")}
          </p>
        ) : null}
      </section>
      <Tabs
        label={t("policies.candidate.results")}
        selected={tab}
        onSelect={(id) => setQuery({ tab: id === "validation" ? undefined : id })}
        tabs={[
          { id: "validation", label: t("policies.tab.validation") },
          { id: "changes", label: t("policies.tab.changes") },
          { id: "simulate", label: t("policies.tab.simulate") },
        ]}
      >
        {tab === "validation" ? (
          uploadRejections.length ? (
            <Rejections
              rejections={uploadRejections}
              source={checked ?? text}
              idPrefix={idPrefix}
              correlationId={upload.state === "failed" ? upload.error.correlation_id : undefined}
            />
          ) : validation.state === "idle" ? (
            <EmptyState title={t("policies.candidate.idle.title")} body={t("policies.candidate.idle.body")} />
          ) : validation.state === "busy" ? (
            <ReportSkeleton />
          ) : validation.state === "failed" ? (
            <ErrorState error={validation.error} kind={validation.kind} onRetry={validate} />
          ) : (
            <CandidateValidation report={validation.data} source={checked ?? text} idPrefix={idPrefix} correlationId={validation.correlationId} />
          )
        ) : tab === "changes" ? (
          changes.state === "idle" ? (
            <EmptyState
              title={t("policies.candidate.changes.idle.title")}
              body={t("policies.candidate.changes.idle.body")}
              action={
                <Button onClick={compare} disabled={empty}>
                  {t("policies.candidate.diff")}
                </Button>
              }
            />
          ) : changes.state === "busy" ? (
            <ChangesSkeleton />
          ) : changes.state === "failed" ? (
            changes.error.code === "policy_invalid" ? (
              <EmptyState title={t("policies.candidate.unparsed.title")} body={t("policies.candidate.unparsed.body")} />
            ) : (
              <ErrorState error={changes.error} kind={changes.kind} onRetry={compare} />
            )
          ) : (
            <Changes
              diff={changes.data}
              label={
                changes.data.from
                  ? t("policies.diff.labelCandidate", { from: `\u2068${versionName(changes.data.from)}\u2069` })
                  : t("policies.diff.labelCandidateWhole")
              }
            />
          )
        ) : (
          <Simulate candidate={{ source: text }} search={location.search} onRange={setQuery} />
        )}
      </Tabs>
    </div>
  );
}

/** A candidate's validation: its rejections at their lines, or its report beside its source. */
function CandidateValidation({
  report,
  source,
  idPrefix,
  correlationId,
}: {
  report: PolicyReport;
  source: string;
  idPrefix: string;
  correlationId?: string;
}) {
  const { t } = useI18n();
  const notes = useMemo(() => sourceNotes([], report.findings, correlationId), [report, correlationId]);
  if (!report.valid) {
    return <Rejections rejections={report.rejections} source={source} idPrefix={idPrefix} correlationId={correlationId} />;
  }
  return (
    <div className={styles.stack}>
      <Report report={report} idPrefix={idPrefix} correlationId={correlationId} onLine={(line) => goToLine(idPrefix, line)} />
      <SourceView source={source} label={t("policies.source.label")} idPrefix={idPrefix} notes={notes} />
    </div>
  );
}
