// What the inventory screens share (0054): the views bar that links the five
// lists and the claim mapping (0055), a paged read of one list, how a time and a label set are shown, and
// how a failed write is reported — at the field the API named, or at the form
// (DESIGN.md §7). Every read and write goes through ApiClient by route name,
// with the operator's own session (M2).

import { useState, type ReactNode } from "react";
import type { Answer } from "../api/client";
import type { Paged } from "../api/types";
import type { RouteName } from "../api/routes";
import { Chip } from "../components/Badge";
import type { ConfirmFailure } from "../components/Dialog";
import { Ident } from "../components/Ident";
import type { PageState } from "../components/Pagination";
import { ErrorMessage } from "../components/States";
import { ViewLinks } from "../components/ViewLinks";
import { useI18n } from "../i18n/I18n";
import { Link, navigate, useLocation } from "../router/router";
import { useLoad, type Load } from "../shell/load";
import { useDeployment } from "../shell/Shell";
import { problemKey, refusedField } from "./inventoryModel";
import styles from "./Inventory.module.css";

export const DEFAULT_LIMIT = 100;

export type InventoryView = "targets" | "subjects" | "groups" | "roles" | "grants" | "mapping";

/** The inventory's lists, each its own deep link. */
export function InventoryViews({ current }: { current: InventoryView }) {
  const { t } = useI18n();
  return (
    <ViewLinks
      label={t("inventory.views")}
      current={current}
      views={[
        { id: "targets", to: "inventory/targets", label: t("inventory.view.targets") },
        { id: "subjects", to: "inventory/subjects", label: t("inventory.view.subjects") },
        { id: "groups", to: "inventory/groups", label: t("inventory.view.groups") },
        { id: "roles", to: "inventory/roles", label: t("inventory.view.roles") },
        { id: "grants", to: "inventory/grants", label: t("inventory.view.grants") },
        { id: "mapping", to: "inventory/claim-mapping", label: t("inventory.view.mapping") },
      ]}
    />
  );
}

export function usePermissions(): readonly string[] {
  const { tenant, session } = useDeployment();
  return (tenant && session.scopes[tenant]?.permissions) || [];
}

/** Rewrites some of the query, keeping the rest: filters live in the URL (DESIGN.md §4). */
export function useSetQuery(): (next: Record<string, string | undefined>) => void {
  const location = useLocation();
  return (next) => {
    const q: Record<string, string> = {};
    for (const [k, v] of location.search) q[k] = v;
    for (const [k, v] of Object.entries(next)) {
      if (v) q[k] = v;
      else delete q[k];
    }
    navigate(location.path, { replace: true, query: q });
  };
}

export function readLimit(search: URLSearchParams): number {
  const n = Number(search.get("limit"));
  return Number.isInteger(n) && n >= 1 && n <= 10000 ? n : DEFAULT_LIMIT;
}

/**
 * One page of a bounded list (M26): the page the operator is on, read by the
 * answer's own cursor, under the filters the caller names. A change of filter
 * starts again from the first page, because a cursor binds its filters and
 * the server refuses one carried across them.
 */
export function usePagedList<T extends Paged>(
  route: RouteName,
  filters: readonly [string, string][],
  limit: number,
  enabled = true,
): { load: Load<T>; refresh(): void; page: PageState; setPage(p: PageState): void } {
  const { tenant, api } = useDeployment();
  const scope = filters.map(([k, v]) => `${k}=${v}`).join("&");
  const [paging, setPaging] = useState<{ scope: string; page: PageState }>({ scope, page: { trail: [], limit } });
  const page = paging.scope === scope && paging.page.limit === limit ? paging.page : { trail: [], limit };
  const after = page.trail[page.trail.length - 1];
  const key = tenant && enabled ? `${route}|${tenant}|${scope}|${limit}|${after ?? ""}` : null;
  const [load, refresh] = useLoad<T>(key, (signal) => {
    const query = new URLSearchParams(filters as [string, string][]);
    query.set("limit", String(limit));
    if (after) query.set("after", after);
    return api.call<T>(route, { params: { tenant: tenant as string }, query, signal });
  });
  return { load, refresh, page, setPage: (p) => setPaging({ scope, page: p }) };
}

/** A one-object read that answers 404 for "there is none": the empty state, not an error. */
export function orNone<T>(a: Answer<T>): Answer<T | null> {
  if (a.kind === "refused" && a.status === 404) return { kind: "ok", status: 404, data: null };
  return a;
}

export function Instant({ at }: { at?: string }) {
  const { f } = useI18n();
  if (!at) return null;
  return (
    <time dateTime={f.utc(at)} title={f.utc(at)}>
      {f.dateTime(at)}
    </time>
  );
}

/** A label set: identifiers and operator data, so mono, LTR and never translated (DESIGN.md §9). */
export function LabelChips({ labels, none }: { labels: Readonly<Record<string, string>> | null | undefined; none?: ReactNode }) {
  const keys = Object.keys(labels ?? {}).sort();
  if (!keys.length) return none ? <span className={styles.quiet}>{none}</span> : null;
  return (
    <span className={styles.chips}>
      {keys.map((k) => (
        <Chip key={k} data>
          {`${k}=${(labels as Record<string, string>)[k]}`}
        </Chip>
      ))}
    </span>
  );
}

/** A list of identifiers as chips, each a link when `to` names where it goes. */
export function IdentChips({ items, to, none }: { items: readonly string[] | null | undefined; to?(item: string): string; none?: ReactNode }) {
  if (!items?.length) return none ? <span className={styles.quiet}>{none}</span> : null;
  return (
    <span className={styles.chips}>
      {items.map((v) =>
        to ? (
          <Link key={v} to={to(v)} className={styles.link}>
            <Ident value={v} />
          </Link>
        ) : (
          <Chip key={v} data>
            {v}
          </Chip>
        ),
      )}
    </span>
  );
}

/**
 * What a failed write says, and where: at the field the API's refusal names
 * (`invalid_request` with a field), or else at the form. Never a toast alone
 * (DESIGN.md §7).
 */
export function useFailure(fields: readonly string[]): {
  failure: ConfirmFailure | null;
  setFailure(f: ConfirmFailure | null): void;
  at(field: string): ReactNode;
  form: ReactNode;
} {
  const { t } = useI18n();
  const [failure, setFailure] = useState<ConfirmFailure | null>(null);
  const field = failure ? refusedField(failure.error) : null;
  const onField = field !== null && fields.some((f) => field === f || field.startsWith(`${f}.`));
  return {
    failure,
    setFailure,
    at: (name) => (failure && onField && (field === name || field?.startsWith(`${name}.`)) ? t(problemKey(failure.error)) : undefined),
    form: failure && !onField ? <ErrorMessage error={failure.error} kind={failure.kind} /> : null,
  };
}

/** An identifier in a table cell or a fact, linking to its detail when it has one. */
export function IdentLink({ to, value, label }: { to: string; value: string; label?: string }) {
  return (
    <Link to={to} className={styles.open} aria-label={label}>
      <span dir="ltr" translate="no">
        {value}
      </span>
    </Link>
  );
}
