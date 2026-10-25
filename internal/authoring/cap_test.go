// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package authoring

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/store"
)

// A finding names at most MaxFindingItems targets or proxies (M26.3, 0035).
// Each list is sized by the estate, and the report answers every validate,
// upload, apply and activate, so each keeps its first hundred beside its whole
// count and whether it was cut.

// item names the i-th target or proxy a fixture puts in a finding's list, so a
// list's order can be read back from it.
func item(prefix string, i int) string { return fmt.Sprintf("%s-%03d", prefix, i) }

func floorRule(id, floor string) string {
	return `  - id: ` + id + `
    effect: allow
    match:
      target:
        hostnames: [a.example.com]
` + route("      algorithm_floor: "+floor+"\n")
}

// cappedLists is every finding whose parameters list targets or proxies: the
// rule it is raised on, and facts that make its list hold n items, named
// item(prefix, 0) to item(prefix, n-1) in the list's own order.
var cappedLists = []struct {
	code, list, prefix string
	rule               string
	facts              func(n int) Facts
}{
	{
		code: CodeNoProxyProvides, list: "proxies", prefix: "proxy",
		rule: restrictedRule("logs", "a.example.com", "account-restricted", "/usr/bin/uptime"),
		facts: func(n int) Facts {
			declared, _ := fleet.MarshalCapabilities(fleet.Capabilities{
				CredentialMethods: []contract.TargetAuthMethod{contract.TargetAuthBrokeredKey},
			})
			sat := fleet.Satisfaction{RuleID: "logs"}
			var f Facts
			for i := range n {
				f.Proxies = append(f.Proxies, store.Proxy{ID: item("proxy", i), DeclaredCapabilities: declared})
				sat.Proxies = append(sat.Proxies, fleet.ProxyVerdict{ProxyID: item("proxy", i), Live: true, Missing: []string{"x"}})
			}
			f.Satisfaction = []fleet.Satisfaction{sat}
			return f
		},
	},
	{
		code: CodeTargetCannotTake, list: "targets", prefix: "host",
		rule: restrictedRule("logs", "a.example.com", "account-restricted", "/usr/bin/uptime"),
		facts: func(n int) Facts {
			sat := fleet.Satisfaction{RuleID: "logs"}
			for i := range n {
				sat.Targets = append(sat.Targets, fleet.TargetVerdict{Hostname: item("host", i), Observed: true, Missing: []string{"x"}})
			}
			return Facts{Satisfaction: []fleet.Satisfaction{sat}}
		},
	},
	{
		code: CodeTargetUnproven, list: "targets", prefix: "host",
		rule: restrictedRule("logs", "a.example.com", "account-restricted", "/usr/bin/uptime"),
		facts: func(n int) Facts {
			sat := fleet.Satisfaction{RuleID: "logs"}
			for i := range n {
				sat.Targets = append(sat.Targets, fleet.TargetVerdict{Hostname: item("host", i), Missing: []string{"x"}})
			}
			return Facts{Satisfaction: []fleet.Satisfaction{sat}}
		},
	},
	{
		code: CodeTargetBelowFloor, list: "targets", prefix: "host",
		rule: floorRule("floored", "modern-kex"),
		facts: func(n int) Facts {
			now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
			f := Facts{Now: now, TTL: 24 * time.Hour, Declarations: []fleet.DeclaredAlgorithms{declaration("proxy-1", "aes128-ctr")}}
			var hosts []string
			for i := range n {
				hosts = append(hosts, item("host", i))
				f.Kex = append(f.Kex, fleet.KexObservation{Hostname: item("host", i), FloorMet: contract.FloorMetNone, ObservedAt: now})
			}
			f.Satisfaction = reaching("floored", hosts...)
			return f
		},
	},
	{
		code: CodeTargetNotReported, list: "targets", prefix: "host",
		rule: floorRule("floored", "modern-kex"),
		facts: func(n int) Facts {
			var hosts []string
			for i := range n {
				hosts = append(hosts, item("host", i))
			}
			return Facts{
				Satisfaction: reaching("floored", hosts...),
				Declarations: []fleet.DeclaredAlgorithms{declaration("proxy-1", "aes128-ctr")},
			}
		},
	},
	{
		code: CodeFloorUndeclared, list: "proxies", prefix: "proxy",
		rule: floorRule("pq", "pq-hybrid-kex"),
		facts: func(n int) Facts {
			var f Facts
			for i := range n {
				f.Declarations = append(f.Declarations, declaration(item("proxy", i), "aes128-ctr"))
			}
			return f
		},
	},
	{
		code: CodeAlgorithmUnknown, list: "proxies", prefix: "proxy",
		rule: floorRule("floored", "modern-kex"),
		facts: func(n int) Facts {
			var f Facts
			for i := range n {
				f.Declarations = append(f.Declarations, fleet.DeclaredAlgorithms{ProxyID: item("proxy", i), Declaration: fleet.AlgorithmDeclaration{
					AlgorithmFloors: declaration("x").Declaration.AlgorithmFloors,
				}})
			}
			return f
		},
	},
	{
		code: CodeAxisEmptied, list: "proxies", prefix: "proxy",
		rule: bannedRule("no-ctr", "a.example.com", "        ciphers: [aes128-ctr]\n"),
		facts: func(n int) Facts {
			var f Facts
			for i := range n {
				f.Declarations = append(f.Declarations, declaration(item("proxy", i), "aes128-ctr"))
			}
			return f
		},
	},
}

// Each list keeps its first hundred in its own order, and says how many there
// were and whether the cap cut it; at a hundred nothing is cut, and at a
// hundred and one exactly one item is. The message gives the whole count.
func TestEveryListAFindingNamesKeepsItsFirstHundred(t *testing.T) {
	t.Parallel()
	if MaxFindingItems != 100 {
		t.Fatalf("MaxFindingItems is %d; a finding names at most 100 (0028's inventory, M26.3)", MaxFindingItems)
	}
	for _, c := range cappedLists {
		for _, n := range []int{1, 100, 101, 300} {
			t.Run(fmt.Sprintf("%s/%d", c.code, n), func(t *testing.T) {
				t.Parallel()
				fs := withCode(run(t, c.rule, c.facts(n)), c.code)
				if len(fs) != 1 {
					t.Fatalf("%d findings, want one for the rule: %+v", len(fs), fs)
				}
				f := fs[0]
				list := reflect.ValueOf(f.Parameters[c.list])
				if list.Kind() != reflect.Slice {
					t.Fatalf("%s is %T, want a list", c.list, f.Parameters[c.list])
				}
				want := min(n, MaxFindingItems)
				if list.Len() != want {
					t.Errorf("%s names %d, want %d", c.list, list.Len(), want)
				}
				if got := f.Parameters[c.list+"_count"]; got != n {
					t.Errorf("%s_count is %v, want %d", c.list, got, n)
				}
				if got := f.Parameters[c.list+"_truncated"]; got != (n > MaxFindingItems) {
					t.Errorf("%s_truncated is %v, want %t", c.list, got, n > MaxFindingItems)
				}
				// The first hundred, in order: the first is item 0, the last
				// kept is item want-1, and nothing past it is named.
				raw, err := json.Marshal(f.Parameters[c.list])
				if err != nil {
					t.Fatal(err)
				}
				first, _ := json.Marshal(list.Index(0).Interface())
				last, _ := json.Marshal(list.Index(list.Len() - 1).Interface())
				if !strings.Contains(string(first), item(c.prefix, 0)) || !strings.Contains(string(last), item(c.prefix, want-1)) {
					t.Errorf("the list runs %s … %s, want %s … %s", first, last, item(c.prefix, 0), item(c.prefix, want-1))
				}
				if n > want && strings.Contains(string(raw), item(c.prefix, want)) {
					t.Errorf("the list names %s, past its first %d", item(c.prefix, want), want)
				}
				if !strings.Contains(f.Message, strconv.Itoa(n)) {
					t.Errorf("the message does not give the count %d: %s", n, f.Message)
				}
			})
		}
	}
}

// The list above is every finding that names targets or proxies: a report
// over a fleet and an estate of three hundred each, in which every check that
// can name them does, holds no finding naming more than a hundred, and each
// that names any says how many there were.
func TestNoFindingInAReportNamesMoreThanAHundred(t *testing.T) {
	t.Parallel()
	const n = 3 * MaxFindingItems
	seen := map[string]bool{}
	for _, c := range cappedLists {
		for _, f := range run(t, c.rule, c.facts(n)) {
			for name, v := range f.Parameters {
				if name != "targets" && name != "proxies" {
					continue
				}
				l := reflect.ValueOf(v)
				if l.Kind() != reflect.Slice {
					// satisfiability.proxies_unreported counts the tenant's
					// proxies, none of which declared anything; it names none.
					if f.Code != CodeProxiesUnreported {
						t.Errorf("%s's %s is %T, want a list", f.ID, name, v)
					}
					continue
				}
				seen[f.Code] = true
				if l.Len() > MaxFindingItems {
					t.Errorf("%s names %d %s", f.ID, l.Len(), name)
				}
				if _, ok := f.Parameters[name+"_count"]; !ok {
					t.Errorf("%s names %s without %s_count", f.ID, name, name)
				}
				if _, ok := f.Parameters[name+"_truncated"]; !ok {
					t.Errorf("%s names %s without %s_truncated", f.ID, name, name)
				}
			}
		}
	}
	for _, c := range cappedLists {
		if !seen[c.code] {
			t.Errorf("%s was not raised with a list, so this test holds nothing about it", c.code)
		}
	}
}

// THE PER-BUILD RULING. A build's floor, profile and bans are judged against
// its own declaration, and what falls short is said once per rule — once per
// rule and axis for an emptied axis — naming each build, so the report is
// sized by the bundle and not by the fleet. Two rules over three hundred
// builds that all fall short are one finding per rule, and the ids name the
// rule (and the axis), never a proxy.
func TestPerBuildFindingsAreOnePerRuleWhateverTheFleet(t *testing.T) {
	t.Parallel()
	var facts Facts
	for i := range 3 * MaxFindingItems {
		facts.Declarations = append(facts.Declarations, declaration(item("proxy", i), "aes128-ctr"))
		facts.Declarations = append(facts.Declarations, fleet.DeclaredAlgorithms{ProxyID: item("bare", i), Declaration: fleet.AlgorithmDeclaration{
			Algorithms: declaration("x", "aes128-ctr").Declaration.Algorithms,
		}})
	}
	rules := ""
	for _, id := range []string{"r-1", "r-2"} {
		rules += `  - id: ` + id + `
    effect: allow
    match:
      target:
        hostnames: [` + id + `.example.com]
` + route("      algorithm_floor: pq-hybrid-kex\n      algorithm_bans:\n        ciphers: [aes128-ctr]\n        macs: [hmac-sha2-256]\n")
	}
	fs := run(t, rules, facts)

	want := map[string]bool{
		findingID(CodeFloorUndeclared, "r-1"):                   true,
		findingID(CodeFloorUndeclared, "r-2"):                   true,
		findingID(CodeAlgorithmUnknown, "r-1", "default"):       true,
		findingID(CodeAlgorithmUnknown, "r-2", "default"):       true,
		findingID(CodeAxisEmptied, "r-1", contract.AxisCiphers): true,
		findingID(CodeAxisEmptied, "r-1", contract.AxisMACs):    true,
		findingID(CodeAxisEmptied, "r-2", contract.AxisCiphers): true,
		findingID(CodeAxisEmptied, "r-2", contract.AxisMACs):    true,
	}
	got := map[string]bool{}
	for _, code := range []string{CodeFloorUndeclared, CodeAlgorithmUnknown, CodeAxisEmptied} {
		for _, f := range withCode(fs, code) {
			if got[f.ID] {
				t.Errorf("%s is raised twice", f.ID)
			}
			got[f.ID] = true
			if !want[f.ID] {
				t.Errorf("unexpected finding %s", f.ID)
			}
		}
	}
	for id := range want {
		if !got[id] {
			t.Errorf("missing finding %s", id)
		}
	}
	for _, f := range withCode(fs, CodeFloorUndeclared) {
		if f.Parameters["proxies_count"] != 6*MaxFindingItems || f.Parameters["proxies_truncated"] != true {
			t.Errorf("%s counts %v, want every one of the %d builds", f.ID, f.Parameters["proxies_count"], 6*MaxFindingItems)
		}
	}
	for _, f := range withCode(fs, CodeAlgorithmUnknown) {
		if f.Parameters["proxies_count"] != 3*MaxFindingItems {
			t.Errorf("%s counts %v, want the %d builds that declare no profiles", f.ID, f.Parameters["proxies_count"], 3*MaxFindingItems)
		}
	}
}
