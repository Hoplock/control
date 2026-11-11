// THE CLAIM MAPPING (0055; PLAN M7, §6 "The directory door"; 0011, 0043).
//
// /console/inventory/claim-mapping[?edit=1]: the tenant's active mapping —
// what a sign-in's claims and a directory's fields become as attributes and
// groups — its group entries, each with the directories it is scoped to, and
// the sign-in connectors it serves, tied to a directory or not. The read is
// identity:read; writing a new version is identity:write, offered only to a
// role holding it and refused by the API to anyone else (M2).
//
// The mapping is a document the server parses, and the console never parses
// it: each entry's directories come from the read's `group_map` (north-bound
// version 5), and a refused write's faults are codes, each rendered at the
// field it names (claimMappingCodes.ts, M21). A new version reaches every
// directory subject by itself, in the background (0043), so while some are
// still on an older one the screen says how many.

import { useState, type FormEvent } from "react";
import { offered } from "../api/permit";
import type { ClaimMapping, ClaimMappingWritten, Connector, GroupEntry, MappingRejection, SignInMethods } from "../api/types";
import { Button } from "../components/Button";
import { SourceView } from "../components/Code";
import { CopyableId } from "../components/CopyableId";
import { DataTable } from "../components/DataTable";
import { Field, Textarea } from "../components/Field";
import { Ident } from "../components/Ident";
import { PageHeader } from "../components/PageHeader";
import { ErrorMessage, ScreenFrame, Skeleton } from "../components/States";
import { StatusOf } from "../components/Status";
import { useToast } from "../components/Toast";
import { useI18n } from "../i18n/I18n";
import { useLocation } from "../router/router";
import { useLoad } from "../shell/load";
import { useDeployment } from "../shell/Shell";
import { rejectionKey } from "./claimMappingCodes";
import { groupPath } from "./Identities";
import { IdentChips, InventoryViews, usePermissions, useSetQuery } from "./InventoryParts";
import styles from "./Inventory.module.css";

export function ClaimMappingScreen() {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const location = useLocation();
  const setQuery = useSetQuery();
  const permissions = usePermissions();
  const canWrite = offered("claimMappingPut", permissions);
  const editing = canWrite && location.search.get("edit") === "1";
  const [load, refresh] = useLoad<ClaimMapping>(tenant ? `claim-mapping|${tenant}` : null, (signal) =>
    api.call("claimMapping", { params: { tenant: tenant as string }, signal }),
  );
  const [methods] = useLoad<SignInMethods>(tenant ? `sign-in-methods|${tenant}` : null, (signal) =>
    api.call("sessionMethods", { query: { tenant: tenant as string }, signal }),
  );

  return (
    <div className={styles.screen}>
      <PageHeader
        title={t("inventory.title")}
        lede={t("claimMapping.lede")}
        actions={
          <>
            {canWrite && !editing ? (
              <Button variant="primary" onClick={() => setQuery({ edit: "1" })} data-edit="">
                {t("claimMapping.write")}
              </Button>
            ) : null}
            <Button onClick={refresh}>{t("inventory.refresh")}</Button>
          </>
        }
      />
      <InventoryViews current="mapping" />
      <ScreenFrame
        load={load}
        onRetry={refresh}
        gaps={methods.state === "outage" ? [t("claimMapping.partial.connectors")] : []}
        // A tenant always has a mapping: before one is written it is version
        // 0, which maps nothing, and the facts say so.
        isEmpty={() => false}
        empty={null}
        loading={
          <div className={styles.stack}>
            <Skeleton size="l" />
            <Skeleton shape="block" size="full" />
          </div>
        }
      >
        {(m) => (
          <>
            {m.stale_directory_subjects > 0 ? (
              <p className={styles.notice} role="status" data-stale={m.stale_directory_subjects}>
                <span className={styles.denials}>
                  <StatusOf name="inProgress" />
                  <span>{t("claimMapping.stale", { count: m.stale_directory_subjects, version: m.version })}</span>
                </span>
              </p>
            ) : null}
            <Facts mapping={m} />
            <Entries mapping={m} />
            {editing ? (
              <section className={styles.panel} aria-labelledby="mapping-edit">
                <h2 id="mapping-edit" className={styles.panelTitle}>
                  {t("claimMapping.edit.title")}
                </h2>
                <MappingEditor
                  mapping={m}
                  onSaved={() => {
                    setQuery({ edit: undefined });
                    refresh();
                  }}
                  onCancel={() => setQuery({ edit: undefined })}
                />
              </section>
            ) : (
              <section className={styles.panel} aria-labelledby="mapping-document">
                <h2 id="mapping-document" className={styles.panelTitle}>
                  {t("claimMapping.document")}
                </h2>
                <SourceView source={m.document} label={t("claimMapping.document.label", { version: m.version })} idPrefix="mapping" />
              </section>
            )}
            <Connectors methods={methods.state === "ok" ? methods.data.methods : null} />
          </>
        )}
      </ScreenFrame>
    </div>
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
      </dl>
    </section>
  );
}

/** What an entry with no directories serves: logins, and every directory no scoped entry lists for its name — unless scoping is required. */
function Serves({ entry, required }: { entry: GroupEntry; required: boolean }) {
  const { t } = useI18n();
  if (entry.directories?.length) return <IdentChips items={entry.directories} />;
  return <span className={styles.quiet}>{required ? t("claimMapping.entry.loginsOnly") : t("claimMapping.entry.unscoped")}</span>;
}

function Entries({ mapping: m }: { mapping: ClaimMapping }) {
  const { t } = useI18n();
  const entries = m.group_map ?? [];
  return (
    <section className={styles.panel} aria-labelledby="mapping-entries" data-entries={entries.length}>
      <h2 id="mapping-entries" className={styles.panelTitle}>
        {t("claimMapping.entries")}
      </h2>
      <p className={styles.quiet}>{t("claimMapping.entries.lede")}</p>
      {entries.length === 0 ? (
        <p className={styles.para}>{t("claimMapping.entries.none")}</p>
      ) : (
        <DataTable
          caption={t("claimMapping.entries.caption")}
          columns={[
            { id: "external", header: t("claimMapping.col.external"), cell: (e) => <Ident value={e.external} /> },
            { id: "group", header: t("claimMapping.col.group"), cell: (e) => <IdentChips items={[e.group]} to={groupPath} /> },
            { id: "directories", header: t("claimMapping.col.directories"), cell: (e) => <Serves entry={e} required={m.require_directories} /> },
          ]}
          rows={entries}
          rowKey={(e) => `${e.external}|${(e.directories ?? []).join(",")}`}
          rowLabel={(e) => e.external}
        />
      )}
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

/** The faults a refused write names, each at its field (`groups.map[0].directories[1]`), in the document's order. */
function Rejections({ rejections }: { rejections: readonly MappingRejection[] }) {
  const { t } = useI18n();
  return (
    <div className={styles.notice} role="alert" data-rejections={rejections.length}>
      <p className={styles.para}>{t("claimMapping.rejected", { count: rejections.length })}</p>
      <ul className={styles.rejections}>
        {rejections.map((r, i) => {
          const { key } = rejectionKey(r.code);
          return (
            <li key={`${r.code}|${r.field ?? ""}|${i}`} data-rejection={r.code} data-field={r.field ?? ""}>
              {r.field ? <Ident value={r.field} /> : <span className={styles.quiet}>{t("claimMapping.rejection.document")}</span>}
              <span>{t(key, { code: r.code })}</span>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

function rejectionsOf(parameters: Record<string, unknown> | undefined): MappingRejection[] | null {
  const r = parameters?.rejections;
  if (!Array.isArray(r)) return null;
  return r.filter((x): x is MappingRejection => typeof x === "object" && x !== null && typeof (x as MappingRejection).code === "string");
}

function MappingEditor({ mapping, onSaved, onCancel }: { mapping: ClaimMapping; onSaved(): void; onCancel(): void }) {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const toast = useToast();
  const [document, setDocument] = useState(mapping.document);
  const [busy, setBusy] = useState(false);
  const [rejections, setRejections] = useState<MappingRejection[] | null>(null);
  const [failure, setFailure] = useState<Parameters<typeof ErrorMessage>[0] | null>(null);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (busy) return;
    setBusy(true);
    setRejections(null);
    setFailure(null);
    const a = await api.call<ClaimMappingWritten>("claimMappingPut", { params: { tenant: tenant as string }, body: { document } });
    setBusy(false);
    if (a.kind === "ok") {
      toast.success(t("claimMapping.saved", { version: a.data.version }));
      onSaved();
      return;
    }
    const listed = a.error.code === "invalid_request" ? rejectionsOf(a.error.parameters) : null;
    if (listed?.length) setRejections(listed);
    else setFailure({ error: a.error, kind: a.kind });
  };

  return (
    <form className={styles.editor} onSubmit={submit} noValidate data-mapping-form="">
      <p className={styles.quiet}>{t("claimMapping.edit.lede")}</p>
      <Field label={t("claimMapping.edit.document")} hint={t("claimMapping.edit.document.hint")}>
        <Textarea mono rows={18} value={document} onChange={(e) => setDocument(e.target.value)} name="document" />
      </Field>
      {rejections ? <Rejections rejections={rejections} /> : null}
      {failure ? <ErrorMessage {...failure} /> : null}
      <div className={styles.actions}>
        <Button type="submit" variant="primary" loading={busy}>
          {t("claimMapping.edit.submit")}
        </Button>
        <Button type="button" variant="ghost" onClick={onCancel}>
          {t("inventory.cancel")}
        </Button>
      </div>
    </form>
  );
}
