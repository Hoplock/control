// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package server

import "net/http"

// Console is a host's console: its screens, their assets and its string
// catalogue, served by one handler on the north-bound listener under the
// host's own prefix, /host-console/<Provider>/, and nowhere else (M27).
//
// Control mounts each pattern as written and serves nothing else under the
// prefix; outside it, no pattern of the host's can answer, and inside it no
// path is Control's. When a host's console is mounted, Control's shell is
// told the host's provider before its first paint and loads the host's
// catalogue for its active locale from
// /host-console/<Provider>/locales/<locale>.json, so that what the host
// registered in Control — a report pack's reports and their parameters
// (`<Provider>:reports.<pack provider>.<report>[.parameters.<name>]`) — is
// named in the host's words. The shell ignores every key in that file that
// is not <Provider>:<key>, and a catalogue that cannot be loaded leaves each
// name its code (M21); it is never an error page.
//
// The shell is served with a Content-Security-Policy that allows its own
// origin and nothing else, so the handler serves everything from this
// listener and the catalogue as JSON: an object of ICU messages, with no
// arguments, keyed by the keys in Catalogue. Loading the host's screen
// modules into the shell is not done yet.
//
// Start-up fails, before the configuration file is read, a database is
// opened or a port is bound, on: a Console with no Provider; a nil Handler or
// no Patterns; a pattern carrying a method or a space, with a "." or ".."
// segment or an empty one, or outside /host-console/<Provider>/; patterns
// none of which serves locales/en.json; a pattern the standard library's mux
// cannot mount; and a Catalogue key that is empty, not <Provider>:<key>, or
// equal to a key of Control's own catalogue.
type Console struct {
	// Patterns are ServeMux patterns without a method, absolute and under
	// /host-console/<Provider>/: "/host-console/hoplock/enterprise/assets/",
	// "/host-console/hoplock/enterprise/locales/en.json". One must serve
	// locales/en.json, the catalogue of the deployment default locale.
	Patterns []string
	// Catalogue is every key the host's catalogues hold, each
	// "<Provider>:<key>": "hoplock/enterprise:approvals.title".
	Catalogue []string
	// Handler serves every pattern. Control's panic recovery, access log,
	// body limit and request deadline apply to it as to every path on the
	// listener; its authentication does not. A console's paths are files
	// and a history fallback, never routes: they carry no access class, are
	// not in the route listing, and are served to a caller who is not
	// signed in, because the shell signs in through the API (M2). Nothing a
	// console serves may need a credential to read.
	Handler http.Handler
}
