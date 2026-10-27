// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package server_test

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

// A host's inbound action channel, registered through server.Options.Registry
// (0039, M15): started once, after the registry is sealed and before either
// listener opens, handed a working executor, and stopped on shutdown.

// actionHandler is a host's ext.ActionHandler that records how it was run.
type actionHandler struct {
	startErr error
	// reachable is the addresses that accepted a connection while Start was
	// running: none, if Start runs before the listeners open.
	probe []string

	mu          sync.Mutex
	starts      int
	stops       int
	reachable   []string
	exec        ext.ActionExecutor
	stoppedDone chan struct{}
}

func (h *actionHandler) Start(_ context.Context, exec ext.ActionExecutor) error {
	var reachable []string
	for _, addr := range h.probe {
		if conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
			_ = conn.Close()
			reachable = append(reachable, addr)
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.starts++
	h.reachable = reachable
	h.exec = exec
	return h.startErr
}

func (h *actionHandler) Stop(context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stops++
	if h.stoppedDone != nil {
		close(h.stoppedDone)
		h.stoppedDone = nil
	}
	return nil
}

func (h *actionHandler) snapshot() (starts, stops int, reachable []string, exec ext.ActionExecutor) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.starts, h.stops, append([]string(nil), h.reachable...), h.exec
}

func registryWith(t *testing.T, h *actionHandler) *ext.Registry {
	t.Helper()
	registry := ext.NewRegistry()
	if err := registry.RegisterActionHandler(ext.Registration{Provider: hostProvider + "/soar"}, h); err != nil {
		t.Fatal(err)
	}
	return registry
}

// actionRow reads the action handler's row of the extension listing.
func actionRow(t *testing.T, base, token string) (registered bool, started *bool, startError string) {
	t.Helper()
	resp := get(t, base+"/api/v1/tenants/default/extensions", token)
	var out struct {
		Points []struct {
			Point      string `json:"point"`
			Registered bool   `json:"registered"`
			Started    *bool  `json:"started"`
			StartError string `json:"start_error"`
		} `json:"points"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	for _, p := range out.Points {
		if p.Point == ext.PointActionHandler.String() {
			return p.Registered, p.Started, p.StartError
		}
	}
	t.Fatal("the extension listing has no row for the action handler")
	return false, nil, ""
}

func TestTheDaemonStartsTheActionHandlerOnceBeforeAnyListenerAcceptsAndStopsItOnShutdown(t *testing.T) {
	south, north := freeAddr(t), freeAddr(t)
	handler := &actionHandler{probe: []string{south, north}, stoppedDone: make(chan struct{})}
	stopped := handler.stoppedDone
	// Registered before the host runs, so it runs after the host has stopped.
	t.Cleanup(func() {
		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
			t.Error("the handler was not stopped on shutdown")
		}
		_, stops, _, exec := handler.snapshot()
		if stops != 1 {
			t.Errorf("Stop was called %d times, want once", stops)
		}
		// An Execute that arrives once Stop has begun is KindUnavailable.
		if _, err := exec.Execute(context.Background(), ext.Action{
			Tenant: "default", Kind: ext.ActionLockSubject, ActionID: "late", SubjectID: "alice",
		}); ext.KindOf(err) != ext.KindUnavailable {
			t.Errorf("Execute after shutdown began: %v, want KindUnavailable", err)
		}
	})

	base, st, _ := runHostAt(t, registryWith(t, handler), "", south, north)

	starts, _, reachable, exec := handler.snapshot()
	if starts != 1 {
		t.Fatalf("Start was called %d times, want once", starts)
	}
	if len(reachable) != 0 {
		t.Errorf("%v accepted a connection while Start was running: it has to run before either listener opens", reachable)
	}
	if exec == nil {
		t.Fatal("the handler was handed no executor")
	}

	// The executor works: the lock it places is the operator's lock.
	ctx := context.Background()
	if err := st.Subjects().Upsert(ctx, "default", store.Subject{ID: "alice@example.com", Source: "local"}); err != nil {
		t.Fatal(err)
	}
	res, err := exec.Execute(ctx, ext.Action{
		Tenant: "default", Kind: ext.ActionLockSubject, ActionID: "a-1", SubjectID: "alice@example.com", ExternalRef: "INC-1",
	})
	if err != nil || res.Affected != 1 {
		t.Fatalf("Execute = %+v, %v", res, err)
	}
	federation, err := identity.NewFederation(st)
	if err != nil {
		t.Fatal(err)
	}
	auditor := issue(t, federation, "default", map[store.Tenant]identity.RoleSet{"default": {identity.RoleAuditor}})
	resp := get(t, base+"/api/v1/tenants/default/subjects/alice@example.com", auditor)
	var subject struct {
		Locked bool `json:"locked"`
		Lock   struct {
			LockedBy struct {
				PrincipalID string `json:"principal_id"`
			} `json:"locked_by"`
			ActionID string `json:"action_id"`
		} `json:"lock"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&subject); err != nil {
		t.Fatal(err)
	}
	if !subject.Locked || subject.Lock.LockedBy.PrincipalID != "action-handler:"+hostProvider+"/soar" || subject.Lock.ActionID != "a-1" {
		t.Errorf("the operator's view of the lock = %+v, want it placed by the handler under its action id", subject)
	}

	// The listing says it is registered and running.
	registered, started, startError := actionRow(t, base, auditor)
	if !registered || started == nil || !*started || startError != "" {
		t.Errorf("the extension row = registered %v, started %v, error %q", registered, started, startError)
	}
}

// A handler whose Start fails does not stop Control: it serves, logs the
// failure at ERROR with the provider and the error's kind, and the listing
// shows the handler registered and not started.
func TestAHandlerWhoseStartFailsDoesNotStopControl(t *testing.T) {
	handler := &actionHandler{startErr: ext.Errorf(ext.PointActionHandler, hostProvider+"/soar", "Start", ext.KindInvalid, "no credentials configured")}
	base, st, logs := runHost(t, registryWith(t, handler), "")

	if starts, _, _, _ := handler.snapshot(); starts != 1 {
		t.Fatalf("Start was called %d times, want once", starts)
	}
	line := logLine(t, logs.String(), "the inbound action handler failed to start; Control serves, and nothing will be executed")
	if line["level"] != "ERROR" || line["provider"] != hostProvider+"/soar" || line["kind"] != "invalid" {
		t.Errorf("the start-up failure was logged as %v", line)
	}

	federation, err := identity.NewFederation(st)
	if err != nil {
		t.Fatal(err)
	}
	auditor := issue(t, federation, "default", map[store.Tenant]identity.RoleSet{"default": {identity.RoleAuditor}})
	registered, started, startError := actionRow(t, base, auditor)
	if !registered || started == nil || *started || startError != "invalid" {
		t.Errorf("the extension row = registered %v, started %v, error %q; want registered, not started, invalid", registered, started, startError)
	}
	// Control serves.
	if resp := get(t, base+"/api/v1/session/methods", ""); resp.StatusCode != 200 {
		t.Errorf("Control answered %d with a handler that failed to start", resp.StatusCode)
	}
}

// With no handler registered no executor exists, nothing about the action
// channel is logged, and the point's row says nothing of a start.
func TestControlWithNoActionHandlerBuildsNoExecutor(t *testing.T) {
	base, st, logs := runHost(t, ext.NewRegistry(), "")

	if strings.Contains(logs.String(), "action_handler") {
		t.Errorf("a deployment with no handler logged about one:\n%s", logs.String())
	}
	federation, err := identity.NewFederation(st)
	if err != nil {
		t.Fatal(err)
	}
	auditor := issue(t, federation, "default", map[store.Tenant]identity.RoleSet{"default": {identity.RoleAuditor}})
	registered, started, startError := actionRow(t, base, auditor)
	if registered || started != nil || startError != "" {
		t.Errorf("the extension row = registered %v, started %v, error %q; want an unregistered point with nothing to say of a start",
			registered, started, startError)
	}
	var rows int
	if err := st.Pool().QueryRow(context.Background(), `SELECT count(*) FROM action_executions`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Errorf("%d executions with no handler", rows)
	}
}
