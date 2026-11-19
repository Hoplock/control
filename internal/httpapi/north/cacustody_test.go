// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/credential"
	"github.com/hoplock/control/internal/credential/credentialtest"
	"github.com/hoplock/control/internal/extdefault"
	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// A CA KEY THE KEY STORE CANNOT PRODUCE, ON THE OPERATOR'S SURFACE (0062).
//
// Start-up says so for the tenant and starts anyway; `GET …/ca` will not name a
// custodian the store does not confirm, answering a 5xx that names the key and
// the store (an absent key `500 internal`, a store that cannot be asked `503
// unavailable`, never a 401); and `POST …/ca/rotate` is the way out, needing
// nothing from the key it retires.
func TestTheCARoutesAnswerForAKeyTheStoreCannotProduce(t *testing.T) {
	var logs bytes.Buffer
	st := storetest.New(t)
	ctx := context.Background()
	federation, err := identity.NewFederation(st)
	if err != nil {
		t.Fatal(err)
	}
	software, err := extdefault.NewSoftwareKeyStore(st, testKEK)
	if err != nil {
		t.Fatal(err)
	}
	softwareCA, err := credential.New(st, software)
	if err != nil {
		t.Fatal(err)
	}
	stranded, err := softwareCA.Ensure(ctx, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}

	const custodian = "pkcs11:slot-test"
	const storeName = "registered ext.KeyStore " + credentialtest.Provider
	keys := credentialtest.New(custodian)
	ca, err := credential.New(st, keys, credential.WithKeyStoreName(storeName))
	if err != nil {
		t.Fatal(err)
	}
	server, err := north.New(withLifecycle(t, st, north.Options{
		Federation: federation, CA: ca, Grants: newGrants(t, st, access.Options{}),
		DefaultTenant: "tenant-a", InsecureCookies: true,
		Logger: slog.New(slog.NewJSONHandler(&logs, nil)),
	}))
	if err != nil {
		t.Fatal(err)
	}

	// Start-up: said, and not fatal, and nothing generated over the key.
	if err := server.Ensure(ctx, "tenant-a"); err != nil {
		t.Fatalf("start-up failed over a key the store does not hold: %v", err)
	}
	if !logged(t, &logs, "ssh_ca_key_not_held", stranded.ActiveKeyID, storeName) {
		t.Errorf("start-up did not say the tenant's key is not held:\n%s", logs.String())
	}
	if keys.Len() != 0 {
		t.Fatalf("start-up made a key: the store holds %d", keys.Len())
	}

	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	cred, _, err := federation.IssueToken(ctx, "tenant-a", identity.TokenRequest{
		DisplayName: "test", Scopes: map[store.Tenant]identity.RoleSet{"tenant-a": {identity.RoleAdmin}},
	})
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path string) (*http.Response, north.Error) {
		t.Helper()
		req, err := http.NewRequest(method, ts.URL+"/api/v1/tenants/tenant-a"+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+cred.String())
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		var e north.Error
		if resp.StatusCode != http.StatusOK {
			_ = json.NewDecoder(resp.Body).Decode(&e)
		}
		return resp, e
	}
	says := func(e north.Error, want ...string) {
		t.Helper()
		for _, w := range want {
			if !strings.Contains(e.Message, w) {
				t.Errorf("the answer %q does not say %q", e.Message, w)
			}
		}
		if e.CorrelationID == "" {
			t.Error("the answer carries no correlation id")
		}
	}

	resp, e := call("GET", "/ca")
	if resp.StatusCode != http.StatusInternalServerError || e.Code != north.CodeInternal {
		t.Fatalf("GET …/ca over a key the store does not hold: %d %q; want 500 internal", resp.StatusCode, e.Code)
	}
	says(e, stranded.ActiveKeyID, storeName, "does not hold", "rotate")

	// The way out: the new key in the resolved store, the old one kept for
	// the overlap.
	resp, _ = call("POST", "/ca/rotate")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST …/ca/rotate over a stranded key: %d", resp.StatusCode)
	}
	var rotated struct {
		PreviousKeyID string `json:"previous_key_id"`
		CA            struct {
			Custodian   string                  `json:"custodian"`
			TrustBundle []credential.TrustedKey `json:"trust_bundle"`
		} `json:"ca"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rotated); err != nil {
		t.Fatal(err)
	}
	if rotated.PreviousKeyID != stranded.ActiveKeyID || rotated.CA.Custodian != custodian ||
		len(rotated.CA.TrustBundle) != 2 || keys.Len() != 1 {
		t.Fatalf("the rotation: %+v, %d keys in the store", rotated, keys.Len())
	}
	if resp, _ := call("GET", "/ca"); resp.StatusCode != http.StatusOK {
		t.Fatalf("GET …/ca after the rotation: %d", resp.StatusCode)
	}

	// A store that cannot be asked is an outage, told apart from an absent
	// key, on the routes and at start-up alike.
	keys.SetDown(true)
	for _, route := range [][2]string{{"GET", "/ca"}, {"POST", "/ca/rotate"}} {
		resp, e := call(route[0], route[1])
		if resp.StatusCode != http.StatusServiceUnavailable || e.Code != north.CodeUnavailable {
			t.Errorf("%s …%s with the store down: %d %q; want 503 unavailable", route[0], route[1], resp.StatusCode, e.Code)
		}
		says(e, storeName, "could not be asked")
		if strings.Contains(e.Message, "does not hold") || strings.Contains(e.Message, "rotate the") {
			t.Errorf("an outage was answered as an absent key: %q", e.Message)
		}
	}
	logs.Reset()
	if err := server.Ensure(ctx, "tenant-a"); err != nil {
		t.Fatalf("start-up failed with the store down: %v", err)
	}
	if !logged(t, &logs, "ssh_ca_key_store_unavailable", storeName) {
		t.Errorf("start-up did not say the store could not be asked:\n%s", logs.String())
	}
}

// logged reports whether a JSON log line carries event and every one of want in
// its values.
func logged(t *testing.T, logs *bytes.Buffer, event string, want ...string) bool {
	t.Helper()
	for line := range strings.SplitSeq(logs.String(), "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) != nil || rec["event"] != event {
			continue
		}
		all := true
		for _, w := range want {
			if !strings.Contains(line, w) {
				all = false
			}
		}
		if all {
			return true
		}
	}
	return false
}
