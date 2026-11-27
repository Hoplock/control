// The host's catalogue, loaded once for the page's locale and provided to
// every screen beneath (PLAN M27, M21; ./host.ts). With no host named on the
// page there is nothing to load and no request is made: the context is null,
// exactly as it was before a host could hand Control a console.

import { useEffect, useState, type ReactNode } from "react";
import { HostCatalogueContext, loadHostCatalogue, type DeclaredHost, type HostCatalogue, type HostFetch } from "./host";

export function HostCatalogueProvider({
  declared,
  locale,
  fetcher,
  children,
}: {
  declared: DeclaredHost | null;
  locale: string;
  fetcher?: HostFetch;
  children: ReactNode;
}) {
  const [catalogue, setCatalogue] = useState<HostCatalogue | null>(() =>
    declared ? { host: declared.host, state: "loading", messages: {} } : null,
  );
  useEffect(() => {
    if (!declared) {
      setCatalogue(null);
      return;
    }
    const abort = new AbortController();
    setCatalogue({ host: declared.host, state: "loading", messages: {} });
    loadHostCatalogue(declared, locale, fetcher, abort.signal).then(
      (loaded) => setCatalogue(loaded),
      () => {
        // Aborted: this page moved on, and the next load answers.
      },
    );
    return () => abort.abort();
  }, [declared?.host, declared?.catalogue, locale, fetcher]);
  return <HostCatalogueContext.Provider value={catalogue}>{children}</HostCatalogueContext.Provider>;
}
