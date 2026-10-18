// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os/user"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/config"
	"github.com/hoplock/control/internal/store"
)

// runAuditRetention is `hoplock-control audit-retention`: the retention key and
// what an operator does with retention no pass signed (0024).
//
// IT IS A COMMAND ON THE BOX, NOT AN ENDPOINT, for the reason `ca issue` is. A
// seal is a statement that a deletion was lawful; adopting one is an operator's
// attestation that nothing can check for them, and it should take a shell, the
// database credential and the key store — the highest privilege there is —
// rather than a token a north-bound caller could hold.
func runAuditRetention(ctx context.Context, h Host, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("audit-retention: a command is required (key, rotate-key, adopt)")
	}
	switch args[0] {
	case "key":
		return runRetentionKey(ctx, h, args[1:], stdout, stderr)
	case "rotate-key":
		return runRetentionRotate(ctx, h, args[1:], stdout, stderr)
	case "adopt":
		return runRetentionAdopt(ctx, h, args[1:], stdout, stderr)
	}
	return fmt.Errorf("audit-retention: unknown command %q (known: key, rotate-key, adopt)", args[0])
}

func retentionFlags(name string, stderr io.Writer) (*flag.FlagSet, *string, *string) {
	fs := flag.NewFlagSet("hoplock-control audit-retention "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", defaultConfigPath, "path to the YAML configuration file")
	tenant := fs.String("tenant", "", "the tenant (default: the configured tenant)")
	return fs, configPath, tenant
}

// openRetentionKeys opens the store and the retention key store exactly as the
// daemon builds them: the host's sections handed to HostConfig, Control's
// defaults registered, the registry sealed, and then a registered ext.KeyStore
// or the software custodian over the key-encryption key. Nil keys mean the
// deployment has none.
func openRetentionKeys(ctx context.Context, h Host, configPath string) (*config.Config, *store.Store, *audit.RetentionKeys, error) {
	cfg, sections, err := config.LoadHost(configPath, h.Sections)
	if err != nil {
		return nil, nil, nil, err
	}
	extensions, err := prepareExtensions(cfg, sections, h)
	if err != nil {
		return nil, nil, nil, err
	}
	st, err := store.Open(ctx, cfg.Database.DSN)
	if err != nil {
		return nil, nil, nil, err
	}
	ks, err := buildKeyStore(cfg, st, extensions)
	if err != nil {
		st.Close()
		return nil, nil, nil, err
	}
	return cfg, st, audit.NewRetentionKeys(ks), nil
}

// openRetention is openRetentionKeys for a command that signs, with the emitter
// that records what it did.
func openRetention(ctx context.Context, h Host, configPath, tenantFlag string) (*audit.Emitter, *audit.RetentionKeys, *store.Store, store.Tenant, error) {
	cfg, st, keys, err := openRetentionKeys(ctx, h, configPath)
	if err != nil {
		return nil, nil, nil, "", err
	}
	if keys == nil {
		st.Close()
		return nil, nil, nil, "", audit.ErrRetentionKeyNotConfigured
	}
	ingest, err := audit.New(audit.Options{Store: st})
	if err != nil {
		st.Close()
		return nil, nil, nil, "", err
	}
	emitter, err := audit.NewEmitter(ingest, audit.WithRetentionSigner(keys))
	if err != nil {
		st.Close()
		return nil, nil, nil, "", err
	}
	tenant := store.Tenant(cfg.Tenant)
	if tenantFlag != "" {
		tenant = store.Tenant(tenantFlag)
	}
	return emitter, keys, st, tenant, nil
}

// localActor is who acted from the box: the operating-system account, since a
// command holding the database credential has no north-bound principal.
func localActor() audit.OperatorActor {
	name := "local"
	if u, err := user.Current(); err == nil && u.Username != "" {
		name = "local:" + u.Username
	}
	return audit.OperatorActor{Principal: name}
}

// runRetentionKey prints the tenant's current retention key and its public
// half, taken from the key store's signer and never from a stored row: it is
// what a departing tenant checks its markers against once it no longer has this
// server (M18).
func runRetentionKey(ctx context.Context, h Host, args []string, stdout, stderr io.Writer) error {
	fs, configPath, tenantFlag := retentionFlags("key", stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	_, keys, st, tenant, err := openRetention(ctx, h, *configPath, *tenantFlag)
	if err != nil {
		return err
	}
	defer st.Close()
	ref, ok, err := keys.Newest(ctx, tenant)
	if err != nil {
		return err
	}
	if !ok {
		_, err := fmt.Fprintf(stdout, "tenant %s has no retention key yet; the first retention pass creates one\n", tenant)
		return err
	}
	return printRetentionKey(ctx, stdout, keys, tenant, ref)
}

func printRetentionKey(ctx context.Context, stdout io.Writer, keys *audit.RetentionKeys, tenant store.Tenant, ref ext.KeyRef) error {
	pub, custodian, err := keys.Public(ctx, tenant, ref.Name)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "tenant %s retention key %s (ed25519, custodian %s)\n  public key: %s\n",
		tenant, ref.Name, custodian, base64.StdEncoding.EncodeToString(pub))
	return err
}

// runRetentionRotate makes a new generation of the tenant's retention key.
// Older generations stay in the key store, so every marker they signed still
// verifies; destroying one is what makes its markers unverifiable, and
// audit-verify then names each of them.
func runRetentionRotate(ctx context.Context, h Host, args []string, stdout, stderr io.Writer) error {
	fs, configPath, tenantFlag := retentionFlags("rotate-key", stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	emitter, keys, st, tenant, err := openRetention(ctx, h, *configPath, *tenantFlag)
	if err != nil {
		return err
	}
	defer st.Close()
	info, err := emitter.RotateRetentionKey(ctx, tenant, localActor())
	if err != nil {
		return err
	}
	if err := printRetentionKey(ctx, stdout, keys, tenant, info.Ref); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "  it signs every retention pass from now on; older keys still check what they signed\n")
	return err
}

// runRetentionAdopt seals retention written before v0.11.0, which no check can
// tell from a forgery: a stream's unsigned marker, or the captures emptied by
// an unsigned pass.
//
// The operator names exactly what audit-verify printed — the marker's sequence
// and hash, or the instant that covers the emptied captures — and the command
// refuses anything else. A marker is adopted only when every record after it
// chains onto it. What it cannot check is whether the deletion was lawful; the
// adoption is recorded on the control chain under the account that ran it.
func runRetentionAdopt(ctx context.Context, h Host, args []string, stdout, stderr io.Writer) error {
	fs, configPath, tenantFlag := retentionFlags("adopt", stderr)
	stream := fs.String("stream", "", "the stream whose unsigned marker to adopt")
	throughSeq := fs.Int64("through-seq", 0, "the marker's sequence, as audit-verify printed it")
	throughHash := fs.String("through-hash", "", "the marker's hash, as audit-verify printed it")
	capturesBefore := fs.String("captures-before", "",
		"adopt the captures emptied before this RFC 3339 instant instead: every capture marked retained must have been recorded before it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	byStream := *stream != "" || *throughSeq != 0 || *throughHash != ""
	if byStream == (*capturesBefore != "") {
		return fmt.Errorf("audit-retention adopt: name a marker (--stream, --through-seq, --through-hash) or --captures-before, exactly one")
	}
	if byStream && (*stream == "" || *throughSeq <= 0 || *throughHash == "") {
		return fmt.Errorf("audit-retention adopt: --stream, --through-seq and --through-hash are all required to adopt a marker")
	}
	var before time.Time
	if !byStream {
		var err error
		if before, err = time.Parse(time.RFC3339Nano, *capturesBefore); err != nil {
			return fmt.Errorf("audit-retention adopt: --captures-before %q is not RFC 3339", *capturesBefore)
		}
	}

	emitter, _, st, tenant, err := openRetention(ctx, h, *configPath, *tenantFlag)
	if err != nil {
		return err
	}
	defer st.Close()
	if byStream {
		marker, err := emitter.AdoptRetention(ctx, tenant, *stream, *throughSeq, *throughHash, localActor())
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "tenant %s stream %s: marker through %d adopted, signed by %s\n",
			tenant, *stream, marker.ThroughSeq, marker.KeyName)
		return err
	}
	mark, err := emitter.AdoptCaptureRetention(ctx, tenant, before, localActor())
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "tenant %s: captures retained before %s adopted, signed by %s\n",
		tenant, mark.Before.UTC().Format(time.RFC3339Nano), mark.KeyName)
	return err
}
