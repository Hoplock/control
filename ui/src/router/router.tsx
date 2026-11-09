// The console's router: the History API, under /console/ (./paths).
//
// Small on purpose. A route is a path pattern with :params, inside the
// console; navigation is pushState plus one event; a link is a real <a> whose
// href works when opened in a new tab, which a deep link must.

import { useEffect, useState, type AnchorHTMLAttributes, type MouseEvent } from "react";
import { consoleHref, insideConsole } from "./paths";

const NAVIGATED = "hoplock:navigated";

export interface Location {
  /** The path inside the console, without the prefix or a leading slash. */
  path: string;
  search: URLSearchParams;
}

function current(): Location {
  return { path: insideConsole(window.location.pathname) ?? "", search: new URLSearchParams(window.location.search) };
}

export function useLocation(): Location {
  const [location, setLocation] = useState(current);
  useEffect(() => {
    const update = () => setLocation(current());
    window.addEventListener("popstate", update);
    window.addEventListener(NAVIGATED, update);
    return () => {
      window.removeEventListener("popstate", update);
      window.removeEventListener(NAVIGATED, update);
    };
  }, []);
  return location;
}

/** Goes to a path inside the console. */
export function navigate(path: string, opts: { replace?: boolean; query?: Record<string, string> } = {}): void {
  const href = consoleHref(path, opts.query);
  if (opts.replace) window.history.replaceState(null, "", href);
  else window.history.pushState(null, "", href);
  window.dispatchEvent(new Event(NAVIGATED));
}

/** Matches a path against a pattern ("audit/records/:record"), or null. */
export function match(pattern: string, path: string): Record<string, string> | null {
  const want = pattern.split("/").filter(Boolean);
  const got = path.split("/").filter(Boolean);
  if (want.length !== got.length) return null;
  const params: Record<string, string> = {};
  for (let i = 0; i < want.length; i++) {
    const w = want[i] as string;
    const g = got[i] as string;
    if (w.startsWith(":")) params[w.slice(1)] = decodeURIComponent(g);
    else if (w !== g) return null;
  }
  return params;
}

export type LinkProps = Omit<AnchorHTMLAttributes<HTMLAnchorElement>, "href"> & {
  to: string;
  query?: Record<string, string>;
};

/** A link inside the console. */
export function Link({ to, query, onClick, ...rest }: LinkProps) {
  const href = consoleHref(to, query);
  const go = (e: MouseEvent<HTMLAnchorElement>) => {
    onClick?.(e);
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    e.preventDefault();
    navigate(to, query ? { query } : {});
  };
  return <a {...rest} href={href} onClick={go} />;
}

/** Goes to a full console URL, such as a sign-in's return path. */
export function go(href: string, opts: { replace?: boolean } = {}): void {
  const url = new URL(href, window.location.origin);
  if (url.origin !== window.location.origin || insideConsole(url.pathname) === null) {
    throw new Error(`${href} is not inside the console`);
  }
  if (opts.replace) window.history.replaceState(null, "", url.pathname + url.search + url.hash);
  else window.history.pushState(null, "", url.pathname + url.search + url.hash);
  window.dispatchEvent(new Event(NAVIGATED));
}
