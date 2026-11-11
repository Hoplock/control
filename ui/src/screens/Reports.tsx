// REPORTS (0056; PLAN §7 "Reports", M15, M21, M27, M11).
//
// /console/reports[?provider=<p>&report=<r>]: every report pack registered
// for this tenant (GET …/reports, audit:read), and a form that generates one
// (POST …/reports/generate, audit:read) and saves the file. A deep link with
// provider and report opens that report's form; both are data in the
// console's URL and in the request's body, and never a path segment.
//
// How it reads, and why:
//   - with no pack registered, the audit view is the reporting available —
//     the point's own answer (M15) — and the screen says so; not broken;
//   - a pack listed with an error is unavailable, never "no reports": its
//     reports are unknown, not absent (M11);
//   - a report is named through its host's catalogue (i18n/host.ts), and by
//     its code where no catalogue names it (M21);
//   - a generation that ends broken is Failed, and nothing is saved: the
//     server aborts a report rather than end it short, and the console
//     never offers what arrived as a report.

import { useContext, useId, useState, type FormEvent } from "react";
import type { ReportDescriptor, ReportListing, ReportProvider } from "../api/types";
import { Badge } from "../components/Badge";
import { Button, ButtonLink } from "../components/Button";
import { Choices, Field, Input, Radio, Select } from "../components/Field";
import { Ident } from "../components/Ident";
import { PageHeader } from "../components/PageHeader";
import { EmptyState, ErrorMessage, ScreenFrame, Skeleton } from "../components/States";
import { StatusOf } from "../components/Status";
import { useToast } from "../components/Toast";
import { errorMessage, type ApiError } from "../i18n/errors";
import { HostCatalogueContext, hostText, reportNameKey, reportParameterKey } from "../i18n/host";
import { useI18n } from "../i18n/I18n";
import { useLocation } from "../router/router";
import { useLoad } from "../shell/load";
import { saveFile } from "../shell/save";
import { useDeployment } from "../shell/Shell";
import { useSetQuery } from "./InventoryParts";
import { check, faultKey, faultOf, FORMAT_LABEL, initialForm, requestBody, unavailable, type Fault, type ReportForm } from "./reportsModel";
import styles from "./Extensions.module.css";
import reportStyles from "./Reports.module.css";

export function ReportsScreen() {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const location = useLocation();
  const setQuery = useSetQuery();
  const [load, refresh] = useLoad<ReportListing>(tenant ? `reports|${tenant}` : null, (signal) =>
    api.call("reports", { params: { tenant: tenant as string }, signal }),
  );
  const open = { provider: location.search.get("provider"), report: location.search.get("report") };

  return (
    <div className={styles.screen}>
      <PageHeader
        title={t("reports.title")}
        lede={t("reports.lede")}
        actions={
          <Button onClick={refresh} data-refresh="">
            {t("reports.refresh")}
          </Button>
        }
      />
      <ScreenFrame
        load={load}
        onRetry={refresh}
        gaps={load.state === "ok" ? unavailable(load.data).map((p) => t("reports.partial.provider", { provider: p.provider })) : []}
        isEmpty={(d) => d.providers.length === 0}
        empty={
          <EmptyState
            title={t("reports.empty.title")}
            body={t("reports.empty.body")}
            action={
              <ButtonLink to="audit/records" variant="primary">
                {t("reports.empty.action")}
              </ButtonLink>
            }
          />
        }
        loading={<ReportsSkeleton />}
      >
        {(d) => (
          <div className={styles.stack}>
            {d.providers.map((p) => (
              <Provider
                key={p.provider}
                provider={p}
                open={open.provider === p.provider ? open.report : null}
                onOpen={(report) => setQuery({ provider: report === null ? undefined : p.provider, report: report ?? undefined })}
              />
            ))}
          </div>
        )}
      </ScreenFrame>
    </div>
  );
}

/** The host's names for a pack's reports and their parameters, or undefined where it names none. */
function useReportNames() {
  const { locale } = useI18n();
  const host = useContext(HostCatalogueContext);
  return {
    report: (provider: string, report: string) => (host ? hostText(host, reportNameKey(host.host, provider, report), locale) : undefined),
    parameter: (provider: string, report: string, name: string) =>
      host ? hostText(host, reportParameterKey(host.host, provider, report, name), locale) : undefined,
  };
}

export function Provider({
  provider: p,
  open,
  onOpen,
}: {
  provider: ReportProvider;
  open: string | null;
  onOpen(report: string | null): void;
}) {
  const { t } = useI18n();
  const headingId = useId();
  const state = p.error ? "unavailable" : "ok";
  return (
    <section className={styles.panel} aria-labelledby={headingId} data-provider={p.provider} data-provider-state={state}>
      <div className={styles.pointHead}>
        <h2 id={headingId} className={styles.pointTitle}>
          <Ident value={p.provider} />
        </h2>
        {p.version ? <span className={styles.quiet}>{t("reports.provider.version", { version: p.version })}</span> : null}
        {p.error ? (
          <span className={styles.pointStatus}>
            <StatusOf name="unavailable" pill />
          </span>
        ) : null}
      </div>
      {p.error ? (
        <div className={styles.absent} data-provider-error={p.error}>
          <p className={styles.text}>{errorMessage(t, { code: p.error }).text}</p>
          <p className={styles.quiet}>{t("reports.provider.unlisted")}</p>
        </div>
      ) : p.reports.length === 0 ? (
        <p className={styles.quiet} data-no-reports="">
          {t("reports.provider.none")}
        </p>
      ) : (
        <ul className={reportStyles.reports}>
          {p.reports.map((r) => (
            <Report key={r.report} provider={p.provider} descriptor={r} open={open === r.report} onOpen={(o) => onOpen(o ? r.report : null)} />
          ))}
        </ul>
      )}
    </section>
  );
}

function Report({
  provider,
  descriptor: d,
  open,
  onOpen,
}: {
  provider: string;
  descriptor: ReportDescriptor;
  open: boolean;
  onOpen(open: boolean): void;
}) {
  const { t } = useI18n();
  const names = useReportNames();
  const name = names.report(provider, d.report);
  const formId = useId();
  return (
    <li className={reportStyles.report} data-report={d.report} data-open={open || undefined}>
      <div className={reportStyles.reportHead}>
        <div className={reportStyles.reportName}>
          {name ? <h3 className={reportStyles.reportTitle}>{name}</h3> : null}
          {name ? (
            <Ident value={d.report} />
          ) : (
            <h3 className={reportStyles.reportTitle} data-named-by="code">
              <Ident value={d.report} />
            </h3>
          )}
        </div>
        <ul className={reportStyles.formats} aria-label={t("reports.formats.label")}>
          {d.formats.map((fmt) => (
            <li key={fmt}>
              <Badge>{t(FORMAT_LABEL[fmt])}</Badge>
            </li>
          ))}
        </ul>
        <span className={styles.quiet}>{t("reports.parameters.count", { count: d.parameters.length })}</span>
        <span className={reportStyles.action}>
          <Button
            variant={open ? "ghost" : "secondary"}
            aria-expanded={open}
            aria-controls={open ? formId : undefined}
            onClick={() => onOpen(!open)}
            data-open-report={d.report}
          >
            {open ? t("reports.close") : t("reports.open")}
          </Button>
        </span>
      </div>
      {open ? <GenerateForm id={formId} provider={provider} descriptor={d} now={Date.now()} onCancel={() => onOpen(false)} /> : null}
    </li>
  );
}

type Outcome =
  | { kind: "idle" }
  | { kind: "generating" }
  | { kind: "saved"; filename: string }
  | { kind: "refused"; error: ApiError }
  | { kind: "failed"; error: ApiError | null };

export function GenerateForm({
  id,
  provider,
  descriptor: d,
  now,
  onCancel,
}: {
  id: string;
  provider: string;
  descriptor: ReportDescriptor;
  now: number;
  onCancel(): void;
}) {
  const { t } = useI18n();
  const { tenant, api } = useDeployment();
  const toast = useToast();
  const names = useReportNames();
  const [form, setForm] = useState<ReportForm>(() => initialForm(d, now));
  const [faults, setFaults] = useState<Fault[]>([]);
  const [outcome, setOutcome] = useState<Outcome>({ kind: "idle" });
  const faultAt = (field: string) => {
    const fault = faults.find((x) => x.field === field);
    return fault ? t(faultKey(fault.problem)) : undefined;
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const local = check(d, form);
    setFaults(local);
    if (local.length > 0 || !tenant) return;
    setOutcome({ kind: "generating" });
    const got = await api.download("reportGenerate", { params: { tenant }, body: requestBody(provider, d, form) });
    switch (got.kind) {
      case "ok": {
        const filename = got.filename ?? `${d.report}.${form.format}`;
        saveFile(got.blob, filename);
        setOutcome({ kind: "saved", filename });
        toast.success(t("reports.saved", { filename }));
        return;
      }
      case "refused": {
        const fault = faultOf(got.error);
        if (fault) setFaults([fault]);
        setOutcome({ kind: "refused", error: got.error });
        return;
      }
      case "outage":
        setOutcome({ kind: "failed", error: got.error });
        return;
      case "broken":
        setOutcome({ kind: "failed", error: null });
        return;
    }
  };

  const refusedElsewhere =
    outcome.kind === "refused" && !faults.some((x) => x.field === "from" || x.field === "to" || x.field === "format" || x.field.startsWith("parameters."));

  return (
    <form id={id} className={reportStyles.form} onSubmit={submit} noValidate data-report-form={d.report} aria-label={t("reports.form.label")}>
      <fieldset className={reportStyles.period}>
        <legend className={reportStyles.legend}>{t("reports.period")}</legend>
        <Field label={t("reports.period.from")} error={faultAt("from")}>
          <Input
            type="datetime-local"
            step="1"
            name="from"
            required
            value={form.from}
            onChange={(e) => setForm({ ...form, from: e.target.value })}
            data-volatile=""
          />
        </Field>
        <Field label={t("reports.period.to")} error={faultAt("to")} hint={t("reports.period.hint")}>
          <Input
            type="datetime-local"
            step="1"
            name="to"
            required
            value={form.to}
            onChange={(e) => setForm({ ...form, to: e.target.value })}
            data-volatile=""
          />
        </Field>
      </fieldset>
      {d.parameters.length > 0 ? (
        <fieldset className={reportStyles.parameters}>
          <legend className={reportStyles.legend}>{t("reports.parameters")}</legend>
          {d.parameters.map((p) => {
            const label = names.parameter(provider, d.report, p.name);
            const value = form.parameters[p.name] ?? "";
            const set = (v: string) => setForm({ ...form, parameters: { ...form.parameters, [p.name]: v } });
            return (
              <Field
                key={p.name}
                label={
                  <span className={reportStyles.parameterLabel} data-parameter={p.name}>
                    {label ? <span>{label}</span> : <Ident value={p.name} />}
                    {p.required ? <span className={reportStyles.required}>{t("reports.parameter.required")}</span> : null}
                  </span>
                }
                error={faultAt(`parameters.${p.name}`)}
              >
                {p.values.length > 0 ? (
                  <Select name={`parameters.${p.name}`} required={p.required} value={value} onChange={(e) => set(e.target.value)}>
                    <option value="">{p.required ? t("reports.parameter.choose") : t("reports.parameter.none")}</option>
                    {p.values.map((v) => (
                      <option key={v} value={v} translate="no">
                        {v}
                      </option>
                    ))}
                  </Select>
                ) : (
                  <Input mono name={`parameters.${p.name}`} required={p.required} value={value} onChange={(e) => set(e.target.value)} />
                )}
              </Field>
            );
          })}
        </fieldset>
      ) : null}
      <Choices legend={t("reports.format")}>
        {d.formats.map((fmt) => (
          <Radio
            key={fmt}
            name={`format-${id}`}
            value={fmt}
            label={t(FORMAT_LABEL[fmt])}
            checked={form.format === fmt}
            onChange={() => setForm({ ...form, format: fmt })}
          />
        ))}
      </Choices>
      {faultAt("format") ? <p className={reportStyles.fault}>{faultAt("format")}</p> : null}
      <div className={reportStyles.actions}>
        <Button type="submit" variant="primary" loading={outcome.kind === "generating"}>
          {t("reports.generate")}
        </Button>
        <Button type="button" variant="ghost" onClick={onCancel}>
          {t("reports.cancel")}
        </Button>
      </div>
      <div className={reportStyles.outcome} aria-live="polite" data-outcome={outcome.kind}>
        {outcome.kind === "saved" ? (
          <p className={styles.text} data-saved={outcome.filename}>
            <span>{t("reports.saved.as")}</span> <Ident value={outcome.filename} />
          </p>
        ) : null}
        {outcome.kind === "refused" && refusedElsewhere ? <ErrorMessage error={outcome.error} kind="refused" /> : null}
        {outcome.kind === "failed" ? (
          <div className={reportStyles.failed} data-failed={outcome.error?.code ?? "broken"}>
            <StatusOf name="failed" pill />
            {outcome.error ? (
              <ErrorMessage error={outcome.error} kind="outage" />
            ) : (
              <p className={styles.text} role="alert">
                {t("reports.failed.broken")}
              </p>
            )}
          </div>
        ) : null}
      </div>
    </form>
  );
}

function ReportsSkeleton() {
  return (
    <div className={styles.stack}>
      {[0, 1].map((i) => (
        <section key={i} className={styles.panel}>
          <Skeleton shape="title" />
          <Skeleton size="full" />
          <Skeleton size="l" />
        </section>
      ))}
    </div>
  );
}
