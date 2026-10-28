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
		ExternalID: externalID, SubjectID: subjectID, Provider: "hoplock/scim",
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
	if d := byID["dir-dana"]; !d.DirectoryManaged || !d.Deactivated || d.Directory == nil || d.Directory.ExternalID != "u-dana" {
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
		ExternalID: "u-early", SubjectID: "dir-early", Provider: "hoplock/scim", LinkedAt: time.Now().UTC(),
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
