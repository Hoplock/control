// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/hoplock/control/internal/config"
	"github.com/hoplock/control/internal/decision"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/httpapi/south"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

// shutdownGrace is how long an in-flight request has to finish once the
// process has been asked to stop.
//
// It is longer than the south-bound request timeout on purpose: a request that
// was already inside its own deadline should be allowed to answer, because an
// answer the proxy can classify beats a closed connection it cannot.
const shutdownGrace = 15 * time.Second

// serveSouth binds the proxy-facing listener and blocks until ctx is done.
//
// The north-bound listener is NOT bound here, and its absence is not an
// oversight: it is 0014's, and two surfaces that never share a port also never
// share a bring-up (M2).
func serveSouth(ctx context.Context, cfg *config.Config, st *store.Store, log *slog.Logger) error {
	registry := fleet.New(st,
		fleet.WithLiveness(fleet.Liveness{
			HeartbeatTTL:         cfg.Fleet.HeartbeatTTL,
			RelayRegistrationTTL: cfg.Fleet.RelayRegistrationTTL,
			TargetCapabilityTTL:  cfg.Fleet.TargetCapabilityTTL,
			ReportAfter:          cfg.Fleet.CapabilityReportAfter,
		}),
		fleet.WithRegistryMaxHops(cfg.Fleet.MaxHops),
		fleet.WithLogger(log),
		fleet.WithMaxCacheTTL(cfg.Decision.MaxCacheTTL),
		fleet.WithUIDAllocation(fleet.UIDAllocation{
			RangeMin:     cfg.UIDs.RangeMin,
			RangeMax:     cfg.UIDs.RangeMax,
			BlockSize:    cfg.UIDs.BlockSize,
			MaxBlockSize: cfg.UIDs.MaxBlockSize,
			Term:         cfg.UIDs.LeaseTerm,
		}),
	)

	// The scripted provider is registered because it is the only one this
	// build has (0011 lands a real one). It is not a second factor: a
	// subject enrolled with it has one that answers the way its row says it
	// will, which is why every challenge names its provider in the log.
	auth := identity.NewService(identity.NewStoreDirectory(st),
		identity.WithMFAProvider(identity.ScriptedMFA{}),
		identity.WithChallengeTTL(cfg.MFA.ChallengeTTL),
		identity.WithPollAfter(cfg.MFA.PollAfter),
		identity.WithMaxPolls(cfg.MFA.MaxPolls),
	)

	// The composition root for `/v1/authorize` (0008). It holds the
	// compiled policy and the fleet graph in memory rather than reloading
	// either per request: a proxy is holding a user's handshake open while
	// this answers (M5).
	decisions, err := decision.New(decision.Options{
		Store:    st,
		Fleet:    registry,
		Subjects: identity.NewStoreDirectory(st),
		Logger:   log,
		Refresh:  cfg.Decision.Refresh,
		Budget:   cfg.Decision.Budget,
	})
	if err != nil {
		return err
	}

	handler, err := south.New(south.Options{
		Identity:       auth,
		Fleet:          registry,
		Decision:       decisions,
		Logger:         log,
		MaxBodyBytes:   cfg.South.MaxBodyBytes,
		RequestTimeout: cfg.South.RequestTimeout,
	})
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:    cfg.Listeners.South,
		Handler: handler,
		// ReadHeaderTimeout bounds a caller that opens a connection and
		// sends nothing. Without it, a handful of such connections is a
		// listener that has stopped accepting.
		ReadHeaderTimeout: cfg.South.RequestTimeout,
	}

	log.Info("south-bound listener starting",
		"address", cfg.Listeners.South,
		"routes", handler.Routes(),
	)

	errs := make(chan error, 1)
	go func() {
		err := srv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errs <- err
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	return <-errs
}
