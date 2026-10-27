// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package action

import "time"

// SetStartTimeout lets a test shorten how long a runner waits for a handler's
// Start.
func SetStartTimeout(r *Runner, d time.Duration) { r.timeout = d }
