// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/credential"
	"github.com/hoplock/control/internal/credential/credentialtest"
	"github.com/hoplock/control/internal/extdefault"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// THE `ca` SUBCOMMANDS RESOLVE THE DAEMON'S KEY STORE (0062): the host's
// sections handed to HostConfig, the registry sealed, and then a registered
// ext.KeyStore, else the software custodian. Every command runs through Main,
// with a host that registers its key store from its own configuration section
// — the case where leaving the host out would put the CA's keys somewhere the
// daemon never looks.
func TestTheCASubcommandsResolveTheDaemonsKeyStore(t *testing.T) {
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
	kek := bytes.Repeat([]byte{0x45}, extdefault.KeyEncryptionKeySize)
	t.Setenv("TEST_CUSTODY_KEK", base64.StdEncoding.EncodeToString(kek))

	const custodian = "pkcs11:slot-test"
	keys := credentialtest.New(custodian)
	// A host registers its key store when it reads its own section, so a
	// command that never hands the host its sections never sees the store.
	// Each command seals the registry it is given, so each gets a fresh one
	// over the same store.
	host := func() Host {
		registry := ext.NewRegistry()
		return Host{
			Provider: "example/test-host", Registry: registry, Sections: []string{"test_keys"},
			Config: func(section string, _ []byte) error {
				return registry.RegisterKeyStore(ext.Registration{Provider: credentialtest.Provider}, keys)
			},
		}
	}
	config := func(hosted, withKEK bool) string {
		t.Helper()
		doc := "tenant: acme\nlisteners:\n  south: 127.0.0.1:7001\n  north: 127.0.0.1:7002\ndatabase:\n  dsn: " + dsn + "\n"
		if hosted {
			doc += "test_keys:\n  slot: test\n"
		}
		if withKEK {
			doc += "credential:\n  key_encryption_key_env: TEST_CUSTODY_KEK\n"
		}
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	// The host's own file carries its section; Control alone's does not,
	// because Control alone refuses a key it does not define.
	withKEK, withoutKEK := config(true, true), config(true, false)
	aloneWithKEK, aloneWithoutKEK := config(false, true), config(false, false)
	run := func(h Host, path string, args ...string) (string, error) {
		t.Helper()
		var out, errOut bytes.Buffer
		err := Main(ctx, append(args, "--config", path), &out, &errOut, h)
		return out.String(), err
	}
	softwareKeys := func(tenant store.Tenant) int {
		t.Helper()
		rows, err := st.SoftwareKeys().List(ctx, tenant)
		if err != nil {
			t.Fatal(err)
		}
		return len(rows)
	}
	certificates := func(tenant store.Tenant) int {
		t.Helper()
		var n int
		if err := st.Pool().QueryRow(ctx, `SELECT count(*) FROM ssh_certificates WHERE tenant = $1`, tenant).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "session.pub")
	if err := os.WriteFile(keyFile, ssh.MarshalAuthorizedKey(wire), 0o600); err != nil {
		t.Fatal(err)
	}
	issueArgs := func(tenant string) []string {
		return []string{"ca", "issue", "--tenant", tenant, "--subject", "alice", "--principals", "deploy",
			"--target", "db-1.prod", "--public-key", keyFile}
	}

	// A registered key store and no key-encryption key: the CA exists, its
	// key is in the registered store, and Control's custodian holds nothing.
	out, err := run(host(), withoutKEK, "ca", "show", "--tenant", "acme")
	if err != nil || !strings.Contains(out, "custodian "+custodian) {
		t.Fatalf("ca show with a registered key store and no key-encryption key: %v\n%s", err, out)
	}
	if refs := keys.Refs("acme"); len(refs) != 1 || softwareKeys("acme") != 0 {
		t.Fatalf("the registered store holds %v and software_keys %d rows; want one key there and none here",
			refs, softwareKeys("acme"))
	}
	if out, err := run(host(), withoutKEK, issueArgs("acme")...); err != nil || certificates("acme") != 1 {
		t.Fatalf("ca issue with the registered store: %v\n%s", err, out)
	}

	// A registered key store AND a key-encryption key: the registered store
	// still wins.
	if out, err := run(host(), withKEK, "ca", "show", "--tenant", "initech"); err != nil ||
		!strings.Contains(out, "custodian "+custodian) {
		t.Fatalf("ca show with both: %v\n%s", err, out)
	}
	if len(keys.Refs("initech")) != 1 || softwareKeys("initech") != 0 {
		t.Fatalf("with both configured, the key went to software_keys (%d rows), not the registered store (%v)",
			softwareKeys("initech"), keys.Refs("initech"))
	}

	// The host's registry left out, with only the key-encryption key: the
	// active key is one Control's custodian does not hold. `ca show` still
	// prints it, says so, and fails; `ca issue` signs nothing.
	out, err = run(Host{}, aloneWithKEK, "ca", "show", "--tenant", "acme")
	if err == nil || !strings.Contains(out, "NOT HELD") || !strings.Contains(err.Error(), "ca rotate") {
		t.Fatalf("ca show over a key the software custodian does not hold: %v\n%s", err, out)
	}
	if !strings.Contains(out, "ssh-ed25519 ") {
		t.Errorf("ca show printed no trust bundle beside the key it cannot find:\n%s", out)
	}
	if _, err := run(Host{}, aloneWithKEK, issueArgs("acme")...); err == nil ||
		!strings.Contains(err.Error(), "does not hold") || certificates("acme") != 1 {
		t.Fatalf("ca issue signed with a key the resolved store does not hold: %v (%d certificates)", err, certificates("acme"))
	}

	// The reverse, and the case a deployment meets: a key made in Control's
	// custodian before the host registered its store. The software custodian
	// still holds it and the key-encryption key is still configured, and the
	// CA signs with neither.
	if out, err := run(Host{}, aloneWithKEK, "ca", "show", "--tenant", "globex"); err != nil {
		t.Fatalf("ca show in Control's custodian: %v\n%s", err, out)
	}
	if _, err := run(host(), withKEK, issueArgs("globex")...); err == nil ||
		!strings.Contains(err.Error(), "does not hold") || !strings.Contains(err.Error(), credentialtest.Provider) ||
		certificates("globex") != 0 {
		t.Fatalf("ca issue signed a key the registered store does not hold with Control's custodian: %v (%d certificates)",
			err, certificates("globex"))
	}
	if out, err := run(host(), withKEK, "ca", "show", "--tenant", "globex"); err == nil || !strings.Contains(out, "NOT HELD") {
		t.Fatalf("ca show over a key made before the store was registered: %v\n%s", err, out)
	}

	// The way out, through the command: a rotation in the resolved store.
	before := keys.Refs("acme")
	out, err = run(host(), withoutKEK, "ca", "rotate", "--tenant", "acme")
	if err != nil || len(keys.Refs("acme")) != len(before)+1 || softwareKeys("acme") != 0 {
		t.Fatalf("ca rotate with the host: %v (store %v, software %d)\n%s", err, keys.Refs("acme"), softwareKeys("acme"), out)
	}
	if out, err := run(host(), withoutKEK, "ca", "show", "--tenant", "acme"); err != nil ||
		strings.Count(out, "custodian "+custodian) != 2 || strings.Count(out, "\nssh-ed25519 ") != 2 {
		t.Fatalf("after the rotation both keys are held and both are in the bundle: %v\n%s", err, out)
	}

	// Neither: no CA, and the answer names both ways to have one.
	_, err = run(Host{}, aloneWithoutKEK, "ca", "show", "--tenant", "acme")
	if err == nil || !strings.Contains(err.Error(), "credential.key_encryption_key_env") || !strings.Contains(err.Error(), "ext.KeyStore") {
		t.Fatalf("ca show with no key store: %v; want both settings named", err)
	}
}

// The key-encryption key alone is what it always was: the software custodian
// holds the CA's key.
func TestTheKeyEncryptionKeyAloneKeepsTheSoftwareCustodian(t *testing.T) {
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
	t.Setenv("TEST_SOFTWARE_KEK", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x46}, extdefault.KeyEncryptionKeySize)))
	doc := "tenant: acme\nlisteners:\n  south: 127.0.0.1:7001\n  north: 127.0.0.1:7002\ndatabase:\n  dsn: " + dsn +
		"\ncredential:\n  key_encryption_key_env: TEST_SOFTWARE_KEK\n"
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if err := Main(ctx, []string{"ca", "show", "--config", path}, &out, &errOut, Host{}); err != nil {
		t.Fatalf("ca show: %v", err)
	}
	if !strings.Contains(out.String(), "custodian "+extdefault.CustodianSoftware) {
		t.Errorf("ca show does not name the software custodian:\n%s", out.String())
	}
	rows, err := st.SoftwareKeys().List(ctx, "acme")
	if err != nil || len(rows) != 1 || !strings.HasPrefix(rows[0].Name, credential.CAKeyName+":") {
		t.Errorf("software_keys holds %v (%v); want the CA key", rows, err)
	}
}
