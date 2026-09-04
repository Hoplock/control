// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package export ships audit records to downstream SIEM sinks (Splunk,
// Sentinel, Elastic). An export is a consumer of the audit store, never a
// source of truth: nothing here is ever read back as the record (PLAN M8).
package export
