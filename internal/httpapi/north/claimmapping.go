// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/provision"
	"github.com/hoplock/control/internal/store"
)

// The claim mapping's two reads for an editor (0068): the directories a tenant
// holds, which a scoped entry lists by their source, and a dry run of a
// document, which answers what it would produce without storing it.
//
// Neither changes anything. The mapping's one write is still `PUT
// …/identity/claim-mapping`, and a version is still its document and that
// document's digest: a dry run's digest is the one a PUT of the same bytes
// would store, because both are [identity.ParseMapping]'s.

// directorySourceList is the directory sources list (M26): by source, the one
// key a distinct source has. Its cursor binds no filter, because the list has
// none.
var directorySourceList = list{name: "directories", key: []keyKind{keyString}}

// directorySourceView is one directory that has written in the tenant.
type directorySourceView struct {
	// Source is the directory's name as its sync sends it (0042): what a
	// scoped group entry lists under `directories`.
	Source string `json:"source"`
	// Linked counts the identities this directory has provisioned in the
	// tenant, the deactivated ones among them; Deactivated counts those.
	Linked      int64 `json:"linked"`
	Deactivated int64 `json:"deactivated"`
}

// handleDirectorySourceList answers the directory sources that have written in
// the tenant, one page at a time (0068, M26). A source a directory wrote only a
// group under is listed with no identities.
func (s *Server) handleDirectorySourceList(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	p, ok := readPage(w, r, directorySourceList)
	if !ok {
		return
	}
	page, err := s.st.DirectoryLinks().SourcePage(r.Context(), tenant, p.Page)
	if err != nil {
		s.fail(w, r, "list directory sources", err)
		return
	}
	out := make([]directorySourceView, 0, len(page.Rows))
	for _, d := range page.Rows {
		out = append(out, directorySourceView{Source: d.Source, Linked: d.Linked, Deactivated: d.Deactivated})
	}
	var last []any
	if n := len(page.Rows); n > 0 {
		last = []any{page.Rows[n-1].Source}
	}
	s.answerPage(w, r, p, "directories", out, page.More, last, nil)
}

// MaxMappingSamples bounds the samples one dry run maps. A sample is one
// identity an operator is looking at, and an editor previews a handful; a
// bound keeps the answer sized by the caller (M26's rule, for a list the
// caller writes).
const MaxMappingSamples = 16

// claimMappingValidateBody is a dry run's request.
type claimMappingValidateBody struct {
	Document string               `json:"document"`
	Samples  []claimMappingSample `json:"samples"`
}

// claimMappingSample is one identity to map: the claims it carries, the group
// names it carries, and — for an identity a directory provisions — the
// directory it comes from. With no directory it is a sign-in: its groups are
// offered under the mapping's `groups.claim`, as a broker's assertion carries
// them; with one, its claims are a directory's attributes (its `email` the
// directory's email) and its groups that directory's group names (0040, 0043).
//
// Or it names a provisioned directory identity (`subject`) and nothing else,
// and is mapped from what that directory last wrote for it, exactly as the
// recalculation would (provision.Mapped). What a directory wrote is not
// readable on this surface, and a document written to copy a field into an
// attribute would read it back, so such a sample is answered only to a caller
// holding identity:write — who can write that document and read the result
// anyway — and refused `403 forbidden` to anyone else.
type claimMappingSample struct {
	Claims          map[string]string `json:"claims"`
	Groups          []string          `json:"groups"`
	DirectorySource string            `json:"directory_source,omitempty"`
	Subject         string            `json:"subject,omitempty"`
}

// claimMappingValidateResponse is what a document that parses would be: the
// digest a PUT of it would store, the read's own members as `GET
// …/identity/claim-mapping` would answer them, and each sample mapped.
type claimMappingValidateResponse struct {
	Digest             string   `json:"digest"`
	Attributes         []string `json:"attributes"`
	Groups             []string `json:"groups"`
	Directories        []string `json:"directories"`
	RequireDirectories bool     `json:"require_directories"`
	// GroupMap is always present here, `[]` when the document maps no
	// group: unlike the read's, it was never answered without it.
	GroupMap []groupEntryView   `json:"group_map"`
	Samples  []mappedSampleView `json:"samples"`
}

// mappedSampleView is what the document produces for one sample: the
// attributes, by name, and the groups, sorted. DirectorySource is the door it
// was mapped through: the sample's own, or a named subject's directory, and
// absent for a sign-in.
type mappedSampleView struct {
	Attributes      map[string]string `json:"attributes"`
	Groups          []string          `json:"groups"`
	DirectorySource string            `json:"directory_source,omitempty"`
}

// handleClaimMappingValidate is the mapping's dry run (0068). It parses the
// document exactly as the PUT does and refuses it exactly as the PUT does; one
// that parses is answered with what it would be and what it would produce for
// each sample, through the mapping's own Apply and ApplyDirectory (M7: one
// mapping serves both doors). It stores nothing, records nothing and starts no
// recalculation, so identity:read may ask it.
func (s *Server) handleClaimMappingValidate(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	var body claimMappingValidateBody
	if err := decodeJSON(r, &body); err != nil {
		s.refuseBody(w, r, err)
		return
	}
	if strings.TrimSpace(body.Document) == "" {
		s.invalid(w, r, "document", access.ProblemRequired, "")
		return
	}
	if len(body.Samples) > MaxMappingSamples {
		s.invalid(w, r, "samples", access.ProblemTooMany, strconv.Itoa(MaxMappingSamples))
		return
	}
	named := false
	for i, sample := range body.Samples {
		if sample.Subject != "" {
			// A named identity is mapped from its directory's data, so
			// nothing else may describe it.
			if sample.Claims != nil || sample.Groups != nil || sample.DirectorySource != "" {
				s.invalid(w, r, fmt.Sprintf("samples[%d].subject", i), access.ProblemInvalid, "")
				return
			}
			named = true
			continue
		}
		if sample.DirectorySource != "" && identity.DirectorySourceProblem(sample.DirectorySource) != "" {
			s.invalid(w, r, fmt.Sprintf("samples[%d].directory_source", i), access.ProblemInvalid, "")
			return
		}
	}
	if p, _ := PrincipalFrom(r.Context()); named && (p == nil || !p.Can(tenant, identity.PermIdentityWrite)) {
		writeError(w, http.StatusForbidden, Error{
			Code: CodeForbidden, CorrelationID: CorrelationIDFrom(r.Context()),
			Message: "a sample naming an identity is mapped from its directory's data, which only identity:write may read this way",
			Parameters: map[string]any{
				"tenant":     tenant.String(),
				"permission": string(identity.PermIdentityWrite),
			},
		})
		return
	}
	m, err := identity.ParseMapping([]byte(body.Document))
	if err != nil {
		var rejected *identity.MappingRejected
		if errors.As(err, &rejected) {
			refuseMapping(w, r, rejected)
			return
		}
		s.fail(w, r, "validate claim mapping", err)
		return
	}
	samples := make([]mappedSampleView, 0, len(body.Samples))
	for i, sample := range body.Samples {
		if sample.Subject == "" {
			samples = append(samples, viewMapped(mapSample(m, sample), sample.DirectorySource))
			continue
		}
		link, err := s.st.DirectoryLinks().BySubject(r.Context(), tenant, sample.Subject)
		if errors.Is(err, store.ErrNotFound) || (err == nil && !link.Provisioned) {
			// Not an identity a directory provisioned in this tenant:
			// there is no directory data to map.
			s.invalid(w, r, fmt.Sprintf("samples[%d].subject", i), access.ProblemInvalid, "")
			return
		}
		if err != nil {
			s.fail(w, r, "read a directory identity to preview", err)
			return
		}
		attrs, err := provision.Mapped(r.Context(), s.st, tenant, link, m)
		if err != nil {
			s.fail(w, r, "read a directory identity's groups to preview", err)
			return
		}
		samples = append(samples, viewMapped(attrs, link.Source))
	}
	groupMap := viewGroupEntries(m.GroupEntries())
	if groupMap == nil {
		groupMap = []groupEntryView{}
	}
	answer(w, http.StatusOK, claimMappingValidateResponse{
		Digest:             m.Digest,
		Attributes:         m.MappedAttributeNames(),
		Groups:             m.MappedGroupNames(),
		Directories:        m.MappedDirectories(),
		RequireDirectories: m.RequiresDirectories(),
		GroupMap:           groupMap,
		Samples:            samples,
	})
}

// mapSample maps one sample through the door it names: a directory's
// ApplyDirectory when it names a source, a sign-in's Apply otherwise.
func mapSample(m *identity.Mapping, sample claimMappingSample) identity.Attributes {
	if sample.DirectorySource != "" {
		attributes := make(map[string]string, len(sample.Claims))
		email := ""
		for name, value := range sample.Claims {
			if name == identity.ClaimEmail {
				email = value
				continue
			}
			attributes[name] = value
		}
		return m.ApplyDirectory(sample.DirectorySource, attributes, email, sample.Groups)
	}
	a := identity.Assertion{Claims: map[string]string{}}
	for name, value := range sample.Claims {
		a.Claims[name] = value
	}
	if claim := m.GroupClaim(); claim != "" && sample.Groups != nil {
		a.MultiClaims = map[string][]string{claim: sample.Groups}
	}
	return m.Apply(a)
}

func viewMapped(a identity.Attributes, source string) mappedSampleView {
	out := mappedSampleView{Attributes: a.Claims, Groups: a.Groups, DirectorySource: source}
	if out.Attributes == nil {
		out.Attributes = map[string]string{}
	}
	if out.Groups == nil {
		out.Groups = []string{}
	}
	return out
}

// refuseMapping answers a document the mapping refused: its rejections, field
// by field, as the compiler's are for a bundle (M21). The PUT and the dry run
// both answer it, so a refusal reads the same whichever was asked.
func refuseMapping(w http.ResponseWriter, r *http.Request, rejected *identity.MappingRejected) {
	writeError(w, http.StatusBadRequest, Error{
		Code: CodeInvalidRequest, CorrelationID: CorrelationIDFrom(r.Context()),
		Parameters: map[string]any{"field": "document", "problem": "invalid", "rejections": viewMappingRejections(rejected.Rejections)},
		Message:    rejected.Error(),
	})
}
