// GRANTS (0054; PLAN M10, M2, M4, M26): time-boxed access, as a policy input.
// Three screens, each a deep link:
//   /console/inventory/grants[?subject=][&state=]  the grants, newest first
//   /console/inventory/grants/<id>                 one grant, and the decisions
//                                                  it supplied (a bounded sample)
//   /console/inventory/new/grants                  grant access for a while
// Reading is grant:read, every reading role's. Creating and revoking are
// grant:write, offered only to a role holding it and refused by the API to
// anyone else (M2). With an approval workflow registered, a create is a
// request the workflow decides (202), and the console says so rather than
// showing a grant that does not exist. Revoking ends the sessions the grant
// backed, shows its holder the reason given, and confirms by typing the
// grant's id (DESIGN.md §2). A grant's reason, scope and selector are
// operator data, never translated (§9).

import { useState, type FormEvent } from "react";
import { offered } from "../api/permit";
import type { Grant, GrantBody, GrantDetail, GrantOutcome, GrantPage, GrantRevocation } from "../api/types";
import { Badge, Breadcrumb } from "../components/Badge";
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
import { STATUS, StatusOf } from "../components/Status";
import { useToast } from "../components/Toast";
import { useI18n } from "../i18n/I18n";
import { Link, navigate, useLocation } from "../router/router";
import { useLoad } from "../shell/load";
import { useDeployment } from "../shell/Shell";
import { subjectPath } from "./Identities";
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
import { GRANT_STATES, grantStatus, parseLabels, parseList } from "./inventoryModel";
import styles from "./Inventory.module.css";

const grantPath = (id: string) => `inventory/grants/${encodeURIComponent(id)}`;

function Selector({ grant }: { grant: Grant }) {
  const { t } = useI18n();
  const s = grant.scope;
  const empty = !s.targets?.length && !Object.keys(s.labels ?? {}).length && !s.zones?.length;
  if (empty) return <span className={styles.quiet}>{t("inventory.grants.selector.any")}</span>;
  return (
    <span className={styles.chips}>
      <IdentChips items={s.targets ?? []} />
      <LabelChips labels={s.labels ?? {}} />
      <IdentChips items={(s.zones ?? []).map((z) => `zone:${z}`)} />
    </span>
  );
}

// ---------------------------------------------------------------------------
// /console/inventory/grants
// ---------------------------------------------------------------------------

export function GrantsScreen() {
  const { t } = useI18n();
  const { tenant } = useDeployment();
  const location = useLocation();
  const setQuery = useSetQuery();
  const permissions = usePermissions();
  const canCreate = offered("grantCreate", permissions);
  const subject = location.search.get("subject") ?? "";
  const rawState = location.search.get("state") ?? "";
  const state = (GRANT_STATES as readonly string[]).includes(rawState) ? rawState : "";
  const limit = readLimit(location.search);
  const filters: [string, string][] = [];
  if (subject) filters.push(["subject", subject]);
  if (state) filters.push(["state", state]);
  const { load, refresh, page, setPage } = usePagedList<GrantPage>("grants", filters, limit);
  const [sort, setSort] = useState<TableSort | null>(null);
  const [hidden, setHidden] = useState<string[]>([]);

  const columns: Column<Grant>[] = [
    {
      id: "id",
      header: t("inventory.col.grant"),
      sortValue: (g) => g.id,
      cell: (g) => (
        <span data-volatile="">
          <IdentLink to={grantPath(g.id)} value={g.id} label={t("inventory.grants.open", { id: g.id })} />
        </span>
      ),
    },
    {
      id: "subject",
      header: t("inventory.col.holderSubject"),
      sortValue: (g) => g.subject,
      cell: (g) => <IdentLink to={subjectPath(g.subject)} value={g.subject} />,
    },
    { id: "scope", header: t("inventory.col.scope"), sortValue: (g) => g.scope.name, cell: (g) => <Ident value={g.scope.name} /> },
    { id: "selector", header: t("inventory.col.selector"), cell: (g) => <Selector grant={g} /> },
    { id: "state", header: t("inventory.col.state"), sortValue: (g) => g.state, cell: (g) => <StatusOf name={grantStatus(g.state)} /> },
    { id: "from", header: t("inventory.col.from"), sortValue: (g) => g.not_before, cell: (g) => <Instant at={g.not_before} /> },
    { id: "until", header: t("inventory.col.expires"), sortValue: (g) => g.expires_at, cell: (g) => <Instant at={g.expires_at} /> },
    { id: "origin", header: t("inventory.col.origin"), sortValue: (g) => g.origin, cell: (g) => <Ident value={g.origin} /> },
  ];

  const filtered = Boolean(subject || state);
  return (
    <div className={styles.screen}>
      <PageHeader
        title={t("inventory.title")}
        lede={t("inventory.grants.lede")}
        actions={
          <>
            {canCreate ? (
              <ButtonLink to="inventory/new/grants" query={subject ? { subject } : undefined} variant="primary" data-create="">
                {t("inventory.grants.add")}
              </ButtonLink>
            ) : null}
            <Button onClick={refresh}>{t("inventory.refresh")}</Button>
          </>
        }
      />
      <InventoryViews current="grants" />
      <FilterBar
        label={t("inventory.filters")}
        fields={[
          { id: "subject", label: t("inventory.filter.subject"), kind: "text", mono: true },
          { id: "state", label: t("inventory.col.state"), options: GRANT_STATES.map((s) => ({ value: s, label: t(STATUS[grantStatus(s)].label) })) },
        ]}
        values={{ subject, state }}
        onChange={(v) => setQuery({ subject: v.subject, state: v.state })}
      />
      <ScreenFrame
        load={load}
        onRetry={refresh}
        isEmpty={(d) => d.grants.length === 0 && page.trail.length === 0}
        loading={<TableSkeleton headers={columns.map((c) => c.header)} />}
        empty={
          filtered ? (
            <EmptyState
              title={t("inventory.grants.filtered.title")}
              body={t("inventory.grants.filtered.body")}
              action={<Button onClick={() => setQuery({ subject: undefined, state: undefined })}>{t("inventory.filter.clear")}</Button>}
            />
          ) : (
            <EmptyState
              title={t("inventory.grants.empty.title")}
              body={t("inventory.grants.empty.body")}
              action={
                canCreate ? (
                  <ButtonLink to="inventory/new/grants" variant="primary">
                    {t("inventory.grants.add")}
                  </ButtonLink>
                ) : (
                  <CopyableId value={`POST /api/v1/tenants/${tenant ?? ""}/grants`} />
                )
              }
            />
          )
        }
      >
        {(data) => (
          <>
            <DataTable
              caption={t("inventory.grants.caption")}
              columns={columns}
              rows={data.grants}
              rowKey={(g) => g.id}
              rowLabel={(g) => g.id}
              sort={sort}
              onSort={setSort}
              hidden={hidden}
              onHidden={setHidden}
              partialNote={data.truncated ? t("inventory.onePage") : undefined}
              rowActions={(g) => [
                { id: "open", label: t("inventory.action.open"), onSelect: () => navigate(grantPath(g.id)) },
                { id: "holder", label: t("inventory.grants.action.holder"), onSelect: () => navigate(subjectPath(g.subject)) },
              ]}
            />
            <Pagination page={page} shown={data.grants.length} truncated={data.truncated} next={data.next} onPage={setPage} />
          </>
        )}
      </ScreenFrame>
    </div>
  );
}

// ---------------------------------------------------------------------------
// /console/inventory/grants/<id>
// ---------------------------------------------------------------------------

export function GrantScreen({ id }: { id: string }) {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const toast = useToast();
  const permissions = usePermissions();
  const canRevoke = offered("grantRevoke", permissions);
  const [revoking, setRevoking] = useState(false);
  const [reason, setReason] = useState("");
  const [load, refresh] = useLoad<GrantDetail | null>(tenant ? `grant|${tenant}|${id}` : null, async (signal) =>
    orNone(await api.call<GrantDetail>("grant", { params: { tenant: tenant as string, grant: id }, signal })),
  );
  const detail = load.state === "ok" ? load.data : null;
  const live = detail && (detail.grant.state === "active" || detail.grant.state === "scheduled");

  const revoke = async (): Promise<ConfirmFailure | null> => {
    const a = await api.call<GrantRevocation>("grantRevoke", { params: { tenant: tenant as string, grant: id }, body: { reason } });
    if (a.kind !== "ok") return { error: a.error, kind: a.kind };
    setRevoking(false);
    setReason("");
    toast.success(t("inventory.grants.revoked", { id, sessions: a.data.sessions.length }));
    refresh();
    return null;
  };

  return (
    <div className={styles.screen}>
      <PageHeader
        breadcrumb={
          <Breadcrumb
            items={[
              { label: t("inventory.title"), to: "inventory/targets" },
              { label: t("inventory.view.grants"), to: "inventory/grants" },
              {
                label: (
                  <span data-volatile="">
                    <Ident value={id} />
                  </span>
                ),
              },
            ]}
          />
        }
        title={<span data-volatile=""><Ident value={id} title /></span>}
        status={detail ? <StatusOf name={grantStatus(detail.grant.state)} pill /> : null}
        actions={
          detail ? (
            <>
              <ButtonLink to="audit/records" query={{ subject: detail.grant.subject }}>
                {t("inventory.grants.action.audit")}
              </ButtonLink>
              {canRevoke && live ? (
                <Button variant="danger" onClick={() => setRevoking(true)} data-revoke="">
                  {t("inventory.grants.revoke")}
                </Button>
              ) : null}
            </>
          ) : null
        }
      />
      <ScreenFrame
        load={load}
        onRetry={refresh}
        isEmpty={(d) => d === null}
        gaps={detail?.decisions_truncated ? [t("inventory.grants.partial.decisions", { count: detail.decisions.length })] : []}
        empty={
          <EmptyState
            title={t("inventory.grants.missing.title")}
            body={t("inventory.grants.missing.body", { id: `⁨${id}⁩` })}
            action={<Link to="inventory/grants">{t("inventory.grants.back")}</Link>}
          />
        }
        loading={
          <div className={styles.stack}>
            <Skeleton size="l" />
            <Skeleton shape="block" size="full" />
          </div>
        }
      >
        {(d) =>
          d === null ? null : (
            <>
              <section className={styles.panel} aria-labelledby="grant-facts" data-grant-state={d.grant.state}>
                <h2 id="grant-facts" className={styles.panelTitle}>
                  {t("inventory.grants.facts")}
                </h2>
                <dl className={styles.facts}>
                  <dt>{t("inventory.col.holderSubject")}</dt>
                  <dd>
                    <IdentLink to={subjectPath(d.grant.subject)} value={d.grant.subject} />
                  </dd>
                  <dt>{t("inventory.col.scope")}</dt>
                  <dd>
                    <Ident value={d.grant.scope.name} />
                  </dd>
                  <dt>{t("inventory.col.selector")}</dt>
                  <dd>
                    <Selector grant={d.grant} />
                  </dd>
                  <dt>{t("inventory.col.from")}</dt>
                  <dd>
                    <Instant at={d.grant.not_before} />
                  </dd>
                  <dt>{t("inventory.col.expires")}</dt>
                  <dd>
                    <Instant at={d.grant.expires_at} />
                  </dd>
                  <dt>{t("inventory.col.reason")}</dt>
                  <dd className={styles.operator} translate="no">
                    {d.grant.reason}
                  </dd>
                  {d.grant.external_ref ? (
                    <>
                      <dt>{t("inventory.col.externalRef")}</dt>
                      <dd>
                        <Ident value={d.grant.external_ref} />
                      </dd>
                    </>
                  ) : null}
                  <dt>{t("inventory.col.origin")}</dt>
                  <dd>
                    <Ident value={d.grant.origin} />
                  </dd>
                  <dt>{t("inventory.col.createdBy")}</dt>
                  <dd className={styles.chips}>
                    <Ident value={d.grant.created_by.subject || d.grant.created_by.principal_id} />
                    {d.grant.created_by.break_glass ? <Badge tone="info">{t("inventory.subjects.breakGlass")}</Badge> : null}
                  </dd>
                  <dt>{t("inventory.col.created")}</dt>
                  <dd>
                    <Instant at={d.grant.created_at} />
                  </dd>
                  {d.grant.revoked_at ? (
                    <>
                      <dt>{t("inventory.col.revoked")}</dt>
                      <dd className={styles.chips}>
                        <Instant at={d.grant.revoked_at} />
                        {d.grant.revoked_by ? <Ident value={d.grant.revoked_by.subject || d.grant.revoked_by.principal_id} /> : null}
                      </dd>
                      <dt>{t("inventory.col.revokeReason")}</dt>
                      <dd className={styles.operator} translate="no">
                        {d.grant.revoke_reason}
                      </dd>
                    </>
                  ) : null}
                </dl>
              </section>
              <section className={styles.panel} aria-labelledby="grant-decisions" data-decisions="">
                <div className={styles.panelHead}>
                  <h2 id="grant-decisions" className={styles.panelTitle}>
                    {t("inventory.grants.decisions")}
                  </h2>
                  <Link to="audit/records" query={{ subject: d.grant.subject }} className={styles.link}>
                    {t("inventory.grants.decisions.all")}
                  </Link>
                </div>
                {d.decisions.length === 0 ? (
                  <p className={styles.quiet}>{t("inventory.grants.decisions.none")}</p>
                ) : (
                  <DataTable
                    caption={t("inventory.grants.decisions")}
                    columns={[
                      {
                        id: "decision",
                        header: t("inventory.col.decision"),
                        cell: (x) => <IdentLink to={`explain/decisions/${encodeURIComponent(x.decision_id)}`} value={x.decision_id} />,
                      },
                      { id: "effect", header: t("inventory.col.effect"), cell: (x) => <StatusOf name={x.effect === "allow" ? "allowed" : "denied"} /> },
                      { id: "rule", header: t("inventory.col.rule"), cell: (x) => (x.rule ? <Ident value={x.rule} /> : null) },
                      {
                        id: "session",
                        header: t("inventory.col.session"),
                        cell: (x) => (x.session_id ? <IdentLink to={`explain/sessions/${encodeURIComponent(x.session_id)}`} value={x.session_id} /> : null),
                      },
                      { id: "at", header: t("inventory.col.when"), cell: (x) => <Instant at={x.decided_at} /> },
                    ]}
                    rows={d.decisions}
                    rowKey={(x) => x.decision_id}
                    rowLabel={(x) => x.decision_id}
                  />
                )}
              </section>
            </>
          )
        }
      </ScreenFrame>
      <ConfirmDialog
        open={revoking}
        title={t("inventory.grants.revoke.title")}
        body={
          <>
            <p className={styles.para}>{t("inventory.grants.revoke.body", { id, subject: detail?.grant.subject ?? "" })}</p>
            <Field label={t("inventory.grants.field.revokeReason")} hint={t("inventory.grants.field.revokeReason.hint")}>
              <Textarea rows={2} value={reason} onChange={(e) => setReason(e.target.value)} name="revoke_reason" translate="no" />
            </Field>
          </>
        }
        name={id}
        action={t("inventory.grants.revoke")}
        onConfirm={revoke}
        onClose={() => setRevoking(false)}
      />
    </div>
  );
}

// ---------------------------------------------------------------------------
// /console/inventory/new/grants
// ---------------------------------------------------------------------------

const GRANT_FIELDS = ["subject", "scope", "duration", "expires_at", "not_before", "reason", "reason_code", "external_ref"] as const;

export function GrantNewScreen() {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const toast = useToast();
  const location = useLocation();
  const permissions = usePermissions();
  const [subject, setSubject] = useState(location.search.get("subject") ?? "");
  const [scope, setScope] = useState("");
  const [targets, setTargets] = useState("");
  const [labels, setLabels] = useState("");
  const [zones, setZones] = useState("");
  const [duration, setDuration] = useState("30m");
  const [reason, setReason] = useState("");
  const [ref, setRef] = useState("");
  const [busy, setBusy] = useState(false);
  const [local, setLocal] = useState<Record<string, string>>({});
  const [pending, setPending] = useState<GrantOutcome["request"] | null>(null);
  const fail = useFailure(GRANT_FIELDS);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (busy) return;
    const parsed = parseLabels(labels);
    const problems: Record<string, string> = {};
    if (parsed === null) problems.labels = t("inventory.problem.labels");
    setLocal(problems);
    fail.setFailure(null);
    setPending(null);
    if (Object.keys(problems).length) return;
    const body: GrantBody = {
      subject: subject.trim(),
      scope: { name: scope.trim(), targets: parseList(targets), labels: parsed ?? {}, zones: parseList(zones) },
      duration: duration.trim(),
      reason: reason.trim(),
      external_ref: ref.trim() || undefined,
    };
    setBusy(true);
    const a = await api.call<GrantOutcome>("grantCreate", { params: { tenant: tenant as string }, body });
    setBusy(false);
    if (a.kind !== "ok") {
      fail.setFailure({ error: a.error, kind: a.kind });
      return;
    }
    if (a.data.grant) {
      toast.success(t("inventory.grants.created", { id: a.data.grant.id }));
      navigate(grantPath(a.data.grant.id));
      return;
    }
    // A registered workflow decides it (M10): nothing is granted yet.
    setPending(a.data.request ?? null);
  };

  if (!offered("grantCreate", permissions)) {
    return (
      <div className={styles.screen}>
        <PageHeader title={t("inventory.grants.new.title")} />
        <ErrorMessage error={{ code: "forbidden" }} kind="refused" />
      </div>
    );
  }
  return (
    <div className={styles.screen}>
      <PageHeader
        breadcrumb={
          <Breadcrumb
            items={[
              { label: t("inventory.title"), to: "inventory/targets" },
              { label: t("inventory.view.grants"), to: "inventory/grants" },
              { label: t("inventory.grants.add") },
            ]}
          />
        }
        title={t("inventory.grants.new.title")}
        lede={t("inventory.grants.new.lede")}
      />
      <section className={styles.panel}>
        <form className={styles.form} onSubmit={submit} noValidate data-grant-form="">
          <div className={styles.row}>
            <Field label={t("inventory.col.holderSubject")} error={fail.at("subject")}>
              <Input mono value={subject} onChange={(e) => setSubject(e.target.value)} name="subject" />
            </Field>
            <Field label={t("inventory.col.scope")} hint={t("inventory.grants.field.scope.hint")} error={fail.at("scope.name")}>
              <Input mono value={scope} onChange={(e) => setScope(e.target.value)} name="scope" />
            </Field>
          </div>
          <div className={styles.row}>
            <Field label={t("inventory.grants.field.targets")} hint={t("inventory.field.list.hint")} error={fail.at("scope.targets")}>
              <Textarea mono rows={2} value={targets} onChange={(e) => setTargets(e.target.value)} name="targets" />
            </Field>
            <Field label={t("inventory.col.labels")} hint={t("inventory.targets.field.labels.hint")} error={local.labels ?? fail.at("scope.labels")}>
              <Textarea mono rows={2} value={labels} onChange={(e) => setLabels(e.target.value)} name="labels" />
            </Field>
            <Field label={t("inventory.grants.field.zones")} hint={t("inventory.field.list.hint")} error={fail.at("scope.zones")}>
              <Textarea mono rows={2} value={zones} onChange={(e) => setZones(e.target.value)} name="zones" />
            </Field>
          </div>
          <div className={styles.row}>
            <Field label={t("inventory.grants.field.duration")} hint={t("inventory.grants.field.duration.hint")} error={fail.at("duration") ?? fail.at("expires_at")}>
              <Input mono value={duration} onChange={(e) => setDuration(e.target.value)} name="duration" />
            </Field>
            <Field label={t("inventory.col.externalRef")} hint={t("inventory.grants.field.ref.hint")} error={fail.at("external_ref")}>
              <Input mono value={ref} onChange={(e) => setRef(e.target.value)} name="external_ref" />
            </Field>
          </div>
          <Field label={t("inventory.col.reason")} hint={t("inventory.grants.field.reason.hint")} error={fail.at("reason")}>
            <Textarea rows={2} value={reason} onChange={(e) => setReason(e.target.value)} name="reason" translate="no" />
          </Field>
          {fail.form}
          {pending ? (
            <p className={styles.notice} role="status" data-request-pending="">
              {t("inventory.grants.pending", { workflow: pending.workflow, id: pending.id })}
            </p>
          ) : null}
          <div className={styles.actions}>
            <Button type="submit" variant="primary" loading={busy}>
              {t("inventory.grants.add")}
            </Button>
            <Button type="button" variant="ghost" onClick={() => navigate("inventory/grants")}>
              {t("inventory.cancel")}
            </Button>
          </div>
        </form>
      </section>
    </div>
  );
}
