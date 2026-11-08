// The console's root: the deployment stripe on every page, then sign-in or
// the signed-in shell, decided by asking the API who this browser is.

import { useEffect, useState } from "react";
import type { ApiClient } from "./api/client";
import type { Session } from "./api/types";
import { ErrorMessage } from "./components/States";
import { VisuallyHidden } from "./components/VisuallyHidden";
import { useI18n } from "./i18n/I18n";
import { consoleHref, safeReturn } from "./router/paths";
import { go, match, navigate, useLocation } from "./router/router";
import { Shell } from "./shell/Shell";
import { fromAnswer, type Load } from "./shell/load";
import { SignIn, SignInCallback } from "./signin/SignIn";
import styles from "./App.module.css";

export function App({ api }: { api: ApiClient }) {
  const { t } = useI18n();
  const location = useLocation();
  const [session, setSession] = useState<Load<Session>>({ state: "loading" });
  const [tick, setTick] = useState(0);

  useEffect(() => {
    document.title = t("app.name");
  }, [t]);

  useEffect(() => {
    api.whenUnauthenticated(() => setSession({ state: "refused", status: 401, error: { code: "unauthenticated" } }));
  }, [api]);

  useEffect(() => {
    let live = true;
    api.call<Session>("session").then((a) => {
      if (live) setSession(fromAnswer(a));
    });
    return () => {
      live = false;
    };
  }, [api, tick]);

  const signingIn = location.path === "sign-in" || location.path.startsWith("sign-in/");
  // Asking again forgets the last answer first, so a refusal from before a
  // sign-in never sends the operator back to sign in once more.
  const reload = () => {
    setSession({ state: "loading" });
    setTick((n) => n + 1);
  };

  // Signed in already: the sign-in page has nothing to do but go back.
  useEffect(() => {
    if (location.path === "sign-in" && session.state === "ok") {
      go(safeReturn(location.search.get("return")), { replace: true });
    }
  }, [location.path, location.search, session.state]);

  useEffect(() => {
    if (!signingIn && session.state === "refused") {
      const here = consoleHref(location.path) + (location.search.size ? `?${location.search}` : "");
      navigate("sign-in", { replace: true, query: { return: here } });
    }
  }, [signingIn, session.state, location.path, location.search]);

  let page;
  const callback = match("sign-in/callback/:connector", location.path);
  if (callback) {
    page = <SignInCallback api={api} connector={callback.connector as string} onSignedIn={reload} />;
  } else if (signingIn) {
    page = <SignIn api={api} onSignedIn={reload} />;
  } else if (session.state === "ok") {
    page = <Shell api={api} session={session.data} onSignedOut={reload} />;
  } else if (session.state === "outage") {
    page = (
      <main className={styles.alone} id="content">
        <ErrorMessage error={session.error} kind="outage" />
        <button type="button" className={styles.retry} onClick={reload}>
          {t("common.retry")}
        </button>
      </main>
    );
  } else {
    page = (
      <main className={styles.alone} id="content" aria-busy="true">
        <VisuallyHidden>{t("app.loading")}</VisuallyHidden>
      </main>
    );
  }

  return (
    <>
      <div className={styles.stripe} aria-hidden="true" />
      {page}
    </>
  );
}
