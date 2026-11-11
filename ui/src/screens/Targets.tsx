// TARGETS (0054; PLAN M2, M3, M26; §6): the hosts policy is about, and their
// labels — policy inputs, so a label is operator data shown verbatim, mono
// and LTR, never translated (DESIGN.md §9). Three screens, each a deep link:
//   /console/inventory/targets[?label=k=v]    the list, one page at a time
//   /console/inventory/targets/<id>[?edit=1]  one target, and its recent
//                                             records in the audit view
//   /console/inventory/new/targets            a new target
// Reading is fleet:read; writing and removing a target is target:write, which
// the console offers only to a role holding it and the API refuses to anyone
// else (M2). Removing one is destructive and confirms by typing its id. A
// quarantine (M28, 0055) is shown in the list and the detail as a deny, and
// placed and released by a holder of session:revoke (Standing.tsx); a
// quarantined target is not removed until it is released.

import { useState, type FormEvent } from "react";
import { offered } from "../api/permit";
import type { AuditRecordPage, Target, TargetBody, TargetPage } from "../api/types";
import { Breadcrumb } from "../components/Badge";
import { Button, ButtonLink } from "../components/Button";
import { CopyableId } from "../components/CopyableId";
import { DataTable, TableSkeleton, type Column, type TableSort } from "../components/DataTable";
import { ConfirmDialog, type ConfirmFailure } from "../components/Dialog";
import { Field, Input, Textarea } from "../components/Field";
import { FilterBar } from "../components/FilterBar";
import { Ident } from "../components/Ident";
import { PageHeader } from "../components/PageHeader";
import { Pagination } from "../components/Pagination";
import { EmptyState, ErrorMessage, ScreenFrame, Skeleton } from "../components/States";
import { useToast } from "../components/Toast";
import { useI18n } from "../i18n/I18n";
import { Link, navigate, useLocation } from "../router/router";
import { useLoad } from "../shell/load";
import { useDeployment } from "../shell/Shell";
import {
  IdentLink,
  Instant,
  InventoryViews,
  LabelChips,
  orNone,
  readLimit,
  useFailure,
  usePagedList,
  usePermissions,
  useSetQuery,
} from "./InventoryParts";
import { labelFilters, labelsText, parseLabels } from "./inventoryModel";
import { TargetAccess, TargetStanding } from "./Standing";
import styles from "./Inventory.module.css";

const targetPath = (id: string) => `inventory/targets/${encodeURIComponent(id)}`;

/** The audit view, filtered to the records about this host (0051). */
const auditOf = (t: Target) => ({ to: "audit/records", query: { target: t.hostname } });

// ---------------------------------------------------------------------------
// /console/inventory/targets
// ---------------------------------------------------------------------------

export function TargetsScreen() {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const toast = useToast();
  const location = useLocation();
  const setQuery = useSetQuery();
  const permissions = usePermissions();
  const canWrite = offered("targetPut", permissions);
  const canDelete = offered("targetDelete", permissions);
  const labels = labelFilters(location.search);
  const limit = readLimit(location.search);
  const { load, refresh, page, setPage } = usePagedList<TargetPage>(
    "targets",
    labels.map((l) => ["label", l] as [string, string]),
    limit,
  );
  const [sort, setSort] = useState<TableSort | null>(null);
  const [hidden, setHidden] = useState<string[]>([]);
  const [removing, setRemoving] = useState<Target | null>(null);

  const remove = async (target: Target): Promise<ConfirmFailure | null> => {
    const a = await api.call("targetDelete", { params: { tenant: tenant as string, target: target.id } });
    if (a.kind !== "ok") return { error: a.error, kind: a.kind };
    setRemoving(null);
    toast.success(t("inventory.targets.removed", { id: target.id }));
    refresh();
    return null;
  };

  const columns: Column<Target>[] = [
    {
      id: "id",
      header: t("inventory.col.id"),
      sortValue: (x) => x.id,
      cell: (x) => <IdentLink to={targetPath(x.id)} value={x.id} label={t("inventory.targets.open", { id: x.id })} />,
    },
    { id: "access", header: t("inventory.col.access"), sortValue: (x) => (x.quarantined ? 0 : 1), cell: (x) => <TargetAccess target={x} /> },
    { id: "hostname", header: t("inventory.col.hostname"), sortValue: (x) => x.hostname, cell: (x) => <Ident value={x.hostname} /> },
    { id: "zone", header: t("inventory.col.zone"), sortValue: (x) => x.zone, cell: (x) => (x.zone ? <Ident value={x.zone} /> : null) },
    { id: "labels", header: t("inventory.col.labels"), cell: (x) => <LabelChips labels={x.labels} none={t("inventory.none")} /> },
    {
      id: "method",
      header: t("inventory.col.method"),
      sortValue: (x) => x.credential_method ?? "",
      cell: (x) => (x.credential_method ? <Ident value={x.credential_method} /> : <span className={styles.quiet}>{t("inventory.method.default")}</span>),
    },
    { id: "updated", header: t("inventory.col.updated"), sortValue: (x) => x.updated_at ?? "", cell: (x) => <Instant at={x.updated_at} /> },
  ];

  const filtered = labels.length > 0;
  return (
    <div className={styles.screen}>
      <PageHeader
        title={t("inventory.title")}
        lede={t("inventory.targets.lede")}
        actions={
          <>
            {canWrite ? (
              <ButtonLink to="inventory/new/targets" variant="primary" data-create="">
                {t("inventory.targets.add")}
              </ButtonLink>
            ) : null}
            <Button onClick={refresh}>{t("inventory.refresh")}</Button>
          </>
        }
      />
      <InventoryViews current="targets" />
      <FilterBar
        label={t("inventory.filters")}
        fields={[{ id: "label", label: t("inventory.filter.label"), kind: "text", mono: true }]}
        values={{ label: labels[0] ?? "" }}
        onChange={(v) => setQuery({ label: v.label })}
      />
      <ScreenFrame
        load={load}
        onRetry={refresh}
        isEmpty={(d) => d.targets.length === 0 && page.trail.length === 0}
        loading={<TableSkeleton headers={columns.map((c) => c.header)} />}
        empty={
          filtered ? (
            <EmptyState
              title={t("inventory.targets.filtered.title")}
              body={t("inventory.targets.filtered.body")}
              action={<Button onClick={() => setQuery({ label: undefined })}>{t("inventory.filter.clear")}</Button>}
            />
          ) : (
            <EmptyState
              title={t("inventory.targets.empty.title")}
              body={t("inventory.targets.empty.body")}
              action={
                canWrite ? (
                  <ButtonLink to="inventory/new/targets" variant="primary">
                    {t("inventory.targets.add")}
                  </ButtonLink>
                ) : (
                  <CopyableId value={`PUT /api/v1/tenants/${tenant ?? ""}/targets/{target}`} />
                )
              }
            />
          )
        }
      >
        {(data) => (
          <>
            <DataTable
              caption={t("inventory.targets.caption")}
              columns={columns}
              rows={data.targets}
              rowKey={(x) => x.id}
              rowLabel={(x) => x.id}
              sort={sort}
              onSort={setSort}
              hidden={hidden}
              onHidden={setHidden}
              partialNote={data.truncated ? t("inventory.onePage") : undefined}
              rowActions={(x) => [
                { id: "open", label: t("inventory.action.open"), onSelect: () => navigate(targetPath(x.id)) },
                { id: "audit", label: t("inventory.action.audit"), onSelect: () => navigate(auditOf(x).to, { query: auditOf(x).query }) },
                ...(canWrite ? [{ id: "edit", label: t("inventory.action.edit"), onSelect: () => navigate(targetPath(x.id), { query: { edit: "1" } }) }] : []),
                ...(canDelete ? [{ id: "remove", label: t("inventory.action.remove"), danger: true, onSelect: () => setRemoving(x) }] : []),
              ]}
            />
            <Pagination page={page} shown={data.targets.length} truncated={data.truncated} next={data.next} onPage={setPage} />
          </>
        )}
      </ScreenFrame>
      <RemoveTarget target={removing} onClose={() => setRemoving(null)} onConfirm={remove} />
    </div>
  );
}

function RemoveTarget({ target, onClose, onConfirm }: { target: Target | null; onClose(): void; onConfirm(t: Target): Promise<ConfirmFailure | null> }) {
  const { t } = useI18n();
  return (
    <ConfirmDialog
      open={target !== null}
      title={t("inventory.targets.remove.title")}
      body={
        <>
          <p className={styles.para}>{t("inventory.targets.remove.body", { id: target?.id ?? "", hostname: target?.hostname ?? "" })}</p>
          <p className={styles.para}>{t("inventory.targets.remove.consequence")}</p>
          {target?.quarantined ? (
            <p className={styles.para} data-remove-quarantined="">
              {t("inventory.targets.remove.quarantined")}
            </p>
          ) : null}
        </>
      }
      name={target?.id ?? ""}
      action={t("inventory.targets.remove.action")}
      onConfirm={() => (target ? onConfirm(target) : Promise.resolve(null))}
      onClose={onClose}
    />
  );
}

// ---------------------------------------------------------------------------
// /console/inventory/targets/<id>
// ---------------------------------------------------------------------------

export function TargetScreen({ id }: { id: string }) {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const toast = useToast();
  const location = useLocation();
  const setQuery = useSetQuery();
  const permissions = usePermissions();
  const canWrite = offered("targetPut", permissions);
  const canDelete = offered("targetDelete", permissions);
  const canAudit = offered("auditRecords", permissions);
  const canRevoke = offered("targetQuarantine", permissions);
  const editing = canWrite && location.search.get("edit") === "1";
  const [removing, setRemoving] = useState<Target | null>(null);
  const [load, refresh] = useLoad<Target | null>(tenant ? `target|${tenant}|${id}` : null, async (signal) =>
    orNone(await api.call<Target>("target", { params: { tenant: tenant as string, target: id }, signal })),
  );
  const hostname = load.state === "ok" && load.data ? load.data.hostname : null;
  const [activity] = useLoad<AuditRecordPage>(tenant && hostname && canAudit ? `target-audit|${tenant}|${hostname}` : null, (signal) =>
    api.call("auditRecords", { params: { tenant: tenant as string }, query: { target: hostname as string, limit: "10" }, signal }),
  );

  const remove = async (target: Target): Promise<ConfirmFailure | null> => {
    const a = await api.call("targetDelete", { params: { tenant: tenant as string, target: target.id } });
    if (a.kind !== "ok") return { error: a.error, kind: a.kind };
    toast.success(t("inventory.targets.removed", { id: target.id }));
    navigate("inventory/targets");
    return null;
  };

  const found = load.state === "ok" ? load.data : null;
  return (
    <div className={styles.screen}>
      <PageHeader
        breadcrumb={<Breadcrumb items={[{ label: t("inventory.title"), to: "inventory/targets" }, { label: t("inventory.view.targets"), to: "inventory/targets" }, { label: <Ident value={id} /> }]} />}
        title={<Ident value={id} title />}
        lede={t("inventory.targets.detail.lede")}
        status={found ? <TargetAccess target={found} /> : null}
        actions={
          found ? (
            <>
              <ButtonLink to={auditOf(found).to} query={auditOf(found).query} data-audit-link="">
                {t("inventory.action.audit")}
              </ButtonLink>
              {canWrite && !editing ? (
                <Button onClick={() => setQuery({ edit: "1" })} data-edit="">
                  {t("inventory.action.edit")}
                </Button>
              ) : null}
              {canDelete ? (
                <Button variant="danger" onClick={() => setRemoving(found)} data-remove="">
                  {t("inventory.action.remove")}
                </Button>
              ) : null}
            </>
          ) : null
        }
      />
      <ScreenFrame
        load={load}
        onRetry={refresh}
        isEmpty={(x) => x === null}
        gaps={activity.state === "outage" ? [t("inventory.targets.partial.activity")] : []}
        empty={
          <EmptyState
            title={t("inventory.targets.missing.title")}
            body={t("inventory.targets.missing.body", { id: `⁨${id}⁩` })}
            action={<Link to="inventory/targets">{t("inventory.targets.back")}</Link>}
          />
        }
        loading={
          <div className={styles.stack}>
            <Skeleton size="l" />
            <Skeleton shape="block" size="full" />
          </div>
        }
      >
        {(target) =>
          target === null ? null : (
            <>
              {editing ? (
                <section className={styles.panel} aria-labelledby="target-edit">
                  <h2 id="target-edit" className={styles.panelTitle}>
                    {t("inventory.targets.edit.title")}
                  </h2>
                  <TargetForm
                    target={target}
                    onSaved={() => {
                      toast.success(t("inventory.targets.saved", { id: target.id }));
                      setQuery({ edit: undefined });
                      refresh();
                    }}
                    onCancel={() => setQuery({ edit: undefined })}
                  />
                </section>
              ) : (
                <section className={styles.panel} aria-labelledby="target-facts">
                  <h2 id="target-facts" className={styles.panelTitle}>
                    {t("inventory.targets.facts")}
                  </h2>
                  <dl className={styles.facts}>
                    <dt>{t("inventory.col.hostname")}</dt>
                    <dd>
                      <Ident value={target.hostname} />
                    </dd>
                    <dt>{t("inventory.col.zone")}</dt>
                    <dd>{target.zone ? <Ident value={target.zone} /> : <span className={styles.quiet}>{t("inventory.none")}</span>}</dd>
                    <dt>{t("inventory.col.labels")}</dt>
                    <dd>
                      <LabelChips labels={target.labels} none={t("inventory.none")} />
                    </dd>
                    <dt>{t("inventory.col.method")}</dt>
                    <dd>
                      {target.credential_method ? <Ident value={target.credential_method} /> : <span className={styles.quiet}>{t("inventory.method.default")}</span>}
                    </dd>
                    <dt>{t("inventory.col.created")}</dt>
                    <dd>
                      <Instant at={target.created_at} />
                    </dd>
                    <dt>{t("inventory.col.updated")}</dt>
                    <dd>
                      <Instant at={target.updated_at} />
                    </dd>
                  </dl>
                </section>
              )}
              <TargetStanding target={target} canRevoke={canRevoke} onChanged={refresh} />
              <section className={styles.panel} aria-labelledby="target-activity" data-activity={activity.state}>
                <div className={styles.panelHead}>
                  <h2 id="target-activity" className={styles.panelTitle}>
                    {t("inventory.targets.activity")}
                  </h2>
                  <Link to={auditOf(target).to} query={auditOf(target).query} className={styles.link}>
                    {t("inventory.targets.activity.all")}
                  </Link>
                </div>
                {!canAudit ? (
                  <p className={styles.quiet}>{t("inventory.targets.activity.forbidden")}</p>
                ) : activity.state === "loading" ? (
                  <TableSkeleton headers={[t("inventory.col.when"), t("inventory.col.kind"), t("inventory.col.subject")]} />
                ) : activity.state !== "ok" ? (
                  <ErrorMessage error={activity.error} kind={activity.state} />
                ) : activity.data.records.length === 0 ? (
                  <p className={styles.quiet}>{t("inventory.targets.activity.none")}</p>
                ) : (
                  <DataTable
                    caption={t("inventory.targets.activity.caption")}
                    columns={[
                      { id: "when", header: t("inventory.col.when"), cell: (r) => <Instant at={r.recorded_at} /> },
                      { id: "kind", header: t("inventory.col.kind"), cell: (r) => <Ident value={r.kind} /> },
                      { id: "subject", header: t("inventory.col.subject"), cell: (r) => (r.subject ? <Ident value={r.subject} /> : null) },
                    ]}
                    rows={activity.data.records}
                    rowKey={(r) => r.record_id}
                    rowLabel={(r) => r.record_id}
                    partialNote={activity.data.truncated ? t("inventory.targets.activity.more") : undefined}
                  />
                )}
              </section>
            </>
          )
        }
      </ScreenFrame>
      <RemoveTarget target={removing} onClose={() => setRemoving(null)} onConfirm={remove} />
    </div>
  );
}

// ---------------------------------------------------------------------------
// /console/inventory/new/targets, and a target's edit
// ---------------------------------------------------------------------------

export function TargetNewScreen() {
  const { t } = useI18n();
  const toast = useToast();
  const permissions = usePermissions();
  return (
    <div className={styles.screen}>
      <PageHeader
        breadcrumb={<Breadcrumb items={[{ label: t("inventory.title"), to: "inventory/targets" }, { label: t("inventory.view.targets"), to: "inventory/targets" }, { label: t("inventory.targets.add") }]} />}
        title={t("inventory.targets.new.title")}
        lede={t("inventory.targets.new.lede")}
      />
      <section className={styles.panel}>
        {offered("targetPut", permissions) ? (
          <TargetForm
            onSaved={(saved) => {
              toast.success(t("inventory.targets.saved", { id: saved.id }));
              navigate(targetPath(saved.id));
            }}
            onCancel={() => navigate("inventory/targets")}
          />
        ) : (
          <ErrorMessage error={{ code: "forbidden" }} kind="refused" />
        )}
      </section>
    </div>
  );
}

const TARGET_FIELDS = ["id", "hostname", "zone", "labels", "credential_method"] as const;

/** Writes a target whole (PUT): a new one when `target` is absent. */
function TargetForm({ target, onSaved, onCancel }: { target?: Target; onSaved(t: Target): void; onCancel(): void }) {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const [id, setId] = useState(target?.id ?? "");
  const [hostname, setHostname] = useState(target?.hostname ?? "");
  const [zone, setZone] = useState(target?.zone ?? "");
  const [labels, setLabels] = useState(labelsText(target?.labels));
  const [method, setMethod] = useState(target?.credential_method ?? "");
  const [busy, setBusy] = useState(false);
  const [local, setLocal] = useState<Record<string, string>>({});
  const fail = useFailure(TARGET_FIELDS);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (busy) return;
    const parsed = parseLabels(labels);
    const problems: Record<string, string> = {};
    if (!id.trim()) problems.id = t("inventory.problem.required");
    if (parsed === null) problems.labels = t("inventory.problem.labels");
    setLocal(problems);
    fail.setFailure(null);
    if (Object.keys(problems).length) return;
    setBusy(true);
    const body: TargetBody = { hostname: hostname.trim(), zone: zone.trim(), labels: parsed ?? {}, credential_method: method.trim() };
    const a = await api.call<Target>("targetPut", { params: { tenant: tenant as string, target: id.trim() }, body });
    setBusy(false);
    if (a.kind === "ok") onSaved(a.data);
    else fail.setFailure({ error: a.error, kind: a.kind });
  };

  return (
    <form className={styles.form} onSubmit={submit} noValidate data-target-form="">
      <div className={styles.row}>
        <Field label={t("inventory.col.id")} hint={target ? undefined : t("inventory.targets.field.id.hint")} error={local.id ?? fail.at("id")}>
          <Input mono value={id} onChange={(e) => setId(e.target.value)} readOnly={Boolean(target)} name="id" />
        </Field>
        <Field label={t("inventory.col.hostname")} error={fail.at("hostname")}>
          <Input mono value={hostname} onChange={(e) => setHostname(e.target.value)} name="hostname" />
        </Field>
      </div>
      <div className={styles.row}>
        <Field label={t("inventory.col.zone")} error={fail.at("zone")}>
          <Input mono value={zone} onChange={(e) => setZone(e.target.value)} name="zone" />
        </Field>
        <Field label={t("inventory.col.method")} hint={t("inventory.targets.field.method.hint")} error={fail.at("credential_method")}>
          <Input mono value={method} onChange={(e) => setMethod(e.target.value)} name="credential_method" />
        </Field>
      </div>
      <Field label={t("inventory.col.labels")} hint={t("inventory.targets.field.labels.hint")} error={local.labels ?? fail.at("labels")}>
        <Textarea mono rows={4} value={labels} onChange={(e) => setLabels(e.target.value)} name="labels" />
      </Field>
      {fail.form}
      <div className={styles.actions}>
        <Button type="submit" variant="primary" loading={busy}>
          {target ? t("inventory.save") : t("inventory.targets.add")}
        </Button>
        <Button type="button" variant="ghost" onClick={onCancel}>
          {t("inventory.cancel")}
        </Button>
      </div>
    </form>
  );
}
