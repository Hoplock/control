// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/config"
	"github.com/hoplock/control/internal/store"
)

// runAuditVerify is the `hoplock-control audit-verify` subcommand: the
// verifier half of M8's tamper evidence.
//
// A CHAIN NOBODY CAN WALK IS NOT TAMPER-EVIDENT, it is tamper-hopeful. The
// hashes are only a guarantee to the extent that somebody checks them, so the
// check is a command an operator can run and a customer can be handed, not an
// internal function with a test beside it.
//
// It verifies ONE TENANT and says which. A verifier that could only check the
// whole store would be unusable by a customer entitled to see only their part
// of it (M18) — and being able to take a chain that still verifies is the
// reason the chain is per tenant in the first place.
//
// The exit code is the answer: 0 for a chain that verifies, 1 for one that
// does not, so this is usable from cron without parsing anything.
func runAuditVerify(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("hoplock-control audit-verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", defaultConfigPath, "path to the YAML configuration file")
	tenant := fs.String("tenant", "", "the tenant whose chains to verify (default: the configured tenant)")
	stream := fs.String("stream", "", "verify one stream only (default: every stream the tenant has)")
	captures := fs.Bool("captures", false,
		"also re-digest stored session captures; reads every captured byte, so it is off by default")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	who := store.Tenant(cfg.Tenant)
	if *tenant != "" {
		who = store.Tenant(*tenant)
	}

	ctx := context.Background()
	st, err := store.Open(ctx, cfg.Database.DSN)
	if err != nil {
		return err
	}
	defer st.Close()

	verifier := audit.NewVerifier(st, audit.WithCaptureVerification(*captures))

	var result audit.Result
	if *stream != "" {
		sr, err := verifier.VerifyStream(ctx, who, *stream)
		if err != nil {
			return err
		}
		result = audit.Result{Tenant: string(who), Streams: []audit.StreamResult{sr}}
	} else {
		result, err = verifier.VerifyTenant(ctx, who)
		if err != nil {
			return err
		}
	}

	return reportVerification(stdout, result)
}

// reportVerification prints what was walked and what was found.
//
// A PASS IS PRINTED IN AS MUCH DETAIL AS A FAILURE. "ok" on its own is
// indistinguishable from a verifier that walked nothing — an empty store, a
// wrong tenant name, a stream that does not exist — so the record count and
// the head hash are printed too. The head is what a departing customer keeps:
// a chain that verifies today and ends on a hash they wrote down cannot have
// been rewritten since without their noticing.
func reportVerification(stdout io.Writer, result audit.Result) error {
	if len(result.Streams) == 0 {
		if _, err := fmt.Fprintf(stdout, "tenant %s: no audit records\n", result.Tenant); err != nil {
			return err
		}
		return nil
	}

	for _, s := range result.Streams {
		if s.Break == nil {
			if _, err := fmt.Fprintf(stdout, "tenant %s stream %s: OK, %d records, head %s\n",
				result.Tenant, s.Stream, s.Records, s.Head); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(stdout, "tenant %s stream %s: BROKEN after %d records\n  %s\n",
			result.Tenant, s.Stream, s.Records, s.Break.Error()); err != nil {
			return err
		}
	}

	if !result.OK() {
		return errChainBroken
	}
	return nil
}

// errChainBroken is what a failed verification returns, so `main` exits
// non-zero without the report having to be parsed.
var errChainBroken = fmt.Errorf("audit chain verification failed")
