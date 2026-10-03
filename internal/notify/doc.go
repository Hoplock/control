// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package notify delivers operator notifications: Control's own outbound
// webhook, and every ext.Notifier a host binary registered (M15).
//
// The webhook is Control's CORE answer at the Notifier seam, not a default
// registered behind it: with nothing registered, notifications go to the
// webhook the deployment configured and nowhere else, and a registered notifier
// adds a channel beside it rather than replacing it (ext.Notifier).
//
// Two rules, both from ext.Notifier's contract:
//
//   - NOTHING WAITS ON A NOTIFICATION. Notify queues and returns; delivery
//     happens on a worker per destination, so a slow webhook delays only the
//     webhook, and the act that produced the notification has already
//     committed.
//   - NOTHING FAILS BECAUSE OF ONE. A notification that cannot be delivered is
//     retried, then logged; it never gates access and never turns into a
//     denial (M11).
package notify
