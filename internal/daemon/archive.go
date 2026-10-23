// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"log/slog"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/archive"
	"github.com/hoplock/control/internal/config"
	"github.com/hoplock/control/internal/store"
)

// buildArchiveFeed builds the feed to the registered ext.ArchiveStore (0025),
// or nothing when none is registered: ArchiveStore is a disabled point (M15),
// so a deployment without one has no feed, writes no position, and retains as
// it always did.
func buildArchiveFeed(cfg *config.Config, st *store.Store, extensions *ext.Extensions, log *slog.Logger) (*archive.Feed, error) {
	impl, ok := extensions.ArchiveStore()
	if !ok {
		return nil, nil
	}
	a := cfg.Audit.Archive
	feed, err := archive.New(archive.Options{
		Store:             st,
		Archive:           impl,
		Provider:          archiveProvider(extensions),
		BatchRecords:      a.BatchRecords,
		CaptureBatchBytes: a.CaptureBatchBytes,
		AttemptTimeout:    a.AttemptTimeout,
		IdleInterval:      a.IdleInterval,
		BackoffCap:        a.BackoffCap,
		Logger:            log,
	})
	if err != nil {
		return nil, err
	}
	log.Info("audit records are fed to a registered archive, and retention waits on it",
		"event", "archive_feed_configured",
		"provider", archiveProvider(extensions),
		"node", feed.Node(),
		"batch_records", a.BatchRecords,
		"capture_batch_bytes", a.CaptureBatchBytes,
		"attempt_timeout", a.AttemptTimeout.String(),
		"idle_interval", a.IdleInterval.String(),
		"backoff_cap", a.BackoffCap.String(),
	)
	return feed, nil
}

// archiveProvider names the registered archive from the registry's own
// listing, as workflowProvider does the workflow.
func archiveProvider(extensions *ext.Extensions) string {
	for _, st := range extensions.Status() {
		if st.Info.Point == ext.PointArchiveStore && st.Registered() {
			return st.Registrations[0].Provider
		}
	}
	return "unnamed-archive"
}
