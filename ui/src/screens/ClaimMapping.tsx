// THE CLAIM MAPPING (0055, 0068; PLAN M7, §6 "Federation (M7)" and "The
// directory door"; 0011, 0043).
//
// /console/inventory/claim-mapping[?view=source]: the tenant's active mapping
// — what a sign-in's claims and a directory's fields become as attributes and
// groups — and a draft of the next version, edited as what it is (the visual
// view, the default: claims that become attributes, group names that become
// local groups, some scoped to directories) or as its document (`?view=source`).
// Both views edit one draft, and the draft is a document: what the visual view
// changes is the smallest change to its text (claimMappingDocument.ts), so the
// version written is the document the operator saw, byte for byte where they
// did not edit it.
//
// Every role that reads identities may edit a draft, have the server check it
// as they work (the dry run, `POST …/identity/claim-mapping/validate`, which
// stores and records nothing) and preview what it would produce. Writing is
// an activation — every sign-in from then on is mapped by it and every
// directory identity is brought onto it in the background (0043) — so it is
// offered to identity:write alone, shows what it changes, and confirms by
// typing the tenant's id (DESIGN.md §2, §7). The API refuses the write to
// anyone else (M2).

import { useEffect, useMemo, useState } from "react";
import { offered as permitted } from "../api/permit";
import type {
  ClaimMapping,
  ClaimMappingWritten,
  Connector,
  DirectorySourcePage,
  GroupPage,
  MappingDryRun,
  MappingRejection,
  SignInMethods,
  SubjectPage,
} from "../api/types";
import { Button } from "../components/Button";
import { CopyableId } from "../components/CopyableId";
import { DataTable } from "../components/DataTable";
import { ConfirmDialog, type ConfirmFailure } from "../components/Dialog";
import { Field, Textarea } from "../components/Field";
import { Ident } from "../components/Ident";
import { Notice } from "../components/Notice";
import { PageHeader } from "../components/PageHeader";
import { PartialNotice, ScreenFrame, Skeleton, type ErrorKind } from "../components/States";
import { StatusOf } from "../components/Status";
import { useToast } from "../components/Toast";
import { ViewLinks } from "../components/ViewLinks";
import type { ApiError } from "../i18n/errors";
import { useI18n } from "../i18n/I18n";
import { useLocation } from "../router/router";
import { useLoad } from "../shell/load";
import { useDeployment } from "../shell/Shell";
import { mappingChanges, mappingOf, parseMapping } from "./claimMappingDocument";
import { ChangeList, ChangesPanel, PreviewPanel } from "./ClaimMappingPreview";
import { MappingVisual, MappingVisualSkeleton, RejectionList, type Offered } from "./ClaimMappingVisual";
import { groupPath } from "./Identities";
import { IdentChips, InventoryViews, usePermissions, useSetQuery } from "./InventoryParts";
import styles from "./ClaimMapping.module.css";

/** The page of directory identities a writer's preview may start from, and of the tenant's directories and groups the editor offers. */
const OFFER_LIMIT = "100";

export function ClaimMappingScreen() {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const [load, refresh] = useLoad<ClaimMapping>(tenant ? `claim-mapping|${tenant}` : null, (signal) =>
    api.call("claimMapping", { params: { tenant: tenant as string }, signal }),
  );
  const [methods] = useLoad<SignInMethods>(tenant ? `sign-in-methods|${tenant}` : null, (signal) =>
    api.call("sessionMethods", { query: { tenant: tenant as string }, signal }),
  );
  const [sources] = useLoad<DirectorySourcePage>(tenant ? `directories|${tenant}` : null, (signal) =>
    api.call("directories", { params: { tenant: tenant as string }, query: { limit: OFFER_LIMIT }, signal }),
  );

  const gaps = [
    ...(methods.state === "outage" ? [t("claimMapping.partial.connectors")] : []),
    ...(sources.state === "outage" ? [t("claimMapping.partial.directories")] : []),
  ];

  return (
    <div className={styles.screen}>
      <PageHeader title={t("inventory.title")} lede={t("claimMapping.lede")} actions={<Button onClick={refresh}>{t("inventory.refresh")}</Button>} />
      <InventoryViews current="mapping" />
      <ScreenFrame
        load={load}
        onRetry={refresh}
        gaps={gaps}
        // A tenant always has a mapping: before one is written it is version
        // 0, which maps nothing, and the facts say so.
        isEmpty={() => false}
        empty={null}
        loading={
          <div className={styles.stack}>
            <Skeleton size="l" />
            <MappingVisualSkeleton />
          </div>
        }
      >
        {(m) => (
          <Mapping
            // A new active version starts a new draft from its document.
            key={m.digest}
            mapping={m}
            sources={sources.state === "ok" ? sources.data : null}
            methods={methods.state === "ok" ? methods.data.methods : null}
            onWritten={refresh}
          />
        )}
      </ScreenFrame>
    </div>
  );
}

type Check =
  | { state: "idle" }
  | { state: "busy" }
  | { state: "ok"; source: string; answer: MappingDryRun }
  | { state: "refused"; source: string; rejections: MappingRejection[] }
  | { state: "failed"; error: ApiError; kind: ErrorKind };

function rejectionsOf(parameters: Record<string, unknown> | undefined): MappingRejection[] | null {
  const r = parameters?.rejections;
  if (!Array.isArray(r)) return null;
  return r.filter((x): x is MappingRejection => typeof x === "object" && x !== null && typeof (x as MappingRejection).code === "string");
}

/** The draft, asked of the server's dry run a moment after each edit: its rejections, each at its field. */
function useCheck(source: string): Check {
  const { tenant, api } = useDeployment();
  const [check, setCheck] = useState<Check>({ state: "idle" });
  useEffect(() => {
    if (!tenant) return;
    if (source.trim() === "") {
      setCheck({ state: "refused", source, rejections: [] });
      return;
    }
    const ctl = new AbortController();
    setCheck((c) => (c.state === "ok" || c.state === "refused" ? c : { state: "busy" }));
    const timer = window.setTimeout(async () => {
      const a = await api.call<MappingDryRun>("claimMappingValidate", { params: { tenant }, body: { document: source }, signal: ctl.signal });
      if (ctl.signal.aborted) return;
      if (a.kind === "ok") setCheck({ state: "ok", source, answer: a.data });
      else if (a.error.code === "invalid_request" && a.error.parameters?.field === "document") {
        setCheck({ state: "refused", source, rejections: rejectionsOf(a.error.parameters) ?? [] });
      } else setCheck({ state: "failed", error: a.error, kind: a.kind });
    }, 400);
    return () => {
      ctl.abort();
      window.clearTimeout(timer);
    };
  }, [tenant, api, source]);
  return check;
}

function Mapping({
  mapping: m,
  sources,
  methods,
  onWritten,
}: {
  mapping: ClaimMapping;
  sources: DirectorySourcePage | null;
  methods: readonly Connector[] | null;
  onWritten(): void;
}) {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const location = useLocation();
  const setQuery = useSetQuery();
  const toast = useToast();
  const permissions = usePermissions();
  const canWrite = permitted("claimMappingPut", permissions);
  const view = location.search.get("view") === "source" ? "source" : "visual";
  const [draft, setDraft] = useState(m.document);
  const [writing, setWriting] = useState(false);
  const [writeRefused, setWriteRefused] = useState<{ source: string; rejections: MappingRejection[] } | null>(null);
  const check = useCheck(draft);
  const edited = draft !== m.document;

  const [groups] = useLoad<GroupPage>(tenant ? `groups|${tenant}|offer` : null, (signal) =>
    api.call("groups", { params: { tenant: tenant as string }, query: { limit: OFFER_LIMIT }, signal }),
  );
  // A writer's preview may start from an identity a directory provisioned: the first page's.
  const [subjects] = useLoad<SubjectPage>(tenant && canWrite ? `subjects|${tenant}|preview` : null, (signal) =>
    api.call("subjects", { params: { tenant: tenant as string }, query: { limit: OFFER_LIMIT }, signal }),
  );

  const offered: Offered = {
    sources: sources ? sources.directories.map((d) => d.source) : null,
    sourcesCut: sources?.truncated ?? false,
    groups: groups.state === "ok" ? groups.data.groups.map((g) => g.id) : [],
  };

  // The server's rejections of THIS draft: the last refused write's, or the dry run's once it has answered for it.
  const rejections: MappingRejection[] =
    writeRefused && writeRefused.source === draft
      ? writeRefused.rejections
      : check.state === "refused" && check.source === draft
        ? check.rejections
        : [];
  const checked = check.state === "ok" && check.source === draft ? "valid" : check.state === "refused" && check.source === draft ? "invalid" : null;

  const activeView = useMemo(() => mappingOf(parseMapping(m.document)), [m.document]);
  const draftView = useMemo(() => mappingOf(parseMapping(draft)), [draft]);
  const changes = typeof activeView !== "string" && typeof draftView !== "string" ? mappingChanges(activeView, draftView, edited) : [];

  const goToSource = () => setQuery({ view: "source" });

  const write = async (): Promise<ConfirmFailure | null> => {
    const a = await api.call<ClaimMappingWritten>("claimMappingPut", { params: { tenant: tenant as string }, body: { document: draft } });
    if (a.kind === "ok") {
      setWriting(false);
      toast.receipt(t("claimMapping.write.receipt", { version: a.data.version, previous: a.data.previous }));
      onWritten();
      return null;
    }
    const listed = a.error.code === "invalid_request" ? rejectionsOf(a.error.parameters) : null;
    if (listed) {
      // Refused: nothing was written, and the refusals go on the rows they name.
      setWriting(false);
      setWriteRefused({ source: draft, rejections: listed });
      return null;
    }
    return { error: a.error, kind: a.kind };
  };

  const identities = canWrite && subjects.state === "ok" ? subjects.data.subjects.filter((s) => s.directory_managed && !s.record_missing) : null;

  return (
    <>
      {m.stale_directory_subjects > 0 ? (
        <Notice tone="info" status={<StatusOf name="inProgress" />} role="status" data-stale={m.stale_directory_subjects}>
          <p>{t("claimMapping.stale", { count: m.stale_directory_subjects, version: m.version })}</p>
        </Notice>
      ) : null}
      <Facts mapping={m} />
      <section className={styles.panel} aria-labelledby="mapping-document" data-mapping-draft={edited ? "edited" : "unchanged"}>
        <div className={styles.head}>
          <h2 id="mapping-document" className={styles.panelTitle}>
            {t("claimMapping.document")}
          </h2>
          {checked ? <StatusOf name={checked} pill /> : null}
          <span className={styles.headActions}>
            {edited ? (
              <Button variant="ghost" onClick={() => setDraft(m.document)} data-mapping-discard="">
                {t("claimMapping.draft.discard")}
              </Button>
            ) : null}
            {canWrite ? (
              <Button variant="danger" onClick={() => setWriting(true)} disabled={!edited || checked !== "valid"} data-mapping-write="">
                {t("claimMapping.draft.write")}
              </Button>
            ) : null}
          </span>
        </div>
        <p className={styles.quiet} data-mapping-draft-said="">
          {edited ? t("claimMapping.draft.edited", { version: m.version }) : t("claimMapping.draft.unchanged", { version: m.version })}
        </p>
        {!canWrite ? <p className={styles.quiet}>{t("claimMapping.draft.readOnly")}</p> : null}
        {writeRefused && writeRefused.source === draft ? (
          <Notice tone="deny" role="alert" data-mapping-write-refused={writeRefused.rejections.length}>
            <p className={styles.para}>{t("claimMapping.rejected", { count: writeRefused.rejections.length })}</p>
          </Notice>
        ) : null}
        {check.state === "failed" ? <PartialNotice gaps={[t("claimMapping.partial.check")]} /> : null}
        <ViewLinks
          label={t("claimMapping.view.label")}
          current={view}
          views={[
            { id: "visual", to: "inventory/claim-mapping", label: t("claimMapping.view.visual") },
            { id: "source", to: "inventory/claim-mapping", query: { view: "source" }, label: t("claimMapping.view.source") },
          ]}
        />
        {view === "source" ? (
          <div className={styles.source}>
            <Field label={t("claimMapping.source.label")} hint={t("claimMapping.source.hint")}>
              <Textarea mono rows={22} value={draft} onChange={(e) => setDraft(e.target.value)} name="document" data-mapping-source="" />
            </Field>
            {rejections.length ? <RejectionList rejections={rejections} /> : null}
          </div>
        ) : (
          <MappingVisual source={draft} onSource={setDraft} rejections={rejections} offered={offered} goToSource={goToSource} />
        )}
      </section>
      <ChangesPanel changes={changes} version={m.version} edited={edited} />
      <PreviewPanel
        active={m.document}
        activeVersion={m.version}
        draft={draft}
        edited={edited}
        sources={offered.sources}
        identities={identities}
        canName={canWrite}
      />
      <Connectors methods={methods} />
      <ConfirmDialog
        open={writing}
        title={t("claimMapping.write.title")}
        name={tenant as string}
        action={t("claimMapping.write.action")}
        hint={t("claimMapping.write.hint", { version: m.version })}
        body={
          <div className={styles.confirm} data-mapping-confirm="">
            <p className={styles.para}>{t("claimMapping.write.body")}</p>
            <p className={styles.para}>{t("claimMapping.write.changes", { version: m.version, count: changes.length })}</p>
            <ChangeList changes={changes} />
          </div>
        }
        onConfirm={write}
        onClose={() => setWriting(false)}
      />
    </>
  );
}

function Facts({ mapping: m }: { mapping: ClaimMapping }) {
  const { t, f } = useI18n();
  return (
    <section className={styles.panel} aria-labelledby="mapping-facts" data-mapping-version={m.version}>
      <h2 id="mapping-facts" className={styles.panelTitle}>
        {t("claimMapping.facts")}
      </h2>
      {m.version === 0 ? <p className={styles.quiet}>{t("claimMapping.none")}</p> : null}
      <dl className={styles.facts}>
        <dt>{t("claimMapping.version")}</dt>
        <dd>{f.number(m.version)}</dd>
        <dt>{t("claimMapping.digest")}</dt>
        <dd>
          <CopyableId value={m.digest} />
        </dd>
        {m.description ? (
          <>
            <dt>{t("claimMapping.description")}</dt>
            <dd>
              <span className={styles.operator} translate="no">
                {m.description}
              </span>
            </dd>
          </>
        ) : null}
        <dt>{t("claimMapping.attributes")}</dt>
        <dd>
          <IdentChips items={m.attributes} none={t("inventory.none")} />
        </dd>
        <dt>{t("claimMapping.groups")}</dt>
        <dd>
          <IdentChips items={m.groups} to={groupPath} none={t("inventory.none")} />
        </dd>
        <dt>{t("claimMapping.directories")}</dt>
        <dd data-directories={m.directories.join(" ")}>
          <IdentChips items={m.directories} none={t("claimMapping.directories.none")} />
        </dd>
        <dt>{t("claimMapping.requireDirectories")}</dt>
        <dd data-require-directories={m.require_directories}>
          {m.require_directories ? t("claimMapping.requireDirectories.on") : t("claimMapping.requireDirectories.off")}
        </dd>
        <dt>{t("claimMapping.entries")}</dt>
        <dd data-entries={(m.group_map ?? []).length} data-group-map={(m.group_map ?? []).map((e) => `${e.external}>${e.group}@${(e.directories ?? []).join(",")}`).join("|")}>
          {t("claimMapping.entries.count", { count: (m.group_map ?? []).length })}
        </dd>
      </dl>
    </section>
  );
}

/** The tenant's sign-in connectors: each tied to a directory (it reaches only who that directory provisioned) or not (it creates identities at sign-in). */
function Connectors({ methods }: { methods: readonly Connector[] | null }) {
  const { t } = useI18n();
  if (methods === null) return null;
  return (
    <section className={styles.panel} aria-labelledby="mapping-connectors" data-connectors={methods.length}>
      <h2 id="mapping-connectors" className={styles.panelTitle}>
        {t("claimMapping.connectors")}
      </h2>
      <p className={styles.quiet}>{t("claimMapping.connectors.lede")}</p>
      {methods.length === 0 ? (
        <p className={styles.para}>{t("claimMapping.connectors.none")}</p>
      ) : (
        <DataTable
          caption={t("claimMapping.connectors.caption")}
          columns={[
            { id: "name", header: t("claimMapping.col.connector"), cell: (c) => <Ident value={c.name} /> },
            {
              id: "display",
              header: t("inventory.col.displayName"),
              cell: (c) => (
                <span className={styles.operator} translate="no">
                  {c.display_name}
                </span>
              ),
            },
            { id: "kind", header: t("claimMapping.col.kind"), cell: (c) => <Ident value={c.kind} /> },
            {
              id: "claim",
              header: t("claimMapping.col.directoryClaim"),
              cell: (c) => (c.directory_claim ? <Ident value={c.directory_claim} /> : <span className={styles.quiet}>{t("claimMapping.connector.untied")}</span>),
            },
            {
              id: "source",
              header: t("claimMapping.col.directorySource"),
              cell: (c) => (c.directory_source ? <Ident value={c.directory_source} /> : null),
            },
          ]}
          rows={methods}
          rowKey={(c) => c.name}
          rowLabel={(c) => c.name}
        />
      )}
    </section>
  );
}
