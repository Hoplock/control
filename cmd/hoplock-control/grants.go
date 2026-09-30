// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/config"
	"github.com/hoplock/control/internal/notify"
	"github.com/hoplock/control/internal/revoke"
	"github.com/hoplock/control/internal/store"
)

// The bring-up of just-in-time grants and of operator notifications (0012).

// settleMargin is added to the decision budget to get revocation's second-pass
// delay: the pass must run after every authorize that could have read the grant
// as live has finished, and "finished" includes writing its record.
const settleMargin = 500 * time.Millisecond

// buildNotifier assembles Control's webhook and the registered notifiers
// (ext.Notifier's core answer, M15).
//
// A webhook that is configured wrongly is a START-UP error, like a named and
// unset key-encryption key: an operator who configured one believes
// notifications are going somewhere.
func buildNotifier(cfg *config.Config, extensions *ext.Extensions, log *slog.Logger) (*notify.Dispatcher, error) {
	var webhook *notify.Webhook
	if raw := strings.TrimSpace(cfg.Notify.WebhookURL); raw != "" {
		var secret []byte
		if variable := strings.TrimSpace(cfg.Notify.WebhookSecretEnv); variable != "" {
			value, ok := os.LookupEnv(variable)
			if !ok || value == "" {
				return nil, fmt.Errorf(
					"notify.webhook_secret_env names %s, which is unset: the webhook cannot sign what it sends", variable)
			}
			secret = []byte(value)
		}
		w, err := notify.NewWebhook(notify.WebhookOptions{
			URL:     raw,
			Secret:  secret,
			Timeout: cfg.Notify.WebhookTimeout,
		})
		if err != nil {
			return nil, err
		}
		webhook = w
		if !w.Signed() {
			log.Warn("the notification webhook is not signed",
				"event", "notify_webhook_unsigned",
				"webhook", w.Host(),
				"note", "set notify.webhook_secret_env so a receiver can tell this server from anybody who learned the URL",
			)
		}
	}

	dispatcher := notify.New(notify.Options{
		Webhook:   webhook,
		Notifiers: extensions.Notifiers(),
		Logger:    log,
	})
	if len(dispatcher.Destinations()) == 0 {
		log.Warn("operator notifications are delivered nowhere",
			"event", "notify_no_destination",
			"note", "set notify.webhook_url, or register an ext.Notifier",
		)
	} else {
		log.Info("operator notifications", "destinations", dispatcher.Destinations())
	}
	return dispatcher, nil
}

// buildGrants assembles the grant service over the store, the audit chain and
// the revocation stream — and, when a host binary registered one, the approval
// workflow every administrator's grant is then routed through.
func buildGrants(
	cfg *config.Config,
	st *store.Store,
	emitter *audit.Emitter,
	bus *revoke.Bus,
	notifier *notify.Dispatcher,
	extensions *ext.Extensions,
	log *slog.Logger,
) (*access.Service, error) {
	opts := access.Options{
		Store:       st,
		Recorder:    emitter,
		Revoker:     bus,
		Notifier:    notifier,
		MaxDuration: cfg.Grants.MaxDuration,
		Settle:      cfg.Decision.Budget + settleMargin,
		Logger:      log,
	}
	if workflow, ok := extensions.GrantWorkflow(); ok {
		opts.Workflow = workflow
		opts.WorkflowProvider = workflowProvider(extensions)
	}
	svc, err := access.New(opts)
	if err != nil {
		return nil, err
	}
	// The configured tenant is polled from the start; any other from its
	// first request (access.Service.Track says why the set is this
	// process's own).
	svc.Track(store.Tenant(cfg.Tenant))
	if opts.Workflow != nil {
		log.Info("grants are routed through an approval workflow",
			"event", "grant_workflow_registered",
			"provider", opts.WorkflowProvider,
			"poll_interval", cfg.Grants.WorkflowPollInterval.String(),
		)
	}
	return svc, nil
}

// workflowProvider names the registered workflow from the registry's own
// listing, so a request says who decided it in the same words the start-up log
// and the north-bound registry use.
func workflowProvider(extensions *ext.Extensions) string {
	for _, st := range extensions.Status() {
		// A single-implementation point lists exactly the registration in
		// play: the extension, where one overrode a default.
		if st.Info.Point == ext.PointGrantWorkflow && st.Registered() {
			return st.Registrations[0].Provider
		}
	}
	return "unnamed-workflow"
}
