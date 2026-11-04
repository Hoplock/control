// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"net/url"
	"time"
)

// The deployment's identity and health (`instance:`) and its opt-in outbound
// registration to a supervisor (`supervisor:`), 0046 and PLAN M19.

// Defaults for `instance:`.
const (
	DefaultInstanceReportInterval = 10 * time.Second
	DefaultInstanceHealthInterval = time.Second
	DefaultInstanceHealthFreshFor = 30 * time.Second
	DefaultInstanceHealthConns    = 2
	DefaultSupervisorHeartbeat    = 5 * time.Second
	DefaultSupervisorDialTimeout  = 10 * time.Second
	DefaultSupervisorMaxBackoff   = time.Minute
)

// InstanceConfig is what a deployment says about itself.
type InstanceConfig struct {
	// DisplayName is the operator's name for the deployment. It may change;
	// the instance id, which lives in the database, does not.
	DisplayName string `yaml:"display_name"`
	// ReportInterval is how often each node writes its health report. A
	// node silent for three of these reads as not reporting.
	ReportInterval time.Duration `yaml:"report_interval"`
	// HealthInterval is the most often health is computed, however often
	// it is asked: the ceiling a poller can impose on this deployment.
	HealthInterval time.Duration `yaml:"health_interval"`
	// HealthFreshFor bounds how old a deployment or fleet check may be
	// before it is served as stale.
	HealthFreshFor time.Duration `yaml:"health_fresh_for"`
	// HealthConnections caps the database pool health reads through. It is
	// a pool of its own, so health never holds a connection the decision
	// path needs (M5).
	HealthConnections int `yaml:"health_connections"`
}

// SupervisorConfig is the outbound registration. Absent — no endpoint — is
// unsupervised: the default, and the common case.
type SupervisorConfig struct {
	// Endpoint is the supervisor's registration URL, https. Empty is
	// unsupervised.
	Endpoint string `yaml:"endpoint"`
	// CredentialEnv names the environment variable holding the bearer
	// credential presented to the supervisor. Required with Endpoint; the
	// credential itself is never in this file.
	CredentialEnv string `yaml:"credential_env"`
	// Heartbeat is how often this side sends a heartbeat and reads its feed.
	Heartbeat time.Duration `yaml:"heartbeat"`
	// DialTimeout bounds connecting and waiting for the supervisor's answer.
	DialTimeout time.Duration `yaml:"dial_timeout"`
	// MaxBackoff caps the wait between attempts to reach it.
	MaxBackoff time.Duration `yaml:"max_backoff"`
	// InsecureTransport permits an http:// endpoint. It is for a lab and
	// for tests, and is spelled so nobody sets it by accident.
	InsecureTransport bool `yaml:"insecure_transport"`
}

// Enabled reports whether a supervisor is configured.
func (s SupervisorConfig) Enabled() bool { return s.Endpoint != "" }

func (c *Config) applyInstanceDefaults() {
	if c.Instance.ReportInterval == 0 {
		c.Instance.ReportInterval = DefaultInstanceReportInterval
	}
	if c.Instance.HealthInterval == 0 {
		c.Instance.HealthInterval = DefaultInstanceHealthInterval
	}
	if c.Instance.HealthFreshFor == 0 {
		c.Instance.HealthFreshFor = DefaultInstanceHealthFreshFor
	}
	if c.Instance.HealthConnections == 0 {
		c.Instance.HealthConnections = DefaultInstanceHealthConns
	}
	if c.Supervisor.Heartbeat == 0 {
		c.Supervisor.Heartbeat = DefaultSupervisorHeartbeat
	}
	if c.Supervisor.DialTimeout == 0 {
		c.Supervisor.DialTimeout = DefaultSupervisorDialTimeout
	}
	if c.Supervisor.MaxBackoff == 0 {
		c.Supervisor.MaxBackoff = DefaultSupervisorMaxBackoff
	}
}

func (i InstanceConfig) validate() error {
	switch {
	case i.ReportInterval < 0:
		return &FieldError{Field: "instance.report_interval", Msg: "must not be negative"}
	case i.HealthInterval < 0:
		return &FieldError{Field: "instance.health_interval", Msg: "must not be negative"}
	case i.HealthFreshFor < 0:
		return &FieldError{Field: "instance.health_fresh_for", Msg: "must not be negative"}
	case i.HealthFreshFor < i.HealthInterval:
		return &FieldError{Field: "instance.health_fresh_for",
			Msg: "must be at least instance.health_interval, or every answer is stale when served"}
	case i.HealthConnections < 0 || i.HealthConnections > 16:
		return &FieldError{Field: "instance.health_connections", Msg: "must be between 1 and 16"}
	}
	return nil
}

func (s SupervisorConfig) validate() error {
	if s.Heartbeat < 0 || s.DialTimeout < 0 || s.MaxBackoff < 0 {
		return &FieldError{Field: "supervisor", Msg: "intervals must not be negative"}
	}
	if !s.Enabled() {
		if s.CredentialEnv != "" {
			return &FieldError{Field: "supervisor.credential_env",
				Msg: "names a credential for a supervisor no endpoint configures; set supervisor.endpoint or remove it"}
		}
		return nil
	}
	u, err := url.Parse(s.Endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return &FieldError{Field: "supervisor.endpoint", Msg: "must be an absolute https URL"}
	}
	if u.Scheme == "http" && !s.InsecureTransport {
		return &FieldError{Field: "supervisor.endpoint",
			Msg: "is http; the registration carries a credential, so it is https unless supervisor.insecure_transport is set"}
	}
	if u.User != nil {
		return &FieldError{Field: "supervisor.endpoint", Msg: "must not carry credentials; use supervisor.credential_env"}
	}
	if s.CredentialEnv == "" {
		return &FieldError{Field: "supervisor.credential_env", Msg: "is required with supervisor.endpoint"}
	}
	return nil
}
