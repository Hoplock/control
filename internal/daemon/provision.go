// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"log/slog"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/config"
	"github.com/hoplock/control/internal/provision"
	"github.com/hoplock/control/internal/revoke"
	"github.com/hoplock/control/internal/store"
)

// buildIdentitySync builds the sink a registered ext.IdentitySync applies what
// it learns through, and the lifecycle that starts it (0040, M15). With no sync
// registered it builds nothing and returns nil: no sink exists, and the point
// stays disabled, as the catalogue says.
//
// The sink is handed the services an operator's levers call (grant revocation
// and the request cancel path, the revocation publisher), so a deactivation
// revokes what an operator would. Its provider is the registration's, which is
// the actor on every record it writes.
func buildIdentitySync(
	cfg *config.Config,
	st *store.Store,
	extensions *ext.Extensions,
	grants *access.Service,
	revocations *revoke.Operator,
	emitter *audit.Emitter,
	log *slog.Logger,
) (*provision.Runner, error) {
	sync, ok := extensions.IdentitySync()
	if !ok {
		return nil, nil
	}
	provider := ""
	for _, s := range extensions.Status() {
		if s.Info.Point == ext.PointIdentitySync && len(s.Registrations) > 0 {
			provider = s.Registrations[0].Provider
		}
	}
	sink, err := provision.New(provision.Options{
		Provider: provider, Store: st, Grants: grants, Revoker: revocations, Recorder: emitter,
		DefaultTenant: store.Tenant(cfg.Tenant), Settle: cfg.Decision.Budget + settleMargin, Logger: log,
	})
	if err != nil {
		return nil, err
	}
	return provision.NewRunner(sync, sink, log), nil
}
