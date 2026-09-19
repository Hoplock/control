// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package compile

import (
	"fmt"
	"time"

	"github.com/hoplock/control/internal/policy/model"
)

// Program is a compiled decision program: one bundle, one tenant, one program
// (M18). It is immutable once returned, holds no clock and no connection, and
// is safe to serve from memory to every request on the decision path (M5).
//
// The tenant is on the program because tenancy selects *which program is
// served*, at compile time. It is deliberately not reachable from evaluation:
// nothing in internal/policy reads a tenant from ambient state, and an
// evaluation cannot ask which tenant it is running for, so it cannot
// accidentally become a predicate that M5's budget has no room for.
type Program struct {
	tenant   model.Tenant
	digest   string
	location *time.Location
	rules    []Rule
}

// Tenant is the tenant this program serves.
func (p *Program) Tenant() model.Tenant { return p.tenant }

// Digest is the SHA-256 of the bundle source this was compiled from, so a
// decision record can name the exact document that produced it (M4).
func (p *Program) Digest() string { return p.digest }

// Location is the location day and time-of-day terms are read in.
func (p *Program) Location() *time.Location { return p.location }

// Len is how many rules the program holds. Evaluation is linear in it.
func (p *Program) Len() int { return len(p.rules) }

// Rule returns the i-th rule, in evaluation order.
func (p *Program) Rule(i int) *Rule { return &p.rules[i] }

// Rule is one compiled rule.
type Rule struct {
	id          string
	line        int
	effect      model.Effect
	reason      string
	route       *model.Route
	obligations []model.Obligation
	cache       *model.CacheHint
	match       matcher
}

// ID is the rule id, which every explanation and audit record names.
func (r *Rule) ID() string { return r.id }

// Line is the line the rule was authored on.
func (r *Rule) Line() int { return r.line }

// Effect is what the rule does when it matches.
func (r *Rule) Effect() model.Effect { return r.effect }

// Reason is a deny rule's reason, which is what an operator reads when they
// resolve the decision id.
func (r *Rule) Reason() string { return r.reason }

// Route is an allow rule's authored snapshot, or nil on a deny.
func (r *Rule) Route() *model.Route { return r.route }

// Obligations are what a permitted session must also do.
func (r *Rule) Obligations() []model.Obligation { return r.obligations }

// Cache is the rule's cache hint, or nil where every connection is re-decided.
func (r *Rule) Cache() *model.CacheHint { return r.cache }

// Match reports whether the rule matches, appending the terms that made it do
// so and returning the grant that satisfied a grant constraint, if any.
func (r *Rule) Match(in model.Input, terms *[]model.MatchedTerm) (*model.Grant, bool) {
	return r.match.match(in, terms)
}

// Set is a tenant-keyed collection of compiled programs — the lookup a caller
// performs to obtain the program to evaluate against (M18).
//
// It exists so that "the tenant is part of the lookup key" is a real object
// rather than a convention each caller reinvents, and it is the only place in
// this package a tenant appears at request time.
type Set struct {
	byTenant map[model.Tenant]*Program
}

// NewSet builds a Set. Two programs for one tenant is an error rather than a
// last-write-wins: "which program is served" must have exactly one answer, and
// the database says so too (one active bundle per tenant).
func NewSet(programs ...*Program) (*Set, error) {
	s := &Set{byTenant: make(map[model.Tenant]*Program, len(programs))}
	for _, p := range programs {
		if p == nil {
			return nil, fmt.Errorf("compile: nil program in set")
		}
		if _, dup := s.byTenant[p.tenant]; dup {
			return nil, fmt.Errorf("compile: two programs for tenant %q", p.tenant)
		}
		s.byTenant[p.tenant] = p
	}
	return s, nil
}

// Program returns the program serving a tenant.
func (s *Set) Program(t model.Tenant) (*Program, bool) {
	p, ok := s.byTenant[t]
	return p, ok
}

// Len is how many tenants the set serves.
func (s *Set) Len() int { return len(s.byTenant) }
