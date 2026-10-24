// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/server"
)

// archive is a host's extension: a long-term archive Control does not ship
// (ext.ArchiveStore is disabled when nothing registers one).
type archive struct{ retention string }

func (a *archive) Archive(context.Context, []ext.AuditRecord) error { return nil }

func (a *archive) ArchiveCaptures(context.Context, []ext.Capture) error { return nil }

func (a *archive) Search(context.Context, ext.ArchiveQuery) (ext.ArchivePage, error) {
	return ext.ArchivePage{}, nil
}

// archived is what the example's list pages: each tenant's record ids, in
// order.
var archived = map[ext.Tenant][]string{"default": {"r-001", "r-002", "r-003", "r-004", "r-005"}}

// listArchive is a host's list in Control's bounded shape (M26): at most
// ?limit= record ids after the caller's position, in id order, saying where it
// stopped. It reads the tenant from the caller on every page, because the
// cursor carries a position and never a tenant.
func listArchive(w http.ResponseWriter, r *http.Request) {
	list, ok := server.ReadList(w, r, "archive", 1)
	if !ok {
		return // ReadList has answered
	}
	caller, _ := server.CallerFrom(r.Context())
	ids := archived[caller.Tenant]
	start := 0
	if list.After != nil {
		var found bool
		if start, found = slices.BinarySearch(ids, list.After[0]); found {
			start++
		}
	}
	page := ids[start:min(start+list.Limit, len(ids))]
	more := start+len(page) < len(ids)
	var last []string
	if more {
		last = []string{page[len(page)-1]}
	}
	list.Write(w, r, "records", page, more, last, nil)
}

// A host binary's main: register an extension, declare a configuration section
// and a route of each class, and start Control. A real host calls server.Main instead of
// server.Run, so that its binary carries Control's subcommands too.
func Example() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	store := &archive{}
	registry := ext.NewRegistry()
	if err := registry.RegisterArchiveStore(ext.Registration{Provider: "example/archive", Version: "v1.0.0"}, store); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}

	config, err := os.Open("config.yaml")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	defer func() { _ = config.Close() }()

	err = server.Run(ctx, config, server.Options{
		Provider: "example/host",
		Registry: registry,

		// `example:` in Control's configuration file is this host's. It is
		// handed over undecoded, before the registry is sealed.
		HostSections: []string{"example"},
		HostConfig: func(section string, raw []byte) error {
			var cfg struct {
				Retention string `yaml:"retention"`
			}
			if err := yaml.Unmarshal(raw, &cfg); err != nil {
				return err
			}
			store.retention = cfg.Retention
			return nil
		},

		Routes: []server.Route{
			// GET /api/v1/tenants/{tenant}/archive?limit=&after=, a list:
			// {"records": [...], "truncated": true, "next": "<cursor>"}.
			{
				Method:     http.MethodGet,
				Pattern:    "archive",
				Permission: "audit:read",
				Summary:    "archived records, a page at a time",
				Handler:    http.HandlerFunc(listArchive),
			},
			// GET /api/v1/tenants/{tenant}/archive/{record}, for a caller
			// holding audit:read in the tenant Control resolved.
			{
				Method:     http.MethodGet,
				Pattern:    "archive/{record}",
				Permission: "audit:read",
				Summary:    "one archived record",
				Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					caller, _ := server.CallerFrom(r.Context())
					server.WriteError(w, r, http.StatusNotFound, "archive_record_not_found",
						map[string]any{"record": r.PathValue("record"), "tenant": string(caller.Tenant)},
						"the archive holds no such record")
				}),
			},
			// GET /api/v1/cross-tenant/archive/summary, for a caller holding
			// audit:read in each tenant it covers. Control resolves the set —
			// narrowed by ?tenant=, never widened — and the answer names it.
			{
				Method:     http.MethodGet,
				Pattern:    "archive/summary",
				Permission: "audit:read",
				Access:     server.AccessCrossTenant,
				Summary:    "the archive, summarised across tenants",
				Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					caller, _ := server.CallerFrom(r.Context())
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{"tenants": caller.Tenants, "records": 0})
				}),
			},
		},
		ErrorCodes: []string{"archive_record_not_found"},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}
