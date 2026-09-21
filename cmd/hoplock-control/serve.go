// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/hoplock/control/internal/audit"
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

// serve binds both listeners and blocks until ctx is done.
//
// THE TWO SURFACES SHARE NO PORT, NO CHAIN AND NO CREDENTIAL (M2) — and they do
// share a bring-up, because a process that starts one and not the other is a
// deployment where half the product is up. The north-bound listener arrives here
// with 0011 rather than 0014 because this phase is what M2 was waiting for: the
// credential model. 0014 adds routes to a listener that already authenticates.
func serve(ctx context.Context, cfg *config.Config, st *store.Store, log *slog.Logger) error {
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

	// The audit ingester. It is built with the store directly and holds no
	// buffer: the priority ack means DURABLE (PLAN §4), so there is nothing
	// between the request and the commit on either log path (M8).
	ingest, err := audit.New(audit.Options{
		Store:  st,
		Logger: log,
		Limits: audit.Limits{
			MaxBatchRecords: cfg.Audit.MaxBatchRecords,
			MaxRecordBytes:  cfg.Audit.MaxRecordBytes,
			MaxCaptureBytes: cfg.Audit.MaxCaptureBytes,
		},
	})
	if err != nil {
		return err
	}

	// The audit ingester also carries THIS SERVER'S OWN records — an
	// authentication outcome, and the break-glass login M7 requires be
	// flagged in one. They go through the same ingest path a proxy's records
	// take, on a chain of their own (`audit.StreamControl`): the chain, the
	// redaction and the idempotency are the parts that must not have a second
	// implementation.
	emitter, err := audit.NewEmitter(ingest)
	if err != nil {
		return err
	}

	handler, err := south.New(south.Options{
		Identity:       auth,
		Fleet:          registry,
		Decision:       decisions,
		Events:         bus,
		Logs:           ingest,
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

	northSrv, northHandler, err := buildNorth(ctx, cfg, st, emitter, log)
	if err != nil {
		return err
	}

	errs := make(chan error, 4)
	go func() {
		err := srv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errs <- err
	}()

	log.Info("north-bound listener starting",
		"address", cfg.Listeners.North,
		"routes", len(northHandler.Routes()),
	)
	for _, route := range northHandler.Routes() {
		log.Debug("north-bound route",
			"method", route.Method, "pattern", route.Pattern,
			"access", route.Access, "permission", string(route.Permission))
	}
	go func() {
		err := northSrv.ListenAndServe()
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

	auditReader, err := startAuditReadListener(cfg, st, log)
	if err != nil {
		return err
	}
	if auditReader != nil {
		go func() {
			err := auditReader.ListenAndServe()
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
	if auditReader != nil {
		if err := auditReader.Shutdown(shutdownCtx); err != nil {
			log.Warn("the audit read listener did not shut down cleanly", "error", err)
		}
	}
	if err := northSrv.Shutdown(shutdownCtx); err != nil {
		log.Warn("the north-bound listener did not shut down cleanly", "error", err)
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

// startAuditReadListener binds the record read-back path, when one is
// configured.
//
// It returns (nil, nil) when it is not, which is the default: see
// auditread.go for why an unconfigured deployment has no read port at all,
// and for the phase that deletes this one.
func startAuditReadListener(cfg *config.Config, st *store.Store, log *slog.Logger) (*http.Server, error) {
	if cfg.Audit.ReadListener == "" {
		return nil, nil
	}
	ar, err := newAuditReadServer(audit.NewReader(st), store.Tenant(cfg.Tenant), cfg.Audit.ReadToken)
	if err != nil {
		return nil, err
	}
	log.Warn("the local audit read listener is enabled",
		"event", "audit_read_listener_enabled",
		"address", cfg.Audit.ReadListener,
		"note", "this is a pre-0014 operator path and serves audit records; do not expose it",
	)
	return &http.Server{
		Addr:              cfg.Audit.ReadListener,
		Handler:           ar.handler(),
		ReadHeaderTimeout: cfg.South.RequestTimeout,
	}, nil
}
