// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

// The fixture's console (0069): what a host hands Control through
// server.Options.Console — here daemon.Host.Console, which it forwards to —
// so that the Reports screen's names are proven through the real mount. It
// serves one file, the host's catalogue for the deployment default locale,
// under the host's own /host-console/<provider>/ (M27), and its keys are the
// ones 0056 reads a report pack's names under:
//
//	<host>:reports.<pack provider>.<report>                    a report's name
//	<host>:reports.<pack provider>.<report>.parameters.<name>  a parameter's label
//
// It names access-review and its scope parameter, and leaves
// privileged-sessions and the owner parameter unnamed, so the screen shows
// both a name in the host's words and a code where the host gave none.

import (
	"encoding/json"
	"net/http"
	"slices"

	"github.com/hoplock/control/internal/daemon"
	"github.com/hoplock/control/internal/httpapi/north"
)

// hostCatalogue is the fixture host's English catalogue: every key it
// declares, and what it serves.
var hostCatalogue = map[string]string{
	reportKey(reportAccessReview):                       "Quarterly access review",
	reportKey(reportAccessReview) + ".parameters.scope": "Who the review covers",
}

func reportKey(report string) string {
	return hostProvider + ":reports." + compliancePack + "." + report
}

// hostConsole is the fixture's console: its catalogue, and nothing else.
func hostConsole() (*daemon.HostConsole, error) {
	body, err := json.Marshal(hostCatalogue)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(hostCatalogue))
	for k := range hostCatalogue {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	catalogue := north.HostCatalogueDefault(hostProvider)
	return &daemon.HostConsole{
		Patterns:  []string{catalogue},
		Catalogue: keys,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path != catalogue:
				http.NotFound(w, r)
			case r.Method != http.MethodGet && r.Method != http.MethodHead:
				w.Header().Set("Allow", "GET, HEAD")
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			default:
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("X-Content-Type-Options", "nosniff")
				_, _ = w.Write(body)
			}
		}),
	}, nil
}
