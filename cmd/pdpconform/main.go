// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Command pdpconform is the black-box conformance suite for the south-bound
// contract (PLAN M1).
//
// It takes a base URL, a bearer token, and an expectation file describing what
// the server in front of it is configured to serve, drives the contract over
// real HTTP, and reports pass/fail per assertion with a non-zero exit on any
// failure. It imports nothing from this server, so it grades an implementation
// rather than agreeing with one — which is why CI also runs it against Hoplock
// Proxy's cmd/mock-control. A suite only ever run against the implementation it
// was written beside tests agreement with itself.
//
// The expectation-file format is documented in cmd/pdpconform/README.md.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	var (
		baseURL = flag.String("base-url", "", "base URL of the server under test, e.g. http://127.0.0.1:8080")
		token   = flag.String("token", "", "bearer token the server accepts from a proxy")
		expect  = flag.String("expectations", "", "path to the expectation file (see cmd/pdpconform/README.md)")
		only    = flag.String("only", "", "run only the groups whose names contain this substring")
		timeout = flag.Duration("timeout", 30*time.Second, "per-request timeout; the event stream is exempt")
		verbose = flag.Bool("v", false, "print the details of passing assertions too")
	)
	flag.Parse()

	if err := run(*baseURL, *token, *expect, *only, *timeout, *verbose); err != nil {
		fmt.Fprintf(os.Stderr, "pdpconform: %v\n", err)
		os.Exit(2)
	}
}

func run(baseURL, token, expectPath, only string, timeout time.Duration, verbose bool) error {
	if baseURL == "" {
		return fmt.Errorf("-base-url is required")
	}
	if expectPath == "" {
		return fmt.Errorf("-expectations is required")
	}

	e, err := LoadExpectations(expectPath)
	if err != nil {
		return err
	}

	s := NewSuite(baseURL, token, e, timeout)
	fmt.Printf("pdpconform: %s (expectations %s)\n", baseURL, expectPath)

	for _, g := range []struct {
		name string
		fn   func()
	}{
		{groupAuth, s.CheckAuth},
		{groupAuthorize, s.CheckAuthorize},
		{groupHostKeys, s.CheckHostKeys},
		{groupCapabilities, s.CheckCapabilities},
		{groupUIDs, s.CheckUIDs},
		{groupLogs, s.CheckLogs},
		{groupEvents, s.CheckEvents},
		{groupErrors, s.CheckErrors},
	} {
		if only != "" && !strings.Contains(g.name, only) {
			continue
		}
		g.fn()
	}

	if len(s.results) == 0 {
		return fmt.Errorf("no assertions ran; -only=%q matched no group", only)
	}
	if !s.Report(os.Stdout, verbose) {
		os.Exit(1)
	}
	return nil
}
