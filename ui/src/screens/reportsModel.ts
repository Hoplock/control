// What the Reports screen reads from GET …/reports and sends to POST
// …/reports/generate (0044), as rules a test can hold without a browser
// (0056). The form is the descriptor's: its period, each parameter — a
// closed set as a choice, the required ones marked — and the formats it
// lists. `provider` and `report` go back exactly as listed, in the body.

import type { ApiError } from "../i18n/errors";
import type { MessageKey } from "../i18n/catalogue";
import { fromLocalInput, isoBefore, localInput } from "../i18n/format";
import type { ReportDescriptor, ReportFormat, ReportListing, ReportProvider, ReportRequestBody } from "../api/types";

export const FORMAT_LABEL: Readonly<Record<ReportFormat, MessageKey>> = {
  json: "reports.format.json",
  csv: "reports.format.csv",
  pdf: "reports.format.pdf",
};

/** The period a form opens with: the last seven days, to the minute. */
export const DEFAULT_PERIOD_MS = 7 * 86_400_000;

export interface ReportForm {
  /** datetime-local values, in the operator's zone. */
  from: string;
  to: string;
  parameters: Record<string, string>;
  format: ReportFormat;
}

/** The providers that could not list their reports: each listed, unavailable, and a gap — never "no packs". */
export function unavailable(listing: ReportListing): ReportProvider[] {
  return listing.providers.filter((p) => p.error);
}

/** A report's form as it opens. */
export function initialForm(d: ReportDescriptor, now: number): ReportForm {
  const minute = Math.floor(now / 60_000) * 60_000;
  return {
    from: localInput(isoBefore(minute, DEFAULT_PERIOD_MS)),
    to: localInput(new Date(minute)),
    parameters: Object.fromEntries(d.parameters.map((p) => [p.name, ""])),
    format: d.formats[0] ?? "json",
  };
}

/** A field the form or the server faulted, and why: the server's own field names (0044). */
export interface Fault {
  field: string;
  problem: string;
}

/**
 * What the form refuses before asking: the period's two ends, present and in
 * order, and each required parameter. The server checks all of it again.
 */
export function check(d: ReportDescriptor, form: ReportForm): Fault[] {
  const out: Fault[] = [];
  const from = form.from ? fromLocalInput(form.from) : undefined;
  const to = form.to ? fromLocalInput(form.to) : undefined;
  if (!from) out.push({ field: "from", problem: form.from ? "invalid" : "required" });
  if (!to) out.push({ field: "to", problem: form.to ? "invalid" : "required" });
  if (from && to && Date.parse(from) >= Date.parse(to)) out.push({ field: "to", problem: "not_after_start" });
  for (const p of d.parameters) {
    if (p.required && !form.parameters[p.name]) out.push({ field: `parameters.${p.name}`, problem: "required" });
  }
  if (!d.formats.includes(form.format)) out.push({ field: "format", problem: "invalid" });
  return out;
}

/** The body the form posts: provider and report exactly as listed, empty parameters left out. */
export function requestBody(provider: string, d: ReportDescriptor, form: ReportForm): ReportRequestBody {
  const parameters: Record<string, string> = {};
  for (const p of d.parameters) {
    const v = form.parameters[p.name];
    if (v) parameters[p.name] = v;
  }
  return {
    provider,
    report: d.report,
    from: fromLocalInput(form.from) as string,
    to: fromLocalInput(form.to) as string,
    parameters,
    format: form.format,
  };
}

/** The field a refusal names, when it is one of the form's (an invalid_request naming a field). */
export function faultOf(error: ApiError): Fault | null {
  if (error.code !== "invalid_request") return null;
  const field = error.parameters?.field;
  const problem = error.parameters?.problem;
  return typeof field === "string" && field !== "" ? { field, problem: typeof problem === "string" ? problem : "invalid" } : null;
}

/** The sentence for a fault at a field. */
export function faultKey(problem: string): MessageKey {
  switch (problem) {
    case "required":
      return "reports.fault.required";
    case "not_after_start":
      return "reports.fault.order";
    default:
      return "reports.fault.invalid";
  }
}
