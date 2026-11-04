// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"
)

// runSupervision is the local control over the supervisory registration
// (0046, M19): an operator stops it — and starts it again — from their own
// deployment, without a restart and without the supervisor's cooperation.
//
//	hoplock-control supervision status  [--config F]
//	hoplock-control supervision revoke  [--config F] --by NAME
//	hoplock-control supervision restore [--config F]
//
// It writes the deployment's record, which every node reads within one
// supervisor heartbeat: the node holding the registration drops it, and no
// node registers again until it is restored. It is a command with database
// access rather than an API route because it is the DEPLOYMENT's operator's,
// not any one tenant's: a tenant administrator revokes a supervisor's
// credential in their tenant (`POST …/instance/supervisors/{principal}/revoke`).
func runSupervision(ctx context.Context, h Host, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("supervision: a command is required (status, revoke, restore)")
	}
	cmd := args[0]
	fs := flag.NewFlagSet("hoplock-control supervision "+cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", defaultConfigPath, "path to the YAML configuration file")
	by := fs.String("by", "", "who is revoking, for the record (revoke only; required)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	switch cmd {
	case "status", "revoke", "restore":
	default:
		return fmt.Errorf("supervision: unknown command %q (known: status, revoke, restore)", cmd)
	}
	if cmd == "revoke" && *by == "" {
		return fmt.Errorf("supervision revoke: --by is required")
	}

	cfg, st, closeStore, err := openForIdentity(ctx, h, *configPath)
	if err != nil {
		return err
	}
	defer closeStore()
	dep := st.Deployment()

	switch cmd {
	case "revoke":
		if _, err := dep.SetSupervisionRevoked(ctx, true, *by, time.Now().UTC()); err != nil {
			return err
		}
	case "restore":
		if _, err := dep.SetSupervisionRevoked(ctx, false, "", time.Time{}); err != nil {
			return err
		}
	}
	id, err := dep.Identity(ctx)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "instance %s\n", id.InstanceID)
	switch {
	case !cfg.Supervisor.Enabled():
		_, _ = fmt.Fprintln(stdout, "supervisor: none configured")
	default:
		_, _ = fmt.Fprintf(stdout, "supervisor: %s\n", cfg.Supervisor.Endpoint)
	}
	if id.SupervisionRevokedAt.IsZero() {
		_, _ = fmt.Fprintln(stdout, "registration: permitted")
	} else {
		_, _ = fmt.Fprintf(stdout, "registration: revoked at %s by %s\n",
			id.SupervisionRevokedAt.Format(time.RFC3339), id.SupervisionRevokedBy)
	}
	return nil
}
