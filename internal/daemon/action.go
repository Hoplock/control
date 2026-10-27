// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"log/slog"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/action"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/config"
	"github.com/hoplock/control/internal/revoke"
	"github.com/hoplock/control/internal/standing"
	"github.com/hoplock/control/internal/store"
)

// buildActionRunner builds the executor a registered ext.ActionHandler acts
// through, and the lifecycle that starts it (0039, M15). With no handler
// registered it builds nothing and returns nil: no executor exists, and the
// point stays disabled, as the catalogue says.
//
// The executor is handed the services an operator's routes call (the standing
// denials, grant revocation, the revocation publisher), so the handler can pull
// no lever an operator cannot. Its provider is the registration's, which is the
// actor on every record it writes.
func buildActionRunner(
	cfg *config.Config,
	st *store.Store,
	extensions *ext.Extensions,
	denials *standing.Service,
	grants *access.Service,
	revocations *revoke.Operator,
	emitter *audit.Emitter,
	log *slog.Logger,
) (*action.Runner, error) {
	handler, ok := extensions.ActionHandler()
	if !ok {
		return nil, nil
	}
	provider := ""
	for _, s := range extensions.Status() {
		if s.Info.Point == ext.PointActionHandler && len(s.Registrations) > 0 {
			provider = s.Registrations[0].Provider
		}
	}
	exec, err := action.New(action.Options{
		Provider: provider, Store: st, Standing: denials, Grants: grants, Revoker: revocations, Recorder: emitter,
		DefaultTenant: store.Tenant(cfg.Tenant), Logger: log,
	})
	if err != nil {
		return nil, err
	}
	return action.NewRunner(handler, exec, log), nil
}
