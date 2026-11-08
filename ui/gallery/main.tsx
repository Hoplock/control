// THE COMPONENT GALLERY (0050): every component of the set, in every state it
// defines, on one page per section — what the browser checks (axe, the
// screenshot baseline, the pseudolocales, reduced motion) run over for the
// components, and what a console PR's screenshots are taken from.
//
// It is a TEST PAGE: built by vite.gallery.config.ts into build/gallery-*,
// never into internal/console/dist, and served at /console/gallery by the E2E
// run's own interception in the browser — no route the server binds serves it
// (docs/PROTOCOL.md §3). Its own scaffolding (section names, state names) is
// developer text marked translate="no"; everything a component shows comes
// from the catalogue, so the pseudolocale run checks the components here.

import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "../src/styles/tokens.css";
import "../src/styles/base.css";
import { directionOf } from "../src/i18n/locale";
import { I18nProvider, pageLocale } from "../src/i18n/I18n";
import { Gallery } from "./Gallery";

const locale = pageLocale(null);
document.documentElement.lang = locale;
document.documentElement.dir = directionOf(locale);
const params = new URLSearchParams(window.location.search);
if (params.get("density") === "compact") document.documentElement.setAttribute("data-density", "compact");

const root = document.getElementById("root");
if (!root) throw new Error("gallery.html has no #root");
createRoot(root).render(
  <StrictMode>
    <I18nProvider locale={locale}>
      <Gallery section={params.get("section") ?? "buttons"} />
    </I18nProvider>
  </StrictMode>,
);
