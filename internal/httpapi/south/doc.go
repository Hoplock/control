// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package south serves the proxy-facing contract on the south-bound listener
// (PLAN M2). Its authentication is a proxy credential and never an operator
// one. A 401 here means the server decided to deny; every other failure is a
// 5xx that says outage (PLAN M11).
package south
