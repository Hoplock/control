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
	"github.com/hoplock/control/internal/revoke"
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
	// The event broker is built FIRST, because the fleet registry reads it:
	// a cache hint is only issued to a proxy holding a live subscription
	// (M9, PLAN §5.4), and that read is [fleet.SubscriptionState]. Before
	// this phase there was no subscription source, so the gate answered no
	// on both responses that carry a hint — correctly, since there was no
	// stream a withdrawal could travel over. Wiring it here is what turns
	// hints on, through the gate they already went through.
	bus, err := revoke.New(revoke.Options{
		HeartbeatInterval: cfg.Events.HeartbeatInterval,
		ReplayBuffer:      cfg.Events.ReplayBuffer,
		SubscriberQueue:   cfg.Events.SubscriberQueue,
		Logger:            log,
	})
	if err != nil {
		return err
	}

	registry := fleet.New(st,
		fleet.WithSubscriptionState(bus),
		fleet.WithLiveness(fleet.Liveness{
			HeartbeatTTL:         cfg.Fleet.HeartbeatTTL,
			RelayRegistrationTTL: cfg.Fleet.RelayRegistrationTTL,
			TargetCapabilityTTL:  cfg.Fleet.TargetCapabilityTTL,
			ReportAfter:          cfg.Fleet.CapabilityReportAfter,
		}),
		fleet.WithRegistryMaxHops(cfg.Fleet.MaxHops),
		fleet.WithLogger(log),
		fleet.WithMaxCacheTTL(cfg.Decision.MaxCacheTTL),
		fleet.WithHostKeyCacheTTL(cfg.Fleet.HostKeyCacheTTL),
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
		Events:         bus,
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
		"heartbeat_interval_seconds", bus.AdvertisedHeartbeatSeconds(),
	)

	errs := make(chan error, 2)
	go func() {
		err := srv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errs <- err
	}()

	publisher, err := startPublishListener(cfg, registry, bus, log)
	if err != nil {
		return err
	}
	if publisher != nil {
		go func() {
			err := publisher.ListenAndServe()
			if errors.Is(err, http.ErrServerClosed) {
				err = nil
			}
			errs <- err
		}()
	}

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
	defer cancel()

	// THE ORDER MATTERS. The listeners stop accepting first, so nothing new
	// arrives; then the broker drains, so every subscription hands over what
	// it already holds and ends its response normally. Dropping them instead
	// would cut a line mid-write and leave a gap the proxy could only
	// discover on reconnect — a deploy is a reconnect, not a fleet-wide
	// cache flush.
	if publisher != nil {
		if err := publisher.Shutdown(shutdownCtx); err != nil {
			log.Warn("the publish listener did not shut down cleanly", "error", err)
		}
	}
	if err := bus.Close(shutdownCtx); err != nil {
		log.Warn("the revocation broker did not drain inside the shutdown grace", "error", err)
	}
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	return <-errs
}

// startPublishListener binds the local publish path, when one is configured.
//
// It returns (nil, nil) when it is not, which is the default: see publish.go
// for why an unconfigured deployment has no publish port at all.
func startPublishListener(cfg *config.Config, registry *fleet.Registry, bus *revoke.Bus, log *slog.Logger) (*http.Server, error) {
	if cfg.Events.PublishListener == "" {
		return nil, nil
	}
	ps, err := newPublishServer(
		revoke.NewOperator(bus, registry),
		store.Tenant(cfg.Tenant),
		cfg.Events.PublishToken,
		log,
	)
	if err != nil {
		return nil, err
	}
	log.Warn("the local revocation publish listener is enabled",
		"event", "revoke_publish_listener_enabled",
		"address", cfg.Events.PublishListener,
		"note", "this is a pre-0014 operator path and publishes the kill switch; do not expose it",
	)
	return &http.Server{
		Addr:              cfg.Events.PublishListener,
		Handler:           ps.handler(),
		ReadHeaderTimeout: cfg.South.RequestTimeout,
	}, nil
}
