// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/extdefault"
	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

type quietValidator struct{}

func (quietValidator) Validate(context.Context, ext.PolicyDocument) ([]ext.Finding, error) {
	return nil, nil
}

// The listing covers every point, including the empty ones: with a fake
// extension at one point it names the provider, and a point with nothing
// registered still has its row and says what Control does instead. The rows are
// counted against ext.Points(), so a seam added later cannot become invisible
// by being forgotten here.
func TestTheExtensionListingCoversEveryPoint(t *testing.T) {
	st := storetest.New(t)
	federation, err := identity.NewFederation(st)
	if err != nil {
		t.Fatalf("federation: %v", err)
	}
	reg := ext.NewRegistry()
	if err := reg.RegisterPolicyValidator(ext.Registration{Provider: "example.com/governance", Version: "1.2.3"}, quietValidator{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := extdefault.Register(reg, extdefault.Deps{}); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	extensions, err := reg.Seal()
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	server, err := north.New(withLifecycle(t, st, north.Options{
		Federation: federation, Grants: newGrants(t, st, access.Options{}), Extensions: extensions,
		DefaultTenant: "tenant-a", InsecureCookies: true,
	}))
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	cred, _, err := federation.IssueToken(t.Context(), "tenant-a", identity.TokenRequest{
		DisplayName: "auditor", Scopes: map[store.Tenant]identity.RoleSet{"tenant-a": {identity.RoleAuditor}},
	})
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/tenants/tenant-a/extensions", nil)
	req.Header.Set("Authorization", "Bearer "+cred.String())
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("extensions = %d", resp.StatusCode)
	}
	var out struct {
		Points []struct {
			Point         string `json:"point"`
			Registered    bool   `json:"registered"`
			Absent        string `json:"absent"`
			Registrations []struct {
				Provider string `json:"provider"`
				Version  string `json:"version"`
			} `json:"registrations"`
		} `json:"points"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Points) != len(ext.Points()) {
		t.Fatalf("%d rows, want one per ext.Points() entry (%d)", len(out.Points), len(ext.Points()))
	}
	sawValidator, sawEmpty := false, false
	for i, p := range out.Points {
		if p.Point != ext.Points()[i].Point.String() {
			t.Errorf("row %d is %s, want %s", i, p.Point, ext.Points()[i].Point)
		}
		if p.Absent == "" {
			t.Errorf("%s does not say what Control does with nothing registered", p.Point)
		}
		if p.Point == ext.PointPolicyValidator.String() {
			sawValidator = p.Registered && len(p.Registrations) == 1 &&
				p.Registrations[0].Provider == "example.com/governance" && p.Registrations[0].Version == "1.2.3"
		}
		if p.Point == ext.PointGrantWorkflow.String() {
			sawEmpty = !p.Registered && len(p.Registrations) == 0
		}
	}
	if !sawValidator {
		t.Error("the listing does not name the registered validator's provider")
	}
	if !sawEmpty {
		t.Error("a point with nothing registered is missing or claims a registration")
	}
}
