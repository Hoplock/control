// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

// The extensions the console's Extensions and Reports screens are checked
// against (0056), each registered in the host's registry as a host registers
// its own (M15). Each is what a real one does in the state it stands for,
// through the same code paths, and none binds a route:
//
//   - an action handler whose Start fails (`e2e-actions`): registered and not
//     started, with the error's kind, while Control serves without it;
//   - two audit sinks: `e2e-siem`, whose Export always fails — behind and
//     failing — and `e2e-lake`, which confirms every batch — caught up;
//   - two report packs: `example.com/e2e/compliance`, with a report that
//     generates and one whose body breaks after its first bytes, and
//     `example.com/e2e/flaky`, which cannot list its reports — unavailable.
//
// The identity sync the fixture already registers (main.go) is the point
// that started.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/hoplock/control/ext"
)

const (
	actionProvider     = "e2e-actions"
	siemProvider       = "e2e-siem"
	lakeProvider       = "e2e-lake"
	compliancePack     = "example.com/e2e/compliance"
	flakyPack          = "example.com/e2e/flaky"
	reportAccessReview = "access-review"
	reportPrivileged   = "privileged-sessions"
)

// failingActions is an action handler whose platform is not configured: its
// Start fails with KindUnavailable, as a handler that cannot reach what it
// listens to at start would.
type failingActions struct{}

func (failingActions) Start(context.Context, ext.ActionExecutor) error {
	return ext.Errorf(ext.PointActionHandler, actionProvider, "Start", ext.KindUnavailable,
		"the fixture's response platform is not configured")
}

func (failingActions) Stop(context.Context) error { return nil }

// failingSink cannot reach its collector: every batch fails, and is retried.
type failingSink struct{}

func (failingSink) Export(context.Context, []ext.AuditRecord) error {
	return ext.Errorf(ext.PointAuditSink, siemProvider, "Export", ext.KindUnavailable, "the fixture's collector refuses every batch")
}

// confirmingSink confirms every batch it is handed.
type confirmingSink struct{}

func (confirmingSink) Export(context.Context, []ext.AuditRecord) error { return nil }

// compliance is a report pack with two reports: one that generates in the
// format asked for, and one whose body fails after its first bytes, as a
// pack whose store fails half-way through a long report does.
type compliance struct{}

func (compliance) Reports(context.Context, ext.Tenant) ([]ext.ReportDescriptor, error) {
	return []ext.ReportDescriptor{
		{
			ReportID: reportAccessReview,
			Formats:  []ext.ReportFormat{ext.ReportFormatCSV, ext.ReportFormatJSON},
			Parameters: []ext.ReportParameter{
				{Name: "scope", Required: true, Values: []string{"all", "privileged"}},
				{Name: "owner"},
			},
		},
		{ReportID: reportPrivileged, Formats: []ext.ReportFormat{ext.ReportFormatCSV}},
	}, nil
}

func (compliance) Generate(_ context.Context, req ext.ReportRequest) (ext.Report, error) {
	switch req.ReportID {
	case reportAccessReview:
		rows := [][2]string{{"ada", "admin"}, {"avery", "auditor"}, {"fenn", "fleet-admin"}, {"gia", "grant-admin"}}
		if req.Parameters["scope"] == "privileged" {
			rows = rows[:1]
		}
		var body []byte
		contentType := "text/csv; charset=utf-8"
		if req.Format == ext.ReportFormatJSON {
			type row struct {
				Subject string `json:"subject"`
				Role    string `json:"role"`
			}
			out := struct {
				Report string    `json:"report"`
				Scope  string    `json:"scope"`
				Owner  string    `json:"owner,omitempty"`
				From   time.Time `json:"from"`
				To     time.Time `json:"to"`
				Rows   []row     `json:"rows"`
			}{Report: req.ReportID, Scope: req.Parameters["scope"], Owner: req.Parameters["owner"], From: req.From, To: req.To}
			for _, r := range rows {
				out.Rows = append(out.Rows, row{Subject: r[0], Role: r[1]})
			}
			var err error
			if body, err = json.Marshal(out); err != nil {
				return ext.Report{}, err
			}
			contentType = "application/json"
		} else {
			var b strings.Builder
			b.WriteString("subject,role\n")
			for _, r := range rows {
				fmt.Fprintf(&b, "%s,%s\n", r[0], r[1])
			}
			body = []byte(b.String())
		}
		return ext.Report{
			ReportID: req.ReportID, GeneratedAt: time.Now().UTC(), Format: req.Format,
			ContentType: contentType, Body: io.NopCloser(bytes.NewReader(body)),
		}, nil
	case reportPrivileged:
		return ext.Report{
			ReportID: req.ReportID, GeneratedAt: time.Now().UTC(), Format: req.Format,
			ContentType: "text/csv; charset=utf-8", Body: &breaksAfter{head: []byte("session,subject,target\n")},
		}, nil
	}
	return ext.Report{}, ext.Errorf(ext.PointReportProvider, compliancePack, "Generate", ext.KindNotFound, "no report %q", req.ReportID)
}

// breaksAfter yields its head and then fails: a body that ends broken after
// its first byte, which Control aborts rather than ends short (0044).
type breaksAfter struct {
	head []byte
	done bool
}

func (b *breaksAfter) Read(p []byte) (int, error) {
	if !b.done {
		b.done = true
		return copy(p, b.head), nil
	}
	return 0, errors.New("the fixture's store failed half-way through the report")
}

func (*breaksAfter) Close() error { return nil }

// flaky is a report pack that cannot list its reports.
type flaky struct{}

func (flaky) Reports(context.Context, ext.Tenant) ([]ext.ReportDescriptor, error) {
	return nil, ext.Errorf(ext.PointReportProvider, flakyPack, "Reports", ext.KindUnavailable, "the fixture's report store is down")
}

func (flaky) Generate(context.Context, ext.ReportRequest) (ext.Report, error) {
	return ext.Report{}, ext.Errorf(ext.PointReportProvider, flakyPack, "Generate", ext.KindUnavailable, "the fixture's report store is down")
}

// registerExtensions adds every extension above to the host's registry.
func registerExtensions(r *ext.Registry) error {
	return errors.Join(
		r.RegisterActionHandler(ext.Registration{Provider: actionProvider, Version: "e2e"}, failingActions{}),
		r.RegisterAuditSink(ext.Registration{Provider: siemProvider, Version: "e2e"}, failingSink{}),
		r.RegisterAuditSink(ext.Registration{Provider: lakeProvider, Version: "e2e"}, confirmingSink{}),
		r.RegisterReportProvider(ext.Registration{Provider: compliancePack, Version: "1.4.0"}, compliance{}),
		r.RegisterReportProvider(ext.Registration{Provider: flakyPack, Version: "0.3.0"}, flaky{}),
	)
}
