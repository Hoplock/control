// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultQueryTimeout bounds a single repository call.
//
// The decision path must answer rather than hang (M5): a timeout the proxy
// classifies as an outage is strictly better than a slow answer that looks
// like one. This is the floor under that promise, not the promise itself —
// 0008 sets the real per-request budget — and it exists so that a caller who
// forgets a deadline gets a bounded failure instead of a held connection.
const DefaultQueryTimeout = 5 * time.Second

// querier is what a repository needs from a connection. Both *pgxpool.Pool and
// pgx.Tx satisfy it, which is what lets the same repository code run inside a
// transaction and outside one without a second implementation.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Store is the repository set. It is obtained from Open, and a second Store
// bound to an open transaction is obtained from InTx.
type Store struct {
	db   querier
	pool *pgxpool.Pool // nil on a transaction-bound Store

	timeout time.Duration
}

// Option configures a Store at Open.
type Option func(*Store)

// WithQueryTimeout overrides DefaultQueryTimeout. A non-positive duration
// disables the store's own bound, leaving the caller's context in charge.
func WithQueryTimeout(d time.Duration) Option {
	return func(s *Store) { s.timeout = d }
}

// Open connects to Postgres and verifies the connection before returning.
//
// It does NOT apply migrations. Migrations are an explicit command (PLAN §8):
// two nodes starting together must not race to build the schema, and a server
// that migrates on boot is a server that does.
func Open(ctx context.Context, dsn string, opts ...Option) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		// The DSN carries a password, so the parse error is not passed
		// through: pgx includes the string it was given (PLAN §8).
		return nil, &Error{Op: "store.Open", Kind: KindInvalid, Err: errDSNUnparseable}
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, wrap("store.Open", err)
	}

	s := &Store{db: pool, pool: pool, timeout: DefaultQueryTimeout}
	for _, opt := range opts {
		opt(s)
	}

	pingCtx, cancel := s.withTimeout(ctx)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, &Error{Op: "store.Open", Kind: KindUnavailable, Err: err}
	}
	return s, nil
}

// errDSNUnparseable stands in for pgx's parse error, which echoes the DSN.
var errDSNUnparseable = fmt.Errorf("database DSN is not a valid Postgres connection string")

// Close releases the connection pool. Calling it on a transaction-bound Store
// is a no-op: the transaction owns the connection, and InTx returns it.
func (s *Store) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

// Pool exposes the underlying pool.
//
// It exists for the test harness and for migrations, both of which need a
// connection this package's repositories do not model. Nothing on the decision
// path should reach through it: a query worth running is a query worth naming
// in a repository, where the tenant argument is not optional.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// withTimeout bounds ctx by the store's query timeout, unless the caller
// already set an earlier deadline — the caller's budget always wins, because
// it is the one that knows what is waiting on the answer.
func (s *Store) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if s.timeout <= 0 {
		return context.WithCancel(ctx)
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= s.timeout {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, s.timeout)
}

// InTx runs fn inside a single transaction, handing it a Store whose
// repositories all use that transaction.
//
// Authorize (0008) needs this: it reads policy inputs and writes a decision
// record on the same path, and a decision record that survives while the read
// it describes is rolled back — or the reverse — is a record that lies.
//
// fn returning an error rolls back and the error is returned unchanged, so a
// caller can still tell a not-found from a failure through the transaction
// boundary. A panic also rolls back, and is re-raised.
func (s *Store) InTx(ctx context.Context, fn func(ctx context.Context, tx *Store) error) (err error) {
	if s.pool == nil {
		return &Error{Op: "store.InTx", Kind: KindInternal, Err: errNestedTx}
	}

	pgtx, err := s.pool.Begin(ctx)
	if err != nil {
		return wrap("store.InTx", err)
	}

	committed := false
	defer func() {
		if committed {
			return
		}
		// Rollback on a context that is already cancelled would fail, and
		// the failure would replace the real error. Give it its own.
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.rollbackTimeout())
		defer cancel()
		_ = pgtx.Rollback(rollbackCtx)
	}()

	txStore := &Store{db: pgtx, timeout: s.timeout}
	if err := fn(ctx, txStore); err != nil {
		return err
	}

	if err := pgtx.Commit(ctx); err != nil {
		return wrap("store.InTx", err)
	}
	committed = true
	return nil
}

// inTx runs fn in a transaction, REUSING the caller's if there already is one.
//
// It exists because a row lock needs a transaction, and the two places that
// take one — the uid cursor's advance and an MFA challenge's poll — may both
// be called from inside InTx, where opening a second transaction on a second
// connection would deadlock against the first. So: when this Store is already
// transaction-bound, run in place; only a pool-bound Store begins anything.
func (s *Store) inTx(ctx context.Context, op string, fn func(context.Context, querier) error) error {
	if s.pool == nil {
		return fn(ctx, s.db)
	}

	pgtx, err := s.pool.Begin(ctx)
	if err != nil {
		return wrap(op, err)
	}
	defer func() {
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.rollbackTimeout())
		defer cancel()
		_ = pgtx.Rollback(rollbackCtx)
	}()

	if err := fn(ctx, pgtx); err != nil {
		return err
	}
	if err := pgtx.Commit(ctx); err != nil {
		return wrap(op, err)
	}
	return nil
}

// errNestedTx reports InTx called on a Store that is already inside one.
var errNestedTx = fmt.Errorf("InTx on a transaction-bound Store: nest the work in the existing transaction instead")

// rollbackTimeout bounds the rollback of a transaction whose context has
// already expired.
func (s *Store) rollbackTimeout() time.Duration {
	if s.timeout <= 0 {
		return DefaultQueryTimeout
	}
	return s.timeout
}

// The repository accessors. Each returns a value bound to this Store's
// connection, so the same call inside InTx uses the transaction.

// Subjects returns the subject repository.
func (s *Store) Subjects() SubjectRepository { return subjectRepo{s} }

// Targets returns the target repository.
func (s *Store) Targets() TargetRepository { return targetRepo{s} }

// Proxies returns the proxy repository.
func (s *Store) Proxies() ProxyRepository { return proxyRepo{s} }

// PolicyBundles returns the policy bundle repository.
func (s *Store) PolicyBundles() PolicyBundleRepository { return bundleRepo{s} }

// Decisions returns the decision record repository.
func (s *Store) Decisions() DecisionRepository { return decisionRepo{s} }

// Audit returns the audit record repository.
func (s *Store) Audit() AuditRepository { return auditRepo{s} }

// Grants returns the grant repository.
func (s *Store) Grants() GrantRepository { return grantRepo{s} }

// UIDCursors returns the uid allocation cursor repository.
func (s *Store) UIDCursors() UIDCursorRepository { return uidRepo{s} }

// ProxyEnrollments returns the enrollment-grant repository (0006).
func (s *Store) ProxyEnrollments() ProxyEnrollmentRepository { return proxyEnrollmentRepo{s} }

// ProxyEdges returns the declared-reachability repository (0006).
func (s *Store) ProxyEdges() ProxyEdgeRepository { return proxyEdgeRepo{s} }

// RelayRegistrations returns the relay-registration repository (0006).
func (s *Store) RelayRegistrations() RelayRegistrationRepository { return relayRegistrationRepo{s} }

// ProxyConfigs returns the fleet configuration repository (0006).
func (s *Store) ProxyConfigs() ProxyConfigRepository { return proxyConfigRepo{s} }

// TargetCapabilities returns the per-target capability repository (0006, M17).
func (s *Store) TargetCapabilities() TargetCapabilityRepository { return targetCapabilityRepo{s} }

// checkTenant rejects an empty tenant.
//
// M18 makes the tenant a value a caller supplies, which means the zero value
// is reachable, which means it must be refused here rather than silently
// matching rows written under "". An empty tenant is a caller bug, and a
// caller bug that reads another tenant's rows is the vulnerability class M18
// exists to close.
func checkTenant(op string, t Tenant) error {
	if t == "" {
		return invalid(op, "tenant is required")
	}
	return nil
}

// SubjectKeys returns the subject key repository (0007).
func (s *Store) SubjectKeys() SubjectKeyRepository { return subjectKeyRepo{s} }

// SubjectPasswords returns the local password repository (0007).
func (s *Store) SubjectPasswords() SubjectPasswordRepository { return passwordRepo{s} }

// MFA returns the second-factor repository (0007).
func (s *Store) MFA() MFARepository { return mfaRepo{s} }

// TargetHostKeys returns the host-key record repository (0007).
func (s *Store) TargetHostKeys() TargetHostKeyRepository { return hostKeyRepo{s} }

// UIDLeases returns the append-only record of granted uid blocks (0007).
func (s *Store) UIDLeases() UIDLeaseRepository { return uidLeaseRepo{s} }

// ProxyTokens returns the proxy channel-credential repository (0007).
func (s *Store) ProxyTokens() ProxyTokenRepository { return proxyTokenRepo{s} }
