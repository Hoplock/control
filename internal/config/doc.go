// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package config loads the server's YAML configuration file (PLAN §8).
// Decoding is strict: an unknown key is an error rather than a silently
// ignored one, because a typo in a security-relevant setting that boots
// happily is worse than one that refuses to.
//
// The scaffold covers only what the scaffold needs — the two listener
// addresses (M2), the Postgres DSN (M13), the log level, and the tenant
// placeholder (M12). Later phases add their own sections; every key added
// here must also be documented in config.example.yaml.
package config
