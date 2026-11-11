// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Command host is the server the console's browser checks run against (0050,
// 0055): Hoplock Control exactly as hoplock-control is, plus one identity
// sync, registered in the host's registry as a host registers its own (M15).
// It starts Control through internal/daemon, which server.Main forwards to,
// because nothing in this module but cmd/hoplock-control may import the
// public façade (architecture_test.go).
//
// A directory-managed subject exists only where a sync provisioned it, and no
// route Control binds can make one, so the fixture's directory is a host's
// extension rather than a test path on the server: at Start the sync applies
// the `e2e_directory` section of the configuration through Control's own
// ext.SyncSink — groups, identities, deactivations — and then does nothing.
// Everything it writes is what a real directory's sync would write, through
// the same code. It binds nothing hoplock-control does not, and it is never
// built into a release: it is package main under ui/e2e, which the module's
// public surface excludes (M23).
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/daemon"
)

// provider names the fixture as a host, and its sync on every record it
// writes (`identity-sync:e2e-directory`).
const (
	hostProvider = "hoplock/e2e-fixture"
	syncProvider = "e2e-directory"
	section      = "e2e_directory"
)

// directory is the section: what the fixture's directory holds.
type directory struct {
	Tenant   string   `yaml:"tenant"`
	Groups   []group  `yaml:"groups"`
	Subjects []person `yaml:"subjects"`
	// DeactivatedBeforeCreate are identities the directory deactivates before
	// it ever creates them: Control keeps the deactivation, and the subject
	// is listed by it alone (record_missing).
	DeactivatedBeforeCreate []deactivation `yaml:"deactivated_before_create"`
}

type group struct {
	Source     string `yaml:"source"`
	ExternalID string `yaml:"external_id"`
	Name       string `yaml:"name"`
}

type person struct {
	Source      string            `yaml:"source"`
	ExternalID  string            `yaml:"external_id"`
	Username    string            `yaml:"username"`
	DisplayName string            `yaml:"display_name"`
	Email       string            `yaml:"email"`
	Groups      []string          `yaml:"groups"`
	Attributes  map[string]string `yaml:"attributes"`
	// Deactivate, when set, is the reason code the directory deactivates
	// the identity with once it has created it.
	Deactivate string `yaml:"deactivate"`
}

type deactivation struct {
	Source     string `yaml:"source"`
	ExternalID string `yaml:"external_id"`
	ReasonCode string `yaml:"reason_code"`
}

// updatedAt is the directory's own modification time for everything it
// holds: one fixed instant, so a run is the same as the last.
var updatedAt = time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)

// fixtureSync applies the section once, at Start.
type fixtureSync struct {
	mu  sync.Mutex
	dir *directory
}

func (s *fixtureSync) configure(raw []byte) error {
	var d directory
	if err := yaml.Unmarshal(raw, &d); err != nil {
		return fmt.Errorf("%s: %w", section, err)
	}
	if d.Tenant == "" {
		return fmt.Errorf("%s: tenant is required", section)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dir = &d
	return nil
}

func (s *fixtureSync) Start(ctx context.Context, sink ext.SyncSink) error {
	s.mu.Lock()
	d := s.dir
	s.mu.Unlock()
	if d == nil {
		return nil // a subcommand, or a configuration with no directory
	}
	tenant := ext.Tenant(d.Tenant)
	for _, g := range d.Groups {
		if err := sink.UpsertGroup(ctx, ext.SyncedGroup{
			Tenant: tenant, Source: g.Source, ExternalID: g.ExternalID, Name: g.Name, UpdatedAt: updatedAt,
		}); err != nil {
			return fmt.Errorf("group %s/%s: %w", g.Source, g.ExternalID, err)
		}
	}
	for _, p := range d.Subjects {
		if err := sink.UpsertSubject(ctx, ext.SyncedSubject{
			Tenant: tenant, Source: p.Source, ExternalID: p.ExternalID, Username: p.Username,
			DisplayName: p.DisplayName, Email: p.Email, Groups: p.Groups, Active: true,
			Attributes: p.Attributes, UpdatedAt: updatedAt,
		}); err != nil {
			return fmt.Errorf("subject %s/%s: %w", p.Source, p.ExternalID, err)
		}
		if p.Deactivate == "" {
			continue
		}
		ref := ext.DirectoryRef{Tenant: tenant, Source: p.Source, ExternalID: p.ExternalID}
		if err := sink.DeactivateSubject(ctx, ref, p.Deactivate); err != nil {
			return fmt.Errorf("deactivate %s/%s: %w", p.Source, p.ExternalID, err)
		}
	}
	for _, a := range d.DeactivatedBeforeCreate {
		ref := ext.DirectoryRef{Tenant: tenant, Source: a.Source, ExternalID: a.ExternalID}
		if err := sink.DeactivateSubject(ctx, ref, a.ReasonCode); err != nil {
			return fmt.Errorf("deactivate %s/%s before its create: %w", a.Source, a.ExternalID, err)
		}
	}
	return nil
}

func (s *fixtureSync) Stop(context.Context) error { return nil }

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx)
	stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e host: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	s := &fixtureSync{}
	registry := ext.NewRegistry()
	if err := registry.RegisterIdentitySync(ext.Registration{Provider: syncProvider, Version: "e2e"}, s); err != nil {
		return err
	}
	return daemon.Main(ctx, os.Args[1:], os.Stdout, os.Stderr, daemon.Host{
		Provider: hostProvider,
		Registry: registry,
		Sections: []string{section},
		Config: func(name string, raw []byte) error {
			if name != section {
				return errors.New("unknown section " + name)
			}
			return s.configure(raw)
		},
	})
}
