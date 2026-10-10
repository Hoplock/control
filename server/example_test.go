// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package server_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"

	"gopkg.in/yaml.v3"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/server"
)

// archive is a host's extension: a long-term archive Control does not ship
// (ext.ArchiveStore is disabled when nothing registers one).
type archive struct{ retention string }

func (a *archive) Archive(context.Context, []ext.AuditRecord) error { return nil }

func (a *archive) Search(context.Context, ext.ArchiveQuery) (ext.ArchivePage, error) {
	return ext.ArchivePage{}, nil
}

// A host binary's main: register an extension, declare a configuration section
// and a route, and start Control. A real host calls server.Main instead of
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

		// GET /api/v1/tenants/{tenant}/archive/{record}, for a caller holding
		// audit:read in the tenant Control resolved.
		Routes: []server.Route{{
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
		}},
		ErrorCodes: []string{"archive_record_not_found"},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}
