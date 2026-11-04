// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package config_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/config"
)

const listenersAndDB = "listeners: {south: \"0.0.0.0:8443\", north: \"127.0.0.1:9443\"}\ndatabase: {dsn: \"postgres://x\"}\n"

// Absent is unsupervised, and the defaults are the documented ones.
func TestNoSupervisorIsTheDefault(t *testing.T) {
	cfg, err := config.Parse(strings.NewReader(listenersAndDB))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Supervisor.Enabled() {
		t.Fatalf("a document without supervisor: is supervised")
	}
	if cfg.Instance.ReportInterval != 10*time.Second || cfg.Instance.HealthInterval != time.Second ||
		cfg.Instance.HealthConnections != 2 || cfg.Supervisor.Heartbeat != 5*time.Second {
		t.Fatalf("defaults = %+v / %+v", cfg.Instance, cfg.Supervisor)
	}
}

func TestTheSupervisorSectionIsChecked(t *testing.T) {
	cases := map[string]string{
		"supervisor: {endpoint: \"https://sup.example.com/r\"}":                                       "supervisor.credential_env",
		"supervisor: {endpoint: \"http://sup.example.com/r\", credential_env: X}":                     "supervisor.endpoint",
		"supervisor: {endpoint: \"https://u:p@sup.example.com/r\", credential_env: X}":                "supervisor.endpoint",
		"supervisor: {endpoint: \"sup.example.com\", credential_env: X}":                              "supervisor.endpoint",
		"supervisor: {credential_env: X}":                                                             "supervisor.credential_env",
		"instance: {health_interval: 10s, health_fresh_for: 5s}":                                      "instance.health_fresh_for",
		"supervisor: {endpoint: \"https://sup.example.com/r\", credential_env: X, unknown_key: true}": "",
	}
	for doc, field := range cases {
		_, err := config.Parse(strings.NewReader(listenersAndDB + doc))
		if err == nil {
			t.Errorf("%s: accepted", doc)
			continue
		}
		var fe *config.FieldError
		if field != "" && (!errors.As(err, &fe) || fe.Field != field) {
			t.Errorf("%s: %v, want a refusal of %s", doc, err, field)
		}
	}
	for _, ok := range []string{
		"supervisor: {endpoint: \"https://sup.example.com/r\", credential_env: X}",
		"supervisor: {endpoint: \"http://127.0.0.1:9/r\", credential_env: X, insecure_transport: true}",
	} {
		cfg, err := config.Parse(strings.NewReader(listenersAndDB + ok))
		if err != nil || !cfg.Supervisor.Enabled() {
			t.Errorf("%s: %v", ok, err)
		}
	}
}
