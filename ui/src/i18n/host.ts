// A HOST'S CATALOGUE (PLAN M27, M21; DESIGN.md §9; 0056).
//
// A binary has at most one host — the program that embeds Control and
// registers its extensions (M15) — and that host names what it registered in
// its own catalogue, under keys `<host>:<key>`, where <host> is its
// `<owner>/<name>` (M27). Control's keys never carry a ":", so no key of a
// host's can be one of Control's.
//
// A report pack is named here (0056): a descriptor carries codes, not titles
// (ext.ReportDescriptor), and the pack's `provider` is its registration's —
// by convention a module path — which is not the host, so the key is the
// host's and carries the pack's provider and the report's code:
//
//     <host>:reports.<provider>.<report>                    the report's name
//     <host>:reports.<provider>.<report>.parameters.<name>  a parameter's label
//
// Where no catalogue names one, the console names it by its code (M21).
//
// The shell provides the catalogue through HostCatalogueContext. Nothing
// hands a host's catalogue to the shell yet: server.Options has no field for
// a host's console (0049's learnings, "What a host's console field on
// server.Options must carry"), so until the phase that adds one, every
// report is named by its code.

import { IntlMessageFormat } from "intl-messageformat";
import { createContext, useContext } from "react";
import { intlLocale } from "./locale";

/** Control's catalogue keys carry no ":"; a host's are `<host>:<key>`. */
export const HOST_SEPARATOR = ":";

export interface HostCatalogue {
  /** The host's `<owner>/<name>`. */
  host: string;
  /** Its catalogue for the active locale: every key `<host>:<key>`, each an ICU message with no arguments. */
  messages: Readonly<Record<string, string>>;
}

export const HostCatalogueContext = createContext<HostCatalogue | null>(null);

/** The key a host names one of its packs' reports under. */
export function reportNameKey(host: string, provider: string, report: string): string {
  return `${host}${HOST_SEPARATOR}reports.${provider}.${report}`;
}

/** The key a host labels one of a report's parameters under. */
export function reportParameterKey(host: string, provider: string, report: string, name: string): string {
  return `${reportNameKey(host, provider, report)}.parameters.${name}`;
}

/**
 * The host's sentence for a key, or undefined: no host catalogue, a key that
 * is not the host's own, a key it does not hold, or a message that does not
 * format. Undefined is the caller's cue to show the code.
 */
export function hostText(catalogue: HostCatalogue | null, key: string, locale: string): string | undefined {
  if (!catalogue || !key.startsWith(catalogue.host + HOST_SEPARATOR)) return undefined;
  const message = catalogue.messages[key];
  if (typeof message !== "string" || message === "") return undefined;
  try {
    const out = new IntlMessageFormat(message, intlLocale(locale)).format();
    return Array.isArray(out) ? out.join("") : String(out);
  } catch {
    return undefined;
  }
}

/** The host's catalogue in scope, and a lookup over it. */
export function useHostText(locale: string): (key: string) => string | undefined {
  const catalogue = useContext(HostCatalogueContext);
  return (key) => hostText(catalogue, key, locale);
}
