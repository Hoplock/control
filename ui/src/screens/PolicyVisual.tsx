// THE VISUAL POLICY EDITOR (0066; PLAN M3, M4, M21, §5.3, §5.6). The
// candidate shown as what it is: rules in the order they are tried, each a
// match and an effect and what it routes to, with the default deny last and
// always there. It is the same candidate the source view edits — the source
// text is the one truth, and every edit here is the smallest change to that
// text that says it (policyDocument.ts), so an author can switch views at
// any time and a reviewer reads a diff of exactly what was changed.
//
// Every enumerated field is a choice from the server's vocabulary
// (policyVocabulary.ts); a label, a value and a group come from the bundle's
// own declarations, which the vocabulary panel edits; only what the schema
// leaves free — an id, a reason, a hostname, a network — is typed, and it is
// checked as the server checks it, with the server's codes. What the view
// does not model is shown read-only with "edit in source", never dropped.
//
// Every rejection and finding that names a rule sits on its card; the rest
// head the list. "Where would this land?" asks the candidate a dry-run
// question and marks the rule that answered, the terms that matched and the
// effect — or the default deny.

import { useEffect, useMemo, useState, type ReactNode } from "react";
import type { DryRunResult, PolicyReport, Rejection, Simulation } from "../api/types";
import { Button, TextButton } from "../components/Button";
import { Chip } from "../components/Badge";
import { ChipEditor } from "../components/ChipEditor";
import { Drawer } from "../components/Dialog";
import { Field, Input, Select, Switch } from "../components/Field";
import { Notice } from "../components/Notice";
import { SortableList, SortableListSkeleton } from "../components/SortableList";
import { EmptyState, ErrorMessage, PartialNotice, Skeleton, type ErrorKind } from "../components/States";
import { StatusOf } from "../components/Status";
import type { MessageKey } from "../i18n/catalogue";
import type { ApiError } from "../i18n/errors";
import { fromLocalInput, localInput } from "../i18n/format";
import { useI18n } from "../i18n/I18n";
import { useDeployment } from "../shell/Shell";
import { FindingCard, Ident, RejectionNote, Rejections } from "./PolicyParts";
import { rejectionText, simulationRefusalText } from "./policyCodes";
import {
  applyEdit,
  checkCidr,
  checkHostPattern,
  checkReason,
  checkRuleId,
  freshRuleId,
  parsePolicy,
  ruleComments,
  visualOf,
  type Edit,
  type EditRefusal,
  type FieldRefusal,
  type Path,
  type RuleView,
  type Value,
  type VisualBundle,
} from "./policyDocument";
import { inReportOrder } from "./policyModel";
import { chipsByAxis, effectStatus, notesByRule, routeSummary, traceMark, type Notes, type TraceMark } from "./policyVisualModel";
import { vocabulary } from "./policyVocabulary";
import styles from "./PolicyVisual.module.css";

// What each axis of a match is called on a card (PLAN §5.1).
const AXIS_LABEL: Readonly<Record<string, MessageKey>> = {
  subject: "policies.visual.axis.subject",
  device: "policies.visual.axis.device",
  context: "policies.visual.axis.context",
  target: "policies.visual.axis.target",
  grant: "policies.visual.axis.grant",
};

type Check = { state: "idle" | "busy" } | { state: "ok"; report: PolicyReport; source: string; correlationId?: string } | { state: "failed"; error: ApiError; kind: ErrorKind };

/** The candidate's report, asked again a moment after each edit, for the notes on the cards. */
function useCheck(source: string): Check {
  const { tenant, api } = useDeployment();
  const [check, setCheck] = useState<Check>({ state: "idle" });
  useEffect(() => {
    if (!tenant || source.trim() === "") {
      setCheck({ state: "idle" });
      return;
    }
    const ctl = new AbortController();
    setCheck((c) => (c.state === "ok" ? c : { state: "busy" }));
    const timer = window.setTimeout(async () => {
      const a = await api.call<PolicyReport>("policyValidate", { params: { tenant }, body: { source }, signal: ctl.signal });
      if (ctl.signal.aborted) return;
      setCheck(a.kind === "ok" ? { state: "ok", report: a.data, source, correlationId: a.correlationId } : { state: "failed", error: a.error, kind: a.kind });
    }, 400);
    return () => {
      ctl.abort();
      window.clearTimeout(timer);
    };
  }, [tenant, api, source]);
  return check;
}

/** A refusal from a check of the console's, said in the server's words for that code. */
function useRefusalText(): (r: FieldRefusal | null) => string | null {
  const { t } = useI18n();
  return (r) => (r ? rejectionText(t, { code: r.code, parameters: r.parameters, message: "" }).text : null);
}

export function VisualView({
  source,
  onSource,
  rule,
  onRule,
  goToSource,
}: {
  source: string;
  onSource(next: string): void;
  /** The rule whose editor is open (`?rule=`): its id, or `#<n>` for a rule with none. */
  rule: string | null;
  onRule(rule: string | null): void;
  /** Switches to the source view at a line. */
  goToSource(line?: number): void;
}) {
  const { t, f } = useI18n();
  const doc = useMemo(() => parsePolicy(source), [source]);
  const visual = useMemo(() => visualOf(doc), [doc]);
  const check = useCheck(source);
  const [said, setSaid] = useState("");
  const [refusal, setRefusal] = useState<EditRefusal | null>(null);
  const [trace, setTrace] = useState<{ result: DryRunResult; source: string } | null>(null);

  const commit = (e: Edit, announce: string): boolean => {
    const a = applyEdit(doc, e);
    if (a.kind === "refused") {
      setRefusal(a.reason);
      setSaid("");
      return false;
    }
    setRefusal(null);
    if (a.source !== source) onSource(a.source);
    setSaid(announce);
    return true;
  };

  const report = check.state === "ok" && check.source === source ? check.report : check.state === "ok" ? check.report : null;
  const stale = check.state === "ok" && check.source !== source;

  if (typeof visual === "string") {
    // A source that does not parse cannot be shown visually: its rejections, each at its line (0053).
    return (
      <section className={styles.panel} aria-labelledby="visual-off" data-visual="off" data-visual-reason={visual}>
        <h2 id="visual-off" className={styles.title}>
          {t("policies.visual.off.title")}
        </h2>
        <Notice tone="deny">
          <p className={styles.para}>{visual === "not_yaml" ? t("policies.visual.off.notYaml") : t("policies.visual.off.notMapping")}</p>
        </Notice>
        <div className={styles.actions}>
          <Button variant="primary" onClick={() => goToSource()} data-visual-to-source="">
            {t("policies.visual.toSource")}
          </Button>
        </div>
        {check.state === "ok" && !check.report.valid ? (
          <Rejections rejections={check.report.rejections} source={check.source} idPrefix="policy-visual-off" correlationId={check.correlationId} />
        ) : check.state === "busy" ? (
          <Skeleton shape="block" size="full" />
        ) : check.state === "failed" ? (
          <ErrorMessage error={check.error} kind={check.kind} />
        ) : null}
      </section>
    );
  }

  const notes = notesByRule(visual.rules, report && !report.valid ? report.rejections : [], report ? report.findings : []);
  const mark: TraceMark | null = trace && trace.source === source ? traceMark(visual.rules, trace.result) : null;
  const open = ruleNamed(visual.rules, rule);
  const ref = (r: RuleView) => (r.id ? r.id : `#${r.index}`);

  const addRule = () => {
    const id = freshRuleId(visual.rules);
    if (commit({ op: "addRule", id }, t("policies.visual.said.added", { id: `\u2068${id}\u2069`, position: f.number(visual.rules.length + 1) }))) {
      onRule(id);
    }
  };
  const removeRule = (r: RuleView) => {
    const comments = ruleComments(doc, r.index).length;
    commit({ op: "removeRule", index: r.index }, t("policies.visual.said.removed", { id: `\u2068${r.id}\u2069`, comments }));
    if (open?.index === r.index) onRule(null);
  };
  const move = (from: number, to: number) => {
    const moving = visual.rules[from];
    commit({ op: "move", from, to }, moving ? t("policies.visual.said.moved", { id: `\u2068${moving.id}\u2069`, position: f.number(to + 1) }) : "");
  };

  return (
    <div className={styles.layout} data-visual="on" data-visual-check={check.state === "ok" && !stale ? "ok" : check.state === "failed" ? "failed" : "busy"}>
      <section className={styles.rules} aria-labelledby="visual-rules">
        <div className={styles.head}>
          <h2 id="visual-rules" className={styles.title}>
            {t("policies.visual.rules.title")}
          </h2>
          {report ? <StatusOf name={report.valid ? "valid" : "invalid"} pill /> : null}
          <span className={styles.meta}>{t("policies.visual.rules.count", { count: visual.rules.length })}</span>
          <span className={styles.headActions}>
            <Button onClick={addRule} disabled={!visual.structural} data-visual-add="">
              {t("policies.visual.add")}
            </Button>
          </span>
        </div>
        <p className={styles.para}>{t("policies.visual.rules.lede")}</p>
        {check.state === "failed" ? <PartialNotice gaps={[t("policies.visual.partial.check")]} /> : null}
        {refusal ? (
          <Notice tone="warn" data-visual-refused={refusal}>
            <p className={styles.para}>{refusal === "comment_lost" ? t("policies.visual.refused.comment") : t("policies.visual.refused.structure")}</p>
            <TextButton onClick={() => goToSource()}>{t("policies.visual.toSource")}</TextButton>
          </Notice>
        ) : null}
        {!visual.structural && visual.rules.length > 0 ? (
          <Notice tone="info">
            <p className={styles.para}>{t("policies.visual.flowRules")}</p>
          </Notice>
        ) : null}
        <DocumentNotes notes={notes.document} correlationId={check.state === "ok" ? check.correlationId : undefined} unmodelled={visual.unmodelled} goToSource={goToSource} />
        {visual.rules.length === 0 ? (
          <EmptyState
            title={t("policies.visual.empty.title")}
            body={t("policies.visual.empty.body")}
            action={
              <Button variant="primary" onClick={addRule} disabled={!visual.structural && visual.rules.length > 0}>
                {t("policies.visual.add")}
              </Button>
            }
          />
        ) : null}
        <SortableList
          label={t("policies.visual.rules.label")}
          items={visual.rules}
          itemKey={(r, i) => keyOf(visual.rules, r, i)}
          itemName={(r) => r.id || t("policies.visual.unnamed")}
          disabled={!visual.structural}
          onMove={move}
          renderItem={(r) => (
            <RuleCard
              rule={r}
              notes={notes.byRule.get(r.index)}
              correlationId={check.state === "ok" ? check.correlationId : undefined}
              mark={mark}
              onEdit={() => onRule(ref(r))}
              onRemove={() => removeRule(r)}
              canRemove={visual.structural}
              goToSource={goToSource}
            />
          )}
          fixedEnd={<DefaultDenyCard marked={mark?.rule === "default"} />}
        />
        <p className={styles.said} role="status" aria-live="polite" data-visual-said="">
          {said}
        </p>
      </section>
      <div className={styles.side}>
        <TracePanel source={source} visual={visual} result={trace && trace.source === source ? trace.result : null} onResult={(result) => setTrace(result ? { result, source } : null)} />
        <VocabularyPanel visual={visual} commit={commit} />
      </div>
      <RuleEditor
        rule={open}
        visual={visual}
        notes={open ? notes.byRule.get(open.index) : undefined}
        commit={commit}
        onClose={() => onRule(null)}
        onRenamed={(id) => onRule(id)}
        goToSource={goToSource}
        refusal={refusal}
      />
    </div>
  );
}

/** The rule a `?rule=` names: by its id, or `#<n>` by its place. */
function ruleNamed(rules: readonly RuleView[], ref: string | null): RuleView | null {
  if (!ref) return null;
  if (ref.startsWith("#")) return rules[Number(ref.slice(1))] ?? null;
  return rules.find((r) => r.id === ref) ?? null;
}

/** A rule's key in the list: its id, told apart by its place among rules sharing it. */
function keyOf(rules: readonly RuleView[], r: RuleView, i: number): string {
  const before = rules.slice(0, i).filter((x) => x.id === r.id).length;
  return `${r.id}#${before}`;
}

/** The visual view's loading state: the rules' shape, so nothing shifts. */
export function VisualSkeleton() {
  return (
    <div className={styles.layout} aria-hidden="true" data-visual="loading">
      <div className={styles.rules}>
        <Skeleton shape="title" size="m" />
        <SortableListSkeleton rows={3} />
      </div>
      <div className={styles.side}>
        <Skeleton shape="block" size="full" />
      </div>
    </div>
  );
}

function DocumentNotes({
  notes,
  correlationId,
  unmodelled,
  goToSource,
}: {
  notes: Notes;
  correlationId?: string;
  unmodelled: VisualBundle["unmodelled"];
  goToSource(line?: number): void;
}) {
  const { t } = useI18n();
  if (notes.rejections.length === 0 && notes.findings.length === 0 && unmodelled.length === 0) return null;
  return (
    <div className={styles.docNotes} data-visual-document-notes="">
      {notes.rejections.map((r, i) => (
        <RejectionNote key={`r${i}`} rejection={r} correlationId={correlationId} />
      ))}
      {inReportOrder(notes.findings).map((fd) => (
        <FindingCard key={fd.id} finding={fd} correlationId={correlationId} onLine={(line) => goToSource(line)} />
      ))}
      {unmodelled.length ? <Unmodelled items={unmodelled} goToSource={goToSource} /> : null}
    </div>
  );
}

/** What the view does not model: shown, read-only, each with a way to its line in the source. */
function Unmodelled({ items, goToSource }: { items: readonly { path: string; line: number }[]; goToSource(line?: number): void }) {
  const { t } = useI18n();
  return (
    <div className={styles.unmodelled} data-visual-unmodelled={items.length}>
      <p className={styles.meta}>{t("policies.visual.unmodelled")}</p>
      <ul className={styles.chipRow}>
        {items.map((u) => (
          <li key={`${u.path}:${u.line}`} className={styles.unmodelledItem}>
            <Ident>{u.path}</Ident>
            <TextButton onClick={() => goToSource(u.line)} data-visual-edit-source={u.path}>
              {t("policies.visual.editInSource", { line: u.line })}
            </TextButton>
          </li>
        ))}
      </ul>
    </div>
  );
}

function RuleCard({
  rule: r,
  notes,
  correlationId,
  mark,
  onEdit,
  onRemove,
  canRemove,
  goToSource,
}: {
  rule: RuleView;
  notes?: Notes;
  correlationId?: string;
  mark: TraceMark | null;
  onEdit(): void;
  onRemove(): void;
  canRemove: boolean;
  goToSource(line?: number): void;
}) {
  const { t } = useI18n();
  const axes = chipsByAxis(r.match);
  const marked = mark?.rule === r.index;
  const route = routeSummary(r);
  const worst = notes?.rejections.length ? "invalid" : notes?.findings.some((x) => x.severity === "blocking") ? "blocking" : undefined;
  return (
    <article
      className={styles.card}
      data-rule-card={r.id}
      data-rule-index={r.index}
      data-trace={marked ? "matched" : undefined}
      data-notes={worst}
      aria-labelledby={`rule-card-${r.index}`}
    >
      <header className={styles.cardHead}>
        <h3 id={`rule-card-${r.index}`} className={styles.cardTitle}>
          <TextButton onClick={onEdit} data-rule-open={r.id}>
            <span dir="ltr" translate="no" className={styles.ruleId}>
              {r.id || t("policies.visual.unnamed")}
            </span>
          </TextButton>
        </h3>
        <StatusOf name={effectStatus(r.effect)} pill />
        {marked ? <StatusOf name="inProgress" /> : null}
        <span className={styles.headActions}>
          <Button variant="ghost" onClick={onEdit} data-rule-edit={r.id}>
            {t("policies.visual.edit")}
          </Button>
          <Button variant="ghost" onClick={onRemove} disabled={!canRemove} data-rule-remove={r.id}>
            {t("policies.visual.remove")}
          </Button>
        </span>
      </header>
      {marked ? (
        <p className={styles.traced} data-trace-here="">
          {t("policies.visual.trace.here")}
        </p>
      ) : null}
      {r.description ? (
        <p className={styles.description} translate="no">
          {r.description}
        </p>
      ) : null}
      <dl className={styles.facts}>
        <div className={styles.fact}>
          <dt>{t("policies.visual.match")}</dt>
          <dd>
            {axes.length === 0 ? (
              <span className={styles.meta} data-match-everything="">
                {t("policies.visual.matchEverything")}
              </span>
            ) : (
              <div className={styles.axes}>
                {axes.map((a) => (
                  <div key={a.axis} className={styles.axis} data-axis={a.axis}>
                    <span className={styles.axisName}>{AXIS_LABEL[a.axis] ? t(AXIS_LABEL[a.axis] as MessageKey) : a.axis}</span>
                    <ul className={styles.chipRow}>
                      {a.chips.map((c) => (
                        <li key={`${c.term}=${c.value}`} data-term={`${c.term}=${c.value}`} data-matched={mark && marked && mark.matched.has(c.key) ? "" : undefined} className={styles.term}>
                          <Chip data>{`${c.term}=${c.value}`}</Chip>
                        </li>
                      ))}
                    </ul>
                  </div>
                ))}
              </div>
            )}
          </dd>
        </div>
        {r.effect === "deny" ? (
          <div className={styles.fact}>
            <dt>{t("policies.visual.reason")}</dt>
            <dd translate="no">{r.reason || <span className={styles.meta}>{t("policies.visual.noReason")}</span>}</dd>
          </div>
        ) : (
          <div className={styles.fact}>
            <dt>{t("policies.visual.route")}</dt>
            <dd data-route-summary="">
              {route.length ? (
                <code className={styles.route} dir="ltr" translate="no">
                  {route.join(" · ")}
                </code>
              ) : (
                <span className={styles.meta}>{t("policies.visual.noRoute")}</span>
              )}
            </dd>
          </div>
        )}
        {r.obligations.length ? (
          <div className={styles.fact}>
            <dt>{t("policies.visual.obligations")}</dt>
            <dd>
              <ul className={styles.chipRow}>
                {r.obligations.map((o) => (
                  <li key={o}>
                    <Chip data>{o}</Chip>
                  </li>
                ))}
              </ul>
            </dd>
          </div>
        ) : null}
        {r.cache ? (
          <div className={styles.fact}>
            <dt>{t("policies.visual.cache")}</dt>
            <dd>
              <code className={styles.route} dir="ltr" translate="no">
                {r.cache.key.join(", ")}
              </code>{" "}
              {r.cache.ttl !== undefined ? <span className={styles.meta}>{t("policies.visual.ttl", { seconds: r.cache.ttl })}</span> : null}
            </dd>
          </div>
        ) : null}
      </dl>
      {r.unmodelled.length ? <Unmodelled items={r.unmodelled} goToSource={goToSource} /> : null}
      {notes && (notes.rejections.length || notes.findings.length) ? (
        <div className={styles.cardNotes} data-card-notes="">
          {notes.rejections.map((x, i) => (
            <RejectionNote key={`r${i}`} rejection={x} correlationId={correlationId} />
          ))}
          {inReportOrder(notes.findings).map((fd) => (
            <FindingCard key={fd.id} finding={fd} correlationId={correlationId} onLine={(line) => goToSource(line)} />
          ))}
        </div>
      ) : null}
    </article>
  );
}

function DefaultDenyCard({ marked }: { marked: boolean }) {
  const { t } = useI18n();
  return (
    <article className={styles.card} data-default-deny="" data-trace={marked ? "matched" : undefined} aria-labelledby="rule-card-default">
      <header className={styles.cardHead}>
        <h3 id="rule-card-default" className={styles.cardTitle}>
          {t("policies.visual.defaultDeny.title")}
        </h3>
        <StatusOf name="denied" pill />
        {marked ? <StatusOf name="inProgress" /> : null}
      </header>
      {marked ? (
        <p className={styles.traced} data-trace-here="">
          {t("policies.visual.trace.here")}
        </p>
      ) : null}
      <p className={styles.para}>{t("policies.visual.defaultDeny.body")}</p>
    </article>
  );
}

// ---------------------------------------------------------------------------
// The rule editor: a drawer, every field a closed choice or checked text.
// ---------------------------------------------------------------------------

type Commit = (e: Edit, announce: string) => boolean;

/** Typed text, applied when the author leaves the field or presses Enter, so a half-typed value changes nothing. */
function TextField({
  label,
  value,
  onApply,
  check,
  error,
  mono = false,
  type = "text",
  name,
}: {
  label: string;
  value: string;
  onApply(v: string): void;
  check?(v: string): string | null;
  error?: string | null;
  mono?: boolean;
  type?: "text" | "number";
  name: string;
}) {
  const [draft, setDraft] = useState(value);
  const [refused, setRefused] = useState<string | null>(null);
  useEffect(() => setDraft(value), [value]);
  const apply = () => {
    if (draft === value) return;
    const r = check?.(draft) ?? null;
    setRefused(r);
    if (!r) onApply(draft);
  };
  return (
    <Field label={label} error={refused ?? error ?? undefined}>
      <Input
        mono={mono}
        type={type}
        value={draft}
        onChange={(e) => {
          setDraft(e.target.value);
          setRefused(null);
        }}
        onBlur={apply}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            apply();
          }
        }}
        translate={mono ? "no" : undefined}
        data-visual-field={name}
      />
    </Field>
  );
}

/** One member of a closed vocabulary, or none where the schema lets the key be left out. */
function Choice({
  label,
  value,
  options,
  onChange,
  unset,
  name,
  error,
}: {
  label: string;
  value: string | undefined;
  options: readonly string[];
  onChange(v: string | undefined): void;
  /** The words for leaving the key out, where that is allowed. */
  unset?: string;
  name: string;
  error?: string | null;
}) {
  const known = value === undefined || options.includes(value);
  return (
    <Field label={label} error={error ?? undefined}>
      <Select value={value ?? ""} onChange={(e) => onChange(e.target.value === "" ? undefined : e.target.value)} data-visual-field={name}>
        {unset !== undefined || value === undefined ? <option value="">{unset ?? ""}</option> : null}
        {!known ? (
          <option value={value} translate="no">
            {value}
          </option>
        ) : null}
        {options.map((o) => (
          <option key={o} value={o} translate="no">
            {o}
          </option>
        ))}
      </Select>
    </Field>
  );
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className={styles.section}>
      <h3 className={styles.sectionTitle}>{title}</h3>
      {children}
    </section>
  );
}

/** The rejections the server reported for a rule, by code, for the field they are about. */
function codeError(notes: Notes | undefined, codes: readonly string[], t: ReturnType<typeof useI18n>["t"]): string | null {
  const r: Rejection | undefined = notes?.rejections.find((x) => codes.includes(x.code));
  return r ? rejectionText(t, r).text : null;
}

function RuleEditor({
  rule: r,
  visual,
  notes,
  commit,
  onClose,
  onRenamed,
  goToSource,
  refusal,
}: {
  rule: RuleView | null;
  visual: VisualBundle;
  notes?: Notes;
  commit: Commit;
  onClose(): void;
  /** The rule's id changed: the URL follows it. */
  onRenamed(id: string): void;
  goToSource(line?: number): void;
  refusal: EditRefusal | null;
}) {
  const { t } = useI18n();
  const said = useRefusalText();
  if (!r) return <Drawer open={false} title="" onClose={onClose}>{null}</Drawer>;
  const i = r.index;
  const name = `\u2068${r.id}\u2069`;
  const set = (path: Path, value: Value | undefined, field: string) =>
    commit({ op: "set", path: ["rules", i, ...path], value }, t("policies.visual.said.set", { field, id: name }));
  const terms = (axis: string, term: string) => r.match.find((m) => m.axis === axis && m.term === term)?.values ?? [];
  const list = (axis: string, term: string, label: string, opts: { options?: readonly string[]; check?(v: string): FieldRefusal | null; codes?: readonly string[] }) => (
    <ChipEditor
      name={`${axis}.${term}`}
      label={label}
      values={terms(axis, term)}
      options={opts.options}
      check={opts.check ? (v) => said(opts.check?.(v) ?? null) : undefined}
      error={opts.codes ? codeError(notes, opts.codes, t) : undefined}
      onChange={(vs) => set(["match", axis, ...term.split(".")], vs, label)}
    />
  );
  const route = r.route;
  const allow = r.effect === "allow";
  return (
    <Drawer
      open
      title={
        <span className={styles.ruleId} dir="ltr" translate="no">
          {r.id || t("policies.visual.unnamed")}
        </span>
      }
      onClose={onClose}
    >
      <div className={styles.editor} data-rule-editor={r.id}>
        {refusal ? (
          <Notice tone="warn" data-visual-refused={refusal}>
            <p className={styles.para}>{refusal === "comment_lost" ? t("policies.visual.refused.comment") : t("policies.visual.refused.structure")}</p>
            <TextButton onClick={() => goToSource(r.line)}>{t("policies.visual.toSource")}</TextButton>
          </Notice>
        ) : null}
        <Section title={t("policies.visual.editor.rule")}>
          <TextField
            name="id"
            label={t("policies.visual.field.id")}
            value={r.id}
            mono
            check={(v) => said(checkRuleId(v, visual.rules, i))}
            error={codeError(notes, ["rule.id_missing", "rule.id_duplicate"], t)}
            onApply={(v) => {
              if (set(["id"], v, t("policies.visual.field.id"))) onRenamed(v);
            }}
          />
          <TextField
            name="description"
            label={t("policies.visual.field.description")}
            value={r.description ?? ""}
            onApply={(v) => set(["description"], v === "" ? undefined : v, t("policies.visual.field.description"))}
          />
          <Choice
            name="effect"
            label={t("policies.visual.field.effect")}
            value={r.effect}
            options={vocabulary("effect")}
            error={codeError(notes, ["rule.effect_missing", "rule.deny_carries_route", "rule.allow_needs_route"], t)}
            onChange={(v) => set(["effect"], v, t("policies.visual.field.effect"))}
          />
          {r.effect === "deny" ? (
            <TextField
              name="reason"
              label={t("policies.visual.field.reason")}
              value={r.reason ?? ""}
              check={(v) => said(checkReason(v))}
              error={codeError(notes, ["rule.deny_needs_reason"], t)}
              onApply={(v) => set(["reason"], v, t("policies.visual.field.reason"))}
            />
          ) : null}
        </Section>
        <Section title={t("policies.visual.axis.subject")}>
          {list("subject", "ids", t("policies.visual.field.ids"), {})}
          {list("subject", "sources", t("policies.visual.field.sources"), {})}
          {list("subject", "groups", t("policies.visual.field.groups"), { options: visual.groups, codes: ["match.unknown_group"] })}
          {list("subject", "auth_methods", t("policies.visual.field.authMethods"), { options: vocabulary("auth_method") })}
          <Switch
            label={t("policies.visual.field.mfa")}
            checked={terms("subject", "mfa")[0] === "true"}
            onChange={(e) => set(["match", "subject", "mfa"], e.target.checked ? true : undefined, t("policies.visual.field.mfa"))}
            data-visual-field="mfa"
          />
        </Section>
        <Section title={t("policies.visual.axis.context")}>
          {list("context", "days", t("policies.visual.field.days"), { options: vocabulary("day") })}
          {list("context", "source_cidrs", t("policies.visual.field.cidrs"), { check: checkCidr, codes: ["match.invalid_cidr"] })}
          {list("context", "proxy_ids", t("policies.visual.field.proxyIds"), {})}
        </Section>
        <Section title={t("policies.visual.axis.target")}>
          {list("target", "hostnames", t("policies.visual.field.hostnames"), { check: checkHostPattern, codes: ["match.invalid_hostname_pattern"] })}
          {visual.labels.map((l) =>
            list("target", `labels.${l.key}`, t("policies.visual.field.label", { key: `\u2068${l.key}\u2069` }), {
              options: l.values,
              codes: ["match.unknown_label_value"],
            }),
          )}
          {list("target", "zones", t("policies.visual.field.zones"), {})}
        </Section>
        <Section title={t("policies.visual.axis.grant")}>
          <Switch
            label={t("policies.visual.field.grantRequired")}
            checked={terms("grant", "required")[0] === "true"}
            onChange={(e) => set(["match", "grant", "required"], e.target.checked ? true : undefined, t("policies.visual.field.grantRequired"))}
            data-visual-field="grant-required"
          />
          {list("grant", "scopes", t("policies.visual.field.scopes"), { options: visual.scopes })}
          {list("grant", "origins", t("policies.visual.field.origins"), { options: vocabulary("grant_origin") })}
        </Section>
        {allow ? (
          <Section title={t("policies.visual.route")}>
            <Choice
              name="intent"
              label={t("policies.visual.field.intent")}
              value={route?.intent}
              options={vocabulary("route_intent")}
              unset={t("policies.visual.notSet")}
              error={codeError(notes, ["route.intent_missing"], t)}
              onChange={(v) => set(["route", "intent"], v, t("policies.visual.field.intent"))}
            />
            <TextField
              name="route-target"
              label={t("policies.visual.field.routeTarget")}
              value={route?.target ?? ""}
              mono
              onApply={(v) => set(["route", "target"], v === "" ? undefined : v, t("policies.visual.field.routeTarget"))}
            />
            <ChipEditor
              name="route.channels"
              label={t("policies.visual.field.channels")}
              values={route?.channels ?? []}
              options={vocabulary("channel_type")}
              error={codeError(notes, ["route.channels_missing", "route.forward_destinations_missing"], t)}
              onChange={(vs) => set(["route", "channels"], vs, t("policies.visual.field.channels"))}
            />
            <ChipEditor
              name="route.requests.types"
              label={t("policies.visual.field.requestTypes")}
              values={route?.requestTypes ?? []}
              options={vocabulary("request_type")}
              onChange={(vs) => set(["route", "requests", "types"], vs, t("policies.visual.field.requestTypes"))}
            />
            <Choice
              name="filter-mode"
              label={t("policies.visual.field.filterMode")}
              value={route?.filterMode}
              options={vocabulary("filter_mode")}
              unset={t("policies.visual.notSet")}
              error={codeError(notes, ["route.filter_mode_missing"], t)}
              onChange={(v) => set(["route", "filter", "mode"], v, t("policies.visual.field.filterMode"))}
            />
            <Choice
              name="exec-mode"
              label={t("policies.visual.field.execMode")}
              value={route?.execMode}
              options={vocabulary("exec_mode")}
              unset={t("policies.visual.notSet")}
              onChange={(v) => set(["route", "filter", "exec_mode"], v, t("policies.visual.field.execMode"))}
            />
            <Ladder rule={r} notes={notes} commit={commit} />
            <Choice
              name="execution"
              label={t("policies.visual.field.execution")}
              value={route?.execution}
              options={vocabulary("execution_rung")}
              unset={t("policies.visual.notSet")}
              onChange={(v) => set(["route", "enforcement", "execution"], v, t("policies.visual.field.execution"))}
            />
            <Choice
              name="reach"
              label={t("policies.visual.field.reach")}
              value={route?.reach}
              options={vocabulary("reach_rung")}
              unset={t("policies.visual.notSet")}
              onChange={(v) => set(["route", "enforcement", "reach"], v, t("policies.visual.field.reach"))}
            />
            <Choice
              name="algorithm-profile"
              label={t("policies.visual.field.algorithmProfile")}
              value={route?.algorithmProfile}
              options={vocabulary("algorithm_profile")}
              unset={t("policies.visual.notSet")}
              onChange={(v) => set(["route", "algorithm_profile"], v, t("policies.visual.field.algorithmProfile"))}
            />
            <Choice
              name="algorithm-floor"
              label={t("policies.visual.field.algorithmFloor")}
              value={route?.algorithmFloor}
              options={vocabulary("algorithm_floor")}
              unset={t("policies.visual.notSet")}
              onChange={(v) => set(["route", "algorithm_floor"], v, t("policies.visual.field.algorithmFloor"))}
            />
            <Switch
              label={t("policies.visual.field.capture")}
              checked={route?.requireSessionCapture === true}
              onChange={(e) => set(["route", "require_session_capture"], e.target.checked ? true : undefined, t("policies.visual.field.capture"))}
              data-visual-field="capture"
            />
          </Section>
        ) : null}
        <Section title={t("policies.visual.obligations")}>
          <ChipEditor
            name="obligations"
            label={t("policies.visual.field.obligations")}
            values={r.obligations}
            options={vocabulary("obligation_kind")}
            error={codeError(notes, ["obligation.on_deny", "obligation.duplicate", "obligation.contradicts_route"], t)}
            onChange={(kinds) => commit({ op: "obligations", rule: i, kinds }, t("policies.visual.said.set", { field: t("policies.visual.field.obligations"), id: name }))}
          />
        </Section>
        <Section title={t("policies.visual.cache")}>
          <ChipEditor
            name="cache.key"
            label={t("policies.visual.field.cacheKey")}
            values={r.cache?.key ?? []}
            options={vocabulary("cache_key_component")}
            error={codeError(notes, ["cache.key_empty", "cache.key_not_identity_bound", "cache.key_component_duplicate", "cache.hint_on_deny"], t)}
            onChange={(vs) => set(["cache", "key"], vs, t("policies.visual.field.cacheKey"))}
          />
          <TextField
            name="cache-ttl"
            type="number"
            label={t("policies.visual.field.ttl")}
            value={r.cache?.ttl !== undefined ? String(r.cache.ttl) : ""}
            error={codeError(notes, ["cache.ttl_invalid"], t)}
            onApply={(v) => set(["cache", "ttl_seconds"], v === "" ? undefined : Number(v), t("policies.visual.field.ttl"))}
          />
        </Section>
        {r.unmodelled.length ? (
          <Section title={t("policies.visual.editor.unmodelled")}>
            <Unmodelled items={r.unmodelled} goToSource={goToSource} />
          </Section>
        ) : null}
        <div className={styles.actions}>
          <Button onClick={() => goToSource(r.line)} data-visual-rule-source="">
            {t("policies.visual.ruleInSource")}
          </Button>
          <Button variant="primary" onClick={onClose} data-visual-done="">
            {t("policies.visual.editor.done")}
          </Button>
        </div>
      </div>
    </Drawer>
  );
}

/** A route's credential ladder: each entry's method and username, in the order the proxy tries them. */
function Ladder({ rule: r, notes, commit }: { rule: RuleView; notes?: Notes; commit: Commit }) {
  const { t } = useI18n();
  const entries = r.route?.credentials ?? [];
  const name = `\u2068${r.id}\u2069`;
  const field = t("policies.visual.field.ladder");
  const announce = t("policies.visual.said.set", { field, id: name });
  return (
    <fieldset className={styles.ladder} data-visual-ladder={entries.length}>
      <legend className={styles.sectionTitle}>{field}</legend>
      {entries.length === 0 ? <p className={styles.meta}>{t("component.chips.none")}</p> : null}
      {entries.map((c, k) => (
        <div key={k} className={styles.entry} data-ladder-entry={k}>
          <Choice
            name={`ladder-${k}-method`}
            label={t("policies.visual.field.method", { n: k + 1 })}
            value={c.method}
            options={vocabulary("credential_method")}
            onChange={(v) => commit({ op: "set", path: ["rules", r.index, "route", "credentials", k, "method"], value: v }, announce)}
          />
          <Choice
            name={`ladder-${k}-username`}
            label={t("policies.visual.field.username")}
            value={c.usernameSource}
            options={vocabulary("username_source")}
            unset={t("policies.visual.notSet")}
            onChange={(v) => v && commit({ op: "username", rule: r.index, entry: k, source: v, literal: c.usernameLiteral ?? "" }, announce)}
          />
          {c.usernameSource === "literal" ? (
            <TextField
              name={`ladder-${k}-literal`}
              label={t("policies.visual.field.literal")}
              value={c.usernameLiteral ?? ""}
              mono
              onApply={(v) => commit({ op: "username", rule: r.index, entry: k, source: "literal", literal: v }, announce)}
            />
          ) : null}
          {c.other.length ? (
            <p className={styles.meta}>
              {t("policies.visual.ladder.other")} <Ident>{c.other.join(", ")}</Ident>
            </p>
          ) : null}
          <Button variant="ghost" onClick={() => commit({ op: "removeCredential", rule: r.index, entry: k }, announce)} data-ladder-remove={k}>
            {t("policies.visual.ladder.remove")}
          </Button>
        </div>
      ))}
      <Choice
        name="ladder-add"
        label={t("policies.visual.ladder.add")}
        value={undefined}
        options={vocabulary("credential_method")}
        unset={t("component.chips.choose")}
        error={codeError(notes, ["route.ladder_empty", "credential.username_missing", "credential.username_empty", "credential.method_missing"], t)}
        onChange={(v) => v && commit({ op: "addCredential", rule: r.index, method: v }, announce)}
      />
    </fieldset>
  );
}

// ---------------------------------------------------------------------------
// The bundle's own vocabulary: its labels and groups.
// ---------------------------------------------------------------------------

function VocabularyPanel({ visual, commit }: { visual: VisualBundle; commit: Commit }) {
  const { t } = useI18n();
  const [key, setKey] = useState("");
  const [first, setFirst] = useState("");
  const [keyError, setKeyError] = useState<string | null>(null);
  const announce = (field: string) => t("policies.visual.said.vocabulary", { field: `\u2068${field}\u2069` });
  const addKey = () => {
    const k = key.trim();
    const v = first.trim();
    if (k === "" || v === "") return;
    if (visual.labels.some((l) => l.key === k)) {
      setKeyError(t("component.chips.duplicate", { value: `\u2068${k}\u2069` }));
      return;
    }
    // A key is declared with the values it permits, so it is added with its first.
    if (commit({ op: "set", path: ["labels", k], value: [v] }, announce(k))) {
      setKey("");
      setFirst("");
      setKeyError(null);
    }
  };
  return (
    <section className={styles.panel} aria-labelledby="visual-vocabulary" data-visual-vocabulary="">
      <h2 id="visual-vocabulary" className={styles.title}>
        {t("policies.visual.vocabulary.title")}
      </h2>
      <p className={styles.para}>{t("policies.visual.vocabulary.lede")}</p>
      <ChipEditor
        name="groups"
        label={t("policies.visual.vocabulary.groups")}
        values={visual.groups}
        onChange={(vs) => commit({ op: "set", path: ["groups"], value: vs }, announce(t("policies.visual.vocabulary.groups")))}
      />
      <h3 className={styles.sectionTitle}>{t("policies.visual.vocabulary.labels")}</h3>
      {visual.labels.length === 0 ? <p className={styles.meta}>{t("component.chips.none")}</p> : null}
      {visual.labels.map((l) => (
        <div key={l.key} className={styles.labelDecl} data-label-decl={l.key}>
          <ChipEditor
            name={`labels.${l.key}`}
            label={t("policies.visual.field.label", { key: `\u2068${l.key}\u2069` })}
            values={l.values}
            onChange={(vs) => commit({ op: "set", path: ["labels", l.key], value: vs }, announce(l.key))}
          />
        </div>
      ))}
      <div className={styles.addKey}>
        <Field label={t("policies.visual.vocabulary.newKey")} error={keyError ?? undefined}>
          <Input
            mono
            value={key}
            onChange={(e) => {
              setKey(e.target.value);
              setKeyError(null);
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                addKey();
              }
            }}
            data-visual-new-key=""
          />
        </Field>
        <Field label={t("policies.visual.vocabulary.firstValue")}>
          <Input
            mono
            value={first}
            onChange={(e) => setFirst(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                addKey();
              }
            }}
            data-visual-new-value=""
          />
        </Field>
        <Button onClick={addKey} disabled={key.trim() === "" || first.trim() === ""} data-visual-add-key="">
          {t("policies.visual.vocabulary.addKey")}
        </Button>
      </div>
      {visual.scopes.length ? (
        <>
          <h3 className={styles.sectionTitle}>{t("policies.visual.vocabulary.scopes")}</h3>
          <ul className={styles.chipRow}>
            {visual.scopes.map((s) => (
              <li key={s}>
                <Chip data>{s}</Chip>
              </li>
            ))}
          </ul>
          <p className={styles.meta}>{t("policies.visual.vocabulary.scopesInSource")}</p>
        </>
      ) : null}
    </section>
  );
}

// ---------------------------------------------------------------------------
// "Where would this land?": a dry-run question to the candidate.
// ---------------------------------------------------------------------------

type Run = { state: "idle" | "busy" } | { state: "failed"; error: ApiError; kind: ErrorKind };

function TracePanel({
  source,
  visual,
  result,
  onResult,
}: {
  source: string;
  visual: VisualBundle;
  result: DryRunResult | null;
  onResult(r: DryRunResult | null): void;
}) {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const [subject, setSubject] = useState("");
  const [groups, setGroups] = useState<string[]>([]);
  const [hostname, setHostname] = useState("");
  const [labels, setLabels] = useState<Record<string, string>>({});
  const [zone, setZone] = useState("");
  const [at, setAt] = useState(() => localInput(new Date()));
  const [run, setRun] = useState<Run>({ state: "idle" });

  const go = async () => {
    const now = fromLocalInput(at);
    setRun({ state: "busy" });
    const chosen = Object.fromEntries(Object.entries(labels).filter(([, v]) => v !== ""));
    const a = await api.call<Simulation>("policySimulate", {
      params: { tenant: tenant as string },
      body: {
        source,
        cases: [
          {
            name: "trace",
            now,
            subject: { id: subject, groups },
            target: { hostname, labels: chosen, ...(zone ? { zone } : {}) },
            context: {},
          },
        ],
      },
    });
    if (a.kind === "ok" && a.data.dry_run[0]) {
      setRun({ state: "idle" });
      onResult(a.data.dry_run[0]);
      return;
    }
    onResult(null);
    setRun(a.kind === "ok" ? { state: "failed", error: { code: "internal", correlation_id: a.correlationId }, kind: "outage" } : { state: "failed", error: a.error, kind: a.kind });
  };

  const landed = result ? (result.rule ? visual.rules.find((r) => r.id === result.rule) : undefined) : undefined;
  return (
    <section className={styles.panel} aria-labelledby="visual-trace" data-visual-trace={result ? (result.rule ? "rule" : "default") : run.state}>
      <h2 id="visual-trace" className={styles.title}>
        {t("policies.visual.trace.title")}
      </h2>
      <p className={styles.para}>{t("policies.visual.trace.lede")}</p>
      <form
        className={styles.traceForm}
        onSubmit={(e) => {
          e.preventDefault();
          void go();
        }}
      >
        <Field label={t("policies.visual.trace.subject")}>
          <Input mono value={subject} onChange={(e) => setSubject(e.target.value)} data-trace-field="subject" />
        </Field>
        <ChipEditor name="trace.groups" label={t("policies.visual.trace.groups")} values={groups} options={visual.groups} onChange={setGroups} />
        <Field label={t("policies.visual.trace.hostname")}>
          <Input mono value={hostname} onChange={(e) => setHostname(e.target.value)} data-trace-field="hostname" />
        </Field>
        {visual.labels.map((l) => (
          <Field key={l.key} label={t("policies.visual.field.label", { key: `\u2068${l.key}\u2069` })}>
            <Select value={labels[l.key] ?? ""} onChange={(e) => setLabels({ ...labels, [l.key]: e.target.value })} data-trace-label={l.key}>
              <option value="">{t("policies.visual.trace.noLabel")}</option>
              {l.values.map((v) => (
                <option key={v} value={v} translate="no">
                  {v}
                </option>
              ))}
            </Select>
          </Field>
        ))}
        <Field label={t("policies.visual.trace.zone")}>
          <Input mono value={zone} onChange={(e) => setZone(e.target.value)} data-trace-field="zone" />
        </Field>
        <Field label={t("policies.visual.trace.at")} hint={t("policies.visual.trace.atHint")}>
          <Input type="datetime-local" value={at} onChange={(e) => setAt(e.target.value)} data-trace-field="at" data-volatile="" />
        </Field>
        <div className={styles.actions}>
          <Button type="submit" variant="primary" loading={run.state === "busy"} disabled={hostname.trim() === "" || subject.trim() === ""} data-trace-run="">
            {t("policies.visual.trace.run")}
          </Button>
        </div>
      </form>
      {run.state === "failed" ? <ErrorMessage error={run.error} kind={run.kind} text={simulationRefusalText(t, run.error)} /> : null}
      <div role="status" aria-live="polite" data-trace-result={result ? result.effect : undefined}>
        {result ? (
          <div className={styles.traceResult}>
            <StatusOf name={effectStatus(result.effect)} pill />
            <p className={styles.para}>
              {result.rule
                ? t("policies.visual.trace.landed", { id: `\u2068${result.rule}\u2069`, line: result.rule_line ?? 0 })
                : t("policies.visual.trace.default")}
            </p>
            {result.terms?.length ? (
              <>
                <p className={styles.meta}>{t("policies.visual.trace.terms")}</p>
                <ul className={styles.chipRow}>
                  {result.terms.map((x) => (
                    <li key={x}>
                      <Chip data>{x}</Chip>
                    </li>
                  ))}
                </ul>
              </>
            ) : null}
            {result.obligations?.length ? (
              <p className={styles.meta}>
                {t("policies.visual.obligations")} <Ident>{result.obligations.join(", ")}</Ident>
              </p>
            ) : null}
            {result.rule && !landed ? <p className={styles.meta}>{t("policies.visual.trace.moved")}</p> : null}
          </div>
        ) : null}
      </div>
    </section>
  );
}
