// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/jackc/pgx/v5"
)

// The deployment's own record (0046, PLAN M19): its identity, the reports its
// nodes write, its tenant set and the supervisory feed.
//
// THIS IS THE ONE REPOSITORY WHOSE METHODS NAME NO TENANT, and it is not in
// tenancy_test.go's list for that reason. What it holds belongs to the
// deployment, which no tenant owns (migration 0024 says why each table is an
// exception to M12). It never reads a tenant's rows: the tenant set is the
// list of tenant NAMES this deployment has served, which is what M19's "its
// tenant set" is, and nothing about any of them beyond when it was last
// served. TestDeploymentRepositoryIsTheOnlyTenantless holds that it stays the
// only one.

// DeploymentIdentity is the logical deployment's record.
type DeploymentIdentity struct {
	// InstanceID is generated once, the first time any node asks, and never
	// changes afterwards — not with a hostname, a pod, or a display name.
	InstanceID string
	CreatedAt  time.Time
	// SupervisionRevokedAt is when the operator revoked the supervisory
	// registration locally; zero when it is not revoked.
	SupervisionRevokedAt time.Time
	// SupervisionRevokedBy is who did, as the command was told.
	SupervisionRevokedBy string
}

// DeploymentNode is one node's last report.
type DeploymentNode struct {
	NodeID     string
	Version    string
	StartedAt  time.Time
	ReportedAt time.Time
	// LeftAt is set by a node that stopped on purpose.
	LeftAt time.Time
	// Report is the node's own report, encoded by the caller.
	Report []byte
}

// DeploymentTenant is one tenant in the deployment's tenant set.
type DeploymentTenant struct {
	Tenant     Tenant
	FirstSeen  time.Time
	LastActive time.Time
}

// SupervisionEntry is one event in the supervisory feed.
type SupervisionEntry struct {
	Seq      int64
	Kind     string
	Body     []byte
	LoggedAt time.Time
	Origin   string
}

// SupervisionStream is the feed's position, as the log keeps it (0027's
// shape): an id under another epoch was not minted here, and a resume from
// below the floor has missed an event that is gone.
type SupervisionStream struct {
	Epoch string
	Head  int64
	Floor int64
}

// DeploymentRepository is the deployment's own record.
type DeploymentRepository interface {
	// Identity returns the deployment's identity, creating it the first time
	// with a fresh instance id. Two nodes asking at once get the same one.
	Identity(ctx context.Context) (DeploymentIdentity, error)
	// SetSupervisionRevoked records (revoked true) or clears a local
	// revocation of the supervisory registration.
	SetSupervisionRevoked(ctx context.Context, revoked bool, by string, at time.Time) (DeploymentIdentity, error)

	// ReportNode writes a node's report, clearing any LeftAt.
	ReportNode(ctx context.Context, n DeploymentNode) error
	// LeaveNode marks a node as having stopped on purpose.
	LeaveNode(ctx context.Context, nodeID string, at time.Time) error
	// Nodes returns every node's last report, by node id. Rows whose last
	// report is older than forgetBefore are deleted first: a node gone that
	// long is history, not health.
	Nodes(ctx context.Context, forgetBefore time.Time) ([]DeploymentNode, error)

	// TouchTenant records that the deployment served a tenant at `at`.
	TouchTenant(ctx context.Context, tenant Tenant, at time.Time) error
	// Tenants returns the deployment's tenant set, by name.
	Tenants(ctx context.Context) ([]DeploymentTenant, error)

	// AppendSupervision logs one supervisory event, keeping the newest
	// `retain` and raising the floor past what it prunes.
	AppendSupervision(ctx context.Context, e SupervisionEntry, retain int) (SupervisionEntry, SupervisionStream, error)
	// SupervisionAfter returns events after a position, oldest first.
	SupervisionAfter(ctx context.Context, after int64, limit int) ([]SupervisionEntry, error)
	// SupervisionStream returns the feed's epoch, head and floor.
	SupervisionStream(ctx context.Context) (SupervisionStream, error)
}

// Deployment returns the deployment's own record (0046).
func (s *Store) Deployment() DeploymentRepository { return deploymentRepo{s} }

type deploymentRepo struct{ s *Store }

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func (r deploymentRepo) Identity(ctx context.Context) (DeploymentIdentity, error) {
	const op = "store.Deployment.Identity"
	id, err := randomHex(16)
	if err != nil {
		return DeploymentIdentity{}, wrap(op, err)
	}
	epoch, err := randomHex(8)
	if err != nil {
		return DeploymentIdentity{}, wrap(op, err)
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	// ON CONFLICT DO NOTHING is what makes two nodes starting together agree:
	// the first insert wins and both read it back.
	if _, err := r.s.db.Exec(ctx, `
		INSERT INTO deployment (instance_id, created_at, log_epoch) VALUES ($1, $2, $3)
		ON CONFLICT (singleton) DO NOTHING`,
		"inst-"+id, time.Now().UTC(), epoch); err != nil {
		return DeploymentIdentity{}, wrap(op, err)
	}
	return r.read(ctx, op)
}

func (r deploymentRepo) read(ctx context.Context, op string) (DeploymentIdentity, error) {
	var (
		d       DeploymentIdentity
		revoked *time.Time
	)
	err := r.s.db.QueryRow(ctx, `
		SELECT instance_id, created_at, supervision_revoked_at, supervision_revoked_by FROM deployment`).
		Scan(&d.InstanceID, &d.CreatedAt, &revoked, &d.SupervisionRevokedBy)
	if err != nil {
		return DeploymentIdentity{}, wrap(op, err)
	}
	if revoked != nil {
		d.SupervisionRevokedAt = revoked.UTC()
	}
	d.CreatedAt = d.CreatedAt.UTC()
	return d, nil
}

func (r deploymentRepo) SetSupervisionRevoked(ctx context.Context, revoked bool, by string, at time.Time) (DeploymentIdentity, error) {
	const op = "store.Deployment.SetSupervisionRevoked"
	if _, err := r.Identity(ctx); err != nil {
		return DeploymentIdentity{}, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	var err error
	if revoked {
		if at.IsZero() {
			return DeploymentIdentity{}, invalid(op, "a revocation has a time")
		}
		_, err = r.s.db.Exec(ctx, `
			UPDATE deployment SET supervision_revoked_at = COALESCE(supervision_revoked_at, $1),
			                      supervision_revoked_by = CASE WHEN supervision_revoked_at IS NULL THEN $2 ELSE supervision_revoked_by END`,
			at.UTC(), by)
	} else {
		_, err = r.s.db.Exec(ctx, `UPDATE deployment SET supervision_revoked_at = NULL, supervision_revoked_by = ''`)
	}
	if err != nil {
		return DeploymentIdentity{}, wrap(op, err)
	}
	return r.read(ctx, op)
}

func (r deploymentRepo) ReportNode(ctx context.Context, n DeploymentNode) error {
	const op = "store.Deployment.ReportNode"
	switch {
	case n.NodeID == "":
		return invalid(op, "a node report names its node")
	case n.ReportedAt.IsZero() || n.StartedAt.IsZero():
		return invalid(op, "a node report has its start and report times")
	}
	report := n.Report
	if len(report) == 0 {
		report = []byte("{}")
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	_, err := r.s.db.Exec(ctx, `
		INSERT INTO deployment_nodes (node_id, version, started_at, reported_at, left_at, report)
		VALUES ($1, $2, $3, $4, NULL, $5)
		ON CONFLICT (node_id) DO UPDATE SET
			version = EXCLUDED.version, started_at = EXCLUDED.started_at,
			reported_at = EXCLUDED.reported_at, left_at = NULL, report = EXCLUDED.report`,
		n.NodeID, n.Version, n.StartedAt.UTC(), n.ReportedAt.UTC(), string(report))
	return wrap(op, err)
}

func (r deploymentRepo) LeaveNode(ctx context.Context, nodeID string, at time.Time) error {
	const op = "store.Deployment.LeaveNode"
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	_, err := r.s.db.Exec(ctx, `UPDATE deployment_nodes SET left_at = $2 WHERE node_id = $1`, nodeID, at.UTC())
	return wrap(op, err)
}

func (r deploymentRepo) Nodes(ctx context.Context, forgetBefore time.Time) ([]DeploymentNode, error) {
	const op = "store.Deployment.Nodes"
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	if !forgetBefore.IsZero() {
		if _, err := r.s.db.Exec(ctx, `DELETE FROM deployment_nodes WHERE reported_at < $1`, forgetBefore.UTC()); err != nil {
			return nil, wrap(op, err)
		}
	}
	rows, err := r.s.db.Query(ctx, `
		SELECT node_id, version, started_at, reported_at, left_at, report
		FROM deployment_nodes ORDER BY node_id`)
	if err != nil {
		return nil, wrap(op, err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (DeploymentNode, error) {
		var (
			n      DeploymentNode
			left   *time.Time
			report string
		)
		if err := row.Scan(&n.NodeID, &n.Version, &n.StartedAt, &n.ReportedAt, &left, &report); err != nil {
			return n, err
		}
		n.StartedAt, n.ReportedAt = n.StartedAt.UTC(), n.ReportedAt.UTC()
		if left != nil {
			n.LeftAt = left.UTC()
		}
		n.Report = []byte(report)
		return n, nil
	})
	return out, wrap(op, err)
}

func (r deploymentRepo) TouchTenant(ctx context.Context, tenant Tenant, at time.Time) error {
	const op = "store.Deployment.TouchTenant"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	_, err := r.s.db.Exec(ctx, `
		INSERT INTO deployment_tenants (tenant, first_seen, last_active) VALUES ($1, $2, $2)
		ON CONFLICT (tenant) DO UPDATE SET last_active = GREATEST(deployment_tenants.last_active, EXCLUDED.last_active)`,
		tenant, at.UTC())
	return wrap(op, err)
}

func (r deploymentRepo) Tenants(ctx context.Context) ([]DeploymentTenant, error) {
	const op = "store.Deployment.Tenants"
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	rows, err := r.s.db.Query(ctx, `SELECT tenant, first_seen, last_active FROM deployment_tenants ORDER BY tenant`)
	if err != nil {
		return nil, wrap(op, err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (DeploymentTenant, error) {
		var t DeploymentTenant
		err := row.Scan(&t.Tenant, &t.FirstSeen, &t.LastActive)
		t.FirstSeen, t.LastActive = t.FirstSeen.UTC(), t.LastActive.UTC()
		return t, err
	})
	return out, wrap(op, err)
}

func (r deploymentRepo) AppendSupervision(ctx context.Context, e SupervisionEntry, retain int) (SupervisionEntry, SupervisionStream, error) {
	const op = "store.Deployment.AppendSupervision"
	switch {
	case retain <= 0:
		return SupervisionEntry{}, SupervisionStream{}, invalid(op, "retain must be positive")
	case e.Kind == "" || len(e.Body) == 0:
		return SupervisionEntry{}, SupervisionStream{}, invalid(op, "an event has a kind and a body")
	case e.LoggedAt.IsZero():
		return SupervisionEntry{}, SupervisionStream{}, invalid(op, "an event has a time")
	}
	if _, err := r.Identity(ctx); err != nil {
		return SupervisionEntry{}, SupervisionStream{}, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	var st SupervisionStream
	err := r.s.inTx(ctx, op, func(ctx context.Context, q querier) error {
		// The feed's publish lock, held to the commit, so positions commit
		// in the order they were taken (0027's rule).
		if err := q.QueryRow(ctx, `SELECT log_epoch, log_head, log_floor FROM deployment FOR UPDATE`).
			Scan(&st.Epoch, &st.Head, &st.Floor); err != nil {
			return wrap(op, err)
		}
		e.Seq = st.Head + 1
		if _, err := q.Exec(ctx, `
			INSERT INTO supervision_log (seq, kind, body, logged_at, origin) VALUES ($1, $2, $3, $4, $5)`,
			e.Seq, e.Kind, string(e.Body), e.LoggedAt.UTC(), e.Origin); err != nil {
			return wrap(op, err)
		}
		var pruned int64
		if err := q.QueryRow(ctx, `
			WITH doomed AS (SELECT seq FROM supervision_log ORDER BY seq DESC OFFSET $1),
			gone AS (DELETE FROM supervision_log l USING doomed d WHERE l.seq = d.seq RETURNING l.seq)
			SELECT COALESCE(max(seq), 0) FROM gone`, retain).Scan(&pruned); err != nil {
			return wrap(op, err)
		}
		return wrap(op, q.QueryRow(ctx, `
			UPDATE deployment SET log_head = $1, log_floor = GREATEST(log_floor, $2)
			RETURNING log_epoch, log_head, log_floor`, e.Seq, pruned).Scan(&st.Epoch, &st.Head, &st.Floor))
	})
	if err != nil {
		return SupervisionEntry{}, SupervisionStream{}, err
	}
	return e, st, nil
}

func (r deploymentRepo) SupervisionAfter(ctx context.Context, after int64, limit int) ([]SupervisionEntry, error) {
	const op = "store.Deployment.SupervisionAfter"
	if limit <= 0 {
		return nil, invalid(op, "limit must be positive")
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	rows, err := r.s.db.Query(ctx, `
		SELECT seq, kind, body, logged_at, origin FROM supervision_log
		WHERE seq > $1 ORDER BY seq LIMIT $2`, after, limit)
	if err != nil {
		return nil, wrap(op, err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (SupervisionEntry, error) {
		var (
			e    SupervisionEntry
			body string
		)
		err := row.Scan(&e.Seq, &e.Kind, &body, &e.LoggedAt, &e.Origin)
		e.Body, e.LoggedAt = []byte(body), e.LoggedAt.UTC()
		return e, err
	})
	return out, wrap(op, err)
}

func (r deploymentRepo) SupervisionStream(ctx context.Context) (SupervisionStream, error) {
	const op = "store.Deployment.SupervisionStream"
	if _, err := r.Identity(ctx); err != nil {
		return SupervisionStream{}, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	var st SupervisionStream
	err := r.s.db.QueryRow(ctx, `SELECT log_epoch, log_head, log_floor FROM deployment`).
		Scan(&st.Epoch, &st.Head, &st.Floor)
	return st, wrap(op, err)
}
