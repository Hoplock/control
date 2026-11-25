// WHAT A CLAIM MAPPING DRAFT WOULD PRODUCE, AND WHAT IT CHANGES (0068; PLAN
// M7). Two panels beside the editor, both answered by the server and the
// draft's own document, neither storing anything:
//
//   - "What would this produce?" maps one sample through the active version
//     and through the draft, side by side, by asking the server's dry run
//     (`POST …/identity/claim-mapping/validate`) of each document — the
//     server's own Apply for a sign-in and ApplyDirectory for a directory, so
//     the console never re-implements matching. A sample is claims and groups
//     typed in, through a sign-in or a directory the tenant holds; or, for a
//     role that may write the mapping, an identity a directory provisioned,
//     mapped from what that directory last wrote (the server refuses that to
//     any other role, and the control is not offered to one);
//   - "What this changes" lists, row by row, what the draft adds, removes and
//     changes against the active version (claimMappingDocument.ts).

import { useMemo, useState, type FormEvent } from "react";
import type { MappedSample, MappingDryRun, MappingSample, Subject } from "../api/types";
import { Button } from "../components/Button";
import { ChipEditor } from "../components/ChipEditor";
import { Field, Select } from "../components/Field";
import { Ident } from "../components/Ident";
import { Notice } from "../components/Notice";
import { ErrorMessage, Skeleton, type ErrorKind } from "../components/States";
import type { MessageKey } from "../i18n/catalogue";
import type { ApiError } from "../i18n/errors";
import type { Translate } from "../i18n/messages";
import { useI18n } from "../i18n/I18n";
import { useDeployment } from "../shell/Shell";
import type { ClaimRow, EntryRow, MappingChange } from "./claimMappingDocument";
import styles from "./ClaimMapping.module.css";

// ---------------------------------------------------------------------------
// The preview.
// ---------------------------------------------------------------------------

type Through = { kind: "signIn" } | { kind: "directory"; source: string } | { kind: "identity"; subject: string };

type Result =
  | { state: "idle" }
  | { state: "busy" }
  | { state: "ok"; active: MappedSample; draft: MappedSample | null; draftRefused: boolean }
  | { state: "failed"; error: ApiError; kind: ErrorKind };

/** One claim typed as `name=value`, as the identity carries it. */
function claimOf(text: string): [string, string] | null {
  const i = text.indexOf("=");
  if (i <= 0) return null;
  return [text.slice(0, i).trim(), text.slice(i + 1)];
}

export function PreviewPanel({
  active,
  activeVersion,
  draft,
  edited,
  sources,
  identities,
  canName,
}: {
  /** The active version's document, and its number. */
  active: string;
  activeVersion: number;
  draft: string;
  edited: boolean;
  /** The directory sources the tenant holds, a sample may come through; null while unread. */
  sources: readonly string[] | null;
  /** The directory identities a writer may start from; null while unread or not offered. */
  identities: readonly Subject[] | null;
  /** Whether this role may name an identity: identity:write (the server's rule). */
  canName: boolean;
}) {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const [through, setThrough] = useState<Through>({ kind: "signIn" });
  const [claims, setClaims] = useState<string[]>([]);
  const [groups, setGroups] = useState<string[]>([]);
  const [result, setResult] = useState<Result>({ state: "idle" });

  const sample = (): MappingSample => {
    if (through.kind === "identity") return { subject: through.subject };
    const typed: Record<string, string> = {};
    for (const c of claims) {
      const kv = claimOf(c);
      if (kv) typed[kv[0]] = kv[1];
    }
    return through.kind === "directory" ? { claims: typed, groups, directory_source: through.source } : { claims: typed, groups };
  };

  const run = async (e: FormEvent) => {
    e.preventDefault();
    if (!tenant) return;
    setResult({ state: "busy" });
    const s = sample();
    const ask = (document: string) => api.call<MappingDryRun>("claimMappingValidate", { params: { tenant }, body: { document, samples: [s] } });
    const [a, d] = await Promise.all([ask(active), edited ? ask(draft) : null]);
    if (a.kind !== "ok") {
      setResult({ state: "failed", error: a.error, kind: a.kind });
      return;
    }
    const activeOut = a.data.samples[0] as MappedSample;
    if (d === null) {
      setResult({ state: "ok", active: activeOut, draft: activeOut, draftRefused: false });
      return;
    }
    if (d.kind === "ok") {
      setResult({ state: "ok", active: activeOut, draft: d.data.samples[0] as MappedSample, draftRefused: false });
      return;
    }
    // A draft the server refuses produces nothing yet; its refusals are on its rows.
    if (d.error.code === "invalid_request" && d.error.parameters?.field === "document") {
      setResult({ state: "ok", active: activeOut, draft: null, draftRefused: true });
      return;
    }
    setResult({ state: "failed", error: d.error, kind: d.kind });
  };

  const choice =
    through.kind === "signIn" ? "signIn" : through.kind === "directory" ? `directory:${through.source}` : `identity:${through.subject}`;
  const choose = (v: string) => {
    if (v === "signIn") setThrough({ kind: "signIn" });
    else if (v.startsWith("directory:")) setThrough({ kind: "directory", source: v.slice("directory:".length) });
    else if (v.startsWith("identity:")) setThrough({ kind: "identity", subject: v.slice("identity:".length) });
    setResult({ state: "idle" });
  };

  return (
    <section className={styles.panel} aria-labelledby="mapping-preview" data-mapping-preview={result.state}>
      <h2 id="mapping-preview" className={styles.panelTitle}>
        {t("claimMapping.preview.title")}
      </h2>
      <p className={styles.quiet}>{t("claimMapping.preview.lede", { version: activeVersion })}</p>
      <form className={styles.previewForm} onSubmit={run} noValidate>
        <Field label={t("claimMapping.preview.through")}>
          <Select value={choice} onChange={(e) => choose(e.target.value)} data-preview-field="through">
            <option value="signIn">{t("claimMapping.preview.signIn")}</option>
            {(sources ?? []).map((s) => (
              <option key={s} value={`directory:${s}`} translate="no">
                {s}
              </option>
            ))}
            {canName
              ? (identities ?? []).map((i) => (
                  <option key={i.id} value={`identity:${i.id}`} translate="no">
                    {i.display_name ? `${i.display_name} · ${i.id}` : i.id}
                  </option>
                ))
              : null}
          </Select>
        </Field>
        {through.kind === "identity" ? (
          <p className={styles.quiet}>{t("claimMapping.preview.identity.hint")}</p>
        ) : (
          <>
            <ChipEditor
              name="preview.claims"
              label={t("claimMapping.preview.claims")}
              hint={t("claimMapping.preview.claims.hint")}
              values={claims}
              check={(v) => (claimOf(v) ? null : t("claimMapping.preview.claims.malformed"))}
              onChange={(vs) => {
                setClaims(vs);
                setResult({ state: "idle" });
              }}
            />
            <ChipEditor
              name="preview.groups"
              label={through.kind === "directory" ? t("claimMapping.preview.groups.directory") : t("claimMapping.preview.groups.signIn")}
              values={groups}
              onChange={(vs) => {
                setGroups(vs);
                setResult({ state: "idle" });
              }}
            />
          </>
        )}
        {canName && identities !== null && identities.length === 0 ? <p className={styles.quiet}>{t("claimMapping.preview.identity.none")}</p> : null}
        <div className={styles.actions}>
          <Button type="submit" loading={result.state === "busy"} data-preview-run="">
            {t("claimMapping.preview.run")}
          </Button>
        </div>
      </form>
      <div role="status" aria-live="polite">
        {result.state === "busy" ? <Skeleton shape="block" size="full" /> : null}
        {result.state === "failed" ? <ErrorMessage error={result.error} kind={result.kind} /> : null}
        {result.state === "ok" ? <Compared activeVersion={activeVersion} active={result.active} draft={result.draft} refused={result.draftRefused} /> : null}
      </div>
      <p className={styles.quiet}>{t("claimMapping.preview.local")}</p>
    </section>
  );
}

type Mark = "same" | "gained" | "lost" | "changed";

const MARK_WORD: Readonly<Record<Exclude<Mark, "same">, MessageKey>> = {
  gained: "claimMapping.preview.mark.gained",
  lost: "claimMapping.preview.mark.lost",
  changed: "claimMapping.preview.mark.changed",
};

/** The active version's answer beside the draft's, each difference marked by its word and sign, never by tint alone. */
function Compared({ activeVersion, active, draft, refused }: { activeVersion: number; active: MappedSample; draft: MappedSample | null; refused: boolean }) {
  const { t } = useI18n();
  const attrs = useMemo(() => [...new Set([...Object.keys(active.attributes), ...Object.keys(draft?.attributes ?? {})])].sort(), [active, draft]);
  const groups = useMemo(() => [...new Set([...active.groups, ...(draft?.groups ?? [])])].sort(), [active, draft]);
  const attrMark = (k: string): Mark => {
    const a = active.attributes[k];
    const d = draft?.attributes[k];
    if (a === d) return "same";
    if (a === undefined) return "gained";
    if (d === undefined) return "lost";
    return "changed";
  };
  const groupMark = (g: string): Mark => (active.groups.includes(g) === (draft?.groups.includes(g) ?? false) ? "same" : active.groups.includes(g) ? "lost" : "gained");
  const same = draft !== null && attrs.every((k) => attrMark(k) === "same") && groups.every((g) => groupMark(g) === "same");
  return (
    <div className={styles.compared} data-preview-same={same}>
      {refused ? (
        <Notice tone="deny">
          <p className={styles.para}>{t("claimMapping.preview.draftRefused")}</p>
        </Notice>
      ) : same ? (
        <p className={styles.quiet} data-preview-unchanged="">
          {t("claimMapping.preview.same")}
        </p>
      ) : null}
      <div className={styles.columns}>
        <Produced title={t("claimMapping.preview.active", { version: activeVersion })} side="active" out={active} attrs={attrs} groups={groups} attrMark={attrMark} groupMark={groupMark} />
        {draft ? <Produced title={t("claimMapping.preview.draft")} side="draft" out={draft} attrs={attrs} groups={groups} attrMark={attrMark} groupMark={groupMark} /> : null}
      </div>
    </div>
  );
}

function MarkWord({ mark, t }: { mark: Mark; t: Translate }) {
  if (mark === "same") return null;
  const sign = mark === "gained" ? "+" : mark === "lost" ? "\u2212" : "±";
  return (
    <span className={styles.mark} data-mark={mark}>
      <span aria-hidden="true">{sign}</span> {t(MARK_WORD[mark])}
    </span>
  );
}

function Produced({
  title,
  side,
  out,
  attrs,
  groups,
  attrMark,
  groupMark,
}: {
  title: string;
  side: "active" | "draft";
  out: MappedSample;
  attrs: readonly string[];
  groups: readonly string[];
  attrMark(k: string): Mark;
  groupMark(g: string): Mark;
}) {
  const { t } = useI18n();
  // A difference is marked on the side that has it: a gain or a change on the draft's, a loss on the active one's.
  const shown = (m: Mark) => (side === "draft" ? m === "gained" || m === "changed" : m === "lost");
  const attrRows = attrs.filter((k) => out.attributes[k] !== undefined);
  const groupRows = groups.filter((g) => out.groups.includes(g));
  return (
    <section className={styles.produced} aria-label={title} data-preview-side={side}>
      <h3 className={styles.sectionTitle}>{title}</h3>
      <h4 className={styles.nameTitle}>{t("claimMapping.preview.attributes")}</h4>
      {attrRows.length === 0 ? (
        <p className={styles.quiet}>{t("claimMapping.preview.none")}</p>
      ) : (
        <ul className={styles.producedList}>
          {attrRows.map((k) => {
            const m = attrMark(k);
            return (
              <li key={k} className={shown(m) ? styles[m] : undefined} data-preview-attribute={k} data-mark={shown(m) ? m : "same"}>
                <Ident value={k} /> <Ident value={out.attributes[k] as string} />
                {shown(m) ? <MarkWord mark={m} t={t} /> : null}
              </li>
            );
          })}
        </ul>
      )}
      <h4 className={styles.nameTitle}>{t("claimMapping.preview.groups")}</h4>
      {groupRows.length === 0 ? (
        <p className={styles.quiet}>{t("claimMapping.preview.none")}</p>
      ) : (
        <ul className={styles.producedList}>
          {groupRows.map((g) => {
            const m = groupMark(g);
            return (
              <li key={g} className={shown(m) ? styles[m] : undefined} data-preview-group={g} data-mark={shown(m) ? m : "same"}>
                <Ident value={g} />
                {shown(m) ? <MarkWord mark={m} t={t} /> : null}
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}

// ---------------------------------------------------------------------------
// What the draft changes, row by row.
// ---------------------------------------------------------------------------

function ClaimFacts({ row }: { row: ClaimRow }) {
  const { t } = useI18n();
  return (
    <dl className={styles.changeFacts}>
      <dt>{t("claimMapping.visual.claim.claim")}</dt>
      <dd>
        <Ident value={row.claim} />
      </dd>
      <dt>{t("claimMapping.visual.claim.attribute")}</dt>
      <dd>
        <Ident value={row.attribute} />
      </dd>
      <dt>{t("claimMapping.visual.claim.values")}</dt>
      <dd>{row.values === null ? t("claimMapping.changes.anyValue") : row.values.map((v) => <Ident key={v} value={v} />)}</dd>
    </dl>
  );
}

function EntryFacts({ row }: { row: EntryRow }) {
  const { t } = useI18n();
  return (
    <dl className={styles.changeFacts}>
      <dt>{t("claimMapping.visual.entry.reads")}</dt>
      <dd>
        <Ident value={row.external} />
      </dd>
      <dt>{t("claimMapping.visual.entry.group")}</dt>
      <dd>
        <Ident value={row.group} />
      </dd>
      <dt>{t("claimMapping.visual.entry.directories")}</dt>
      <dd>{row.directories === null ? t("claimMapping.changes.unscoped") : row.directories.map((d) => <Ident key={d} value={d} />)}</dd>
    </dl>
  );
}

function Optional({ value }: { value: string | undefined }) {
  const { t } = useI18n();
  return value === undefined ? <span className={styles.quiet}>{t("claimMapping.changes.unset")}</span> : <Ident value={value} />;
}

const SIDE_WORD: Readonly<Record<"added" | "removed" | "changed", MessageKey>> = {
  added: "claimMapping.changes.side.added",
  removed: "claimMapping.changes.side.removed",
  changed: "claimMapping.changes.side.changed",
};

const KIND_WORD: Readonly<Record<MappingChange["kind"], MessageKey>> = {
  description: "claimMapping.changes.kind.description",
  groupClaim: "claimMapping.changes.kind.groupClaim",
  requireDirectories: "claimMapping.changes.kind.requireDirectories",
  claimAdded: "claimMapping.changes.kind.claim",
  claimRemoved: "claimMapping.changes.kind.claim",
  claimChanged: "claimMapping.changes.kind.claim",
  claimsReordered: "claimMapping.changes.kind.claimsReordered",
  entryAdded: "claimMapping.changes.kind.entry",
  entryRemoved: "claimMapping.changes.kind.entry",
  entryChanged: "claimMapping.changes.kind.entry",
  unmodelled: "claimMapping.changes.kind.unmodelled",
};

/** One change, its kind said by a word and a sign and set on the diff's grounds (DESIGN.md §3.8), never by tint alone. */
function ChangeItem({ change }: { change: MappingChange }) {
  const { t } = useI18n();
  const side = change.kind.endsWith("Added") ? "added" : change.kind.endsWith("Removed") ? "removed" : "changed";
  const sign = side === "added" ? "+" : side === "removed" ? "\u2212" : "±";
  const body = (() => {
    switch (change.kind) {
      case "claimAdded":
      case "claimRemoved":
        return <ClaimFacts row={change.row} />;
      case "entryAdded":
      case "entryRemoved":
        return <EntryFacts row={change.row} />;
      case "claimChanged":
        return (
          <div className={styles.beforeAfter}>
            <ClaimFacts row={change.before} />
            <ClaimFacts row={change.after} />
          </div>
        );
      case "entryChanged":
        return (
          <div className={styles.beforeAfter}>
            <EntryFacts row={change.before} />
            <EntryFacts row={change.after} />
          </div>
        );
      case "description":
        return (
          <dl className={styles.changeFacts}>
            <dt>{t("claimMapping.changes.was")}</dt>
            <dd>
              <Optional value={change.before} />
            </dd>
            <dt>{t("claimMapping.changes.becomes")}</dt>
            <dd>
              <Optional value={change.after} />
            </dd>
          </dl>
        );
      case "groupClaim":
        return (
          <dl className={styles.changeFacts}>
            <dt>{t("claimMapping.changes.was")}</dt>
            <dd>
              <Optional value={change.before} />
            </dd>
            <dt>{t("claimMapping.changes.becomes")}</dt>
            <dd>
              <Optional value={change.after} />
            </dd>
          </dl>
        );
      case "requireDirectories":
        return <p className={styles.para}>{change.after ? t("claimMapping.requireDirectories.on") : t("claimMapping.requireDirectories.off")}</p>;
      case "claimsReordered":
        return <p className={styles.para}>{t("claimMapping.changes.reordered.body")}</p>;
      case "unmodelled":
        return <p className={styles.para}>{t("claimMapping.changes.unmodelled.body")}</p>;
    }
  })();
  return (
    <li className={[styles.change, styles[side]].join(" ")} data-change={change.kind}>
      <p className={styles.changeHead}>
        <span className={styles.mark} data-mark={side}>
          <span aria-hidden="true">{sign}</span> {t(SIDE_WORD[side])}
        </span>{" "}
        <span>{t(KIND_WORD[change.kind])}</span>
      </p>
      {body}
    </li>
  );
}

export function ChangeList({ changes }: { changes: readonly MappingChange[] }) {
  return (
    <ul className={styles.changes} data-changes={changes.length}>
      {changes.map((c, i) => (
        <ChangeItem key={`${c.kind}|${i}`} change={c} />
      ))}
    </ul>
  );
}

export function ChangesPanel({ changes, version, edited }: { changes: readonly MappingChange[]; version: number; edited: boolean }) {
  const { t } = useI18n();
  return (
    <section className={styles.panel} aria-labelledby="mapping-changes" data-mapping-changes={changes.length}>
      <h2 id="mapping-changes" className={styles.panelTitle}>
        {t("claimMapping.changes.title")}
      </h2>
      {!edited ? (
        <p className={styles.quiet}>{t("claimMapping.changes.none", { version })}</p>
      ) : (
        <>
          <p className={styles.quiet}>{t("claimMapping.changes.lede", { version })}</p>
          <ChangeList changes={changes} />
        </>
      )}
    </section>
  );
}
