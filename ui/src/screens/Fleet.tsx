// FLEET (0050; PLAN M6, M17, M19, M26; proxy D18): the proxies this tenant
// has enrolled — zone, health, last heartbeat, contract version, live relay
// registrations — and each one's configuration as the proxy REPORTED it.
//
// It reads two things: one page of GET …/proxies (paged by proxy id, M26),
// and the deployment's health, whose fleet summary counts the whole tenant
// for the chart. Each can fail without the other, and a fleet with a proxy
// the server cannot hear is PARTIAL — the rows that are there, and the gap
// named — never an error page. Its one mutation is withdrawing a proxy:
// destructive, so it confirms by typing the proxy's id, it is offered only to
// a role holding fleet:write, and the API refuses it to anyone else.

import { useMemo, useState } from "react";
import { offered } from "../api/permit";
import type { FleetHealth, Proxy, ProxyPage, Withdrawn } from "../api/types";
import { Badge, Chip } from "../components/Badge";
import { Button } from "../components/Button";
import { CodeBlock, JsonViewer } from "../components/Code";
import { CopyableId } from "../components/CopyableId";
import { DataTable, TableSkeleton, type Column, type TableSort } from "../components/DataTable";
import { ConfirmDialog, Drawer, type ConfirmFailure } from "../components/Dialog";
import { FilterBar, type FilterField, type SavedView } from "../components/FilterBar";
import { PageHeader } from "../components/PageHeader";
import { Pagination, type PageState } from "../components/Pagination";
import { EmptyState, ScreenFrame, Skeleton } from "../components/States";
import { STATUS, StatusOf, type StatusName } from "../components/Status";
import { Tabs } from "../components/Tabs";
import { useToast } from "../components/Toast";
import { Tree, type TreeNode } from "../components/Tree";
import { useI18n } from "../i18n/I18n";
import { loadViews, saveViews } from "../prefs/prefs";
import { navigate, useLocation } from "../router/router";
import { useLoad } from "../shell/load";
import { useDeployment } from "../shell/Shell";
import { FleetChart } from "./FleetChart";
import { applyFilters, configStatus, proxyHealth, unreachable, type FleetFilters } from "./fleetModel";
import styles from "./Fleet.module.css";

/** How often Fleet re-reads its page while visible. */
const REFRESH_MS = 30_000;
const DEFAULT_LIMIT = 100;
const HEALTH_FILTER: readonly StatusName[] = ["healthy", "stale", "unreachable", "pending", "revoked"];
const CONFIG_FILTER: readonly StatusName[] = [
  "applied",
  "inProgress",
  "pendingRestart",
  "rejected",
  "fetchFailed",
  "notReported",
];

function readFilters(search: URLSearchParams): FleetFilters {
  const pick = (k: string, allowed: readonly StatusName[]) => {
    const v = search.get(k) as StatusName | null;
    return v && allowed.includes(v) ? v : undefined;
  };
  return {
    zone: search.get("zone") || undefined,
    health: pick("health", HEALTH_FILTER),
    config: pick("config", CONFIG_FILTER),
    q: search.get("q") || undefined,
  };
}

function readLimit(search: URLSearchParams): number {
  const n = Number(search.get("limit"));
  return Number.isInteger(n) && n >= 1 && n <= 10000 ? n : DEFAULT_LIMIT;
}

function Instant({ at, never }: { at?: string; never: string }) {
  const { f } = useI18n();
  if (!at) return <span className={styles.quiet}>{never}</span>;
  return (
    <time dateTime={f.utc(at)} title={f.utc(at)}>
      {f.dateTime(at)}
    </time>
  );
}

function Version({ v }: { v?: string }) {
  return v ? (
    <code className={styles.version} dir="ltr" translate="no">
      {v}
    </code>
  ) : null;
}

export function FleetScreen() {
  const { t, f, locale } = useI18n();
  const { tenant, session, health, api, refresh: refreshDeployment } = useDeployment();
  const toast = useToast();
  const location = useLocation();
  const filters = readFilters(location.search);
  const limit = readLimit(location.search);
  const view = location.search.get("view") === "zones" ? "zones" : "table";
  const [page, setPage] = useState<PageState>({ trail: [], limit });
  const [sort, setSort] = useState<TableSort | null>(null);
  const [hidden, setHidden] = useState<string[]>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [withdrawing, setWithdrawing] = useState<Proxy | null>(null);
  const [views, setViews] = useState<SavedView[]>(() => loadViews(session.principal_id, "fleet"));
  const permissions = (tenant && session.scopes[tenant]?.permissions) || [];
  const canWithdraw = offered("proxyWithdraw", permissions);
  const after = page.trail[page.trail.length - 1];
  const key = tenant ? `${tenant}|${limit}|${after ?? ""}` : null;
  const [load, refresh] = useLoad<ProxyPage>(
    key,
    (signal) =>
      api.call("proxies", {
        params: { tenant: tenant as string },
        query: after ? { limit: String(limit), after } : { limit: String(limit) },
        signal,
      }),
    REFRESH_MS,
  );
  const open = location.search.get("proxy");

  const setQuery = (next: Record<string, string | undefined>) => {
    const q: Record<string, string> = {};
    for (const [k, v] of location.search) q[k] = v;
    for (const [k, v] of Object.entries(next)) {
      if (v) q[k] = v;
      else delete q[k];
    }
    navigate(location.path, { replace: true, query: q });
  };

  const fleetHealth: FleetHealth | undefined =
    health.state === "ok" ? health.data.fleet?.find((x) => x.tenant === tenant) : undefined;

  const rows = load.state === "ok" ? applyFilters(load.data.proxies, filters, locale) : [];
  const zones = useMemo(
    () => (load.state === "ok" ? Array.from(new Set(load.data.proxies.map((p) => p.zone))).sort() : []),
    [load],
  );

  const gaps: string[] = [];
  if (load.state === "ok") {
    const silent = unreachable(load.data.proxies);
    if (silent.length) gaps.push(t("fleet.partial.unreachable", { count: silent.length, ids: f.list(silent.map((p) => p.proxy_id)) }));
  }
  if (health.state === "outage") gaps.push(t("fleet.partial.summary"));

  const withdraw = async (p: Proxy): Promise<ConfirmFailure | null> => {
    const a = await api.call<Withdrawn>("proxyWithdraw", { params: { tenant: tenant as string, proxy: p.proxy_id } });
    if (a.kind === "ok") {
      setWithdrawing(null);
      toast.success(t("fleet.withdraw.done", { id: p.proxy_id, tokens: a.data.tokens_revoked }));
      refresh();
      refreshDeployment();
      return null;
    }
    return { error: a.error, kind: a.kind };
  };

  const columns: Column<Proxy>[] = [
    {
      id: "proxy",
      header: t("fleet.col.proxy"),
      sortValue: (p) => p.proxy_id,
      cell: (p) => (
        <button
          type="button"
          className={styles.open}
          onClick={() => setQuery({ proxy: p.proxy_id })}
          aria-label={t("fleet.open", { id: p.proxy_id })}
        >
          <code dir="ltr" translate="no">
            {p.proxy_id}
          </code>
        </button>
      ),
    },
    {
      id: "zone",
      header: t("fleet.col.zone"),
      sortValue: (p) => p.zone,
      cell: (p) => (
        <code dir="ltr" translate="no" className={styles.zone}>
          {p.zone}
        </code>
      ),
    },
    {
      id: "health",
      header: t("fleet.col.health"),
      sortValue: (p) => proxyHealth(p),
      cell: (p) => <StatusOf name={proxyHealth(p)} />,
    },
    {
      id: "heartbeat",
      header: t("fleet.col.heartbeat"),
      sortValue: (p) => p.last_heartbeat_at ?? "",
      cell: (p) => <Instant at={p.last_heartbeat_at} never={t("fleet.never")} />,
    },
    {
      id: "contract",
      header: t("fleet.col.contract"),
      numeric: true,
      sortValue: (p) => p.contract_version,
      cell: (p) => (p.contract_version ? f.number(p.contract_version) : <span className={styles.quiet}>{t("fleet.none")}</span>),
    },
    {
      id: "relays",
      header: t("fleet.col.relays"),
      numeric: true,
      sortValue: (p) => p.relay_registrations.length,
      cell: (p) => (p.relay_registrations.length ? <Badge tone="info">{f.number(p.relay_registrations.length)}</Badge> : <span className={styles.quiet}>{t("fleet.none")}</span>),
    },
    {
      id: "config",
      header: t("fleet.col.config"),
      sortValue: (p) => configStatus(p.config),
      cell: (p) => (
        <span className={styles.config}>
          <StatusOf name={configStatus(p.config)} />
          {p.config.desired_version ? (
            <span className={styles.versions}>
              <Version v={p.config.running_version} />
              {p.config.running_version && p.config.running_version !== p.config.desired_version ? (
                <span aria-hidden="true">{"→"}</span>
              ) : null}
              {p.config.running_version !== p.config.desired_version ? <Version v={p.config.desired_version} /> : null}
            </span>
          ) : null}
        </span>
      ),
    },
    {
      id: "sessions",
      header: t("fleet.col.sessions"),
      numeric: true,
      sortValue: (p) => p.session_count,
      cell: (p) => f.number(p.session_count),
    },
  ];

  const filterFields: FilterField[] = [
    { id: "zone", label: t("fleet.col.zone"), options: zones.map((z) => ({ value: z, label: z })) },
    { id: "health", label: t("fleet.col.health"), options: HEALTH_FILTER.map((s) => ({ value: s, label: t(STATUS[s].label) })) },
    { id: "config", label: t("fleet.col.config"), options: CONFIG_FILTER.map((s) => ({ value: s, label: t(STATUS[s].label) })) },
  ];

  const tree = (proxies: readonly Proxy[]): TreeNode[] =>
    Array.from(new Set(proxies.map((p) => p.zone)))
      .sort()
      .map((zone) => ({
        id: `zone:${zone}`,
        label: (
          <code dir="ltr" translate="no">
            {zone}
          </code>
        ),
        meta: <Badge>{f.number(proxies.filter((p) => p.zone === zone).length)}</Badge>,
        children: proxies
          .filter((p) => p.zone === zone)
          .map((p) => ({
            id: p.proxy_id,
            label: (
              <code dir="ltr" translate="no">
                {p.proxy_id}
              </code>
            ),
            meta: (
              <>
                <StatusOf name={proxyHealth(p)} />
                <StatusOf name={configStatus(p.config)} />
              </>
            ),
          })),
      }));

  const opened = load.state === "ok" && open ? load.data.proxies.find((p) => p.proxy_id === open) : undefined;
  const fleetStatus = fleetHealth ? fleetHealthStatus(fleetHealth.state) : null;

  return (
    <div className={styles.screen}>
      <PageHeader
        title={t("fleet.title")}
        lede={t("fleet.lede")}
        status={fleetStatus ? <StatusOf name={fleetStatus} pill /> : null}
        actions={
          <Button
            onClick={() => {
              refresh();
              refreshDeployment();
            }}
          >
            {t("fleet.refresh")}
          </Button>
        }
      />
      <section className={styles.panel} aria-labelledby="fleet-summary">
        <h2 id="fleet-summary" className={styles.panelTitle}>
          {t("fleet.summary")}
        </h2>
        <Summary health={health.state} fleet={fleetHealth} />
      </section>
      <ScreenFrame
        load={load}
        onRetry={refresh}
        gaps={gaps}
        isEmpty={(d) => d.proxies.length === 0 && page.trail.length === 0}
        loading={
          <TableSkeleton
            headers={[t("fleet.col.proxy"), t("fleet.col.zone"), t("fleet.col.health"), t("fleet.col.heartbeat"), t("fleet.col.contract"), t("fleet.col.relays"), t("fleet.col.config"), t("fleet.col.sessions")]}
          />
        }
        empty={
          <EmptyState
            title={t("fleet.empty.title")}
            body={t("fleet.empty.body")}
            action={<CopyableId value={`POST /api/v1/tenants/${tenant ?? ""}/proxies/enrollments`} />}
          />
        }
      >
        {(data) => (
          <>
            <FilterBar
              label={t("fleet.filters")}
              fields={filterFields}
              values={{ zone: filters.zone ?? "", health: filters.health ?? "", config: filters.config ?? "" }}
              onChange={(v) => setQuery({ zone: v.zone, health: v.health, config: v.config })}
              search={{ label: t("fleet.search"), value: filters.q ?? "", onChange: (q) => setQuery({ q }) }}
              views={{
                saved: views,
                onApply: (v) => navigate(location.path, { replace: true, query: Object.fromEntries(new URLSearchParams(v.query)) }),
                onSave: () => {
                  const query = location.search.toString();
                  const name = query ? decodeURIComponent(query.replaceAll("&", " · ")) : t("fleet.view.all");
                  const next = [...views.filter((v) => v.name !== name), { name, query }];
                  setViews(next);
                  saveViews(session.principal_id, "fleet", next);
                },
                onForget: () => {
                  setViews([]);
                  saveViews(session.principal_id, "fleet", []);
                },
              }}
            />
            <Tabs
              label={t("fleet.views")}
              selected={view}
              onSelect={(v) => setQuery({ view: v === "zones" ? "zones" : undefined })}
              tabs={[
                { id: "table", label: t("fleet.view.table") },
                { id: "zones", label: t("fleet.view.zones") },
              ]}
            >
              {view === "zones" ? (
                rows.length ? (
                  <Tree label={t("fleet.view.zones")} nodes={tree(rows)} onOpen={(id) => !id.startsWith("zone:") && setQuery({ proxy: id })} />
                ) : (
                  <EmptyState title={t("fleet.filtered.title")} body={t("fleet.filtered.body")} />
                )
              ) : (
                <DataTable
                  caption={t("fleet.table.caption")}
                  columns={columns}
                  rows={rows}
                  rowKey={(p) => p.proxy_id}
                  rowLabel={(p) => p.proxy_id}
                  sort={sort}
                  onSort={setSort}
                  hidden={hidden}
                  onHidden={setHidden}
                  selected={selected}
                  onSelected={setSelected}
                  partialNote={data.truncated ? t("fleet.table.onePage") : undefined}
                  empty={<EmptyState title={t("fleet.filtered.title")} body={t("fleet.filtered.body")} />}
                  rowActions={(p) => [
                    {
                      id: "open",
                      label: t("fleet.action.details"),
                      onSelect: () => setQuery({ proxy: p.proxy_id }),
                    },
                    ...(canWithdraw && p.state !== "revoked"
                      ? [{ id: "withdraw", label: t("fleet.action.withdraw"), danger: true, onSelect: () => setWithdrawing(p) }]
                      : []),
                  ]}
                />
              )}
            </Tabs>
            {selected.size ? (
              <p className={styles.selection} role="status">
                {t("fleet.selected", { count: selected.size })}
              </p>
            ) : null}
            <Pagination
              page={page}
              shown={data.proxies.length}
              truncated={data.truncated}
              next={data.next}
              onPage={(p) => {
                setSelected(new Set());
                setPage(p);
              }}
            />
          </>
        )}
      </ScreenFrame>
      <Drawer open={Boolean(opened)} title={opened ? <code dir="ltr" translate="no">{opened.proxy_id}</code> : ""} onClose={() => setQuery({ proxy: undefined })}>
        {opened ? (
          <ProxyDetail
            proxy={opened}
            canWithdraw={canWithdraw}
            onWithdraw={() => setWithdrawing(opened)}
          />
        ) : null}
      </Drawer>
      <ConfirmDialog
        open={withdrawing !== null}
        title={t("fleet.withdraw.title")}
        body={
          <>
            <p className={styles.para}>{t("fleet.withdraw.body", { id: withdrawing?.proxy_id ?? "" })}</p>
            <p className={styles.para}>{t("fleet.withdraw.irreversible")}</p>
          </>
        }
        name={withdrawing?.proxy_id ?? ""}
        action={t("fleet.withdraw.action")}
        onConfirm={() => (withdrawing ? withdraw(withdrawing) : Promise.resolve(null))}
        onClose={() => setWithdrawing(null)}
      />
    </div>
  );
}

function fleetHealthStatus(state: string): StatusName {
  switch (state) {
    case "healthy":
      return "healthy";
    case "degraded":
      return "degraded";
    case "in_progress":
      return "inProgress";
    case "stale":
      return "stale";
    default:
      return "notReported";
  }
}

function Summary({ health, fleet }: { health: "loading" | "ok" | "refused" | "outage"; fleet?: FleetHealth }) {
  const { t, f } = useI18n();
  if (health === "loading") {
    return (
      <div className={styles.summaryBody} aria-hidden="true">
        <Skeleton shape="title" size="full" />
        <Skeleton size="l" />
      </div>
    );
  }
  if (health === "refused") return <p className={styles.quiet}>{t("fleet.summary.forbidden")}</p>;
  if (health === "outage") return <p className={styles.quiet}>{t("fleet.summary.unread")}</p>;
  if (!fleet?.summary) return <p className={styles.quiet}>{t("fleet.summary.notReported")}</p>;
  const s = fleet.summary;
  const versions = Object.entries(s.contract_versions).sort(([a], [b]) => Number(a) - Number(b));
  return (
    <div className={styles.summaryBody}>
      <FleetChart summary={s} />
      <dl className={styles.facts}>
        <div>
          <dt>{t("fleet.summary.enrolled")}</dt>
          <dd>{f.number(s.enrolled)}</dd>
        </div>
        <div>
          <dt>{t("fleet.summary.drifted")}</dt>
          <dd>{f.number(s.config_drifted)}</dd>
        </div>
        <div>
          <dt>{t("fleet.summary.relays")}</dt>
          <dd>{f.number(s.relay_registrations)}</dd>
        </div>
        <div>
          <dt>{t("fleet.summary.contracts")}</dt>
          <dd>
            {versions.length
              ? f.list(versions.map(([v, n]) => t("fleet.summary.contract", { version: v, count: n })))
              : t("fleet.none")}
          </dd>
        </div>
      </dl>
      {s.truncated ? <p className={styles.quiet}>{t("fleet.summary.truncated")}</p> : null}
    </div>
  );
}

function ProxyDetail({ proxy: p, canWithdraw, onWithdraw }: { proxy: Proxy; canWithdraw: boolean; onWithdraw(): void }) {
  const { t, f } = useI18n();
  const c = p.config;
  return (
    <>
      <div className={styles.detailStatus}>
        <StatusOf name={proxyHealth(p)} pill />
        <StatusOf name={configStatus(c)} pill />
      </div>
      <dl className={styles.fields}>
        <dt>{t("fleet.col.zone")}</dt>
        <dd>
          <code dir="ltr" translate="no">
            {p.zone}
          </code>
        </dd>
        <dt>{t("fleet.detail.enrollment")}</dt>
        <dd>
          <code dir="ltr" translate="no">
            {p.state}
          </code>
        </dd>
        <dt>{t("fleet.col.heartbeat")}</dt>
        <dd>
          <Instant at={p.last_heartbeat_at} never={t("fleet.never")} />
        </dd>
        <dt>{t("fleet.detail.stream")}</dt>
        <dd>
          <Instant at={p.subscription_seen_at} never={t("fleet.never")} />
        </dd>
        <dt>{t("fleet.col.contract")}</dt>
        <dd>{p.contract_version ? f.number(p.contract_version) : t("fleet.none")}</dd>
        <dt>{t("fleet.col.sessions")}</dt>
        <dd>{f.number(p.session_count)}</dd>
        <dt>{t("fleet.col.relays")}</dt>
        <dd>
          {p.relay_registrations.length ? (
            <span className={styles.chips}>
              {p.relay_registrations.map((r) => (
                <Chip key={r} data>
                  {r}
                </Chip>
              ))}
            </span>
          ) : (
            t("fleet.detail.noRelays")
          )}
        </dd>
      </dl>
      <section className={styles.detailSection} aria-labelledby="fleet-detail-config">
        <h3 id="fleet-detail-config" className={styles.detailTitle}>
          {t("fleet.detail.config")}
        </h3>
        {c.state ? null : <p className={styles.para}>{t("fleet.detail.config.never")}</p>}
        <dl className={styles.fields}>
          <dt>{t("fleet.detail.desired")}</dt>
          <dd>
            {c.desired_version ? (
              <span className={styles.versions}>
                <Version v={c.desired_version} />
                {c.desired_hash ? <CopyableId value={c.desired_hash} short={c.desired_hash.slice(0, 19)} /> : null}
              </span>
            ) : (
              t("fleet.detail.nothingPublished")
            )}
          </dd>
          <dt>{t("fleet.detail.running")}</dt>
          <dd>
            {c.running_version ? (
              <span className={styles.versions}>
                <Version v={c.running_version} />
                {c.running_hash ? <CopyableId value={c.running_hash} short={c.running_hash.slice(0, 19)} /> : null}
              </span>
            ) : (
              t("fleet.never")
            )}
          </dd>
          <dt>{t("fleet.detail.reported")}</dt>
          <dd>
            <Instant at={c.reported_at} never={t("fleet.never")} />
          </dd>
          {c.restart_required?.length ? (
            <>
              <dt>{t("fleet.detail.restart")}</dt>
              <dd>
                <span className={styles.chips}>
                  {c.restart_required.map((k) => (
                    <Chip key={k} data>
                      {k}
                    </Chip>
                  ))}
                </span>
              </dd>
            </>
          ) : null}
        </dl>
        {c.last_error ? <CodeBlock label={t("fleet.detail.configError")} code={c.last_error} /> : null}
      </section>
      {p.last_error ? (
        <section className={styles.detailSection}>
          <CodeBlock label={t("fleet.detail.lastError")} code={p.last_error} />
        </section>
      ) : null}
      <JsonViewer label={t("fleet.detail.capabilities")} value={p.capabilities} />
      {canWithdraw && p.state !== "revoked" ? (
        <div>
          <Button variant="danger" onClick={onWithdraw}>
            {t("fleet.action.withdraw")}
          </Button>
        </div>
      ) : null}
    </>
  );
}
