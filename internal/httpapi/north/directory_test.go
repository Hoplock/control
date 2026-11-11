// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

// A subject the directory manages, over the wire (0040): the reads show whose
// it is and any deactivation, and an operator's write of it is refused.

// provision seeds what the identity sync leaves for a person: the row and the
// link, deactivated or not.
func (f *serverFixture) provision(t *testing.T, subjectID, externalID string, deactivated bool) {
	t.Helper()
	ctx := t.Context()
	if err := f.st.Subjects().Upsert(ctx, "tenant-a", store.Subject{
		ID: subjectID, Source: "hoplock/scim", DisplayName: "Dana", Principals: []string{"dana"}, Groups: []string{"sre"},
	}); err != nil {
		t.Fatal(err)
	}
	l := store.DirectoryLink{
		Source: "okta-a", ExternalID: externalID, SubjectID: subjectID, Provider: "hoplock/scim",
		LinkedAt: time.Now().UTC(), Provisioned: true, UpdatedAt: time.Now().UTC(), Username: "dana",
	}
	if deactivated {
		l.DeactivatedAt, l.DeactivationReason = time.Now().UTC().Truncate(time.Second), "terminated"
	}
	if err := f.st.DirectoryLinks().Put(ctx, "tenant-a", l); err != nil {
		t.Fatal(err)
	}
}

type directorySubjectView struct {
	ID               string `json:"id"`
	DirectoryManaged bool   `json:"directory_managed"`
	Directory        *struct {
		Provider   string `json:"provider"`
		Source     string `json:"source"`
		ExternalID string `json:"external_id"`
	} `json:"directory"`
	Deactivated  bool `json:"deactivated"`
	Deactivation *struct {
		DeactivatedAt time.Time `json:"deactivated_at"`
		ReasonCode    string    `json:"reason_code"`
	} `json:"deactivation"`
	RecordMissing bool `json:"record_missing"`
}

func TestASubjectTheDirectoryManagesIsShownAndNeverWrittenByAnOperator(t *testing.T) {
	f := newServer(t)
	admin := f.roleToken(t, identity.RoleAdmin)
	auditor := f.roleToken(t, identity.RoleAuditor)
	f.putSubject(t, admin, "local@example.com")
	f.provision(t, "dir-dana", "u-dana", true)

	resp := f.send(t, "GET", api+"/subjects/dir-dana", auditor, nil)
	mustStatus(t, resp, http.StatusOK)
	var one directorySubjectView
	decodeInto(t, resp, &one)
	if !one.DirectoryManaged || one.Directory == nil || one.Directory.Provider != "hoplock/scim" || one.Directory.ExternalID != "u-dana" {
		t.Errorf("GET subject = %+v, want the provider and the external id", one)
	}
	if one.Directory != nil && one.Directory.Source != "okta-a" {
		t.Errorf("GET subject's directory source = %q, want okta-a", one.Directory.Source)
	}
	if !one.Deactivated || one.Deactivation == nil || one.Deactivation.ReasonCode != "terminated" || one.Deactivation.DeactivatedAt.IsZero() {
		t.Errorf("GET subject = %+v, want the deactivation", one.Deactivation)
	}

	resp = f.send(t, "GET", api+"/subjects", auditor, nil)
	mustStatus(t, resp, http.StatusOK)
	var list struct {
		Subjects  []directorySubjectView `json:"subjects"`
		Truncated bool                   `json:"truncated"`
	}
	decodeInto(t, resp, &list)
	byID := map[string]directorySubjectView{}
	for _, s := range list.Subjects {
		byID[s.ID] = s
	}
	if d := byID["dir-dana"]; !d.DirectoryManaged || !d.Deactivated || d.Directory == nil || d.Directory.ExternalID != "u-dana" ||
		d.Directory.Source != "okta-a" {
		t.Errorf("the list shows the directory's subject as %+v", d)
	}
	if l := byID["local@example.com"]; l.DirectoryManaged || l.Directory != nil || l.Deactivated {
		t.Errorf("the list shows a local subject as %+v", l)
	}

	for _, method := range []string{"PUT", "DELETE"} {
		var body any
		if method == "PUT" {
			body = map[string]any{"groups": []string{"admins"}}
		}
		resp := f.send(t, method, api+"/subjects/dir-dana", admin, body)
		mustStatus(t, resp, http.StatusConflict)
		if e := decodeError(t, resp); e.Code != north.CodeSubjectDirectoryManaged || e.Parameters["subject"] != "dir-dana" {
			t.Errorf("%s of a directory subject answered %+v", method, e)
		}
	}
	got, err := f.st.Subjects().Get(t.Context(), "tenant-a", "dir-dana")
	if err != nil || len(got.Groups) != 1 || got.Groups[0] != "sre" {
		t.Errorf("a refused write changed the subject: %+v, %v", got, err)
	}
	// An operator's levers are still there: the lock.
	desk := f.roleToken(t, identity.RoleGrantAdmin)
	mustStatus(t, f.send(t, "POST", api+"/subjects/dir-dana/lock", desk, map[string]any{}), http.StatusOK)
	// And a subject the directory does not manage is written as before.
	mustStatus(t, f.send(t, "PUT", api+"/subjects/local@example.com", admin, map[string]any{"groups": []string{"dba"}}), http.StatusOK)
}

// A deactivation kept for an id the directory has not created yet is readable,
// marked as having no row, and its id cannot be taken by an operator's write.
func TestADeactivationAheadOfItsCreateIsReadableAndItsIDIsTheDirectorys(t *testing.T) {
	f := newServer(t)
	admin := f.roleToken(t, identity.RoleAdmin)
	if err := f.st.DirectoryLinks().Put(t.Context(), "tenant-a", store.DirectoryLink{
		Source: "okta-a", ExternalID: "u-early", SubjectID: "dir-early", Provider: "hoplock/scim", LinkedAt: time.Now().UTC(),
		DeactivatedAt: time.Now().UTC(), DeactivationReason: "offboarded",
	}); err != nil {
		t.Fatal(err)
	}
	resp := f.send(t, "GET", api+"/subjects/dir-early", admin, nil)
	mustStatus(t, resp, http.StatusOK)
	var one directorySubjectView
	decodeInto(t, resp, &one)
	if !one.RecordMissing || !one.Deactivated || one.ID != "dir-early" {
		t.Errorf("GET a kept deactivation = %+v", one)
	}
	resp = f.send(t, "PUT", api+"/subjects/dir-early", admin, map[string]any{"groups": []string{"sre"}})
	mustStatus(t, resp, http.StatusConflict)
}

// The directory's source is `directory.source`, and the subject's top-level
// `source` stays what asserted it (0042): the same word for two things, each
// shown where it belongs.
func TestADirectorySubjectShowsItsDirectorysSourceBesideItsOwn(t *testing.T) {
	f := newServer(t)
	auditor := f.roleToken(t, identity.RoleAuditor)
	f.provision(t, "dir-dana", "u-dana", false)

	resp := f.send(t, "GET", api+"/subjects/dir-dana", auditor, nil)
	mustStatus(t, resp, http.StatusOK)
	var one struct {
		Source    string `json:"source"`
		Directory struct {
			Source string `json:"source"`
		} `json:"directory"`
	}
	decodeInto(t, resp, &one)
	if one.Source != "hoplock/scim" || one.Directory.Source != "okta-a" {
		t.Errorf("source = %q, directory.source = %q; want the sync's provider and the directory's source", one.Source, one.Directory.Source)
	}
}

// The sign-in methods show a tied connector's claim and source, and neither
// for a connector that is not tied (0042).
func TestTheSignInMethodsShowATiedConnectorsSource(t *testing.T) {
	f := newServer(t)
	for name, tie := range map[string][2]string{"okta": {"oid", "okta-a"}, "github": {"", ""}} {
		if err := f.st.Connectors().Upsert(t.Context(), "tenant-a", store.Connector{
			Name: name, Kind: store.ConnectorOIDC, DisplayName: name, Enabled: true, Config: []byte(`{}`),
			DirectoryClaim: tie[0], DirectorySource: tie[1],
		}); err != nil {
			t.Fatal(err)
		}
	}
	resp := f.do(t, "GET", "/api/v1/session/methods", "", nil)
	mustStatus(t, resp, http.StatusOK)
	var body struct {
		Methods []map[string]any `json:"methods"`
	}
	decodeInto(t, resp, &body)
	byName := map[string]map[string]any{}
	for _, m := range body.Methods {
		name, _ := m["name"].(string)
		byName[name] = m
	}
	if m := byName["okta"]; m["directory_claim"] != "oid" || m["directory_source"] != "okta-a" {
		t.Errorf("the tied connector shows %v", m)
	}
	if m := byName["github"]; m == nil || m["directory_claim"] != nil || m["directory_source"] != nil {
		t.Errorf("the untied connector shows %v", m)
	}
}

// `?record=missing` lists the ids a standing denial holds with no subject row
// (0055): a lock that outlived its subject and a deactivation ahead of its
// create, each answered as the single read answers it, and never a subject
// that has a row, locked or not. A value it does not know, and a group beside
// it, are refused rather than answered.
func TestTheSubjectsAStandingDenialHoldsWithNoRecordAreListed(t *testing.T) {
	f := newServer(t)
	admin := f.roleToken(t, identity.RoleAdmin)
	ctx := t.Context()
	for _, id := range []string{"gone", "kept"} {
		if err := f.st.Subjects().Upsert(ctx, "tenant-a", store.Subject{ID: id, Source: "local", Principals: []string{id}}); err != nil {
			t.Fatal(err)
		}
		mustStatus(t, f.send(t, "POST", api+"/subjects/"+id+"/lock", admin, map[string]any{"reason_code": "inc-1"}), http.StatusOK)
	}
	mustStatus(t, f.send(t, "DELETE", api+"/subjects/gone", admin, nil), http.StatusNoContent)
	if err := f.st.DirectoryLinks().Put(ctx, "tenant-a", store.DirectoryLink{
		Source: "okta-a", ExternalID: "u-early", SubjectID: "dir-early", Provider: "hoplock/scim", LinkedAt: time.Now().UTC(),
		DeactivatedAt: time.Now().UTC(), DeactivationReason: "never_started",
	}); err != nil {
		t.Fatal(err)
	}

	resp := f.send(t, "GET", api+"/subjects?record=missing", admin, nil)
	mustStatus(t, resp, http.StatusOK)
	var page struct {
		Subjects []struct {
			ID            string `json:"id"`
			RecordMissing bool   `json:"record_missing"`
			Locked        bool   `json:"locked"`
			Lock          *struct {
				ReasonCode string `json:"reason_code"`
			} `json:"lock"`
			DirectoryManaged bool `json:"directory_managed"`
			Deactivated      bool `json:"deactivated"`
			Deactivation     *struct {
				ReasonCode string `json:"reason_code"`
			} `json:"deactivation"`
		} `json:"subjects"`
		Truncated bool `json:"truncated"`
	}
	decodeInto(t, resp, &page)
	if len(page.Subjects) != 2 || page.Truncated {
		t.Fatalf("listed %+v; want the deleted locked subject and the early deactivation, and nothing cut", page.Subjects)
	}
	early, gone := page.Subjects[0], page.Subjects[1]
	if early.ID != "dir-early" || !early.RecordMissing || !early.DirectoryManaged || !early.Deactivated ||
		early.Deactivation == nil || early.Deactivation.ReasonCode != "never_started" || early.Locked {
		t.Errorf("the early deactivation = %+v", early)
	}
	if gone.ID != "gone" || !gone.RecordMissing || !gone.Locked || gone.Lock == nil || gone.Lock.ReasonCode != "inc-1" ||
		gone.DirectoryManaged || gone.Deactivated {
		t.Errorf("the deleted locked subject = %+v", gone)
	}

	for query, field := range map[string]string{"record=all": "record", "record=missing&group=sre": "record"} {
		resp := f.send(t, "GET", api+"/subjects?"+query, admin, nil)
		var e north.Error
		decodeInto(t, resp, &e)
		if resp.StatusCode != http.StatusBadRequest || e.Code != north.CodeInvalidRequest || e.Parameters["field"] != field {
			t.Errorf("?%s = %d %+v; want 400 invalid_request on %s", query, resp.StatusCode, e, field)
		}
	}
}
