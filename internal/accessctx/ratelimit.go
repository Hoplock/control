// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package accessctx

import (
	"math"
	"sync"
	"time"
)

// limiter is a token bucket per integration per tenant.
//
// It is node-local, and that is acceptable for what it is: a bound on how fast
// one integration can make this server write audit records and grants, not an
// access decision. A deployment of N nodes admits N times the rate, which is
// still a bound.
type limiter struct {
	rate  float64
	burst float64
	now   func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	at     time.Time
}

func newLimiter(rate float64, burst int, now func() time.Time) *limiter {
	return &limiter{rate: rate, burst: float64(burst), now: now, buckets: map[string]*bucket{}}
}

// allow takes one token for key, or says how long until one is available.
func (l *limiter) allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, at: now}
		l.buckets[key] = b
	}
	if elapsed := now.Sub(b.at).Seconds(); elapsed > 0 {
		b.tokens = math.Min(l.burst, b.tokens+elapsed*l.rate)
	}
	b.at = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
	return false, max(wait, time.Second)
}
