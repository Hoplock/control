// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/hoplock/control/internal/contract"
)

// The suite's machinery: one HTTP client, one result list, and a `case`
// abstraction that lets an assertion fail without taking the rest of the run
// with it. Every check in this binary is written against these.

// Status is the outcome of one assertion.
type Status string

const (
	StatusPass Status = "PASS"
	StatusFail Status = "FAIL"
)

// Result is one graded assertion.
type Result struct {
	Group   string
	Name    string
	Status  Status
	Details []string
}

// Suite drives one server.
type Suite struct {
	baseURL string
	token   string
	expect  *Expectations
	client  *http.Client
	results []Result
	// seq makes ids unique within a run, so a re-run against a stateful server
	// does not collide with itself.
	seq int
	// runID distinguishes this run from every earlier one against the same
	// server — the host-key first-sighting case needs a key nobody has reported.
	runID string
}

// NewSuite builds a suite for one server.
func NewSuite(baseURL, token string, e *Expectations, timeout time.Duration) *Suite {
	e.defaults()
	return &Suite{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		expect:  e,
		client:  &http.Client{Timeout: timeout},
		runID:   fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff),
	}
}

// abort is the sentinel a case panics with when it cannot continue. It ends that
// case and nothing else: one endpoint being unimplemented must not hide whether
// the other nine are right.
type abort struct{}

// Case accumulates the failures of one assertion group.
type Case struct {
	details []string
	failed  bool
}

// require records a failure and carries on, so one call can report everything
// wrong with a response rather than only the first thing.
func (c *Case) require(cond bool, format string, args ...any) bool {
	if !cond {
		c.failed = true
		c.details = append(c.details, fmt.Sprintf(format, args...))
	}
	return cond
}

// must records a failure and stops this case. Use it when continuing would only
// produce noise — a request that did not answer at all, say.
func (c *Case) must(cond bool, format string, args ...any) {
	if !c.require(cond, format, args...) {
		panic(abort{})
	}
}

// note records context on a passing case, for the report.
func (c *Case) note(format string, args ...any) {
	c.details = append(c.details, fmt.Sprintf(format, args...))
}

// run grades one named assertion.
func (s *Suite) run(group, name string, fn func(*Case)) {
	c := &Case{}
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(abort); ok {
					return
				}
				panic(r)
			}
		}()
		fn(c)
	}()

	status := StatusPass
	if c.failed {
		status = StatusFail
	}
	s.results = append(s.results, Result{Group: group, Name: name, Status: status, Details: c.details})
}

// nextID returns an id unique within this run.
func (s *Suite) nextID(prefix string) string {
	s.seq++
	return fmt.Sprintf("%s-%s-%04d", prefix, s.runID, s.seq)
}

// conn builds the ConnMeta every call carries.
func (s *Suite) conn(proxyID string) contract.ConnMeta {
	if proxyID == "" {
		proxyID = s.expect.ProxyID
	}
	return contract.ConnMeta{
		SessionID:     s.nextID("pdpconform"),
		ProxyID:       proxyID,
		ClientAddr:    "203.0.113.7:52344",
		ServerAddr:    "10.0.0.5:2222",
		ClientVersion: "SSH-2.0-pdpconform",
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
	}
}

// response is one HTTP answer, kept raw as well as decoded: an assertion about
// an ABSENT field cannot be made against a decoded struct, because a zero value
// and an omitted key look identical there.
type response struct {
	status int
	body   []byte
	// fields is the body decoded as a generic object, which is what makes
	// "this key is not present" expressible.
	fields map[string]any
}

// has reports whether a top-level key is present on the wire, not whether the
// decoded value is non-zero. Every absent-value-default assertion is built on
// this distinction.
func (r *response) has(key string) bool {
	if r.fields == nil {
		return false
	}
	_, ok := r.fields[key]
	return ok
}

func (r *response) into(v any) error { return json.Unmarshal(r.body, v) }

// postJSON sends a request body the caller has already shaped. Taking raw bytes
// rather than a struct is what lets a case send a body that the Go types cannot
// express — an authorize request with policy_version omitted entirely, for one,
// which is the whole of the 400 case.
func (s *Suite) postJSON(path string, body []byte) (*response, error) {
	return s.do(http.MethodPost, s.baseURL+path, body, s.token)
}

func (s *Suite) postObject(path string, v any) (*response, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return s.postJSON(path, b)
}

func (s *Suite) do(method, url string, body []byte, token string) (*response, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	out := &response{status: resp.StatusCode, body: raw}
	// A body that is not a JSON object is not an error here: the events stream
	// is NDJSON and an empty body is legal. Cases that need fields say so.
	_ = json.Unmarshal(raw, &out.fields)
	return out, nil
}

// requireEnvelope asserts the contract's error envelope on a non-2xx answer.
// M11 is the reason this is checked everywhere rather than on the 401 case
// alone: the envelope is how a caller tells a decision from an outage, and a
// server that drops it on one path has dropped it.
func (s *Suite) requireEnvelope(c *Case, r *response) {
	var env contract.ErrorResponse
	if !c.require(r.into(&env) == nil, "response %d is not the contract's error envelope: %s", r.status, snippet(r.body)) {
		return
	}
	c.require(env.Error.Code != "", "response %d carries an envelope with no error.code", r.status)
	c.require(env.Error.Message != "", "response %d carries an envelope with no error.message", r.status)
}

// Report writes the per-assertion results and returns true when everything
// passed.
func (s *Suite) Report(w io.Writer, verbose bool) bool {
	sort.SliceStable(s.results, func(i, j int) bool { return s.results[i].Group < s.results[j].Group })

	var passed, failed int
	group := ""
	say := func(format string, args ...any) { _, _ = fmt.Fprintf(w, format, args...) }
	for _, r := range s.results {
		if r.Group != group {
			group = r.Group
			say("\n%s\n", group)
		}
		say("  %-4s %s\n", r.Status, r.Name)
		if r.Status == StatusFail || verbose {
			for _, d := range r.Details {
				say("         %s\n", d)
			}
		}
		if r.Status == StatusFail {
			failed++
		} else {
			passed++
		}
	}
	say("\n%d assertions: %d passed, %d failed\n", len(s.results), passed, failed)
	return failed == 0
}

func snippet(b []byte) string {
	const max = 240
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		return s[:max] + "…"
	}
	if s == "" {
		return "<empty body>"
	}
	return s
}
