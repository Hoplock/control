// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/config"
)

// A webhook configured wrongly is a START-UP error: an operator who set one
// believes notifications are going somewhere.
func TestAMisconfiguredWebhookRefusesToStart(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	none := ext.NoExtensions()

	cfg := &config.Config{}
	cfg.Notify.WebhookURL = "http://hooks.example.com/hoplock"
	if _, err := buildNotifier(cfg, none, log); err == nil {
		t.Error("a clear-text webhook across the network was accepted")
	}

	cfg.Notify.WebhookURL = "https://hooks.example.com/hoplock"
	cfg.Notify.WebhookSecretEnv = "HOPLOCK_TEST_WEBHOOK_KEY_UNSET"
	_, err := buildNotifier(cfg, none, log)
	if err == nil || !strings.Contains(err.Error(), "HOPLOCK_TEST_WEBHOOK_KEY_UNSET") {
		t.Errorf("a named and unset signing key: %v, want an error naming the variable", err)
	}

	t.Setenv("HOPLOCK_TEST_WEBHOOK_KEY", "the key itself")
	cfg.Notify.WebhookSecretEnv = "HOPLOCK_TEST_WEBHOOK_KEY"
	d, err := buildNotifier(cfg, none, log)
	if err != nil {
		t.Fatalf("a correctly configured webhook: %v", err)
	}
	if got := d.Destinations(); len(got) != 1 || got[0] != "webhook" {
		t.Errorf("destinations = %v", got)
	}
}

// With nothing configured and nothing registered, notifications go nowhere —
// which starts, and says so, rather than refusing a deployment that asked for
// no notifications.
func TestNoWebhookIsAStartableAnswer(t *testing.T) {
	d, err := buildNotifier(&config.Config{}, ext.NoExtensions(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("no webhook: %v", err)
	}
	if len(d.Destinations()) != 0 {
		t.Errorf("destinations = %v, want none", d.Destinations())
	}
}
