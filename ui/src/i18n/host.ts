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
// The shell is told the host before its first paint, on <html> as the stripe
// is: `data-host` (the host's `<owner>/<name>`) and `data-host-catalogue` (the
// directory its catalogues are served from, inside the host's own
// `/host-console/<host>/`). The server writes both only when the host hands
// Control a console (server.Options.Console, 0069), and the console never
// guesses a host from anything else: with neither, there is no host, and
// nothing is requested under `/host-console/`.
//
// The shell loads `<catalogue><locale>.json` for the active locale — English,
// the only one that ships (M21), whose catalogue a pseudolocale build
// pseudolocalises here as it does Control's — from this origin (the shell's
// CSP allows no other), keeps the keys that are `<host>:<key>` and drops every
// other, and provides it through HostCatalogueContext. A catalogue that cannot
// be loaded is `failed`: every name falls back to its code, and a screen that
// would have named something through it says so as a gap (M21) — never an
// error page.

import type { MessageFormatElement } from "@formatjs/icu-messageformat-parser";
import { IntlMessageFormat } from "intl-messageformat";
import { createContext, useContext } from "react";
import { FORCED_LOCALE } from "./catalogue";
import { intlLocale } from "./locale";
import { pseudolocalise } from "./pseudo";

/** Control's catalogue keys carry no ":"; a host's are `<host>:<key>`. */
export const HOST_SEPARATOR = ":";

/** A host's `<owner>/<name>`, as the server refuses any other (PLAN M27). */
const PROVIDER = /^[a-z0-9][a-z0-9-]*\/[a-z0-9][a-z0-9-]*$/;

/** Where every host's console lives (PLAN M27). */
export const HOST_CONSOLE_PREFIX = "/host-console/";

/** How long the shell waits for a host's catalogue before naming the gap. */
export const HOST_CATALOGUE_TIMEOUT_MS = 10_000;

export interface HostCatalogue {
  /** The host's `<owner>/<name>`. */
  host: string;
  /**
   * `loading` until the catalogue's request settles; `ok` once it has been
   * read; `failed` when it could not be, which a screen names as a gap.
   */
  state: "loading" | "ok" | "failed";
  /** Its catalogue for the active locale: every key `<host>:<key>`, each an ICU message with no arguments. */
  messages: Readonly<Record<string, string | MessageFormatElement[]>>;
}

export const HostCatalogueContext = createContext<HostCatalogue | null>(null);

/** A host's console, as the page names it. */
export interface DeclaredHost {
  host: string;
  /** The directory its catalogues are served from, ending in "/". */
  catalogue: string;
}

/**
 * The host the page names, or null: none named, or one named in a shape the
 * server never writes — a provider that is not `<owner>/<name>`, or a
 * catalogue outside the host's own `/host-console/<host>/`.
 */
export function declaredHost(root: { dataset: DOMStringMap } | null = typeof document === "undefined" ? null : document.documentElement): DeclaredHost | null {
  const host = root?.dataset.host;
  const catalogue = root?.dataset.hostCatalogue;
  if (!host || !catalogue || !PROVIDER.test(host)) return null;
  const own = `${HOST_CONSOLE_PREFIX}${host}/`;
  const rest = catalogue.slice(own.length);
  if (!catalogue.startsWith(own) || !catalogue.endsWith("/")) return null;
  if (rest.split("/").slice(0, -1).some((seg) => seg === "" || seg === "." || seg === "..")) return null;
  return { host, catalogue };
}

/** The URL of a host's catalogue for a locale: a pseudolocale reads English's. */
export function hostCatalogueURL(declared: DeclaredHost, locale: string): string {
  return `${declared.catalogue}${intlLocale(locale)}.json`;
}

/**
 * A loaded catalogue's messages, keeping only the host's own: a key that is
 * not `<host>:<key>`, a value that is not a non-empty string, and — in a
 * pseudolocale build — a message that does not parse, are each dropped.
 */
export function hostMessages(host: string, body: unknown, locale: string): Record<string, string | MessageFormatElement[]> {
  const out: Record<string, string | MessageFormatElement[]> = {};
  if (typeof body !== "object" || body === null || Array.isArray(body)) return out;
  const prefix = host + HOST_SEPARATOR;
  for (const [key, message] of Object.entries(body as Record<string, unknown>)) {
    if (!key.startsWith(prefix) || key.length === prefix.length || typeof message !== "string" || message === "") continue;
    if (FORCED_LOCALE !== null && locale === FORCED_LOCALE) {
      try {
        out[key] = pseudolocalise(message, FORCED_LOCALE as "en-XA" | "en-XB");
      } catch {
        // A message that does not parse names nothing; its code stands.
      }
      continue;
    }
    out[key] = message;
  }
  return out;
}

/** A fetch the loader is built over: the browser's, or a test's. */
export type HostFetch = (input: string, init: RequestInit) => Promise<Response>;

/**
 * Loads the host's catalogue for a locale. It never throws: an answer that is
 * not 2xx JSON, no answer, or none within the timeout is `failed`. Only an
 * abort by the caller rejects.
 */
export async function loadHostCatalogue(
  declared: DeclaredHost,
  locale: string,
  fetcher: HostFetch = (input, init) => fetch(input, init),
  signal?: AbortSignal,
): Promise<HostCatalogue> {
  const failed: HostCatalogue = { host: declared.host, state: "failed", messages: {} };
  const timeout = new AbortController();
  const timer = setTimeout(() => timeout.abort(), HOST_CATALOGUE_TIMEOUT_MS);
  const onAbort = () => timeout.abort();
  signal?.addEventListener("abort", onAbort);
  try {
    const res = await fetcher(hostCatalogueURL(declared, locale), {
      method: "GET",
      credentials: "same-origin",
      headers: { Accept: "application/json" },
      signal: timeout.signal,
    });
    if (!res.ok) return failed;
    return { host: declared.host, state: "ok", messages: hostMessages(declared.host, await res.json(), locale) };
  } catch (err) {
    if (signal?.aborted) throw err;
    return failed;
  } finally {
    clearTimeout(timer);
    signal?.removeEventListener("abort", onAbort);
  }
}

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
  if (!catalogue || catalogue.state !== "ok" || !key.startsWith(catalogue.host + HOST_SEPARATOR)) return undefined;
  const message = catalogue.messages[key];
  if (message === undefined || message.length === 0) return undefined;
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
