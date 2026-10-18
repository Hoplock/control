// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/extdefault"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// `audit-verify` checks retention seals against the key store the server signs
// with, built from the same configuration and the same host registry (0024): the
// software custodian over the configured key-encryption key, or a host's
// ext.KeyStore. With neither, a sealed marker is a break it cannot check, never
// a pass.
func TestAuditVerifyChecksSealsWithTheServersOwnKeyStore(t *testing.T) {
	dsn := storetest.DSN(t)
	schema := storetest.NewSchema(t, dsn)
	dsn = storetest.DSNForSchema(dsn, schema)
	ctx := t.Context()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	kek := bytes.Repeat([]byte{0x42}, extdefault.KeyEncryptionKeySize)
	t.Setenv("TEST_RETENTION_KEK", base64.StdEncoding.EncodeToString(kek))
	software, err := extdefault.NewSoftwareKeyStore(st, kek)
	if err != nil {
		t.Fatal(err)
	}
	// A host's key store: here the software custodian under another
	// key-encryption key, which the configured one cannot open. It shares the
	// table, so it keeps to a tenant of its own.
	hostKeys, err := extdefault.NewSoftwareKeyStore(st, bytes.Repeat([]byte{0x43}, extdefault.KeyEncryptionKeySize))
	if err != nil {
		t.Fatal(err)
	}

	retain := func(ks ext.KeyStore, tenant store.Tenant, proxy string) {
		t.Helper()
		in, err := audit.New(audit.Options{Store: st})
		if err != nil {
			t.Fatal(err)
		}
		recs := make([]contract.LogRecord, 4)
		for i := range recs {
			recs[i] = contract.LogRecord{
				RecordID: proxy + "-" + string(rune('a'+i)), Kind: "command", Severity: contract.SeverityInfo,
				Timestamp: "2026-10-18T12:00:00Z", Message: "a command ran",
			}
		}
		if _, err := in.Ingest(ctx, audit.Submission{Tenant: tenant, ProxyID: proxy, Records: recs}); err != nil {
			t.Fatal(err)
		}
		emitter, err := audit.NewEmitter(in, audit.WithRetentionSigner(audit.NewRetentionKeys(ks)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := emitter.RetainPrefix(ctx, tenant, audit.PrefixPass{Stream: audit.StreamFor(proxy), ThroughSeq: 2},
			audit.OperatorActor{Principal: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	verify := func(withKEK bool, h Host, tenant store.Tenant, stream string) (string, error) {
		t.Helper()
		doc := "tenant: acme\nlisteners:\n  south: 127.0.0.1:7001\n  north: 127.0.0.1:7002\ndatabase:\n  dsn: " + dsn + "\n"
		if withKEK {
			doc += "credential:\n  key_encryption_key_env: TEST_RETENTION_KEK\n"
		}
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		var out, errOut bytes.Buffer
		err := Main(ctx, []string{"audit-verify", "--config", path, "--tenant", string(tenant), "--stream", stream}, &out, &errOut, h)
		return out.String(), err
	}

	retain(software, "acme", "p-software")
	if out, err := verify(true, Host{}, "acme", "proxy:p-software"); err != nil || !strings.Contains(out, "OK, 2 records") || !strings.Contains(out, "retained through 2") {
		t.Errorf("a marker the configured custodian signed: %v\n%s", err, out)
	}
	if out, err := verify(false, Host{}, "acme", "proxy:p-software"); !errors.Is(err, errChainBroken) || !strings.Contains(out, "no key store is configured") {
		t.Errorf("with no key store configured: %v\n%s", err, out)
	}

	retain(hostKeys, "initech", "p-host")
	registry := ext.NewRegistry()
	if err := registry.RegisterKeyStore(ext.Registration{Provider: "example-host"}, hostKeys); err != nil {
		t.Fatal(err)
	}
	if out, err := verify(true, Host{Registry: registry}, "initech", "proxy:p-host"); err != nil || !strings.Contains(out, "retained through 2") {
		t.Errorf("a marker the host's key store signed, verified with the host's registry: %v\n%s", err, out)
	}
	if out, err := verify(true, Host{}, "initech", "proxy:p-host"); err == nil {
		t.Errorf("a marker the host's key store signed verified against Control's own custodian:\n%s", out)
	}
}
