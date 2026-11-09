// The gallery's sections. Each row is one component; each cell one state.
// A cell whose state is a pseudo-class (hover, focus-visible, active) marks
// its element data-force="<state>", and the E2E run forces that state on it
// through the browser's developer protocol, so every state is visible at
// once in one screenshot without a stylesheet that fakes it.

import { useState, type ReactNode } from "react";
import type { ApiError } from "../src/i18n/errors";
import { Avatar, Badge, Breadcrumb, Chip, KeyboardHint } from "../src/components/Badge";
import { Button, ButtonLink, IconButton } from "../src/components/Button";
import { CodeBlock, JsonViewer, SourceView } from "../src/components/Code";
import { Combobox, CommandPalette } from "../src/components/Combobox";
import { CopyableId } from "../src/components/CopyableId";
import { DataTable, TableSkeleton, type Column } from "../src/components/DataTable";
import { ConfirmDialog, Dialog, Drawer } from "../src/components/Dialog";
import { DiffSkeleton, DiffView } from "../src/components/Diff";
import { Checkbox, Choices, Field, Input, Radio, Select, Switch, Textarea } from "../src/components/Field";
import { FilterBar } from "../src/components/FilterBar";
import { Menu, Tooltip } from "../src/components/Menu";
import { PageHeader } from "../src/components/PageHeader";
import { Pagination } from "../src/components/Pagination";
import { EmptyState, ErrorMessage, ErrorState, PartialNotice, Skeleton, SpinnerOverlay } from "../src/components/States";
import { STATUS, Status, StatusGlyph, StatusOf, type StatusName, type Tone } from "../src/components/Status";
import { Tabs } from "../src/components/Tabs";
import { ToastRegion } from "../src/components/Toast";
import { TimeRangePicker } from "../src/components/TimeRange";
import { Timeline, TimelineSkeleton, type TimelineEvent } from "../src/components/Timeline";
import { Tree } from "../src/components/Tree";
import { useI18n } from "../src/i18n/I18n";
import styles from "./Gallery.module.css";

const STATES = ["default", "hover", "focus-visible", "active", "disabled", "loading", "error", "empty"] as const;

function Row({ name, cells }: { name: string; cells: Partial<Record<(typeof STATES)[number], ReactNode>> }) {
  return (
    <div className={styles.row} data-component={name}>
      <div className={styles.name} translate="no">
        {name}
      </div>
      {STATES.map((s) => (
        <div key={s} className={styles.cell} data-state={s}>
          {cells[s] ?? null}
        </div>
      ))}
    </div>
  );
}

function Head() {
  return (
    <div className={styles.row} aria-hidden="true">
      <div />
      {STATES.map((s) => (
        <div key={s} className={styles.stateName} translate="no">
          {s}
        </div>
      ))}
    </div>
  );
}

const Plus = (
  <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5">
    <path d="M8 3v10M3 8h10" />
  </svg>
);

const OUTAGE: ApiError = { code: "unavailable", correlation_id: "corr-7f3a9c2e" };
const REFUSAL: ApiError = { code: "forbidden", correlation_id: "corr-1b4d8e06" };
const UNKNOWN: ApiError = { code: "quota_exhausted", correlation_id: "corr-55e0aa13" };

interface Demo {
  id: string;
  zone: string;
  status: StatusName;
  sessions: number;
}
const ROWS: Demo[] = [
  { id: "px-core-01", zone: "core", status: "healthy", sessions: 12 },
  { id: "px-dmz-03", zone: "dmz", status: "rejected", sessions: 3 },
  { id: "px-edge-07", zone: "edge", status: "unreachable", sessions: 0 },
];

function Buttons() {
  const { t } = useI18n();
  return (
    <>
      <Head />
      {(["primary", "secondary", "ghost", "danger"] as const).map((v) => (
        <Row
          key={v}
          name={`button ${v}`}
          cells={{
            default: <Button variant={v}>{t("common.retry")}</Button>,
            hover: <Button variant={v} data-force="hover">{t("common.retry")}</Button>,
            "focus-visible": <Button variant={v} data-force="focus-visible">{t("common.retry")}</Button>,
            active: <Button variant={v} data-force="active">{t("common.retry")}</Button>,
            disabled: <Button variant={v} disabled>{t("common.retry")}</Button>,
            loading: <Button variant={v} loading>{t("common.retry")}</Button>,
          }}
        />
      ))}
      {(["primary", "secondary"] as const).map((v) => (
        <Row
          key={`link-${v}`}
          name={`button link ${v}`}
          cells={{
            default: <ButtonLink variant={v} to="policy/candidate">{t("policies.author")}</ButtonLink>,
            hover: <ButtonLink variant={v} to="policy/candidate" data-force="hover">{t("policies.author")}</ButtonLink>,
            "focus-visible": (
              <ButtonLink variant={v} to="policy/candidate" data-force="focus-visible">
                {t("policies.author")}
              </ButtonLink>
            ),
            active: <ButtonLink variant={v} to="policy/candidate" data-force="active">{t("policies.author")}</ButtonLink>,
          }}
        />
      ))}
      <Row
        name="icon button"
        cells={{
          default: <IconButton label={t("component.dialog.close")} icon={Plus} />,
          hover: <IconButton label={t("component.dialog.close")} icon={Plus} data-force="hover" />,
          "focus-visible": <IconButton label={t("component.dialog.close")} icon={Plus} data-force="focus-visible" />,
          active: <IconButton label={t("component.dialog.close")} icon={Plus} data-force="active" />,
          disabled: <IconButton label={t("component.dialog.close")} icon={Plus} disabled />,
        }}
      />
    </>
  );
}

function Fields() {
  const { t } = useI18n();
  const opts = [
    { value: "a", label: t("prefs.theme.system") },
    { value: "b", label: t("prefs.theme.dark") },
  ];
  return (
    <>
      <Head />
      <Row
        name="input"
        cells={{
          default: <Field label={t("signIn.breakGlass.login")}><Input /></Field>,
          hover: <Field label={t("signIn.breakGlass.login")}><Input data-force="hover" /></Field>,
          "focus-visible": <Field label={t("signIn.breakGlass.login")}><Input data-force="focus-visible" /></Field>,
          disabled: <Field label={t("signIn.breakGlass.login")}><Input disabled /></Field>,
          error: (
            <Field label={t("signIn.breakGlass.login")} error={t("component.confirm.mismatch")}>
              <Input defaultValue="px-core-0l" mono />
            </Field>
          ),
          empty: <Field label={t("fleet.search")} hint={t("component.confirm.hint")}><Input placeholder={t("fleet.search")} /></Field>,
        }}
      />
      <Row
        name="textarea"
        cells={{
          default: <Field label={t("fleet.detail.lastError")}><Textarea /></Field>,
          "focus-visible": <Field label={t("fleet.detail.lastError")}><Textarea data-force="focus-visible" /></Field>,
          disabled: <Field label={t("fleet.detail.lastError")}><Textarea disabled /></Field>,
          error: <Field label={t("fleet.detail.lastError")} error={t("error.invalid_request")}><Textarea /></Field>,
        }}
      />
      <Row
        name="select"
        cells={{
          default: <Field label={t("prefs.theme.label")}><Select>{opts.map((o) => <option key={o.value}>{o.label}</option>)}</Select></Field>,
          hover: <Field label={t("prefs.theme.label")}><Select data-force="hover">{opts.map((o) => <option key={o.value}>{o.label}</option>)}</Select></Field>,
          "focus-visible": <Field label={t("prefs.theme.label")}><Select data-force="focus-visible">{opts.map((o) => <option key={o.value}>{o.label}</option>)}</Select></Field>,
          disabled: <Field label={t("prefs.theme.label")}><Select disabled>{opts.map((o) => <option key={o.value}>{o.label}</option>)}</Select></Field>,
        }}
      />
      <Row
        name="combobox"
        cells={{
          default: <Combobox label={t("palette.title")} options={[]} onChoose={() => {}} emptyText={t("palette.none")} />,
          active: (
            <Combobox
              label={t("palette.title")}
              alwaysOpen
              options={[
                { id: "a", label: t("palette.go", { screen: t("nav.fleet") }) },
                { id: "b", label: t("palette.go", { screen: t("nav.deployment") }) },
              ]}
              onChoose={() => {}}
              emptyText={t("palette.none")}
            />
          ),
          empty: <Combobox label={t("palette.title")} alwaysOpen options={[]} onChoose={() => {}} emptyText={t("palette.none")} />,
        }}
      />
      {(["checkbox", "radio", "switch"] as const).map((kind) => {
        const C = kind === "checkbox" ? Checkbox : kind === "radio" ? Radio : Switch;
        return (
          <Row
            key={kind}
            name={kind}
            cells={{
              default: <C label={t("prefs.density.compact")} />,
              hover: <C label={t("prefs.density.compact")} data-force="hover" />,
              "focus-visible": <C label={t("prefs.density.compact")} data-force="focus-visible" />,
              active: <C label={t("prefs.density.compact")} defaultChecked />,
              disabled: <C label={t("prefs.density.compact")} disabled />,
            }}
          />
        );
      })}
      <Row
        name="choices"
        cells={{
          default: (
            <Choices legend={t("prefs.density.label")}>
              <Radio name="g" label={t("prefs.density.compact")} defaultChecked />
              <Radio name="g" label={t("prefs.density.comfortable")} />
            </Choices>
          ),
        }}
      />
    </>
  );
}

function Statuses() {
  const { t } = useI18n();
  const tones: Tone[] = ["allow", "deny", "warn", "outage", "info", "unknown"];
  return (
    <>
      <div className={styles.grid}>
        {(Object.keys(STATUS) as StatusName[]).map((s) => (
          <div key={s} className={styles.statusRow} data-status={s}>
            <span className={styles.name} translate="no">
              {s}
            </span>
            <StatusOf name={s} />
            <StatusOf name={s} pill />
          </div>
        ))}
      </div>
      <div className={styles.inlineRow} data-component="glyphs">
        {tones.map((tone) => (
          <span key={tone} className={styles.glyphCell}>
            <StatusGlyph tone={tone} />
            <Status tone={tone} label={t(STATUS[toneName(tone)].label)} />
          </span>
        ))}
      </div>
    </>
  );
}

function toneName(tone: Tone): StatusName {
  return ({ allow: "healthy", deny: "denied", warn: "degraded", outage: "unreachable", info: "pending", unknown: "notReported" } as const)[tone];
}

function Labels() {
  const { t } = useI18n();
  return (
    <>
      <Head />
      <Row name="badge" cells={{ default: <span className={styles.inlineRow}><Badge>{"12"}</Badge><Badge tone="accent">{"3"}</Badge><Badge tone="info">{"2"}</Badge></span> }} />
      <Row
        name="chip"
        cells={{
          default: <Chip data>{"env=prod"}</Chip>,
          hover: (
            <Chip onRemove={() => {}} removeLabel={t("component.filter.remove", { field: t("fleet.col.zone"), value: "dmz" })}>
              {t("component.filter.chip", { field: t("fleet.col.zone"), value: "dmz" })}
            </Chip>
          ),
          "focus-visible": <Chip data>{"team=payments"}</Chip>,
        }}
      />
      <Row name="avatar" cells={{ default: <span className={styles.inlineRow}><Avatar name="Ada Admin" /><Avatar name="fenn" /></span> }} />
      <Row name="keyboard hint" cells={{ default: <KeyboardHint keys={[t("palette.key.ctrl"), "K"]} /> }} />
      <Row
        name="breadcrumb"
        cells={{ default: <Breadcrumb items={[{ label: t("nav.deployment"), to: "deployment" }, { label: t("nav.fleet") }]} /> }}
      />
      <Row
        name="copyable id"
        cells={{
          default: <CopyableId value="sha256:e43f2f5c9e79e456987fc8612f56fd2e057dcdfd3ea66b749bb978ec5992368a" short="sha256:e43f2f5c9e79" />,
          hover: <span data-force-child="hover"><CopyableId value="px-core-01" /></span>,
        }}
      />
      <Row
        name="tooltip"
        cells={{
          hover: (
            <Tooltip text={t("chrome.deployment.version", { version: "0.37.0" })}>
              {(id) => (
                <span data-force-parent="hover">
                  <Button aria-describedby={id}>{t("nav.deployment")}</Button>
                </span>
              )}
            </Tooltip>
          ),
        }}
      />
    </>
  );
}

function Feedback() {
  const { t } = useI18n();
  return (
    <div className={styles.stack}>
      <div data-component="skeleton" className={styles.panel}>
        <Skeleton shape="title" size="m" />
        <Skeleton size="full" />
        <Skeleton size="l" />
        <Skeleton shape="block" size="full" />
      </div>
      <div data-component="spinner" className={[styles.panel, styles.overlayHost].join(" ")}>
        <p>{t("fleet.lede")}</p>
        <SpinnerOverlay label={t("common.loading")} />
      </div>
      <div data-component="empty state">
        <EmptyState title={t("fleet.empty.title")} body={t("fleet.empty.body")} action={<Button variant="primary">{t("fleet.refresh")}</Button>} />
      </div>
      <div data-component="error state, outage">
        <ErrorState error={OUTAGE} kind="outage" onRetry={() => {}} />
      </div>
      <div data-component="error state, refused">
        <ErrorState error={REFUSAL} kind="refused" />
      </div>
      <div data-component="error, unknown code">
        <ErrorMessage error={UNKNOWN} kind="outage" />
      </div>
      <div data-component="partial">
        <PartialNotice gaps={[t("fleet.partial.unreachable", { count: 1, ids: "px-edge-07" }), t("fleet.partial.summary")]} />
      </div>
    </div>
  );
}

function Navigation() {
  const { t } = useI18n();
  const [tab, setTab] = useState("table");
  return (
    <div className={styles.stack}>
      <div data-component="page header">
        <PageHeader
          breadcrumb={<Breadcrumb items={[{ label: t("nav.deployment"), to: "deployment" }, { label: t("nav.fleet") }]} />}
          title={t("fleet.title")}
          id={<CopyableId value="px-core-01" />}
          status={<StatusOf name="healthy" pill />}
          lede={t("fleet.lede")}
          actions={
            <>
              <Button>{t("fleet.refresh")}</Button>
              <Button variant="danger">{t("fleet.action.withdraw")}</Button>
            </>
          }
        />
      </div>
      <div data-component="tabs">
        <Tabs
          label={t("fleet.views")}
          selected={tab}
          onSelect={setTab}
          tabs={[
            { id: "table", label: t("fleet.view.table") },
            { id: "zones", label: t("fleet.view.zones") },
            { id: "off", label: t("fleet.summary"), disabled: true },
          ]}
        >
          <p>{t("fleet.lede")}</p>
        </Tabs>
      </div>
      <div data-component="pagination, first page">
        <Pagination page={{ trail: [], limit: 3 }} shown={3} truncated next="c1" onPage={() => {}} />
      </div>
      <div data-component="pagination, last page">
        <Pagination page={{ trail: ["c1", "c2"], limit: 3 }} shown={2} truncated={false} onPage={() => {}} />
      </div>
      <div data-component="menu">
        <Menu
          label={t("component.table.rowActions", { name: "px-core-01" })}
          triggerLabel={t("component.table.rowActions", { name: "px-core-01" })}
          trigger={Plus}
          items={[
            { id: "a", label: t("fleet.action.details"), onSelect: () => {} },
            { id: "b", label: t("component.copy.button"), onSelect: () => {}, disabled: true },
            { id: "c", label: t("fleet.action.withdraw"), danger: true, onSelect: () => {} },
          ]}
        />
      </div>
    </div>
  );
}

function Data() {
  const { t } = useI18n();
  const [selected, setSelected] = useState<Set<string>>(new Set(["px-dmz-03"]));
  const columns: Column<Demo>[] = [
    { id: "id", header: t("fleet.col.proxy"), sortValue: (r) => r.id, cell: (r) => <code dir="ltr" translate="no">{r.id}</code> },
    { id: "zone", header: t("fleet.col.zone"), sortValue: (r) => r.zone, cell: (r) => <code dir="ltr" translate="no">{r.zone}</code> },
    { id: "status", header: t("fleet.col.health"), cell: (r) => <StatusOf name={r.status} /> },
    { id: "sessions", header: t("fleet.col.sessions"), numeric: true, sortValue: (r) => r.sessions, cell: (r) => r.sessions },
  ];
  return (
    <div className={styles.stack}>
      <div data-component="filter bar">
        <FilterBar
          label={t("fleet.filters")}
          fields={[
            { id: "zone", label: t("fleet.col.zone"), options: [{ value: "dmz", label: "dmz" }] },
            { id: "health", label: t("fleet.col.health"), options: [{ value: "rejected", label: t("status.rejected") }] },
          ]}
          values={{ zone: "dmz", health: "rejected" }}
          onChange={() => {}}
          search={{ label: t("fleet.search"), value: "", onChange: () => {} }}
          views={{ saved: [], onApply: () => {}, onSave: () => {}, onForget: () => {} }}
        />
      </div>
      <div data-component="data table">
        <DataTable
          caption={t("fleet.table.caption")}
          columns={columns}
          rows={ROWS}
          rowKey={(r) => r.id}
          rowLabel={(r) => r.id}
          sort={{ column: "id", dir: "ascending" }}
          onSort={() => {}}
          hidden={[]}
          onHidden={() => {}}
          selected={selected}
          onSelected={setSelected}
          partialNote={t("fleet.table.onePage")}
          rowActions={() => [{ id: "d", label: t("fleet.action.details"), onSelect: () => {} }]}
        />
      </div>
      <div data-component="data table, refreshing">
        <DataTable caption={t("fleet.table.caption")} columns={columns} rows={ROWS.slice(0, 2)} rowKey={(r) => r.id} rowLabel={(r) => r.id} refreshing />
      </div>
      <div data-component="data table, loading">
        <TableSkeleton headers={columns.map((c) => c.header)} rows={3} />
      </div>
      <div data-component="data table, empty">
        <DataTable
          caption={t("fleet.table.caption")}
          columns={columns}
          rows={[]}
          rowKey={(r) => r.id}
          rowLabel={(r) => r.id}
          empty={<EmptyState title={t("fleet.filtered.title")} body={t("fleet.filtered.body")} />}
        />
      </div>
      <div data-component="tree">
        <Tree
          label={t("fleet.view.zones")}
          nodes={[
            {
              id: "zone:core",
              label: <code dir="ltr" translate="no">{"core"}</code>,
              meta: <Badge>{"2"}</Badge>,
              children: [
                { id: "px-core-01", label: <code dir="ltr" translate="no">{"px-core-01"}</code>, meta: <StatusOf name="healthy" /> },
                { id: "px-core-02", label: <code dir="ltr" translate="no">{"px-core-02"}</code>, meta: <StatusOf name="pendingRestart" /> },
              ],
            },
            { id: "zone:edge", label: <code dir="ltr" translate="no">{"edge"}</code>, children: [] },
          ]}
        />
      </div>
      <div data-component="code block">
        <CodeBlock label={t("fleet.detail.configError")} code={"logging.batch_size: value out of range (1..10000)"} />
      </div>
      <div data-component="json viewer">
        <JsonViewer
          label={t("fleet.detail.capabilities")}
          value={{ credential_methods: ["password", "certificate"], platforms: ["linux"], execution: [], max_hops: 4, attested: true, note: null }}
        />
      </div>
    </div>
  );
}

function Overlay({ which }: { which: string }) {
  const { t } = useI18n();
  const close = () => {};
  switch (which) {
    case "dialog":
      return (
        <Dialog open title={t("fleet.withdraw.title")} onClose={close} footer={<Button variant="primary">{t("fleet.refresh")}</Button>}>
          <p>{t("fleet.withdraw.body", { id: "px-spare-09" })}</p>
        </Dialog>
      );
    case "confirm":
    case "confirm-failed":
      return (
        <ConfirmDialog
          open
          title={t("fleet.withdraw.title")}
          body={<p>{t("fleet.withdraw.body", { id: "px-spare-09" })}</p>}
          name="px-spare-09"
          action={t("fleet.withdraw.action")}
          onConfirm={async () => (which === "confirm-failed" ? { error: OUTAGE, kind: "outage" } : null)}
          onClose={close}
        />
      );
    case "drawer":
      return (
        <Drawer open title={<code dir="ltr" translate="no">{"px-core-01"}</code>} onClose={close}>
          <StatusOf name="healthy" pill />
          <CodeBlock label={t("fleet.detail.configError")} code={"chain.max_hops: restart required"} />
        </Drawer>
      );
    case "menu":
      return (
        <Menu
          label={t("component.table.rowActions", { name: "px-core-01" })}
          triggerLabel={t("component.table.rowActions", { name: "px-core-01" })}
          trigger={Plus}
          heading={t("chrome.account.label", { name: "Ada Admin" })}
          items={[
            { id: "a", label: t("fleet.action.details"), onSelect: () => {} },
            { id: "b", label: t("component.copy.button"), onSelect: () => {}, disabled: true },
            { id: "c", label: t("prefs.density.compact"), onSelect: () => {}, checked: true },
            { id: "r1", kind: "radio", group: t("prefs.theme.label"), label: t("prefs.theme.light"), onSelect: () => {}, checked: false },
            { id: "r2", kind: "radio", group: t("prefs.theme.label"), label: t("prefs.theme.dark"), onSelect: () => {}, checked: true },
            { id: "d", label: t("fleet.action.withdraw"), danger: true, onSelect: () => {} },
          ]}
        />
      );
    case "palette":
      return (
        <CommandPalette
          commands={[
            { id: "go:fleet", label: t("palette.go", { screen: t("nav.fleet") }) },
            { id: "go:deployment", label: t("palette.go", { screen: t("nav.deployment") }) },
            { id: "theme:dark", label: t("palette.theme", { theme: t("prefs.theme.dark") }) },
          ]}
          onRun={() => {}}
        />
      );
    case "toast":
      return (
        <ToastRegion
          closeLabel={t("component.toast.dismiss")}
          onDismiss={close}
          items={[
            { id: 1, kind: "success", text: t("fleet.withdraw.done", { id: "px-spare-09", tokens: 1 }) },
            { id: 2, kind: "error", text: t("error.unavailable") },
          ]}
        />
      );
    default:
      return null;
  }
}


function Cell({ state, children }: { state: string; children: ReactNode }) {
  return (
    <div className={styles.panel} data-component-state={state}>
      <div className={styles.stateName} translate="no">
        {state}
      </div>
      {children}
    </div>
  );
}

const EVENTS: TimelineEvent[] = [
  {
    id: "d-edge",
    at: "2026-11-01T08:00:00Z",
    title: <code dir="ltr" translate="no">{"px-core-01"}</code>,
    status: { tone: "allow", label: "" },
    meta: <code dir="ltr" translate="no">{"ops-shell:12"}</code>,
  },
  {
    id: "d-inner",
    at: "2026-11-01T08:00:01Z",
    title: <code dir="ltr" translate="no">{"px-dmz-03"}</code>,
    status: { tone: "deny", label: "" },
  },
  {
    id: "d-unserved",
    at: "2026-11-01T08:00:02Z",
    title: <code dir="ltr" translate="no">{"px-dmz-04"}</code>,
    status: { tone: "outage", label: "" },
  },
];

/** The timeline and the time range picker (0051), every state. */
function Time() {
  const { t } = useI18n();
  const [selected, setSelected] = useState("d-inner");
  const labelled = (tone: string) =>
    tone === "allow" ? t("status.allowed") : tone === "deny" ? t("status.denied") : t("status.unserved");
  const events = EVENTS.map((e) => ({ ...e, status: e.status ? { ...e.status, label: labelled(e.status.tone) } : undefined }));
  return (
    <div className={styles.stack}>
      <div className={styles.grid} data-component="timeline">
        <Cell state="default">
          <Timeline label={t("explain.timeline")} events={events} />
        </Cell>
        <Cell state="active">
          <Timeline label={t("explain.timeline")} events={events} selected={selected} onSelect={setSelected} />
        </Cell>
        <Cell state="hover">
          <div data-force-child="hover">
            <Timeline label={t("explain.timeline")} events={events.slice(0, 1)} selected="none" onSelect={() => {}} />
          </div>
        </Cell>
        <Cell state="focus-visible">
          <div data-force-child="focus-visible">
            <Timeline label={t("explain.timeline")} events={events.slice(0, 1)} selected="none" onSelect={() => {}} />
          </div>
        </Cell>
        <Cell state="disabled">
          <Timeline
            label={t("explain.timeline")}
            events={[events[0] as TimelineEvent, { ...(events[1] as TimelineEvent), disabled: true }]}
            selected="d-edge"
            onSelect={() => {}}
          />
        </Cell>
        <Cell state="loading">
          <TimelineSkeleton rows={3} />
        </Cell>
        <Cell state="error">
          <Timeline label={t("explain.timeline")} events={events.slice(2)} />
        </Cell>
        <Cell state="empty">
          <Timeline
            label={t("explain.timeline")}
            events={[]}
            empty={<EmptyState title={t("replay.empty.title")} body={t("replay.empty.body")} />}
          />
        </Cell>
      </div>
      <div className={styles.grid} data-component="time range picker">
        <Cell state="default">
          <TimeRangePicker label={t("audit.filter.time")} value={{ kind: "all" }} onChange={() => {}} />
        </Cell>
        <Cell state="active">
          <TimeRangePicker label={t("audit.filter.time")} value={{ kind: "relative", last: "24h" }} onChange={() => {}} />
        </Cell>
        <Cell state="absolute">
          <TimeRangePicker
            label={t("audit.filter.time")}
            value={{ kind: "absolute", from: "2026-11-01T08:00:00Z", to: "2026-11-02T08:00:00Z" }}
            onChange={() => {}}
          />
        </Cell>
        <Cell state="error">
          <div data-range-invalid>
            <TimeRangePicker
              label={t("audit.filter.time")}
              value={{ kind: "absolute", from: "2026-11-02T08:00:00Z", to: "2026-11-01T08:00:00Z" }}
              onChange={() => {}}
            />
          </div>
        </Cell>
        <Cell state="disabled">
          <TimeRangePicker label={t("audit.filter.time")} value={{ kind: "relative", last: "7d" }} onChange={() => {}} disabled />
        </Cell>
      </div>
    </div>
  );
}

const DIFF = [
  "@@ -9,7 +9,8 @@",
  " groups: [sre]",
  " ",
  " rules:",
  "-  - id: ops-db-shell",
  "+  - id: ops-db-shell-recorded",
  "+    description: an SRE's recorded shell on a production database",
  "     effect: allow",
  "     match:",
  "       subject:",
  "",
].join("\n");

const SOURCE = ["schema_version: 1", "tenant: default", "groups: [sre]", "rules:", "  - id: ops-db-shell", "    effect: allow", "    match:", "      subject: {groups: [sre-typo]}"].join(
  "\n",
);

/** The diff view (0053), and the source view and mono textarea Policy edits and reads source with. */
function Diffs() {
  const { t } = useI18n();
  const failed: ApiError = { code: "unavailable", correlation_id: "corr-gallery-diff" };
  return (
    <div className={styles.stack}>
      <div className={styles.grid} data-component="diff view">
        <Cell state="default">
          <DiffView label={t("policies.diff.label", { to: "v3", from: "v2" })} text={DIFF} />
        </Cell>
        <Cell state="focus-visible">
          <div data-force-child="focus-visible">
            <DiffView label={t("policies.diff.label", { to: "v3", from: "v2" })} text={DIFF.split("\n").slice(0, 6).join("\n")} />
          </div>
        </Cell>
        <Cell state="loading">
          <DiffSkeleton rows={5} />
        </Cell>
        <Cell state="error">
          <ErrorMessage error={failed} kind="outage" />
        </Cell>
        <Cell state="empty">
          <DiffView label={t("policies.diff.label", { to: "v3", from: "v3" })} text="" />
        </Cell>
        <Cell state="omitted">
          <DiffView label={t("policies.diff.label", { to: "v9", from: "v1" })} text="" omitted />
        </Cell>
      </div>
      <div className={styles.grid} data-component="source view">
        <Cell state="default">
          <SourceView source={SOURCE} label={t("policies.source.label")} idPrefix="gallery-source" />
        </Cell>
        <Cell state="error">
          <SourceView
            source={SOURCE}
            label={t("policies.source.label")}
            idPrefix="gallery-source-noted"
            notes={[
              { line: 8, key: "r", tone: "deny", content: <ErrorMessage error={{ code: "policy_invalid" }} kind="refused" /> },
              { line: 0, key: "d", tone: "info", content: <Status tone="info" label={t("status.advice")} /> },
            ]}
          />
        </Cell>
      </div>
      <div className={styles.grid} data-component="mono textarea">
        <Cell state="default">
          <Field label={t("policies.candidate.label")} hint={t("policies.candidate.hintBlank")}>
            <Textarea mono wrap="off" rows={4} defaultValue={SOURCE} />
          </Field>
        </Cell>
        <Cell state="focus-visible">
          <Field label={t("policies.candidate.label")}>
            <Textarea mono wrap="off" rows={4} defaultValue={SOURCE} data-force="focus-visible" />
          </Field>
        </Cell>
        <Cell state="error">
          <Field label={t("policies.candidate.label")} error={t("policies.candidate.required")}>
            <Textarea mono wrap="off" rows={4} defaultValue="" />
          </Field>
        </Cell>
        <Cell state="disabled">
          <Field label={t("policies.candidate.label")}>
            <Textarea mono wrap="off" rows={4} defaultValue={SOURCE} disabled />
          </Field>
        </Cell>
      </div>
    </div>
  );
}

const SECTIONS: Record<string, () => ReactNode> = {
  buttons: () => <Buttons />,
  fields: () => <Fields />,
  status: () => <Statuses />,
  labels: () => <Labels />,
  feedback: () => <Feedback />,
  navigation: () => <Navigation />,
  data: () => <Data />,
  time: () => <Time />,
  diff: () => <Diffs />,
};

/** The gallery's pages: one per section, and one per overlay state. */
export const GALLERY_PAGES = [
  ...Object.keys(SECTIONS),
  "overlay-dialog",
  "overlay-confirm",
  "overlay-confirm-failed",
  "overlay-drawer",
  "overlay-menu",
  "overlay-palette",
  "overlay-toast",
];

export function Gallery({ section }: { section: string }) {
  const overlay = section.startsWith("overlay-") ? section.slice("overlay-".length) : null;
  const render = SECTIONS[section];
  return (
    <main className={styles.page} id="content" data-gallery={section}>
      <h1 className={styles.title} translate="no">
        {section}
      </h1>
      {overlay ? (
        <div className={styles.inlineRow}>
          <Overlay which={overlay} />
        </div>
      ) : render ? (
        render()
      ) : null}
    </main>
  );
}
