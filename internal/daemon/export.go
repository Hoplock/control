// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"log/slog"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/config"
	"github.com/hoplock/control/internal/export"
	"github.com/hoplock/control/internal/store"
)

// buildSinkDelivery builds delivery to every registered ext.AuditSink (0026),
// or nothing when none is registered: AuditSink is a core point (M15), so a
// deployment without one keeps its records in the local store, writes no sink
// position, and runs nothing here.
func buildSinkDelivery(cfg *config.Config, st *store.Store, extensions *ext.Extensions, log *slog.Logger) (*export.Delivery, error) {
	sinks := extensions.AuditSinks()
	if len(sinks) == 0 {
		return nil, nil
	}
	s := cfg.Audit.Sinks
	delivery, err := export.New(export.Options{
		Store:          st,
		Sinks:          sinks,
		BatchRecords:   s.BatchRecords,
		AttemptTimeout: s.AttemptTimeout,
		IdleInterval:   s.IdleInterval,
		BackoffCap:     s.BackoffCap,
		MaxInFlight:    s.MaxInFlight,
		Logger:         log,
	})
	if err != nil {
		return nil, err
	}
	providers := make([]string, len(sinks))
	for i, b := range sinks {
		providers[i] = b.Registration.Provider
	}
	log.Info("audit records are delivered to every registered sink, and retention does not wait on them",
		"event", "audit_sink_delivery_configured",
		"providers", providers,
		"node", delivery.Node(),
		"batch_records", s.BatchRecords,
		"attempt_timeout", s.AttemptTimeout.String(),
		"idle_interval", s.IdleInterval.String(),
		"backoff_cap", s.BackoffCap.String(),
		"max_in_flight", s.MaxInFlight,
	)
	return delivery, nil
}
