// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
)

// A CONSOLE IS MOUNTED INSIDE ITS OWN PREFIX AND NOWHERE ELSE (M27, M20, M21).
//
// Control's console (0045) and a host's screens are served from this listener,
// and each is a single-page app whose history fallback answers a deep link it
// does not know with its index. Served at "/", that fallback would be a
// catch-all over the whole listener, and an API typo would come back as HTML.
// So each console is mounted under its own prefix, the mount refuses any
// pattern outside it, and outside every prefix a path is Control's 404
// envelope. The bare "/" redirects to Control's console once there is one;
// nothing else redirects.
//
// A console is a north-bound API client with no privilege of its own (M2): its
// paths are files and a fallback, never routes, so they carry no access class
// and are not in the route listing. They are listed by [Server.Consoles].

// Console is one console's mount on this listener: Control's, or a host's
// screens.
type Console struct {
	// Provider is empty for Control's console, and the server's host
	// provider for a host's screens.
	Provider string
	// Patterns are what the console serves, as ServeMux patterns without a
	// method: its shell, its assets, its catalogue and the subtree its
	// history fallback answers. Each is absolute and inside the console's own
	// prefix — [ConsolePrefix], or the provider's [HostConsoleRoot].
	Patterns []string
	// Catalogue is its string catalogue's keys (M21). Control's carry no
	// ":"; a host's are its provider, ":", and a key of its own, and none is
	// one of Control's.
	Catalogue []string
	// Handler serves every pattern.
	Handler http.Handler
}

// ConsoleInfo is one mounted console pattern, as [Server.Consoles] lists it.
type ConsoleInfo struct {
	// Provider is empty for Control's console.
	Provider string `json:"provider,omitempty"`
	Pattern  string `json:"pattern"`
}

// consolePrefix is where a console of provider is mounted.
func consolePrefix(provider string) string {
	if provider == "" {
		return ConsolePrefix
	}
	return HostConsoleRoot(provider)
}

// mountConsoles mounts every console, Control's first so that a host's
// catalogue is judged against it, refusing one that is not inside its own
// prefix. host is the server's host provider: a host's screens are its.
func (r *Router) mountConsoles(host string, consoles []Console) error {
	ordered := slices.Clone(consoles)
	slices.SortStableFunc(ordered, func(a, b Console) int {
		return strings.Compare(a.Provider, b.Provider)
	})
	var control []string
	for _, c := range ordered {
		if err := r.mountConsole(host, c, control); err != nil {
			return err
		}
		if c.Provider == "" {
			control = c.Catalogue
		}
	}
	if !slices.ContainsFunc(ordered, func(c Console) bool { return c.Provider == "" }) {
		return nil
	}
	return r.handle("/{$}", http.RedirectHandler(ConsolePrefix, http.StatusFound))
}

// mountConsole mounts one console.
func (r *Router) mountConsole(host string, c Console, control []string) error {
	prefix := consolePrefix(c.Provider)
	name := "Control's console"
	if c.Provider != "" {
		name = c.Provider + "'s console"
	}
	switch {
	case c.Provider != "" && c.Provider != host:
		return fmt.Errorf("httpapi/north: %s is not this server's host, %q", name, host)
	case c.Provider != "":
		if err := checkProvider(c.Provider); err != nil {
			return err
		}
	case slices.ContainsFunc(r.consoles, func(i ConsoleInfo) bool { return i.Provider == "" }):
		return fmt.Errorf("httpapi/north: Control's console is mounted twice")
	}
	switch {
	case c.Handler == nil:
		return fmt.Errorf("httpapi/north: %s has no handler", name)
	case len(c.Patterns) == 0:
		return fmt.Errorf("httpapi/north: %s mounts nothing", name)
	}
	if err := checkCatalogue(c.Provider, c.Catalogue, control); err != nil {
		return fmt.Errorf("httpapi/north: %s: %w", name, err)
	}
	for _, pattern := range c.Patterns {
		switch {
		case strings.ContainsAny(pattern, " \t"):
			return fmt.Errorf("httpapi/north: %s pattern %q carries a method or a space", name, pattern)
		case unclean(pattern) != "":
			return fmt.Errorf("httpapi/north: %s pattern %q has %s", name, pattern, unclean(pattern))
		case !inside(pattern, prefix):
			return fmt.Errorf("httpapi/north: %s pattern %q is outside %s", name, pattern, prefix)
		}
		if err := r.handle(pattern, c.Handler); err != nil {
			return fmt.Errorf("httpapi/north: %s pattern %q: %w", name, pattern, err)
		}
		r.consoles = append(r.consoles, ConsoleInfo{Provider: c.Provider, Pattern: pattern})
	}
	return nil
}
