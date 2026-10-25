// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/hoplock/control/internal/credential"
	"github.com/hoplock/control/internal/extdefault"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// `ca certificates` reads the outstanding list a page at a time (M26) and
// prints exactly the lines it printed when it read the list whole: one per
// certificate, newest first, whatever the page size. A failure on a later page
// is the command's error, and nothing of the pages before it is printed.
func TestCACertificatesPrintsEveryPageAsOneList(t *testing.T) {
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

	kek := bytes.Repeat([]byte{0x44}, extdefault.KeyEncryptionKeySize)
	t.Setenv("TEST_CA_KEK", base64.StdEncoding.EncodeToString(kek))
	keys, err := extdefault.NewSoftwareKeyStore(st, kek)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := credential.New(st, keys)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ca.Ensure(ctx, "acme"); err != nil {
		t.Fatal(err)
	}
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}

	// Five certificates, more than the two-a-page walk below holds in a
	// page. What the command prints for each, newest first, is the line it
	// printed before it paged.
	var want []string
	for i := range 5 {
		issued, err := ca.Issue(ctx, "acme", credential.IssueRequest{
			SubjectID: fmt.Sprintf("user-%d@example.com", i), Principals: []string{"deploy", "ops"},
			Target: fmt.Sprintf("db-%d.prod", i), PublicKey: wire.Marshal(), ValidFor: 30 * time.Minute,
		})
		if err != nil {
			t.Fatalf("issue %d: %v", i, err)
		}
		line := fmt.Sprintf("%d\tuser-%d@example.com\tdeploy,ops\tdb-%d.prod\texpires %s\n",
			issued.Serial, i, i, issued.ValidBefore.UTC().Format(time.RFC3339))
		want = append([]string{line}, want...)
	}
	wantOut := strings.Join(want, "")

	doc := "tenant: acme\nlisteners:\n  south: 127.0.0.1:7001\n  north: 127.0.0.1:7002\ndatabase:\n  dsn: " + dsn +
		"\ncredential:\n  key_encryption_key_env: TEST_CA_KEK\n"
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	command := func(tenant string) (string, error) {
		t.Helper()
		var out, errOut bytes.Buffer
		err := Main(ctx, []string{"ca", "certificates", "--config", path, "--tenant", tenant}, &out, &errOut, Host{})
		return out.String(), err
	}

	if out, err := command("acme"); err != nil || out != wantOut {
		t.Errorf("ca certificates: %v\n%s\nwant\n%s", err, out, wantOut)
	}
	for _, limit := range []int{1, 2, 4} {
		var out bytes.Buffer
		if err := writeOutstanding(ctx, ca.OutstandingPage, "acme", limit, &out); err != nil || out.String() != wantOut {
			t.Errorf("read %d a page: %v\n%s\nwant\n%s", limit, err, out.String(), wantOut)
		}
	}

	if out, err := command("empty"); err != nil || out != "tenant empty has no outstanding certificates\n" {
		t.Errorf("a tenant with none: %v %q", err, out)
	}

	failure := errors.New("the second page failed")
	pages := 0
	failing := func(ctx context.Context, tenant store.Tenant, p store.Page) (store.Paged[store.SSHCertificate], error) {
		if pages++; pages == 2 {
			return store.Paged[store.SSHCertificate]{}, failure
		}
		return ca.OutstandingPage(ctx, tenant, p)
	}
	var out bytes.Buffer
	if err := writeOutstanding(ctx, failing, "acme", 2, &out); !errors.Is(err, failure) || out.Len() != 0 {
		t.Errorf("a failure on the second page: %v, printed %q; want the failure and nothing printed", err, out.String())
	}
}
