// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package store holds the Postgres repositories and the forward-only SQL
// migrations that back them (PLAN M13). Every table carries the tenant column
// and every query filters on it (M12). No ORM: the decision path's queries are
// few, hot, and worth reading.
package store
