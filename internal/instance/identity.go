// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package instance

import (
	"context"
	"sync"
	"time"

	"github.com/hoplock/control/internal/store"
)

// Versions is the deployment's version set: three axes, none of which is
// another (M23, rule 7).
type Versions struct {
	// Software is the module release this binary was built from (M23).
	Software string `json:"software"`
	// Contract is the vendored contract document's version, and
	// ContractCommit the upstream commit it came from (M1).
	Contract       string `json:"contract"`
	ContractCommit string `json:"contract_commit"`
	// NorthAPI is the north-bound API version this deployment serves, and
	// NorthAPIMin the oldest it still answers in (M19).
	NorthAPI    int `json:"north_api"`
	NorthAPIMin int `json:"north_api_min"`
}

// Identity is what a deployment says it is.
type Identity struct {
	// InstanceID is stable for the life of the deployment, generated once
	// and kept in the store; never a hostname or a pod name.
	InstanceID string `json:"instance_id"`
	// DisplayName is the operator's, from configuration; it may change and
	// the id does not.
	DisplayName string    `json:"display_name"`
	CreatedAt   time.Time `json:"created_at"`
	Versions    Versions  `json:"versions"`
	// Tenants is the deployment's tenant set, as far as the reader may see
	// it (M18).
	Tenants []TenantState `json:"tenants"`
}

// TenantState is one tenant in the set.
type TenantState struct {
	Tenant string `json:"tenant"`
	// Active is whether the deployment served the tenant within the active
	// window.
	Active     bool      `json:"active"`
	LastActive time.Time `json:"last_active"`
}

// DefaultTenantActiveWindow is how recently a tenant must have been served to
// count as active.
const DefaultTenantActiveWindow = 24 * time.Hour

// tenantTouchEvery bounds how often one node writes that it served a tenant.
const tenantTouchEvery = time.Minute

// Tenants records which tenants this node serves, and writes it down at most
// once a minute per tenant: it is called from the paths that resolve a
// tenant, and a write per request would be a write per request.
type Tenants struct {
	st  *store.Store
	now func() time.Time

	mu      sync.Mutex
	touched map[store.Tenant]time.Time
}

// NewTenants returns a tracker writing through st.
func NewTenants(st *store.Store, now func() time.Time) *Tenants {
	if now == nil {
		now = time.Now
	}
	return &Tenants{st: st, now: now, touched: map[store.Tenant]time.Time{}}
}

// Touch records that the deployment served tenant now. It never blocks on the
// store for longer than a write, it is never on the decision path (callers
// call it from goroutines of their own or the management plane), and a failed
// write is retried at the next touch.
func (t *Tenants) Touch(ctx context.Context, tenant store.Tenant) {
	if tenant == "" || t == nil {
		return
	}
	now := t.now()
	t.mu.Lock()
	last, ok := t.touched[tenant]
	if ok && now.Sub(last) < tenantTouchEvery {
		t.mu.Unlock()
		return
	}
	t.touched[tenant] = now
	t.mu.Unlock()
	if err := t.st.Deployment().TouchTenant(ctx, tenant, now); err != nil {
		t.mu.Lock()
		delete(t.touched, tenant)
		t.mu.Unlock()
	}
}

// identityOf assembles an identity from the store's record.
func identityOf(d store.DeploymentIdentity, name string, v Versions, tenants []store.DeploymentTenant,
	now time.Time, window time.Duration, visible func(store.Tenant) bool) Identity {
	out := Identity{
		InstanceID:  d.InstanceID,
		DisplayName: name,
		CreatedAt:   d.CreatedAt,
		Versions:    v,
		Tenants:     []TenantState{},
	}
	for _, t := range tenants {
		if visible != nil && !visible(t.Tenant) {
			continue
		}
		out.Tenants = append(out.Tenants, TenantState{
			Tenant:     string(t.Tenant),
			Active:     now.Sub(t.LastActive) <= window,
			LastActive: t.LastActive,
		})
	}
	return out
}
