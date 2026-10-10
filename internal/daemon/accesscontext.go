// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"log/slog"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/accessctx"
	"github.com/hoplock/control/internal/accessctx/declarative"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/config"
	"github.com/hoplock/control/internal/decision"
	"github.com/hoplock/control/internal/policy/model"
	"github.com/hoplock/control/internal/store"
)

// External access context (M16, 0013): Control's declarative providers, the
// probe path on the authorize call, and the push receiver behind the
// north-bound route.

// registerDeclarative registers every configured declarative integration as its
// own access-context provider. It runs BEFORE the registry is sealed, beside
// Control's other registrations: registration is before start and immutable
// afterwards (0004), and a declarative integration is registered under the
// same rules as Hoplock Enterprise's packaged ones — visible in the listing,
// one row per system.
func registerDeclarative(reg *ext.Registry, c config.AccessContextConfig) error {
	for _, pc := range c.Providers {
		p, err := declarative.New(pc, c.Egress, declarative.Options{})
		if err != nil {
			return err
		}
		if err := reg.RegisterAccessContextProvider(ext.Registration{
			Provider: declarative.RegistrationPrefix + pc.Name,
			Version:  versionString(),
		}, p); err != nil {
			return err
		}
	}
	return nil
}

// buildProber builds the provider set from the sealed registry and the probe
// path the decision service runs. It comes before the decision service, which
// holds it.
func buildProber(cfg *config.Config, st *store.Store, extensions *ext.Extensions, log *slog.Logger) (*accessctx.Providers, *accessctx.Prober, error) {
	providers, err := accessctx.NewProviders(extensions.AccessContextProviders(), cfg.AccessContext.MaxProbesInFlight)
	if err != nil {
		return nil, nil, err
	}
	prober, err := accessctx.NewProber(accessctx.ProberOptions{
		Store:          st,
		Providers:      providers,
		Budget:         cfg.AccessContext.ProbeBudget,
		Ceiling:        cfg.AccessContext.MaxWindow,
		ClockSkew:      cfg.AccessContext.ClockSkew,
		MaxTTL:         cfg.AccessContext.MaxProbeTTL,
		Unanswered:     model.Unanswered(cfg.AccessContext.Unanswered),
		BindingRefresh: cfg.Decision.Refresh,
		Logger:         log,
	})
	if err != nil {
		return nil, nil, err
	}
	for _, p := range providers.List() {
		log.Info("access-context provider",
			"name", p.Info.Name, "probes", p.Info.Probes, "pushes", p.Info.Pushes,
			"registration", p.Registration.String())
	}
	return providers, prober, nil
}

// buildPushReceiver builds the push receiver. It comes after the grant service
// — an admitted window becomes a grant through it — and after the decision
// service, which answers whether the active policy marks a scope privileged.
func buildPushReceiver(
	cfg *config.Config,
	st *store.Store,
	grants *access.Service,
	emitter *audit.Emitter,
	providers *accessctx.Providers,
	decisions *decision.Service,
	log *slog.Logger,
) (*accessctx.Service, error) {
	return accessctx.New(accessctx.Options{
		Store:           st,
		Grants:          grants,
		Recorder:        emitter,
		Providers:       providers,
		Policy:          decisions,
		Ceiling:         cfg.AccessContext.MaxWindow,
		ClockSkew:       cfg.AccessContext.ClockSkew,
		MaxAssertionAge: cfg.AccessContext.MaxAssertionAge,
		PushRate:        cfg.AccessContext.PushRate,
		PushBurst:       cfg.AccessContext.PushBurst,
		Logger:          log,
	})
}
