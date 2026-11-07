// The console's one way to the north-bound API (PLAN M2, M11).
//
// Every call names a route in ./routes. It is sent with the operator's own
// session cookie (same origin: the console and the API share this listener)
// and nothing else: the console holds no credential and has no path of its
// own. An answer is one of three things, kept apart because M11 keeps them
// apart: data; a refusal (a 4xx the server meant, with its code); or an
// outage (a 5xx, or no answer at all) — which is never rendered as a denial.

import { NETWORK_FAILURE, type ApiError } from "../i18n/errors";
import { fill, ROUTES, type RouteName } from "./routes";

export type Answer<T> =
  | { kind: "ok"; status: number; data: T }
  | { kind: "refused"; status: number; error: ApiError }
  | { kind: "outage"; status: number; error: ApiError };

export interface Call {
  /** The route's wildcards. */
  params?: Record<string, string>;
  /** A query string, such as a tenant selector or a callback's parameters. */
  query?: URLSearchParams | Record<string, string>;
  /** A JSON body. */
  body?: unknown;
  signal?: AbortSignal;
}

/** A fetch the client is built over: the browser's, or a test's. */
export type Fetch = (input: string, init: RequestInit) => Promise<Response>;

/** Called when any answer says the session is gone: the shell signs out. */
export type OnUnauthenticated = () => void;

async function envelope(res: Response): Promise<ApiError> {
  try {
    const body = (await res.json()) as Partial<ApiError>;
    if (typeof body.code === "string") return body as ApiError;
  } catch {
    // Not the envelope: a proxy in front of the server, or a crash.
  }
  return { code: res.status >= 500 ? "internal" : "invalid_request" };
}

export class ApiClient {
  constructor(
    private readonly fetcher: Fetch = (input, init) => fetch(input, init),
    private onUnauthenticated: OnUnauthenticated = () => {},
  ) {}

  /** Sets what an unauthenticated answer does. */
  whenUnauthenticated(f: OnUnauthenticated): void {
    this.onUnauthenticated = f;
  }

  /** The URL a call goes to: always the route's own pattern, filled. */
  static url(name: RouteName, call: Call = {}): string {
    const route = ROUTES[name];
    const path = fill(route.pattern, call.params ?? {});
    const query = call.query instanceof URLSearchParams ? call.query : new URLSearchParams(call.query ?? {});
    const q = query.toString();
    return q ? `${path}?${q}` : path;
  }

  async call<T>(name: RouteName, call: Call = {}): Promise<Answer<T>> {
    const route = ROUTES[name];
    const init: RequestInit = {
      method: route.method,
      credentials: "same-origin",
      headers: { Accept: "application/json" },
      signal: call.signal,
    };
    if (call.body !== undefined) {
      init.body = JSON.stringify(call.body);
      init.headers = { ...init.headers, "Content-Type": "application/json" };
    }
    let res: Response;
    try {
      res = await this.fetcher(ApiClient.url(name, call), init);
    } catch (err) {
      if (err instanceof DOMException && err.name === "AbortError") throw err;
      return { kind: "outage", status: 0, error: { code: NETWORK_FAILURE } };
    }
    if (res.ok) {
      const data = res.status === 204 ? (undefined as T) : ((await res.json()) as T);
      return { kind: "ok", status: res.status, data };
    }
    const error = await envelope(res);
    if (res.status >= 500) return { kind: "outage", status: res.status, error };
    if (res.status === 401 && name !== "localLogin" && name !== "federatedCallback") this.onUnauthenticated();
    return { kind: "refused", status: res.status, error };
  }
}
