// Sign-in (0049; PLAN M2, M7, M18). The console has no login of its own: it
// lists the session methods the API offers (GET /api/v1/session/methods) and
// starts the API's own flow, so the session it ends with is the API's —
// the same HttpOnly cookie a browser gets from curl's flow, and nothing else.
//
// OIDC: start returns the identity provider's URL and a state; the provider
// sends the browser back to the connector's redirect_uri, which a deployment
// sets to /console/sign-in/callback/<connector>; the callback page hands the
// provider's parameters to the API's callback, which sets the cookie.
//
// With several tenants, the methods are a tenant's (M18): the page asks which,
// and only then. Break-glass is the API's local login (M7), shown apart,
// because it is not a normal way in.

import { useEffect, useState, type FormEvent } from "react";
import type { ApiClient } from "../api/client";
import type { FederationStart, SessionIssued, SignInMethods } from "../api/types";
import { ErrorText } from "../components/ErrorText";
import { VisuallyHidden } from "../components/VisuallyHidden";
import { useI18n } from "../i18n/I18n";
import { loadPrefs, savePrefs } from "../prefs/prefs";
import { safeReturn } from "../router/paths";
import { go, navigate, useLocation } from "../router/router";
import { fromAnswer, type Load } from "../shell/load";
import { savePending, takePending } from "./pending";
import styles from "./SignIn.module.css";

/** The command an administrator adds a connector with: an identifier, not prose. */
const CONNECTOR_COMMAND = "hoplock-control identity connector-put";

function tenantQuery(tenant: string | null): Record<string, string> {
  return tenant ? { tenant } : {};
}

function remember(tenant: string | null): void {
  if (tenant) savePrefs(undefined, { ...loadPrefs(), tenant });
}

export function SignIn({ api, onSignedIn }: { api: ApiClient; onSignedIn(): void }) {
  const { t } = useI18n();
  const location = useLocation();
  const returnTo = safeReturn(location.search.get("return"));
  const [tenant, setTenant] = useState<string | null>(location.search.get("tenant"));
  const [draft, setDraft] = useState(tenant ?? "");
  const [methods, setMethods] = useState<Load<SignInMethods>>({ state: "loading" });
  const [failure, setFailure] = useState<Load<never> | null>(null);
  const [breakGlass, setBreakGlass] = useState(false);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    const abort = new AbortController();
    setMethods({ state: "loading" });
    api.call<SignInMethods>("sessionMethods", { query: tenantQuery(tenant), signal: abort.signal }).then(
      (a) => setMethods(fromAnswer(a)),
      () => {},
    );
    return () => abort.abort();
  }, [api, tenant]);

  const needsTenant = methods.state === "refused" && methods.error.code === "tenant_ambiguous";

  const start = async (connector: string) => {
    setBusy(true);
    setFailure(null);
    const a = await api.call<FederationStart>("federatedStart", { params: { connector }, query: tenantQuery(tenant) });
    if (a.kind !== "ok") {
      setFailure(fromAnswer(a) as Load<never>);
      setBusy(false);
      return;
    }
    savePending({ state: a.data.state, connector, returnTo, ...(tenant ? { tenant } : {}) });
    window.location.assign(a.data.redirect_url);
  };

  const local = async (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    setBusy(true);
    setFailure(null);
    const a = await api.call<SessionIssued>("localLogin", {
      query: tenantQuery(tenant),
      body: { login: String(form.get("login") ?? ""), password: String(form.get("password") ?? "") },
    });
    setBusy(false);
    if (a.kind !== "ok") {
      setFailure(fromAnswer(a) as Load<never>);
      return;
    }
    remember(tenant);
    onSignedIn();
    go(returnTo, { replace: true });
  };

  return (
    <main className={styles.page} id="content">
      <section className={styles.card} aria-labelledby="sign-in-title" aria-busy={busy || undefined}>
        <h1 id="sign-in-title" className={styles.title}>
          {t("signIn.title", { product: t("app.name") })}
        </h1>

        {needsTenant ? (
          <form
            className={styles.form}
            onSubmit={(e) => {
              e.preventDefault();
              setTenant(draft.trim() || null);
            }}
          >
            <h2 className={styles.heading}>{t("signIn.tenant.heading")}</h2>
            <p className={styles.help}>{t("signIn.tenant.help")}</p>
            <label className={styles.field}>
              <span className={styles.label}>{t("signIn.tenant.label")}</span>
              <input
                className={styles.input}
                name="tenant"
                value={draft}
                onChange={(e) => setDraft(e.currentTarget.value)}
                autoComplete="organization"
                spellCheck={false}
                dir="ltr"
                required
              />
            </label>
            <button type="submit" className={styles.primary}>
              {t("signIn.tenant.continue")}
            </button>
          </form>
        ) : methods.state === "loading" ? (
          <div className={styles.skeleton} aria-busy="true">
            <VisuallyHidden>{t("common.loading")}</VisuallyHidden>
          </div>
        ) : methods.state === "ok" ? (
          <>
            <h2 className={styles.heading}>{t("signIn.methods.heading")}</h2>
            {methods.data.methods.length === 0 ? (
              <p className={styles.help}>{t("signIn.methods.none", { command: CONNECTOR_COMMAND })}</p>
            ) : (
              <ul className={styles.methods}>
                {methods.data.methods.map((m) => (
                  <li key={m.name}>
                    {m.kind === "oidc" ? (
                      <button type="button" className={styles.primary} disabled={busy} onClick={() => start(m.name)}>
                        {t("signIn.method.continue", { name: m.display_name || m.name })}
                      </button>
                    ) : (
                      <p className={styles.help}>{t("signIn.method.saml", { name: m.display_name || m.name })}</p>
                    )}
                  </li>
                ))}
              </ul>
            )}
            {tenant ? (
              <button type="button" className={styles.quiet} onClick={() => setTenant(null)}>
                {t("signIn.tenant.change")}
              </button>
            ) : null}
            {methods.data.local_login ? (
              <div className={styles.breakGlass}>
                <button
                  type="button"
                  className={styles.quiet}
                  aria-expanded={breakGlass}
                  aria-controls="break-glass"
                  onClick={() => setBreakGlass(!breakGlass)}
                >
                  {t("signIn.breakGlass.toggle")}
                </button>
                {breakGlass ? (
                  <form id="break-glass" className={styles.form} onSubmit={local}>
                    <h2 className={styles.heading}>{t("signIn.breakGlass.heading")}</h2>
                    <p className={styles.help}>{t("signIn.breakGlass.help")}</p>
                    <label className={styles.field}>
                      <span className={styles.label}>{t("signIn.breakGlass.login")}</span>
                      <input className={styles.input} name="login" autoComplete="username" dir="ltr" required />
                    </label>
                    <label className={styles.field}>
                      <span className={styles.label}>{t("signIn.breakGlass.password")}</span>
                      <input
                        className={styles.input}
                        name="password"
                        type="password"
                        autoComplete="current-password"
                        required
                      />
                    </label>
                    <button type="submit" className={styles.warning} disabled={busy}>
                      {t("signIn.breakGlass.submit")}
                    </button>
                  </form>
                ) : null}
              </div>
            ) : null}
          </>
        ) : (
          <ErrorText error={methods.error} kind={methods.state} />
        )}

        {failure && failure.state !== "loading" && failure.state !== "ok" ? (
          <ErrorText error={failure.error} kind={failure.state} />
        ) : null}
      </section>
    </main>
  );
}

export function SignInCallback({ api, connector, onSignedIn }: { api: ApiClient; connector: string; onSignedIn(): void }) {
  const { t } = useI18n();
  const location = useLocation();
  const [result, setResult] = useState<Load<SessionIssued> | "expired">({ state: "loading" });

  useEffect(() => {
    const pending = takePending();
    const state = location.search.get("state");
    if (!pending || pending.connector !== connector || !state || pending.state !== state) {
      setResult("expired");
      return;
    }
    const query = new URLSearchParams(location.search);
    if (pending.tenant) query.set("tenant", pending.tenant);
    api.call<SessionIssued>("federatedCallback", { params: { connector }, query }).then((a) => {
      setResult(fromAnswer(a));
      if (a.kind === "ok") {
        remember(pending.tenant ?? null);
        onSignedIn();
        go(safeReturn(pending.returnTo), { replace: true });
      }
    });
    // The provider's parameters are read once, on arrival.
  }, []);

  const again = () => navigate("sign-in", { replace: true });

  return (
    <main className={styles.page} id="content">
      <section className={styles.card} aria-labelledby="sign-in-title">
        <h1 id="sign-in-title" className={styles.title}>
          {t("signIn.title", { product: t("app.name") })}
        </h1>
        {result === "expired" ? (
          <p className={styles.help}>{t("signIn.callback.expired")}</p>
        ) : result.state === "loading" || result.state === "ok" ? (
          <p className={styles.help} aria-live="polite">
            {t("signIn.callback.working")}
          </p>
        ) : (
          <ErrorText error={result.error} kind={result.state} />
        )}
        {result === "expired" || result.state === "refused" || result.state === "outage" ? (
          <button type="button" className={styles.primary} onClick={again}>
            {t("signIn.again")}
          </button>
        ) : null}
      </section>
    </main>
  );
}
