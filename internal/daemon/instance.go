// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/config"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/instance"
	"github.com/hoplock/control/internal/revoke"
	"github.com/hoplock/control/internal/store"
)

// buildInstance builds the deployment's identity and health (0046, M19) and
// starts what runs beside them: this node's report and, when a supervisor is
// configured, the supervisory singleton. stop ends both and closes the pool.
//
// It opens a store pool of its own, capped at instance.health_connections, so
// that health — however often it is polled — reads through connections the
// decision path never waits for (M5).
func buildInstance(ctx context.Context, cfg *config.Config, bus *revoke.Bus, extensions *ext.Extensions,
	log *slog.Logger) (*instance.Service, func(), error) {
	coord, ok := extensions.ClusterCoordinator()
	if !ok {
		return nil, nil, errors.New("daemon: no cluster coordinator is registered, and Control registers one by default")
	}
	var credential string
	if cfg.Supervisor.Enabled() {
		credential = strings.TrimSpace(os.Getenv(cfg.Supervisor.CredentialEnv))
		if credential == "" {
			return nil, nil, fmt.Errorf("daemon: supervisor.credential_env names %s, which is empty", cfg.Supervisor.CredentialEnv)
		}
	}
	healthStore, err := store.Open(ctx, cfg.Database.DSN, store.WithMaxConns(int32(cfg.Instance.HealthConnections)))
	if err != nil {
		return nil, nil, err
	}
	migrations, err := store.LoadMigrations()
	if err != nil {
		healthStore.Close()
		return nil, nil, err
	}
	var expected int64
	if len(migrations) > 0 {
		expected = migrations[len(migrations)-1].Version
	}
	svc, err := instance.New(instance.Options{
		Store:       healthStore,
		Coordinator: coord,
		Fleet: fleet.New(healthStore,
			fleet.WithSubscriptionState(bus),
			fleet.WithLiveness(fleet.Liveness{
				HeartbeatTTL:         cfg.Fleet.HeartbeatTTL,
				RelayRegistrationTTL: cfg.Fleet.RelayRegistrationTTL,
				TargetCapabilityTTL:  cfg.Fleet.TargetCapabilityTTL,
				ReportAfter:          cfg.Fleet.CapabilityReportAfter,
			}),
			fleet.WithLogger(log)),
		Plane: bus,
		Versions: instance.Versions{
			Software: versionString(), Contract: contract.DocumentVersion, ContractCommit: contract.VendoredCommit,
			NorthAPI: north.APIVersion, NorthAPIMin: north.MinAPIVersion,
		},
		DisplayName:    cfg.Instance.DisplayName,
		ExpectedSchema: expected,
		ReportInterval: cfg.Instance.ReportInterval,
		MinInterval:    cfg.Instance.HealthInterval,
		FreshFor:       cfg.Instance.HealthFreshFor,
		Logger:         log,
	})
	if err != nil {
		healthStore.Close()
		return nil, nil, err
	}
	id, err := svc.Identity(ctx, nil)
	if err != nil {
		healthStore.Close()
		return nil, nil, err
	}
	node, err := coord.Node(ctx)
	if err != nil {
		healthStore.Close()
		return nil, nil, err
	}
	log.Info("this deployment's identity",
		"event", "instance_identity", "instance_id", id.InstanceID, "display_name", id.DisplayName,
		"node", string(node.ID), "software", id.Versions.Software, "contract", id.Versions.Contract,
		"north_api", id.Versions.NorthAPI, "supervised", cfg.Supervisor.Enabled())

	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	var wg sync.WaitGroup
	wg.Go(func() { svc.Run(runCtx) })

	if cfg.Supervisor.Enabled() {
		feed := instance.NewFeed(svc, string(node.ID), instance.DefaultFeedRetain)
		reg, err := instance.NewRegistration(svc, feed, instance.RegistrationOptions{
			Endpoint: cfg.Supervisor.Endpoint, Credential: credential,
			Heartbeat: cfg.Supervisor.Heartbeat, DialTimeout: cfg.Supervisor.DialTimeout,
			MaxBackoff: cfg.Supervisor.MaxBackoff, Logger: log,
		})
		if err != nil {
			cancel()
			wg.Wait()
			healthStore.Close()
			return nil, nil, err
		}
		sup := instance.NewSupervision(coord, svc, feed, reg, cfg.Instance.ReportInterval)
		wg.Go(func() {
			if err := sup.Run(runCtx); err != nil {
				log.Error("the supervisory registration could not campaign for its singleton",
					"event", "supervision_singleton_failed", "error", err.Error())
			}
		})
	}

	stop := func() {
		cancel()
		wg.Wait()
		healthStore.Close()
	}
	return svc, stop, nil
}
