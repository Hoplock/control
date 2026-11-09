// IDENTITIES, GROUPS AND ROLES (0054; PLAN M2, M7, M18, M26; §6): who policy
// is about, the groups policy matches on, and the fixed roles that decide
// what each operator may do here. Every screen is a deep link:
//   /console/inventory/subjects[?group=]          the subjects, a page at a time
//   /console/inventory/subjects/<id>[?edit=1]      one subject: its groups, the
//                                                  roles it holds directly, and
//                                                  its grants
//   /console/inventory/new/subjects                a new local subject
//   /console/inventory/groups                      every group name in the tenant
//   /console/inventory/groups/<name>               one group: its members and roles
//   /console/inventory/roles                       the fixed role set
//   /console/inventory/roles/<role>                one role's holders
// Reading is identity:read. Writing a subject and binding or withdrawing a role
// is identity:write — admin's alone (§6) — offered only to a role holding it
// and refused by the API to anyone else (M2). A role is the product's code, so
// its description is the console's; a group name, a subject id and a claim are
// data, mono and untranslated (DESIGN.md §9). A subject's lock, a directory's
// facts and a deactivation are 0055's.

import { useState, type FormEvent, type ReactNode } from "react";
import { offered } from "../api/permit";
import type { GrantPage, Group, GroupPage, RoleBinding, RoleBindingPage, Subject, SubjectBody, SubjectPage } from "../api/types";
import { Badge, Breadcrumb } from "../components/Badge";
import { Button, ButtonLink } from "../components/Button";
import { CopyableId } from "../components/CopyableId";
import { DataTable, TableSkeleton, type Column, type TableSort } from "../components/DataTable";
import { ConfirmDialog, type ConfirmFailure } from "../components/Dialog";
import { Checkbox, Field, Input, Radio, Select, Textarea, Choices } from "../components/Field";
import { FilterBar } from "../components/FilterBar";
import { Ident } from "../components/Ident";
import { PageHeader } from "../components/PageHeader";
import { Pagination } from "../components/Pagination";
import { EmptyState, ErrorMessage, ScreenFrame, Skeleton } from "../components/States";
import { StatusOf } from "../components/Status";
import { useToast } from "../components/Toast";
import { useI18n } from "../i18n/I18n";
import { Link, navigate, useLocation } from "../router/router";
import { useLoad, type Load } from "../shell/load";
import { useDeployment } from "../shell/Shell";
import {
  IdentChips,
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
import { bindingKey, grantStatus, holderOf, holderPath, labelsText, parseLabels, parseList, roleKey } from "./inventoryModel";
import styles from "./Inventory.module.css";

export const subjectPath = (id: string) => `inventory/subjects/${encodeURIComponent(id)}`;
export const groupPath = (id: string) => `inventory/groups/${encodeURIComponent(id)}`;
const rolePath = (role: string) => `inventory/roles/${encodeURIComponent(role)}`;
const grantPath = (id: string) => `inventory/grants/${encodeURIComponent(id)}`;

function Crumbs({ view, to, here }: { view: string; to: string; here: ReactNode }) {
  const { t } = useI18n();
  return <Breadcrumb items={[{ label: t("inventory.title"), to: "inventory/targets" }, { label: view, to }, { label: here }]} />;
}

// ---------------------------------------------------------------------------
// /console/inventory/subjects
// ---------------------------------------------------------------------------

export function SubjectsScreen() {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const toast = useToast();
  const location = useLocation();
  const setQuery = useSetQuery();
  const permissions = usePermissions();
  const canWrite = offered("subjectPut", permissions);
  const canDelete = offered("subjectDelete", permissions);
  const group = location.search.get("group") ?? "";
  const limit = readLimit(location.search);
  const { load, refresh, page, setPage } = usePagedList<SubjectPage>("subjects", group ? [["group", group]] : [], limit);
  const [sort, setSort] = useState<TableSort | null>(null);
  const [hidden, setHidden] = useState<string[]>([]);
  const [removing, setRemoving] = useState<Subject | null>(null);

  const remove = async (s: Subject): Promise<ConfirmFailure | null> => {
    const a = await api.call("subjectDelete", { params: { tenant: tenant as string, subject: s.id } });
    if (a.kind !== "ok") return { error: a.error, kind: a.kind };
    setRemoving(null);
    toast.success(t("inventory.subjects.removed", { id: s.id }));
    refresh();
    return null;
  };

  const columns: Column<Subject>[] = [
    {
      id: "id",
      header: t("inventory.col.id"),
      sortValue: (s) => s.id,
      cell: (s) => <IdentLink to={subjectPath(s.id)} value={s.id} label={t("inventory.subjects.open", { id: s.id })} />,
    },
    {
      id: "name",
      header: t("inventory.col.displayName"),
      sortValue: (s) => s.display_name,
      cell: (s) => (
        <span className={styles.operator} translate="no">
          {s.display_name}
        </span>
      ),
    },
    { id: "source", header: t("inventory.col.source"), sortValue: (s) => s.source, cell: (s) => <Ident value={s.source} /> },
    { id: "groups", header: t("inventory.col.groups"), cell: (s) => <IdentChips items={s.groups} to={groupPath} none={t("inventory.none")} /> },
    {
      id: "breakGlass",
      header: t("inventory.col.breakGlass"),
      sortValue: (s) => (s.break_glass ? 0 : 1),
      cell: (s) => (s.break_glass ? <Badge tone="info">{t("inventory.subjects.breakGlass")}</Badge> : null),
    },
  ];

  return (
    <div className={styles.screen}>
      <PageHeader
        title={t("inventory.title")}
        lede={t("inventory.subjects.lede")}
        actions={
          <>
            {canWrite ? (
              <ButtonLink to="inventory/new/subjects" variant="primary" data-create="">
                {t("inventory.subjects.add")}
              </ButtonLink>
            ) : null}
            <Button onClick={refresh}>{t("inventory.refresh")}</Button>
          </>
        }
      />
      <InventoryViews current="subjects" />
      <FilterBar
        label={t("inventory.filters")}
        fields={[{ id: "group", label: t("inventory.filter.group"), kind: "text", mono: true }]}
        values={{ group }}
        onChange={(v) => setQuery({ group: v.group })}
      />
      <ScreenFrame
        load={load}
        onRetry={refresh}
        isEmpty={(d) => d.subjects.length === 0 && page.trail.length === 0}
        loading={<TableSkeleton headers={columns.map((c) => c.header)} />}
        empty={
          group ? (
            <EmptyState
              title={t("inventory.subjects.filtered.title")}
              body={t("inventory.subjects.filtered.body", { group: `⁨${group}⁩` })}
              action={<Button onClick={() => setQuery({ group: undefined })}>{t("inventory.filter.clear")}</Button>}
            />
          ) : (
            <EmptyState
              title={t("inventory.subjects.empty.title")}
              body={t("inventory.subjects.empty.body")}
              action={
                canWrite ? (
                  <ButtonLink to="inventory/new/subjects" variant="primary">
                    {t("inventory.subjects.add")}
                  </ButtonLink>
                ) : (
                  <CopyableId value={`PUT /api/v1/tenants/${tenant ?? ""}/subjects/{subject}`} />
                )
              }
            />
          )
        }
      >
        {(data) => (
          <>
            <DataTable
              caption={t("inventory.subjects.caption")}
              columns={columns}
              rows={data.subjects}
              rowKey={(s) => s.id}
              rowLabel={(s) => s.id}
              sort={sort}
              onSort={setSort}
              hidden={hidden}
              onHidden={setHidden}
              partialNote={data.truncated ? t("inventory.onePage") : undefined}
              rowActions={(s) => [
                { id: "open", label: t("inventory.action.open"), onSelect: () => navigate(subjectPath(s.id)) },
                { id: "audit", label: t("inventory.action.audit"), onSelect: () => navigate("audit/records", { query: { subject: s.id } }) },
                ...(canWrite ? [{ id: "edit", label: t("inventory.action.edit"), onSelect: () => navigate(subjectPath(s.id), { query: { edit: "1" } }) }] : []),
                ...(canDelete ? [{ id: "remove", label: t("inventory.action.remove"), danger: true, onSelect: () => setRemoving(s) }] : []),
              ]}
            />
            <Pagination page={page} shown={data.subjects.length} truncated={data.truncated} next={data.next} onPage={setPage} />
          </>
        )}
      </ScreenFrame>
      <RemoveSubject subject={removing} onClose={() => setRemoving(null)} onConfirm={remove} />
    </div>
  );
}

function RemoveSubject({ subject, onClose, onConfirm }: { subject: Subject | null; onClose(): void; onConfirm(s: Subject): Promise<ConfirmFailure | null> }) {
  const { t } = useI18n();
  return (
    <ConfirmDialog
      open={subject !== null}
      title={t("inventory.subjects.remove.title")}
      body={
        <>
          <p className={styles.para}>{t("inventory.subjects.remove.body", { id: subject?.id ?? "" })}</p>
          <p className={styles.para}>{t("inventory.subjects.remove.consequence")}</p>
        </>
      }
      name={subject?.id ?? ""}
      action={t("inventory.subjects.remove.action")}
      onConfirm={() => (subject ? onConfirm(subject) : Promise.resolve(null))}
      onClose={onClose}
    />
  );
}

// ---------------------------------------------------------------------------
// /console/inventory/subjects/<id>
// ---------------------------------------------------------------------------

export function SubjectScreen({ id }: { id: string }) {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const toast = useToast();
  const location = useLocation();
  const setQuery = useSetQuery();
  const permissions = usePermissions();
  const canWrite = offered("subjectPut", permissions);
  const canDelete = offered("subjectDelete", permissions);
  const canGrants = offered("grants", permissions);
  const editing = canWrite && location.search.get("edit") === "1";
  const [removing, setRemoving] = useState<Subject | null>(null);
  const [load, refresh] = useLoad<Subject | null>(tenant ? `subject|${tenant}|${id}` : null, async (signal) =>
    orNone(await api.call<Subject>("subject", { params: { tenant: tenant as string, subject: id }, signal })),
  );
  const found = load.state === "ok" ? load.data : null;
  const bindings = usePagedList<RoleBindingPage>("roleBindings", [["subject", id]], 100, Boolean(found));
  const [grants] = useLoad<GrantPage>(tenant && found && canGrants ? `subject-grants|${tenant}|${id}` : null, (signal) =>
    api.call("grants", { params: { tenant: tenant as string }, query: { subject: id, limit: "10" }, signal }),
  );

  const remove = async (s: Subject): Promise<ConfirmFailure | null> => {
    const a = await api.call("subjectDelete", { params: { tenant: tenant as string, subject: s.id } });
    if (a.kind !== "ok") return { error: a.error, kind: a.kind };
    toast.success(t("inventory.subjects.removed", { id: s.id }));
    navigate("inventory/subjects");
    return null;
  };

  const gaps = [
    ...(bindings.load.state === "outage" ? [t("inventory.subjects.partial.roles")] : []),
    ...(grants.state === "outage" ? [t("inventory.subjects.partial.grants")] : []),
  ];
  return (
    <div className={styles.screen}>
      <PageHeader
        breadcrumb={<Crumbs view={t("inventory.view.subjects")} to="inventory/subjects" here={<Ident value={id} />} />}
        title={<Ident value={id} title />}
        lede={found?.display_name ? <span translate="no">{found.display_name}</span> : t("inventory.subjects.detail.lede")}
        status={found?.break_glass ? <Badge tone="info">{t("inventory.subjects.breakGlass")}</Badge> : null}
        actions={
          found ? (
            <>
              <ButtonLink to="audit/records" query={{ subject: id }} data-audit-link="">
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
        gaps={gaps}
        empty={
          <EmptyState
            title={t("inventory.subjects.missing.title")}
            body={t("inventory.subjects.missing.body", { id: `⁨${id}⁩` })}
            action={<Link to="inventory/subjects">{t("inventory.subjects.back")}</Link>}
          />
        }
        loading={
          <div className={styles.stack}>
            <Skeleton size="l" />
            <Skeleton shape="block" size="full" />
          </div>
        }
      >
        {(subject) =>
          subject === null ? null : (
            <>
              {editing ? (
                <section className={styles.panel} aria-labelledby="subject-edit">
                  <h2 id="subject-edit" className={styles.panelTitle}>
                    {t("inventory.subjects.edit.title")}
                  </h2>
                  <SubjectForm
                    subject={subject}
                    onSaved={() => {
                      toast.success(t("inventory.subjects.saved", { id: subject.id }));
                      setQuery({ edit: undefined });
                      refresh();
                    }}
                    onCancel={() => setQuery({ edit: undefined })}
                  />
                </section>
              ) : (
                <section className={styles.panel} aria-labelledby="subject-facts">
                  <h2 id="subject-facts" className={styles.panelTitle}>
                    {t("inventory.subjects.facts")}
                  </h2>
                  <dl className={styles.facts}>
                    <dt>{t("inventory.col.source")}</dt>
                    <dd>
                      <Ident value={subject.source} />
                    </dd>
                    <dt>{t("inventory.col.principals")}</dt>
                    <dd>
                      <IdentChips items={subject.principals} none={t("inventory.none")} />
                    </dd>
                    <dt>{t("inventory.col.groups")}</dt>
                    <dd>
                      <IdentChips items={subject.groups} to={groupPath} none={t("inventory.none")} />
                    </dd>
                    <dt>{t("inventory.col.claims")}</dt>
                    <dd>
                      <LabelChips labels={subject.claims} none={t("inventory.none")} />
                    </dd>
                    {subject.mapping_version ? (
                      <>
                        <dt>{t("inventory.col.mapping")}</dt>
                        <dd>{t("inventory.subjects.mapping", { version: subject.mapping_version })}</dd>
                      </>
                    ) : null}
                  </dl>
                </section>
              )}
              <BindingsPanel
                title={t("inventory.subjects.roles")}
                lede={t("inventory.subjects.roles.lede")}
                list={bindings}
                fixed={{ subject_id: subject.id }}
              />
              <section className={styles.panel} aria-labelledby="subject-grants" data-grants={grants.state}>
                <div className={styles.panelHead}>
                  <h2 id="subject-grants" className={styles.panelTitle}>
                    {t("inventory.subjects.grants")}
                  </h2>
                  <Link to="inventory/grants" query={{ subject: subject.id }} className={styles.link}>
                    {t("inventory.subjects.grants.all")}
                  </Link>
                </div>
                {!canGrants ? (
                  <p className={styles.quiet}>{t("inventory.subjects.grants.forbidden")}</p>
                ) : grants.state === "loading" ? (
                  <TableSkeleton headers={[t("inventory.col.grant"), t("inventory.col.scope"), t("inventory.col.state")]} />
                ) : grants.state !== "ok" ? (
                  <ErrorMessage error={grants.error} kind={grants.state} />
                ) : grants.data.grants.length === 0 ? (
                  <p className={styles.quiet}>{t("inventory.subjects.grants.none")}</p>
                ) : (
                  <DataTable
                    caption={t("inventory.subjects.grants.caption")}
                    columns={[
                      {
                        id: "grant",
                        header: t("inventory.col.grant"),
                        cell: (g) => (
                          <span data-volatile="">
                            <IdentLink to={grantPath(g.id)} value={g.id} />
                          </span>
                        ),
                      },
                      { id: "scope", header: t("inventory.col.scope"), cell: (g) => <Ident value={g.scope.name} /> },
                      { id: "state", header: t("inventory.col.state"), cell: (g) => <StatusOf name={grantStatus(g.state)} /> },
                      { id: "ends", header: t("inventory.col.expires"), cell: (g) => <Instant at={g.expires_at} /> },
                    ]}
                    rows={grants.data.grants}
                    rowKey={(g) => g.id}
                    rowLabel={(g) => g.id}
                    partialNote={grants.data.truncated ? t("inventory.subjects.grants.more") : undefined}
                  />
                )}
              </section>
            </>
          )
        }
      </ScreenFrame>
      <RemoveSubject subject={removing} onClose={() => setRemoving(null)} onConfirm={remove} />
    </div>
  );
}

export function SubjectNewScreen() {
  const { t } = useI18n();
  const toast = useToast();
  const permissions = usePermissions();
  return (
    <div className={styles.screen}>
      <PageHeader
        breadcrumb={<Crumbs view={t("inventory.view.subjects")} to="inventory/subjects" here={t("inventory.subjects.add")} />}
        title={t("inventory.subjects.new.title")}
        lede={t("inventory.subjects.new.lede")}
      />
      <section className={styles.panel}>
        {offered("subjectPut", permissions) ? (
          <SubjectForm
            onSaved={(saved) => {
              toast.success(t("inventory.subjects.saved", { id: saved.id }));
              navigate(subjectPath(saved.id));
            }}
            onCancel={() => navigate("inventory/subjects")}
          />
        ) : (
          <ErrorMessage error={{ code: "forbidden" }} kind="refused" />
        )}
      </section>
    </div>
  );
}

const SUBJECT_FIELDS = ["id", "display_name", "source", "principals", "groups", "claims"] as const;

/** Writes a subject whole (PUT). */
function SubjectForm({ subject, onSaved, onCancel }: { subject?: Subject; onSaved(s: Subject): void; onCancel(): void }) {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const [id, setId] = useState(subject?.id ?? "");
  const [name, setName] = useState(subject?.display_name ?? "");
  const [source, setSource] = useState(subject?.source ?? "local");
  const [principals, setPrincipals] = useState((subject?.principals ?? []).join("\n"));
  const [groups, setGroups] = useState((subject?.groups ?? []).join("\n"));
  const [claims, setClaims] = useState(labelsText(subject?.claims));
  const [breakGlass, setBreakGlass] = useState(subject?.break_glass ?? false);
  const [busy, setBusy] = useState(false);
  const [local, setLocal] = useState<Record<string, string>>({});
  const fail = useFailure(SUBJECT_FIELDS);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (busy) return;
    const parsed = parseLabels(claims);
    const problems: Record<string, string> = {};
    if (!id.trim()) problems.id = t("inventory.problem.required");
    if (parsed === null) problems.claims = t("inventory.problem.labels");
    setLocal(problems);
    fail.setFailure(null);
    if (Object.keys(problems).length) return;
    setBusy(true);
    const body: SubjectBody = {
      source: source.trim(),
      display_name: name.trim(),
      principals: parseList(principals),
      groups: parseList(groups),
      claims: parsed ?? {},
      break_glass: breakGlass,
    };
    const a = await api.call<Subject>("subjectPut", { params: { tenant: tenant as string, subject: id.trim() }, body });
    setBusy(false);
    if (a.kind === "ok") onSaved(a.data);
    else fail.setFailure({ error: a.error, kind: a.kind });
  };

  return (
    <form className={styles.form} onSubmit={submit} noValidate data-subject-form="">
      <div className={styles.row}>
        <Field label={t("inventory.col.id")} hint={subject ? undefined : t("inventory.subjects.field.id.hint")} error={local.id ?? fail.at("id")}>
          <Input mono value={id} onChange={(e) => setId(e.target.value)} readOnly={Boolean(subject)} name="id" />
        </Field>
        <Field label={t("inventory.col.displayName")} error={fail.at("display_name")}>
          <Input value={name} onChange={(e) => setName(e.target.value)} name="display_name" translate="no" />
        </Field>
      </div>
      <div className={styles.row}>
        <Field label={t("inventory.col.source")} hint={t("inventory.subjects.field.source.hint")} error={fail.at("source")}>
          <Input mono value={source} onChange={(e) => setSource(e.target.value)} name="source" />
        </Field>
        <Field label={t("inventory.col.principals")} hint={t("inventory.field.list.hint")} error={fail.at("principals")}>
          <Textarea mono rows={3} value={principals} onChange={(e) => setPrincipals(e.target.value)} name="principals" />
        </Field>
      </div>
      <div className={styles.row}>
        <Field label={t("inventory.col.groups")} hint={t("inventory.field.list.hint")} error={fail.at("groups")}>
          <Textarea mono rows={3} value={groups} onChange={(e) => setGroups(e.target.value)} name="groups" />
        </Field>
        <Field label={t("inventory.col.claims")} hint={t("inventory.subjects.field.claims.hint")} error={local.claims ?? fail.at("claims")}>
          <Textarea mono rows={3} value={claims} onChange={(e) => setClaims(e.target.value)} name="claims" />
        </Field>
      </div>
      <Checkbox label={t("inventory.subjects.field.breakGlass")} checked={breakGlass} onChange={(e) => setBreakGlass(e.target.checked)} name="break_glass" />
      {fail.form}
      <div className={styles.actions}>
        <Button type="submit" variant="primary" loading={busy}>
          {subject ? t("inventory.save") : t("inventory.subjects.add")}
        </Button>
        <Button type="button" variant="ghost" onClick={onCancel}>
          {t("inventory.cancel")}
        </Button>
      </div>
    </form>
  );
}

// ---------------------------------------------------------------------------
// Role bindings, wherever they are listed: on a subject, a group or a role.
// ---------------------------------------------------------------------------

type BindingList = ReturnType<typeof usePagedList<RoleBindingPage>>;

/**
 * One page of role bindings under a filter, with the writes identity:write
 * offers: bind a role (to the fixed holder, or to one the operator names on a
 * role's page) and withdraw one, which confirms by typing the holder's id —
 * withdrawing a role can take away the operator's own access.
 */
function BindingsPanel({
  title,
  lede,
  list,
  fixed,
}: {
  title: string;
  lede: string;
  list: BindingList;
  /** The holder or the role every binding here shares. */
  fixed: { subject_id: string } | { group_id: string } | { role: string };
}) {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const toast = useToast();
  const permissions = usePermissions();
  const canBind = offered("roleBind", permissions);
  const canUnbind = offered("roleUnbind", permissions);
  const [withdrawing, setWithdrawing] = useState<RoleBinding | null>(null);
  const byRole = "role" in fixed;

  const unbind = async (b: RoleBinding): Promise<ConfirmFailure | null> => {
    const h = holderOf(b);
    const body = h.kind === "group" ? { group_id: h.id, role: b.role } : { subject_id: h.id, role: b.role };
    const a = await api.call("roleUnbind", { params: { tenant: tenant as string }, body });
    if (a.kind !== "ok") return { error: a.error, kind: a.kind };
    setWithdrawing(null);
    toast.success(t("inventory.roles.withdrawn", { role: b.role, holder: h.id }));
    list.refresh();
    return null;
  };

  const columns: Column<RoleBinding>[] = [
    byRole
      ? {
          id: "holder",
          header: t("inventory.col.holder"),
          cell: (b) => (
            <span className={styles.chips}>
              <IdentLink to={holderPath(b)} value={holderOf(b).id} />
              <Badge>{holderOf(b).kind === "group" ? t("inventory.holder.group") : t("inventory.holder.subject")}</Badge>
            </span>
          ),
        }
      : { id: "role", header: t("inventory.col.role"), cell: (b) => <IdentLink to={rolePath(b.role)} value={b.role} /> },
    ...(byRole ? [] : [{ id: "what", header: t("inventory.col.roleMeans"), cell: (b: RoleBinding) => t(roleKey(b.role)) }]),
    {
      id: "by",
      header: t("inventory.col.grantedBy"),
      // The principal that bound it: a sign-in's session id, minted per sign-in.
      cell: (b) =>
        b.granted_by ? (
          <span data-volatile="">
            <Ident value={b.granted_by} />
          </span>
        ) : null,
    },
    { id: "since", header: t("inventory.col.since"), cell: (b) => <Instant at={b.created_at} /> },
  ];

  const load = list.load;
  return (
    <section className={styles.panel} aria-label={title} data-bindings={load.state}>
      <div className={styles.panelHead}>
        <h2 className={styles.panelTitle}>{title}</h2>
      </div>
      <p className={styles.quiet}>{lede}</p>
      {load.state === "loading" ? (
        <TableSkeleton headers={columns.map((c) => c.header)} />
      ) : load.state !== "ok" ? (
        <ErrorMessage error={load.error} kind={load.state} />
      ) : (
        <>
          {load.data.bindings.length === 0 && list.page.trail.length === 0 ? (
            <p className={styles.quiet} data-no-bindings="">
              {byRole ? t("inventory.roles.holders.none") : t("inventory.bindings.none")}
            </p>
          ) : (
            <>
              <DataTable
                caption={title}
                columns={columns}
                rows={load.data.bindings}
                rowKey={bindingKey}
                rowLabel={(b) => `${b.role} ${holderOf(b).id}`}
                partialNote={load.data.truncated ? t("inventory.onePage") : undefined}
                rowActions={(b) => [
                  byRole
                    ? { id: "open", label: t("inventory.roles.action.holder"), onSelect: () => navigate(holderPath(b)) }
                    : { id: "open", label: t("inventory.roles.action.holders"), onSelect: () => navigate(rolePath(b.role)) },
                  ...(canUnbind ? [{ id: "withdraw", label: t("inventory.roles.withdraw"), danger: true, onSelect: () => setWithdrawing(b) }] : []),
                ]}
              />
              <Pagination page={list.page} shown={load.data.bindings.length} truncated={load.data.truncated} next={load.data.next} onPage={list.setPage} />
            </>
          )}
          {canBind ? <BindForm fixed={fixed} roles={load.data.roles} onBound={list.refresh} /> : null}
        </>
      )}
      <ConfirmDialog
        open={withdrawing !== null}
        title={t("inventory.roles.withdraw.title")}
        body={
          <>
            <p className={styles.para}>
              {t("inventory.roles.withdraw.body", { role: withdrawing?.role ?? "", holder: withdrawing ? holderOf(withdrawing).id : "" })}
            </p>
            <p className={styles.para}>{t("inventory.roles.withdraw.consequence")}</p>
          </>
        }
        name={withdrawing ? holderOf(withdrawing).id : ""}
        action={t("inventory.roles.withdraw")}
        onConfirm={() => (withdrawing ? unbind(withdrawing) : Promise.resolve(null))}
        onClose={() => setWithdrawing(null)}
      />
    </section>
  );
}

function BindForm({ fixed, roles, onBound }: { fixed: { subject_id: string } | { group_id: string } | { role: string }; roles: readonly string[]; onBound(): void }) {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const toast = useToast();
  const byRole = "role" in fixed;
  const [role, setRole] = useState(byRole ? fixed.role : "");
  const [kind, setKind] = useState<"subject" | "group">("subject");
  const [holder, setHolder] = useState("");
  const [busy, setBusy] = useState(false);
  const fail = useFailure(["role", "subject_id", "group_id"]);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (busy) return;
    fail.setFailure(null);
    const body = byRole
      ? kind === "group"
        ? { group_id: holder.trim(), role }
        : { subject_id: holder.trim(), role }
      : { ...fixed, role };
    setBusy(true);
    const a = await api.call("roleBind", { params: { tenant: tenant as string }, body });
    setBusy(false);
    if (a.kind !== "ok") {
      fail.setFailure({ error: a.error, kind: a.kind });
      return;
    }
    toast.success(t("inventory.roles.bound", { role, holder: byRole ? holder.trim() : "subject_id" in fixed ? fixed.subject_id : (fixed as { group_id: string }).group_id }));
    setHolder("");
    if (!byRole) setRole("");
    onBound();
  };

  return (
    <form className={styles.stack} onSubmit={submit} noValidate data-bind-form="">
      <div className={styles.bind}>
        {byRole ? (
          <>
            <Choices legend={t("inventory.col.holder")}>
              <Radio label={t("inventory.holder.subject")} name="holder-kind" checked={kind === "subject"} onChange={() => setKind("subject")} />
              <Radio label={t("inventory.holder.group")} name="holder-kind" checked={kind === "group"} onChange={() => setKind("group")} />
            </Choices>
            <Field label={kind === "group" ? t("inventory.roles.field.group") : t("inventory.roles.field.subject")} error={fail.at(kind === "group" ? "group_id" : "subject_id")}>
              <Input mono value={holder} onChange={(e) => setHolder(e.target.value)} name="holder" />
            </Field>
          </>
        ) : (
          <Field label={t("inventory.col.role")} error={fail.at("role")}>
            <Select value={role} onChange={(e) => setRole(e.target.value)} name="role">
              <option value="">{t("inventory.roles.field.choose")}</option>
              {roles.map((r) => (
                <option key={r} value={r}>
                  {r}
                </option>
              ))}
            </Select>
          </Field>
        )}
        <Button type="submit" loading={busy} disabled={!role || (byRole && !holder.trim())} data-bind="">
          {t("inventory.roles.bind")}
        </Button>
      </div>
      {fail.form}
    </form>
  );
}

// ---------------------------------------------------------------------------
// /console/inventory/groups and /console/inventory/groups/<name>
// ---------------------------------------------------------------------------

export function GroupsScreen() {
  const { t, f } = useI18n();
  const location = useLocation();
  const limit = readLimit(location.search);
  const { load, refresh, page, setPage } = usePagedList<GroupPage>("groups", [], limit);
  const [sort, setSort] = useState<TableSort | null>(null);
  const columns: Column<Group>[] = [
    {
      id: "id",
      header: t("inventory.col.group"),
      sortValue: (g) => g.id,
      cell: (g) => <IdentLink to={groupPath(g.id)} value={g.id} label={t("inventory.groups.open", { id: g.id })} />,
    },
    { id: "members", header: t("inventory.col.members"), numeric: true, sortValue: (g) => g.member_count, cell: (g) => f.number(g.member_count) },
    { id: "roles", header: t("inventory.col.roles"), cell: (g) => <IdentChips items={g.roles} to={rolePath} none={t("inventory.none")} /> },
    {
      id: "record",
      header: t("inventory.col.record"),
      cell: (g) => (g.record ? <Ident value={g.record.source} /> : <span className={styles.quiet}>{t("inventory.groups.noRecord")}</span>),
    },
  ];
  return (
    <div className={styles.screen}>
      <PageHeader title={t("inventory.title")} lede={t("inventory.groups.lede")} actions={<Button onClick={refresh}>{t("inventory.refresh")}</Button>} />
      <InventoryViews current="groups" />
      <ScreenFrame
        load={load}
        onRetry={refresh}
        isEmpty={(d) => d.groups.length === 0 && page.trail.length === 0}
        loading={<TableSkeleton headers={columns.map((c) => c.header)} />}
        empty={
          <EmptyState
            title={t("inventory.groups.empty.title")}
            body={t("inventory.groups.empty.body")}
            action={<Link to="inventory/subjects">{t("inventory.groups.empty.action")}</Link>}
          />
        }
      >
        {(data) => (
          <>
            <DataTable
              caption={t("inventory.groups.caption")}
              columns={columns}
              rows={data.groups}
              rowKey={(g) => g.id}
              rowLabel={(g) => g.id}
              sort={sort}
              onSort={setSort}
              partialNote={data.truncated ? t("inventory.onePage") : undefined}
              rowActions={(g) => [
                { id: "open", label: t("inventory.action.open"), onSelect: () => navigate(groupPath(g.id)) },
                { id: "members", label: t("inventory.groups.action.members"), onSelect: () => navigate("inventory/subjects", { query: { group: g.id } }) },
              ]}
            />
            <Pagination page={page} shown={data.groups.length} truncated={data.truncated} next={data.next} onPage={setPage} />
          </>
        )}
      </ScreenFrame>
    </div>
  );
}

export function GroupScreen({ id }: { id: string }) {
  const { t, f } = useI18n();
  const { tenant, api } = useDeployment();
  const [load, refresh] = useLoad<Group | null>(tenant ? `group|${tenant}|${id}` : null, async (signal) =>
    orNone(await api.call<Group>("group", { params: { tenant: tenant as string, group: id }, signal })),
  );
  const found = load.state === "ok" ? load.data : null;
  const members = usePagedList<SubjectPage>("subjects", [["group", id]], 100, Boolean(found));
  const bindings = usePagedList<RoleBindingPage>("roleBindings", [["group", id]], 100, Boolean(found));
  const gaps = [
    ...(members.load.state === "outage" ? [t("inventory.groups.partial.members")] : []),
    ...(bindings.load.state === "outage" ? [t("inventory.groups.partial.roles")] : []),
  ];
  const refreshAll = () => {
    refresh();
    members.refresh();
    bindings.refresh();
  };
  return (
    <div className={styles.screen}>
      <PageHeader
        breadcrumb={<Crumbs view={t("inventory.view.groups")} to="inventory/groups" here={<Ident value={id} />} />}
        title={<Ident value={id} title />}
        lede={found?.record?.display_name ? <span translate="no">{found.record.display_name}</span> : t("inventory.groups.detail.lede")}
        actions={<Button onClick={refreshAll}>{t("inventory.refresh")}</Button>}
      />
      <ScreenFrame
        load={load}
        onRetry={refresh}
        isEmpty={(x) => x === null}
        gaps={gaps}
        empty={
          <EmptyState
            title={t("inventory.groups.missing.title")}
            body={t("inventory.groups.missing.body", { id: `⁨${id}⁩` })}
            action={<Link to="inventory/groups">{t("inventory.groups.back")}</Link>}
          />
        }
        loading={
          <div className={styles.stack}>
            <Skeleton size="l" />
            <Skeleton shape="block" size="full" />
          </div>
        }
      >
        {(group) =>
          group === null ? null : (
            <>
              <section className={styles.panel} aria-labelledby="group-facts">
                <h2 id="group-facts" className={styles.panelTitle}>
                  {t("inventory.groups.facts")}
                </h2>
                <dl className={styles.facts}>
                  <dt>{t("inventory.col.members")}</dt>
                  <dd>{f.number(group.member_count)}</dd>
                  <dt>{t("inventory.col.record")}</dt>
                  <dd>{group.record ? <Ident value={group.record.source} /> : <span className={styles.quiet}>{t("inventory.groups.noRecord")}</span>}</dd>
                  {group.record?.description ? (
                    <>
                      <dt>{t("inventory.col.description")}</dt>
                      <dd translate="no">{group.record.description}</dd>
                    </>
                  ) : null}
                </dl>
                <p className={styles.quiet}>{t("inventory.groups.policy")}</p>
              </section>
              <BindingsPanel title={t("inventory.groups.roles")} lede={t("inventory.groups.roles.lede")} list={bindings} fixed={{ group_id: group.id }} />
              <MembersPanel list={members} group={group.id} />
            </>
          )
        }
      </ScreenFrame>
    </div>
  );
}

function MembersPanel({ list, group }: { list: ReturnType<typeof usePagedList<SubjectPage>>; group: string }) {
  const { t } = useI18n();
  const load: Load<SubjectPage> = list.load;
  return (
    <section className={styles.panel} aria-labelledby="group-members" data-members={load.state}>
      <div className={styles.panelHead}>
        <h2 id="group-members" className={styles.panelTitle}>
          {t("inventory.groups.members")}
        </h2>
        <Link to="inventory/subjects" query={{ group }} className={styles.link}>
          {t("inventory.groups.members.all")}
        </Link>
      </div>
      {load.state === "loading" ? (
        <TableSkeleton headers={[t("inventory.col.id"), t("inventory.col.displayName"), t("inventory.col.source")]} />
      ) : load.state !== "ok" ? (
        <ErrorMessage error={load.error} kind={load.state} />
      ) : load.data.subjects.length === 0 && list.page.trail.length === 0 ? (
        <p className={styles.quiet}>{t("inventory.groups.members.none")}</p>
      ) : (
        <>
          <DataTable
            caption={t("inventory.groups.members")}
            columns={[
              { id: "id", header: t("inventory.col.id"), cell: (s) => <IdentLink to={subjectPath(s.id)} value={s.id} /> },
              {
                id: "name",
                header: t("inventory.col.displayName"),
                cell: (s) => (
                  <span className={styles.operator} translate="no">
                    {s.display_name}
                  </span>
                ),
              },
              { id: "source", header: t("inventory.col.source"), cell: (s) => <Ident value={s.source} /> },
            ]}
            rows={load.data.subjects}
            rowKey={(s) => s.id}
            rowLabel={(s) => s.id}
            partialNote={load.data.truncated ? t("inventory.onePage") : undefined}
          />
          <Pagination page={list.page} shown={load.data.subjects.length} truncated={load.data.truncated} next={load.data.next} onPage={list.setPage} />
        </>
      )}
    </section>
  );
}

// ---------------------------------------------------------------------------
// /console/inventory/roles and /console/inventory/roles/<role>
// ---------------------------------------------------------------------------

/**
 * The fixed role set (PLAN §6), as the server names it: the `roles` member of
 * the bindings list, the same on every page. A role this console has no
 * description for is still listed, by its code, and named as a gap — a newer
 * server beside an older console says something true (M21).
 */
export function RolesScreen() {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const [load, refresh] = useLoad<RoleBindingPage>(tenant ? `roles|${tenant}` : null, (signal) =>
    api.call("roleBindings", { params: { tenant: tenant as string }, query: { limit: "1" }, signal }),
  );
  const unknown = load.state === "ok" ? load.data.roles.filter((r) => roleKey(r) === "inventory.role.unknown") : [];
  const columns: Column<string>[] = [
    { id: "role", header: t("inventory.col.role"), cell: (r) => <IdentLink to={rolePath(r)} value={r} label={t("inventory.roles.open", { role: r })} /> },
    { id: "what", header: t("inventory.col.roleMeans"), cell: (r) => t(roleKey(r)) },
  ];
  return (
    <div className={styles.screen}>
      <PageHeader title={t("inventory.title")} lede={t("inventory.roles.lede")} actions={<Button onClick={refresh}>{t("inventory.refresh")}</Button>} />
      <InventoryViews current="roles" />
      <ScreenFrame
        load={load}
        onRetry={refresh}
        isEmpty={(d) => d.roles.length === 0}
        gaps={unknown.length ? [t("inventory.roles.partial.unknown", { count: unknown.length })] : []}
        loading={<TableSkeleton headers={columns.map((c) => c.header)} />}
        empty={<EmptyState title={t("inventory.roles.empty.title")} body={t("inventory.roles.empty.body")} />}
      >
        {(data) => (
          <DataTable
            caption={t("inventory.roles.caption")}
            columns={columns}
            rows={data.roles}
            rowKey={(r) => r}
            rowLabel={(r) => r}
            rowActions={(r) => [{ id: "open", label: t("inventory.roles.action.holders"), onSelect: () => navigate(rolePath(r)) }]}
          />
        )}
      </ScreenFrame>
    </div>
  );
}

export function RoleScreen({ role }: { role: string }) {
  const { t } = useI18n();
  const location = useLocation();
  const limit = readLimit(location.search);
  const list = usePagedList<RoleBindingPage>("roleBindings", [["role", role]], limit);
  // A role this build does not define is refused (400 invalid_request on
  // `role`): there is no such role, which is the empty state, not an error.
  const missing = list.load.state === "refused" && list.load.status === 400;
  const shown: Load<RoleBindingPage | null> = missing ? { state: "ok", data: null } : list.load;
  return (
    <div className={styles.screen}>
      <PageHeader
        breadcrumb={<Crumbs view={t("inventory.view.roles")} to="inventory/roles" here={<Ident value={role} />} />}
        title={<Ident value={role} title />}
        lede={t(roleKey(role))}
        actions={<Button onClick={list.refresh}>{t("inventory.refresh")}</Button>}
      />
      <ScreenFrame
        load={shown}
        onRetry={list.refresh}
        isEmpty={(d) => d === null}
        empty={
          <EmptyState
            title={t("inventory.roles.missing.title")}
            body={t("inventory.roles.missing.body", { role: `⁨${role}⁩` })}
            action={<Link to="inventory/roles">{t("inventory.roles.back")}</Link>}
          />
        }
        loading={<TableSkeleton headers={[t("inventory.col.holder"), t("inventory.col.grantedBy"), t("inventory.col.since")]} />}
      >
        {() => <BindingsPanel title={t("inventory.roles.holders")} lede={t("inventory.roles.holders.lede")} list={list} fixed={{ role }} />}
      </ScreenFrame>
    </div>
  );
}
