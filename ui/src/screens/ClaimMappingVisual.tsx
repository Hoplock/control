// THE VISUAL CLAIM-MAPPING EDITOR (0068; PLAN M7, §6 "Federation (M7)" and
// "The directory door"). The mapping shown as what it is: claims that become
// attributes, and group names that become local groups, some scoped to the
// directories they come from. It is the same draft the source view edits —
// the document is the one truth, and every edit here is the smallest change
// to its text that says it (claimMappingDocument.ts), so an operator can
// switch views at any time and nothing they did not touch changes.
//
// Every name typed is held where it is typed to the server's own pattern and
// reserved set (claimMappingRules.json, generated from internal/identity);
// a directory is chosen from the sources the tenant holds (`GET
// …/identity/directories`) and a local group offered from its groups (`GET
// …/groups`), never from a list written here. The server stays the
// authority: every `mapping.*` rejection of the debounced dry run renders on
// the row and field its `field` path names, and one about the whole document
// heads the view.

import { useId, useMemo, useState, type FormEvent, type ReactNode } from "react";
import type { MappingRejection } from "../api/types";
import { Button, TextButton } from "../components/Button";
import { ChipEditor } from "../components/ChipEditor";
import { Field, Input, Select, Suggestions, Switch, TextField } from "../components/Field";
import { Ident } from "../components/Ident";
import { Notice } from "../components/Notice";
import { SortableList, SortableListSkeleton } from "../components/SortableList";
import { EmptyState, PartialNotice, Skeleton } from "../components/States";
import type { Translate } from "../i18n/messages";
import { useI18n } from "../i18n/I18n";
import { rejectionKey } from "./claimMappingCodes";
import {
  applyMappingEdit,
  asWritten,
  checkMappingField,
  mappingOf,
  parseMapping,
  rejectionTarget,
  removedComments,
  type ClaimRow,
  type EntryRow,
  type MappingEdit,
  type MappingField,
  type NameGroup,
  type RejectionTarget,
  type Unmodelled,
  type VisualMapping,
} from "./claimMappingDocument";
import styles from "./ClaimMapping.module.css";

/** What the view knows of the tenant's directories and groups: the options a chip or a field offers. */
export interface Offered {
  /** The directory sources the tenant holds; null while they could not be read. */
  sources: readonly string[] | null;
  /** The answer was cut: more sources exist than are offered. */
  sourcesCut: boolean;
  /** The tenant's group names, offered for a local group; it may be new. */
  groups: readonly string[];
}

/** The draft's rejections, sorted to where each renders. */
interface Placed {
  document: MappingRejection[];
  claims: Map<number, MappingRejection[]>;
  groups: MappingRejection[];
  entries: Map<number, MappingRejection[]>;
}

function place(rejections: readonly MappingRejection[]): Placed {
  const out: Placed = { document: [], claims: new Map(), groups: [], entries: new Map() };
  const push = (m: Map<number, MappingRejection[]>, i: number, r: MappingRejection) => m.set(i, [...(m.get(i) ?? []), r]);
  for (const r of rejections) {
    const t = rejectionTarget(r.field);
    if (t.kind === "claim") push(out.claims, t.index, r);
    else if (t.kind === "entry") push(out.entries, t.index, r);
    else if (t.kind === "groups") out.groups.push(r);
    else out.document.push(r);
  }
  return out;
}

/** The sentence for the rejections of one field, or null: the server's code's words. */
function errorAt(t: Translate, rows: readonly MappingRejection[] | undefined, match: (target: RejectionTarget) => boolean): string | null {
  const hits = (rows ?? []).filter((r) => match(rejectionTarget(r.field)));
  if (hits.length === 0) return null;
  return hits.map((r) => t(rejectionKey(r.code).key, { code: r.code })).join(" ");
}

function codeText(t: Translate, code: string | null): string | null {
  return code ? t(rejectionKey(code).key, { code }) : null;
}

/** Makes an edit and says what it did; a removal's words are told how many comments went with the row. */
type Commit = (e: MappingEdit, announce: string | ((comments: number) => string)) => boolean;

export function MappingVisual({
  source,
  onSource,
  rejections,
  offered,
  goToSource,
}: {
  source: string;
  onSource(next: string): void;
  /** The server's rejections of this draft, from its dry run or a refused write. */
  rejections: readonly MappingRejection[];
  offered: Offered;
  /** Switches to the source view, at a line. */
  goToSource(line?: number): void;
}) {
  const { t } = useI18n();
  const doc = useMemo(() => parseMapping(source), [source]);
  const visual = useMemo(() => mappingOf(doc), [doc]);
  const [said, setSaid] = useState("");
  const [refusal, setRefusal] = useState<"structure" | "comment_lost" | null>(null);

  const commit: Commit = (e, announce) => {
    const comments = removedComments(doc, e);
    const a = applyMappingEdit(doc, e);
    if (a.kind === "refused") {
      setRefusal(a.reason);
      setSaid("");
      return false;
    }
    setRefusal(null);
    if (a.source !== source) onSource(a.source);
    setSaid(typeof announce === "function" ? announce(comments) : announce);
    return true;
  };

  if (typeof visual === "string") {
    // A document that does not parse cannot be shown visually: its rejections head the view, as the source shows them.
    return (
      <section className={styles.panel} aria-labelledby="mapping-visual-off" data-mapping-visual="off" data-mapping-visual-reason={visual}>
        <h3 id="mapping-visual-off" className={styles.sectionTitle}>
          {t("claimMapping.visual.off.title")}
        </h3>
        <Notice tone="deny">
          <p className={styles.para}>{visual === "not_yaml" ? t("claimMapping.visual.off.notYaml") : t("claimMapping.visual.off.notMapping")}</p>
        </Notice>
        {rejections.length ? <RejectionList rejections={rejections} /> : null}
        <div className={styles.actions}>
          <Button variant="primary" onClick={() => goToSource()} data-mapping-to-source="">
            {t("claimMapping.visual.toSource")}
          </Button>
        </div>
      </section>
    );
  }

  const placed = place(rejections);
  return (
    <div className={styles.visual} data-mapping-visual="on">
      {refusal ? (
        <Notice tone="warn" data-mapping-refused={refusal}>
          <p className={styles.para}>{refusal === "comment_lost" ? t("claimMapping.visual.refused.comment") : t("claimMapping.visual.refused.structure")}</p>
          <TextButton onClick={() => goToSource()}>{t("claimMapping.visual.toSource")}</TextButton>
        </Notice>
      ) : null}
      {placed.document.length ? <RejectionList rejections={placed.document} /> : null}
      <DocumentPanel visual={visual} commit={commit} goToSource={goToSource} />
      <ClaimsPanel visual={visual} commit={commit} rejections={placed.claims} goToSource={goToSource} />
      <GroupsPanel visual={visual} commit={commit} groupRejections={placed.groups} rejections={placed.entries} offered={offered} goToSource={goToSource} />
      <p className={styles.said} role="status" aria-live="polite" data-mapping-said="">
        {said}
      </p>
    </div>
  );
}

/** The faults the server named that no row or field can carry, each with its field as data. */
export function RejectionList({ rejections }: { rejections: readonly MappingRejection[] }) {
  const { t } = useI18n();
  return (
    <Notice tone="deny" role="alert" data-rejections={rejections.length}>
      <p className={styles.para}>{t("claimMapping.rejected.draft", { count: rejections.length })}</p>
      <ul className={styles.rejections}>
        {rejections.map((r, i) => (
          <li key={`${r.code}|${r.field ?? ""}|${i}`} data-rejection={r.code} data-field={r.field ?? ""}>
            {r.field ? <Ident value={r.field} /> : <span className={styles.quiet}>{t("claimMapping.rejection.document")}</span>}
            <span>{t(rejectionKey(r.code).key, { code: r.code })}</span>
          </li>
        ))}
      </ul>
    </Notice>
  );
}

/** Keys the view does not model, kept as written, each with a way to its line in the source. */
function UnmodelledKeys({ keys, goToSource }: { keys: readonly Unmodelled[]; goToSource(line?: number): void }) {
  const { t } = useI18n();
  if (keys.length === 0) return null;
  return (
    <ul className={styles.unmodelled} data-mapping-unmodelled={keys.length}>
      {keys.map((k) => (
        <li key={`${k.key}|${k.line}`} className={styles.unmodelledItem}>
          <span className={styles.quiet}>{t("claimMapping.visual.unmodelled")}</span>
          <Ident value={k.key} />
          <TextButton onClick={() => goToSource(k.line)} data-mapping-unmodelled-line={k.line}>
            {t("claimMapping.visual.toSourceLine", { line: k.line })}
          </TextButton>
        </li>
      ))}
    </ul>
  );
}

function DocumentPanel({ visual, commit, goToSource }: { visual: VisualMapping; commit: Commit; goToSource(line?: number): void }) {
  const { t } = useI18n();
  return (
    <section className={styles.panel} aria-labelledby="mapping-visual-document" data-mapping-section="document">
      <h3 id="mapping-visual-document" className={styles.sectionTitle}>
        {t("claimMapping.visual.document")}
      </h3>
      <dl className={styles.facts}>
        <dt>{t("claimMapping.visual.schema")}</dt>
        <dd>{visual.schemaVersion ? <Ident value={visual.schemaVersion} /> : <span className={styles.quiet}>{t("claimMapping.visual.schema.missing")}</span>}</dd>
      </dl>
      <TextField
        label={t("claimMapping.visual.description")}
        hint={t("claimMapping.visual.description.hint")}
        value={visual.description ?? ""}
        onApply={(v) => commit({ op: "description", value: v }, t("claimMapping.visual.said.description"))}
        data={{ "data-mapping-field": "description" }}
      />
      <UnmodelledKeys keys={visual.unmodelled} goToSource={goToSource} />
    </section>
  );
}

// ---------------------------------------------------------------------------
// Claims that become attributes.
// ---------------------------------------------------------------------------

function ClaimsPanel({
  visual,
  commit,
  rejections,
  goToSource,
}: {
  visual: VisualMapping;
  commit: Commit;
  rejections: Map<number, MappingRejection[]>;
  goToSource(line?: number): void;
}) {
  const { t, f } = useI18n();
  const name = (c: ClaimRow) => c.attribute || c.claim || t("claimMapping.visual.claim.unnamed");
  return (
    <section className={styles.panel} aria-labelledby="mapping-visual-claims" data-mapping-section="claims" data-claims={visual.claims.length}>
      <div className={styles.head}>
        <h3 id="mapping-visual-claims" className={styles.sectionTitle}>
          {t("claimMapping.visual.claims.title")}
        </h3>
        <span className={styles.meta}>{t("claimMapping.visual.claims.count", { count: visual.claims.length })}</span>
      </div>
      <p className={styles.quiet}>{t("claimMapping.visual.claims.lede")}</p>
      {!visual.claimsEditable ? (
        <Notice tone="info">
          <p className={styles.para}>{t("claimMapping.visual.claims.flow")}</p>
        </Notice>
      ) : null}
      {visual.claims.length === 0 ? <EmptyState title={t("claimMapping.visual.claims.empty.title")} body={t("claimMapping.visual.claims.empty.body")} /> : null}
      <SortableList
        label={t("claimMapping.visual.claims.label")}
        items={visual.claims}
        itemKey={(c, i) => `${c.attribute}#${visual.claims.slice(0, i).filter((x) => x.attribute === c.attribute).length}`}
        itemName={(c) => name(c)}
        disabled={!visual.claimsEditable}
        onMove={(from, to) => commit({ op: "moveClaim", from, to }, "")}
        renderItem={(c) => (
          <ClaimEditor
            row={c}
            rejections={rejections.get(c.index)}
            commit={commit}
            canRemove={visual.claimsEditable}
            name={name(c)}
            goToSource={goToSource}
            position={f.number(c.index + 1)}
          />
        )}
      />
      {visual.claimsEditable ? <AddClaim commit={commit} position={visual.claims.length + 1} /> : null}
    </section>
  );
}

function ClaimEditor({
  row,
  rejections,
  commit,
  canRemove,
  name,
  position,
  goToSource,
}: {
  row: ClaimRow;
  rejections: MappingRejection[] | undefined;
  commit: Commit;
  canRemove: boolean;
  name: string;
  position: string;
  goToSource(line?: number): void;
}) {
  const { t } = useI18n();
  const at = (key: string) => (target: RejectionTarget) => target.kind === "claim" && target.key === key;
  const rowLevel = errorAt(t, rejections, at("row"));
  const changed = (field: string) => t("claimMapping.visual.said.claimSet", { field, position });
  return (
    <article className={styles.row} data-claim-row={row.index} data-claim-attribute={row.attribute} aria-label={t("claimMapping.visual.claim.label", { position })}>
      <div className={styles.rowFields}>
        <TextField
          label={t("claimMapping.visual.claim.claim")}
          value={row.claim}
          mono
          check={(v) => codeText(t, checkMappingField("claim", v))}
          error={errorAt(t, rejections, at("claim"))}
          onApply={(v) => commit({ op: "claim", index: row.index, claim: asWritten("claim", v) }, changed(t("claimMapping.visual.claim.claim")))}
          data={{ "data-mapping-field": `claims[${row.index}].claim` }}
        />
        <TextField
          label={t("claimMapping.visual.claim.attribute")}
          value={row.attribute}
          mono
          check={(v) => codeText(t, checkMappingField("attribute", v))}
          error={errorAt(t, rejections, at("attribute"))}
          onApply={(v) => commit({ op: "attribute", index: row.index, attribute: asWritten("attribute", v) }, changed(t("claimMapping.visual.claim.attribute")))}
          data={{ "data-mapping-field": `claims[${row.index}].attribute` }}
        />
      </div>
      <ChipEditor
        name={`claims[${row.index}].values`}
        label={t("claimMapping.visual.claim.values")}
        hint={row.values === null ? t("claimMapping.visual.claim.values.any") : t("claimMapping.visual.claim.values.listed")}
        values={row.values ?? []}
        check={(v) => codeText(t, checkMappingField("value", v))}
        error={errorAt(t, rejections, at("values"))}
        onChange={(vs) => commit({ op: "values", index: row.index, values: vs }, changed(t("claimMapping.visual.claim.values")))}
      />
      {rowLevel ? (
        <Notice tone="deny">
          <p className={styles.para}>{rowLevel}</p>
        </Notice>
      ) : null}
      <UnmodelledKeys keys={row.unmodelled} goToSource={goToSource} />
      {canRemove ? (
        <div className={styles.rowActions}>
          <Button
            variant="ghost"
            onClick={(e) => {
              e.preventDefault();
              commit({ op: "removeClaim", index: row.index }, (comments) => t("claimMapping.visual.said.claimRemoved", { name: `\u2068${name}\u2069`, comments }));
            }}
            data-claim-remove={row.index}
          >
            {t("claimMapping.visual.claim.remove", { name: `\u2068${name}\u2069` })}
          </Button>
        </div>
      ) : null}
    </article>
  );
}

function AddClaim({ commit, position }: { commit: Commit; position: number }) {
  const { t, f } = useI18n();
  const [claim, setClaim] = useState("");
  const [attribute, setAttribute] = useState("");
  const [errors, setErrors] = useState<{ claim?: string | null; attribute?: string | null }>({});
  const submit = (e: FormEvent) => {
    e.preventDefault();
    const next = { claim: codeText(t, checkMappingField("claim", claim)), attribute: codeText(t, checkMappingField("attribute", attribute)) };
    setErrors(next);
    if (next.claim || next.attribute) return;
    const c = asWritten("claim", claim);
    const a = asWritten("attribute", attribute);
    if (commit({ op: "addClaim", claim: c, attribute: a }, t("claimMapping.visual.said.claimAdded", { claim: `\u2068${c}\u2069`, attribute: `\u2068${a}\u2069`, position: f.number(position) }))) {
      setClaim("");
      setAttribute("");
    }
  };
  return (
    <form className={styles.add} onSubmit={submit} noValidate data-mapping-add="claim" aria-label={t("claimMapping.visual.claim.add")}>
      <Field label={t("claimMapping.visual.claim.claim")} error={errors.claim ?? undefined}>
        <Input mono translate="no" value={claim} onChange={(e) => setClaim(e.target.value)} data-mapping-add-field="claim" />
      </Field>
      <Field label={t("claimMapping.visual.claim.attribute")} error={errors.attribute ?? undefined}>
        <Input mono translate="no" value={attribute} onChange={(e) => setAttribute(e.target.value)} data-mapping-add-field="attribute" />
      </Field>
      <div className={styles.addAction}>
        <Button type="submit">{t("claimMapping.visual.claim.add")}</Button>
      </div>
    </form>
  );
}

// ---------------------------------------------------------------------------
// Groups that become local groups, by the name they read.
// ---------------------------------------------------------------------------

function GroupsPanel({
  visual,
  commit,
  groupRejections,
  rejections,
  offered,
  goToSource,
}: {
  visual: VisualMapping;
  commit: Commit;
  groupRejections: MappingRejection[];
  rejections: Map<number, MappingRejection[]>;
  offered: Offered;
  goToSource(line?: number): void;
}) {
  const { t } = useI18n();
  const g = visual.groups;
  const at = (key: string) => (target: RejectionTarget) => target.kind === "groups" && target.key === key;
  return (
    <section className={styles.panel} aria-labelledby="mapping-visual-groups" data-mapping-section="groups" data-entries={g?.entries.length ?? 0}>
      <div className={styles.head}>
        <h3 id="mapping-visual-groups" className={styles.sectionTitle}>
          {t("claimMapping.visual.groups.title")}
        </h3>
        {g ? <span className={styles.meta}>{t("claimMapping.visual.groups.count", { count: g.entries.length, names: g.names.length })}</span> : null}
      </div>
      <p className={styles.quiet}>{t("claimMapping.visual.groups.lede")}</p>
      {offered.sources === null ? <PartialNotice gaps={[t("claimMapping.visual.directories.unread")]} /> : null}
      {offered.sourcesCut ? (
        <Notice tone="info">
          <p className={styles.para}>{t("claimMapping.visual.directories.cut", { count: offered.sources?.length ?? 0 })}</p>
        </Notice>
      ) : null}
      <TextField
        label={t("claimMapping.visual.groups.claim")}
        hint={g ? t("claimMapping.visual.groups.claim.hint") : t("claimMapping.visual.groups.claim.start")}
        value={g?.claim ?? ""}
        mono
        check={(v) => codeText(t, checkMappingField("group_claim", v))}
        error={errorAt(t, groupRejections, at("claim"))}
        onApply={(v) => commit({ op: "groupClaim", claim: asWritten("group_claim", v) }, t("claimMapping.visual.said.groupClaim"))}
        data={{ "data-mapping-field": "groups.claim" }}
      />
      {g ? (
        <>
          <div className={styles.require} data-require-directories={g.requireDirectories}>
            <Switch
              label={t("claimMapping.requireDirectories")}
              checked={g.requireDirectories}
              onChange={(e) => commit({ op: "requireDirectories", on: e.target.checked }, t("claimMapping.visual.said.require", { on: String(e.target.checked) }))}
              data-mapping-field="groups.require_directories"
            />
            <p className={styles.quiet}>{g.requireDirectories ? t("claimMapping.requireDirectories.on") : t("claimMapping.requireDirectories.off")}</p>
          </div>
          {errorAt(t, groupRejections, at("row")) ? (
            <Notice tone="deny">
              <p className={styles.para}>{errorAt(t, groupRejections, at("row"))}</p>
            </Notice>
          ) : null}
          <UnmodelledKeys keys={g.unmodelled} goToSource={goToSource} />
          {!g.editable ? (
            <Notice tone="info">
              <p className={styles.para}>{t("claimMapping.visual.groups.flow")}</p>
            </Notice>
          ) : null}
          {g.entries.length === 0 ? <EmptyState title={t("claimMapping.visual.groups.empty.title")} body={t("claimMapping.visual.groups.empty.body")} /> : null}
          <ul className={styles.names} aria-label={t("claimMapping.visual.groups.names")}>
            {g.names.map((n) => (
              <li key={n.external}>
                <NameEntries name={n} required={g.requireDirectories} commit={commit} rejections={rejections} offered={offered} editable={g.editable} goToSource={goToSource} />
              </li>
            ))}
          </ul>
          {g.editable ? <AddEntry commit={commit} offered={offered} /> : null}
        </>
      ) : null}
    </section>
  );
}

/** Every entry that reads one name: the one that serves sign-ins and each scoped to directories, seen together. */
function NameEntries({
  name,
  required,
  commit,
  rejections,
  offered,
  editable,
  goToSource,
}: {
  name: NameGroup;
  required: boolean;
  commit: Commit;
  rejections: Map<number, MappingRejection[]>;
  offered: Offered;
  editable: boolean;
  goToSource(line?: number): void;
}) {
  const { t } = useI18n();
  const headingId = useId();
  const listed = name.entries.flatMap((e) => e.directories ?? []);
  return (
    <section className={styles.name} aria-labelledby={headingId} data-entry-name={name.external}>
      <h4 id={headingId} className={styles.nameTitle}>
        <span className={styles.quiet}>{t("claimMapping.visual.entry.reads")}</span> <Ident value={name.external} />
      </h4>
      {name.entries.map((e) => (
        <EntryEditor
          key={e.index}
          entry={e}
          required={required}
          commit={commit}
          rejections={rejections.get(e.index)}
          offered={offered}
          canRemove={editable}
          goToSource={goToSource}
        />
      ))}
      {editable ? <AddScoped external={name.external} listed={listed} commit={commit} offered={offered} /> : null}
    </section>
  );
}

function EntryEditor({
  entry,
  required,
  commit,
  rejections,
  offered,
  canRemove,
  goToSource,
}: {
  entry: EntryRow;
  required: boolean;
  commit: Commit;
  rejections: MappingRejection[] | undefined;
  offered: Offered;
  canRemove: boolean;
  goToSource(line?: number): void;
}) {
  const { t } = useI18n();
  const at = (key: string) => (target: RejectionTarget) => target.kind === "entry" && target.key === key;
  const scoped = entry.directories !== null;
  const options = useMemo(() => [...new Set([...(offered.sources ?? []), ...(entry.directories ?? [])])].sort(), [offered.sources, entry.directories]);
  const serves = scoped ? t("claimMapping.visual.entry.scoped") : required ? t("claimMapping.entry.loginsOnly") : t("claimMapping.entry.unscoped");
  const name = `\u2068${entry.external}\u2069`;
  const rowLevel = errorAt(t, rejections, (x) => x.kind === "entry" && (x.key === "row" || x.key === "external"));
  return (
    <article className={styles.row} data-entry-row={entry.index} data-entry-scoped={scoped} aria-label={t("claimMapping.visual.entry.label", { name, group: `\u2068${entry.group}\u2069` })}>
      <dl className={styles.facts}>
        <dt>{t("claimMapping.visual.entry.serves")}</dt>
        <dd data-entry-serves="">{serves}</dd>
      </dl>
      <div className={styles.rowFields}>
        <TextField
          label={t("claimMapping.visual.entry.group")}
          hint={t("claimMapping.visual.entry.group.hint")}
          value={entry.group}
          mono
          suggestions={offered.groups}
          check={(v) => codeText(t, checkMappingField("group", v))}
          error={errorAt(t, rejections, at("group"))}
          onApply={(v) => commit({ op: "entryGroup", index: entry.index, group: asWritten("group", v) }, t("claimMapping.visual.said.entrySet", { name, field: t("claimMapping.visual.entry.group") }))}
          data={{ "data-mapping-field": `groups.map[${entry.index}].group` }}
        />
        <ChipEditor
          name={`groups.map[${entry.index}].directories`}
          label={t("claimMapping.visual.entry.directories")}
          hint={scoped ? undefined : t("claimMapping.visual.entry.directories.hint")}
          values={entry.directories ?? []}
          options={options}
          error={errorAt(t, rejections, at("directories"))}
          disabled={offered.sources === null && !scoped}
          onChange={(vs) => commit({ op: "entryDirectories", index: entry.index, directories: vs }, t("claimMapping.visual.said.entrySet", { name, field: t("claimMapping.visual.entry.directories") }))}
        />
      </div>
      {rowLevel ? (
        <Notice tone="deny">
          <p className={styles.para}>{rowLevel}</p>
        </Notice>
      ) : null}
      <UnmodelledKeys keys={entry.unmodelled} goToSource={goToSource} />
      {canRemove ? (
        <div className={styles.rowActions}>
          <Button
            variant="ghost"
            onClick={() => commit({ op: "removeEntry", index: entry.index }, (comments) => t("claimMapping.visual.said.entryRemoved", { name, comments }))}
            data-entry-remove={entry.index}
          >
            {t("claimMapping.visual.entry.remove")}
          </Button>
        </div>
      ) : null}
    </article>
  );
}

/** Adds an entry for one more directory to a name: the directory chosen from the tenant's, the group offered from its groups. */
function AddScoped({ external, listed, commit, offered }: { external: string; listed: readonly string[]; commit: Commit; offered: Offered }) {
  const { t } = useI18n();
  const listId = useId();
  const [source, setSource] = useState("");
  const [group, setGroup] = useState("");
  const [error, setError] = useState<string | null>(null);
  const choices = (offered.sources ?? []).filter((s) => !listed.includes(s));
  if (offered.sources === null) return null;
  const submit = (e: FormEvent) => {
    e.preventDefault();
    const problem = source === "" ? t("claimMapping.visual.entry.chooseDirectory") : codeText(t, checkMappingField("group", group));
    setError(problem);
    if (problem) return;
    const g = asWritten("group", group);
    if (commit({ op: "addEntry", external, group: g, directories: [source] }, t("claimMapping.visual.said.entryAdded", { name: `\u2068${external}\u2069`, group: `\u2068${g}\u2069` }))) {
      setSource("");
      setGroup("");
    }
  };
  return (
    <form className={styles.add} onSubmit={submit} noValidate data-mapping-add="scoped" data-mapping-add-name={external} aria-label={t("claimMapping.visual.entry.addScoped")}>
      <Field label={t("claimMapping.visual.entry.directory")} hint={choices.length ? undefined : t("claimMapping.visual.entry.noDirectory")}>
        <Select value={source} onChange={(e) => setSource(e.target.value)} disabled={choices.length === 0} data-mapping-add-field="directory">
          <option value="">{t("claimMapping.visual.entry.chooseDirectory")}</option>
          {choices.map((s) => (
            <option key={s} value={s} translate="no">
              {s}
            </option>
          ))}
        </Select>
      </Field>
      <Field label={t("claimMapping.visual.entry.group")} error={error ?? undefined}>
        <Input mono translate="no" list={listId} autoComplete="off" value={group} onChange={(e) => setGroup(e.target.value)} data-mapping-add-field="group" />
      </Field>
      <Suggestions id={listId} values={offered.groups} />
      <div className={styles.addAction}>
        <Button type="submit" disabled={choices.length === 0}>
          {t("claimMapping.visual.entry.addScoped")}
        </Button>
      </div>
    </form>
  );
}

/** Adds an entry for a name the mapping does not read yet: unscoped, or scoped to directories chosen from the tenant's. */
function AddEntry({ commit, offered }: { commit: Commit; offered: Offered }) {
  const { t } = useI18n();
  const listId = useId();
  const [external, setExternal] = useState("");
  const [group, setGroup] = useState("");
  const [directories, setDirectories] = useState<string[]>([]);
  const [errors, setErrors] = useState<{ external?: string | null; group?: string | null }>({});
  const submit = (e: FormEvent) => {
    e.preventDefault();
    const next = { external: codeText(t, checkMappingField("external", external)), group: codeText(t, checkMappingField("group", group)) };
    setErrors(next);
    if (next.external || next.group) return;
    const x = asWritten("external", external);
    const g = asWritten("group", group);
    if (commit({ op: "addEntry", external: x, group: g, directories }, t("claimMapping.visual.said.entryAdded", { name: `\u2068${x}\u2069`, group: `\u2068${g}\u2069` }))) {
      setExternal("");
      setGroup("");
      setDirectories([]);
    }
  };
  return (
    <form className={styles.addEntry} onSubmit={submit} noValidate data-mapping-add="entry" aria-labelledby={`${listId}-title`}>
      <h4 id={`${listId}-title`} className={styles.nameTitle}>
        {t("claimMapping.visual.entry.add.title")}
      </h4>
      <div className={styles.rowFields}>
        <Field label={t("claimMapping.visual.entry.add.external")} error={errors.external ?? undefined}>
          <Input mono translate="no" value={external} onChange={(e) => setExternal(e.target.value)} data-mapping-add-field="external" />
        </Field>
        <Field label={t("claimMapping.visual.entry.group")} hint={t("claimMapping.visual.entry.group.hint")} error={errors.group ?? undefined}>
          <Input mono translate="no" list={listId} autoComplete="off" value={group} onChange={(e) => setGroup(e.target.value)} data-mapping-add-field="group" />
        </Field>
      </div>
      <Suggestions id={listId} values={offered.groups} />
      {offered.sources !== null ? (
        <ChipEditor
          name="add.directories"
          label={t("claimMapping.visual.entry.directories")}
          hint={t("claimMapping.visual.entry.directories.hint")}
          values={directories}
          options={offered.sources}
          onChange={setDirectories}
        />
      ) : null}
      <div className={styles.addAction}>
        <Button type="submit">{t("claimMapping.visual.entry.add.submit")}</Button>
      </div>
    </form>
  );
}

/** The visual view's loading state: its sections' shape, so nothing shifts. */
export function MappingVisualSkeleton(): ReactNode {
  return (
    <div className={styles.visual} aria-hidden="true" data-mapping-visual="loading">
      <div className={styles.panel}>
        <Skeleton shape="title" size="m" />
        <SortableListSkeleton rows={2} />
      </div>
      <div className={styles.panel}>
        <Skeleton shape="title" size="m" />
        <Skeleton shape="block" size="full" />
      </div>
    </div>
  );
}
