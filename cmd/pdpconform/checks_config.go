// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hoplock/control/internal/contract"
)

const groupConfig = "fleet configuration (GET/POST /v1/proxies/{proxy_id}/config)"

// CheckConfig grades fleet configuration delivery (proxy D18): the fetch, its
// entity tag, the `config_changed` that names what the fetch then serves, and the
// report.
//
// PUBLISHING IS NOT ON THE CONTRACT — an operator publishes, and no proxy calls
// it — so the suite takes the publish path as an input (`config.publish_url`,
// `publish_method`, `publish_body`, `publish_token`), exactly as it takes
// `events.publish_url`, and asserts nothing about its shape. What it grades is
// what the server ANSWERS afterwards. `version` and `hash` are opaque on the
// wire, so they are compared with each other, never with a literal.
//
// The cases run in order and the first assumes nothing is published yet: run
// the group against a server whose fixtures publish nothing.
func (s *Suite) CheckConfig() {
	e := &s.expect.Config
	fetchPath := func(id string) string {
		return s.baseURL + strings.ReplaceAll(contract.PathProxyConfig, "{proxy_id}", url.PathEscape(id))
	}
	reportPath := func(id string) string {
		return s.baseURL + strings.ReplaceAll(contract.PathProxyConfigReport, "{proxy_id}", url.PathEscape(id))
	}

	s.run(groupConfig, "with nothing published the fetch answers 204", func(c *Case) {
		r, err := s.do(http.MethodGet, fetchPath(e.ProxyID), nil, s.token)
		c.must(err == nil, "request failed: %v", err)
		c.require(r.status == http.StatusNoContent,
			"want 204 — the proxy runs on its bootstrap file alone — got %d: %s", r.status, snippet(r.body))
		c.require(len(r.body) == 0, "a 204 carried a body: %s", snippet(r.body))
	})

	var announced *contract.ConfigChangedEvent
	s.run(groupConfig, "a publish emits config_changed naming the document the fetch then serves", func(c *Case) {
		st, status, err := s.subscribe(e.ProxyID, "")
		c.must(err == nil, "subscribe failed: %v", err)
		defer st.close()
		c.must(status == 200, "subscribe answered %d", status)

		method := e.PublishMethod
		if method == "" {
			method = http.MethodPost
		}
		var body []byte
		if e.PublishBody != "" {
			body = []byte(e.PublishBody)
		}
		token := e.PublishToken
		if token == "" {
			token = s.token
		}
		r, err := s.do(method, e.PublishURL, body, token)
		c.must(err == nil, "publish failed: %v", err)
		c.must(r.status >= 200 && r.status < 300, "publish answered %d: %s", r.status, snippet(r.body))

		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			ev, err := st.next(time.Until(deadline))
			c.must(err == nil, "no config_changed on the stream: %v", err)
			if ev.Type == contract.EventTypeConfigChanged {
				announced = ev.ConfigChanged
				break
			}
		}
		c.must(announced != nil, "the stream carried no config_changed after the publish")
		c.require(announced.Version != "" && announced.Hash != "",
			"config_changed names version %q and hash %q; both are required", announced.Version, announced.Hash)

		f, err := s.do(http.MethodGet, fetchPath(e.ProxyID), nil, s.token)
		c.must(err == nil, "fetch failed: %v", err)
		c.must(f.status == 200, "the fetch after a publish answered %d: %s", f.status, snippet(f.body))
		var doc contract.ProxyConfigDocument
		c.must(f.into(&doc) == nil, "undecodable document: %s", snippet(f.body))
		c.require(doc.Version == announced.Version && doc.Hash == announced.Hash,
			"the stream announced %s/%s and the fetch serves %s/%s; an event must name the document served",
			announced.Version, announced.Hash, doc.Version, doc.Hash)
	})

	s.run(groupConfig, "the fetch answers 200 with an ETag equal to the body's hash, and 304 on it", func(c *Case) {
		f, err := s.do(http.MethodGet, fetchPath(e.ProxyID), nil, s.token)
		c.must(err == nil, "fetch failed: %v", err)
		c.must(f.status == 200, "fetch answered %d: %s", f.status, snippet(f.body))
		var doc contract.ProxyConfigDocument
		c.must(f.into(&doc) == nil, "undecodable document: %s", snippet(f.body))
		etag := f.header.Get("ETag")
		c.require(strings.Trim(strings.TrimPrefix(etag, "W/"), `"`) == doc.Hash,
			"ETag %q is not the body's hash %q", etag, doc.Hash)

		n, err := s.do(http.MethodGet, fetchPath(e.ProxyID), nil, s.token, "If-None-Match", etag)
		c.must(err == nil, "conditional fetch failed: %v", err)
		c.require(n.status == http.StatusNotModified, "If-None-Match with the held ETag answered %d, want 304", n.status)
		c.require(len(n.body) == 0, "a 304 carried a body: %s", snippet(n.body))
	})

	s.run(groupConfig, "an id the server has not enrolled answers 404 not_enrolled, never 401", func(c *Case) {
		for _, path := range []string{fetchPath(e.UnknownProxyID), reportPath(e.UnknownProxyID)} {
			method, body := http.MethodGet, []byte(nil)
			if strings.HasSuffix(path, "/report") {
				method, body = http.MethodPost, s.configReportBody(nil)
			}
			r, err := s.do(method, path, body, s.token)
			c.must(err == nil, "request failed: %v", err)
			c.require(r.status == http.StatusNotFound, "%s %s answered %d, want 404", method, path, r.status)
			c.require(r.status != http.StatusUnauthorized, "an unenrolled id answered 401; it is not a decision about a user")
			var env contract.ErrorResponse
			if c.require(r.into(&env) == nil, "the 404 is not the envelope: %s", snippet(r.body)) {
				c.require(env.Error.Code == contract.ErrCodeNotEnrolled,
					"code %q, want %q", env.Error.Code, contract.ErrCodeNotEnrolled)
			}
		}
	})

	s.run(groupConfig, "a valid report answers 200 accepted, and the same report twice is 200 both times", func(c *Case) {
		body := s.configReportBody(announced)
		for i := range 2 {
			r, err := s.do(http.MethodPost, reportPath(e.ProxyID), body, s.token)
			c.must(err == nil, "report failed: %v", err)
			c.require(r.status == 200,
				"report %d answered %d — the proxy reports after every sync, so a repeat must be accepted: %s",
				i+1, r.status, snippet(r.body))
			var got contract.ProxyConfigReportResponse
			c.require(r.into(&got) == nil && got.Accepted, "report %d is not accepted: %s", i+1, snippet(r.body))
		}
	})
}

// configReportBody is a VALID report: `state` from the enum and `reported_at`
// present, both required. A bare `{}` would be refused by any conforming server
// for reasons that have nothing to do with what the case grades.
func (s *Suite) configReportBody(running *contract.ConfigChangedEvent) []byte {
	rep := contract.ProxyConfigReport{
		State:      contract.ConfigStateApplied,
		ReportedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if running != nil {
		rep.RunningVersion, rep.RunningHash = running.Version, running.Hash
		rep.DesiredVersion, rep.DesiredHash = running.Version, running.Hash
	}
	b, err := json.Marshal(rep)
	if err != nil {
		panic(err)
	}
	return b
}
