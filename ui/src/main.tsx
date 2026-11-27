import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./styles/tokens.css";
import "./styles/base.css";
import { ApiClient } from "./api/client";
import { App } from "./App";
import { declaredHost } from "./i18n/host";
import { HostCatalogueProvider } from "./i18n/HostCatalogueProvider";
import { directionOf } from "./i18n/locale";
import { I18nProvider, pageLocale } from "./i18n/I18n";
import { applyTheme, loadPrefs } from "./prefs/prefs";

// Before the first paint: the last operator's theme, and the locale's
// language and direction on <html> (never a hardcoded ltr, DESIGN.md §9).
// The host, when the server names one there, is read once (i18n/host.ts).
const prefs = loadPrefs();
applyTheme(prefs.theme);
const locale = pageLocale(prefs.locale ?? null);
document.documentElement.lang = locale;
document.documentElement.dir = directionOf(locale);
const host = declaredHost();

const root = document.getElementById("root");
if (!root) throw new Error("index.html has no #root");
createRoot(root).render(
  <StrictMode>
    <I18nProvider locale={locale}>
      <HostCatalogueProvider declared={host} locale={locale}>
        <App api={new ApiClient()} />
      </HostCatalogueProvider>
    </I18nProvider>
  </StrictMode>,
);
