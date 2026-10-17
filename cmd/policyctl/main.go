// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Command policyctl is the policy lifecycle from a terminal: validate, diff,
// simulate, apply and explain, plus the version history and activation.
//
// IT IS A CLIENT OF THE NORTH-BOUND API AND NOTHING ELSE (PLAN M2). It never
// opens the database and never compiles a bundle itself: the server's answer is
// the answer, so a pipeline, the console and this command cannot disagree about
// what a bundle means. Anything it does, an operator can do with curl.
//
// OUTPUT IS FOR SCRIPTS. Every line is one record, tab-separated, and its first
// field names its kind — `rejection`, `finding`, `flip`, `version` — so a
// pipeline greps for the kind it cares about and an added kind breaks nothing.
// `-json` prints the server's answer instead, unchanged.
//
// EXIT CODES: 0 done; 1 the server refused or the bundle is not publishable as
// it stands (rejections, unaccepted warnings, a blocking finding, a flip under
// -fail-on-flip); 2 a usage error, an unreachable server or an outage.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}

const usage = `usage: policyctl [-server URL] [-token TOKEN] [-tenant NAME] [-json] <command> [args]

commands:
  validate FILE                         compile a bundle and report what publishing it would meet
  diff FILE [-against N]                what separates FILE from version N (default: the active one)
  simulate FILE [-from T -to T] [-cases FILE] [-fail-on-flip]
                                        replay recorded decisions, and dry-run synthetic ones
  apply FILE [-accept ID]...            upload and activate; accept warnings by id
  activate VERSION [-accept ID]...      make a stored version active; rolling back is activating an older one
  versions                              every stored version, newest first
  explain DECISION_ID                   the whole story of one decision
  explain -session SESSION_ID           the story of every decision taken for a session

-server, -token and -tenant default to HOPLOCK_SERVER, HOPLOCK_TOKEN and
HOPLOCK_TENANT (default "default").
`

// Exit codes.
const (
	exitOK      = 0
	exitRefused = 1
	exitError   = 2
)

type client struct {
	server, token, tenant string
	json                  bool
	out, errOut           io.Writer
	http                  *http.Client
}

// apiError is the north-bound error envelope (M21).
type apiError struct {
	Code          string         `json:"code"`
	Message       string         `json:"message"`
	Parameters    map[string]any `json:"parameters"`
	CorrelationID string         `json:"correlation_id"`
}

// refusal is a 4xx: the server said no, on purpose.
type refusal struct {
	status int
	body   apiError
	raw    []byte
}

func (r *refusal) Error() string {
	return fmt.Sprintf("%d %s: %s", r.status, r.body.Code, r.body.Message)
}

func run(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	global := flag.NewFlagSet("policyctl", flag.ContinueOnError)
	global.SetOutput(stderr)
	global.Usage = func() { _, _ = fmt.Fprint(stderr, usage) }
	c := &client{out: stdout, errOut: stderr, http: &http.Client{Timeout: 60 * time.Second}}
	tenant := getenv("HOPLOCK_TENANT")
	if tenant == "" {
		tenant = "default"
	}
	global.StringVar(&c.server, "server", getenv("HOPLOCK_SERVER"), "north-bound base URL")
	global.StringVar(&c.token, "token", getenv("HOPLOCK_TOKEN"), "north-bound credential")
	global.StringVar(&c.tenant, "tenant", tenant, "tenant")
	global.BoolVar(&c.json, "json", false, "print the server's answer as JSON")
	if err := global.Parse(args); err != nil {
		return exitError
	}
	rest := global.Args()
	if len(rest) == 0 {
		global.Usage()
		return exitError
	}
	if c.server == "" {
		_, _ = fmt.Fprintln(stderr, "policyctl: -server (or HOPLOCK_SERVER) is required")
		return exitError
	}
	c.server = strings.TrimRight(c.server, "/")

	cmds := map[string]func([]string) (int, error){
		"validate": c.validate, "diff": c.diff, "simulate": c.simulate, "apply": c.apply,
		"activate": c.activate, "versions": c.versions, "explain": c.explain,
	}
	cmd, ok := cmds[rest[0]]
	if !ok {
		_, _ = fmt.Fprintf(stderr, "policyctl: unknown command %q\n%s", rest[0], usage)
		return exitError
	}
	code, err := cmd(rest[1:])
	if err != nil {
		var ref *refusal
		if errors.As(err, &ref) {
			c.printRefusal(ref)
			return exitRefused
		}
		_, _ = fmt.Fprintf(stderr, "policyctl: %v\n", err)
		return exitError
	}
	return code
}

// call sends one request and decodes a 2xx into out. A 4xx is a *refusal; a
// 5xx or a transport failure is an error.
func (c *client) call(method, path string, body, out any) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.server+"/api/v1/tenants/"+url.PathEscape(c.tenant)+path, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode >= 500:
		var e apiError
		_ = json.Unmarshal(raw, &e)
		return nil, fmt.Errorf("the server answered %d %s: %s (correlation id %s)", resp.StatusCode, e.Code, e.Message, e.CorrelationID)
	case resp.StatusCode >= 400:
		ref := &refusal{status: resp.StatusCode, raw: raw}
		_ = json.Unmarshal(raw, &ref.body)
		return nil, ref
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return nil, fmt.Errorf("the server's answer does not decode: %w", err)
		}
	}
	return raw, nil
}

// fieldSafe keeps a field inside its record: a tab would split it into two
// fields and a line break into two records, and scripts read both by position.
var fieldSafe = strings.NewReplacer("\t", " ", "\r\n", " ", "\n", " ", "\r", " ")

// line prints one tab-separated record.
func (c *client) line(fields ...any) {
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		parts = append(parts, fieldSafe.Replace(fmt.Sprint(f)))
	}
	_, _ = fmt.Fprintln(c.out, strings.Join(parts, "\t"))
}

func (c *client) raw(b []byte) {
	_, _ = c.out.Write(bytes.TrimRight(b, "\n"))
	_, _ = fmt.Fprintln(c.out)
}

// ---------------------------------------------------------------------------
// the shapes this command prints from
// ---------------------------------------------------------------------------

type rejection struct {
	Code    string `json:"code"`
	Rule    string `json:"rule"`
	Line    int    `json:"line"`
	Message string `json:"message"`
}

type finding struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Rule     string `json:"rule"`
	Message  string `json:"message"`
}

type report struct {
	Valid      bool        `json:"valid"`
	Digest     string      `json:"digest"`
	Rejections []rejection `json:"rejections"`
	Findings   []finding   `json:"findings"`
}

type version struct {
	Version    int64     `json:"version"`
	Digest     string    `json:"digest"`
	UploadedBy string    `json:"uploaded_by"`
	UploadedAt time.Time `json:"uploaded_at"`
	Active     bool      `json:"active"`
}

func (c *client) printRejections(rs []rejection) {
	for _, r := range rs {
		c.line("rejection", r.Code, r.Rule, r.Line, r.Message)
	}
}

func (c *client) printFindings(fs []finding) {
	for _, f := range fs {
		c.line("finding", f.Severity, f.ID, f.Message)
	}
}

// printRefusal renders a 4xx: the server's own rejections and findings where it
// sent them, so a CI log shows what to fix and which ids to accept.
func (c *client) printRefusal(r *refusal) {
	if c.json {
		_, _ = c.errOut.Write(r.raw)
		_, _ = fmt.Fprintln(c.errOut)
		return
	}
	w := c.out
	c.out = c.errOut
	defer func() { c.out = w }()
	c.line("refused", r.status, r.body.Code, r.body.Message, "correlation_id", r.body.CorrelationID)
	if v, ok := r.body.Parameters["version"]; ok {
		c.line("version", v)
	}
	for _, key := range []string{"rejections", "findings"} {
		raw, ok := r.body.Parameters[key]
		if !ok {
			continue
		}
		b, _ := json.Marshal(raw)
		if key == "rejections" {
			var rs []rejection
			_ = json.Unmarshal(b, &rs)
			c.printRejections(rs)
			continue
		}
		var fs []finding
		_ = json.Unmarshal(b, &fs)
		for _, f := range fs {
			label := "unaccepted"
			if f.Severity == "blocking" {
				label = "blocking"
			}
			c.line(label, f.ID, f.Message)
		}
	}
}

func readSource(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// acceptFlags collects repeated -accept ids.
type acceptFlags []string

func (a *acceptFlags) String() string     { return strings.Join(*a, ",") }
func (a *acceptFlags) Set(v string) error { *a = append(*a, v); return nil }

func parse(fs *flag.FlagSet, args []string, positional int) ([]string, error) {
	// Flags may follow the positional arguments, so the two are separated
	// first: `apply bundle.yaml -accept X` reads the way an operator types it.
	var pos, flags []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			pos = append(pos, arg)
			continue
		}
		flags = append(flags, arg)
		name := strings.TrimLeft(arg, "-")
		f := fs.Lookup(name)
		if strings.Contains(name, "=") || f == nil {
			continue
		}
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			continue
		}
		if i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}
	if err := fs.Parse(flags); err != nil {
		return nil, err
	}
	if len(pos) != positional {
		return nil, fmt.Errorf("%s takes %d argument(s), got %d\n%s", fs.Name(), positional, len(pos), usage)
	}
	return pos, nil
}

// ---------------------------------------------------------------------------
// commands
// ---------------------------------------------------------------------------

func (c *client) validate(args []string) (int, error) {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	pos, err := parse(fs, args, 1)
	if err != nil {
		return exitError, err
	}
	source, err := readSource(pos[0])
	if err != nil {
		return exitError, err
	}
	var rep report
	raw, err := c.call(http.MethodPost, "/policy/validate", map[string]any{"source": source}, &rep)
	if err != nil {
		return exitError, err
	}
	if c.json {
		c.raw(raw)
	} else {
		c.line("valid", rep.Valid, rep.Digest)
		c.printRejections(rep.Rejections)
		c.printFindings(rep.Findings)
	}
	if !rep.Valid {
		return exitRefused, nil
	}
	for _, f := range rep.Findings {
		if f.Severity == "blocking" {
			return exitRefused, nil
		}
	}
	return exitOK, nil
}

type diffResult struct {
	From        int64    `json:"from"`
	To          int64    `json:"to"`
	Added       []string `json:"added"`
	Removed     []string `json:"removed"`
	Changed     []string `json:"changed"`
	Moved       []string `json:"moved"`
	Document    []string `json:"document"`
	Text        string   `json:"text"`
	TextOmitted bool     `json:"text_omitted"`
}

func (c *client) diff(args []string) (int, error) {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	against := fs.Int64("against", -1, "the stored version to diff against; default the active one, 0 for nothing")
	pos, err := parse(fs, args, 1)
	if err != nil {
		return exitError, err
	}
	source, err := readSource(pos[0])
	if err != nil {
		return exitError, err
	}
	body := map[string]any{"source": source}
	if *against >= 0 {
		body["against"] = *against
	}
	var d diffResult
	raw, err := c.call(http.MethodPost, "/policy/diff", body, &d)
	if err != nil {
		return exitError, err
	}
	if c.json {
		c.raw(raw)
		return exitOK, nil
	}
	c.line("from", d.From)
	for _, set := range []struct {
		kind  string
		items []string
	}{{"added", d.Added}, {"removed", d.Removed}, {"changed", d.Changed}, {"moved", d.Moved}, {"document", d.Document}} {
		for _, item := range set.items {
			c.line(set.kind, item)
		}
	}
	if d.TextOmitted {
		c.line("text", "omitted")
	} else if d.Text != "" {
		for _, l := range strings.Split(strings.TrimRight(d.Text, "\n"), "\n") {
			c.line("text", l)
		}
	}
	return exitOK, nil
}

type flipGroup struct {
	From    string `json:"from"`
	To      string `json:"to"`
	WasRule string `json:"was_rule"`
	NowRule string `json:"now_rule"`
	Flips   []struct {
		DecisionID string    `json:"decision_id"`
		Subject    string    `json:"subject"`
		Target     string    `json:"target"`
		DecidedAt  time.Time `json:"decided_at"`
	} `json:"flips"`
}

type simulateResult struct {
	Digest string `json:"digest"`
	DryRun []struct {
		Name   string `json:"name"`
		Effect string `json:"effect"`
		Rule   string `json:"rule"`
	} `json:"dry_run"`
	Replay *struct {
		Replayed     int         `json:"replayed"`
		Unchanged    int         `json:"unchanged"`
		Truncated    bool        `json:"truncated"`
		AllowToDeny  []flipGroup `json:"allow_to_deny"`
		DenyToAllow  []flipGroup `json:"deny_to_allow"`
		Unreplayable []struct {
			DecisionID string `json:"decision_id"`
			Reason     string `json:"reason"`
		} `json:"unreplayable"`
	} `json:"replay"`
}

func (c *client) simulate(args []string) (int, error) {
	fs := flag.NewFlagSet("simulate", flag.ContinueOnError)
	from := fs.String("from", "", "replay from this instant (RFC 3339)")
	to := fs.String("to", "", "replay to this instant (RFC 3339); default now")
	casesPath := fs.String("cases", "", "a JSON file of synthetic questions to dry-run")
	failOnFlip := fs.Bool("fail-on-flip", false, "exit 1 when the candidate flips any recorded decision")
	pos, err := parse(fs, args, 1)
	if err != nil {
		return exitError, err
	}
	source, err := readSource(pos[0])
	if err != nil {
		return exitError, err
	}
	// A simulation asks something; one that asks nothing is a usage error,
	// caught here, because the server's refusal would exit 1 — "the policy
	// is wrong" — and it is not.
	if *from == "" && *to != "" {
		return exitError, errors.New("simulate: -to needs -from")
	}
	if *from == "" && *casesPath == "" {
		return exitError, errors.New("simulate asks something: give -from (a replay), -cases (a dry run), or both")
	}
	body := map[string]any{"source": source}
	if *from != "" {
		end := *to
		if end == "" {
			end = time.Now().UTC().Format(time.RFC3339)
		}
		body["replay"] = map[string]any{"from": *from, "to": end}
	}
	if *casesPath != "" {
		b, err := os.ReadFile(*casesPath)
		if err != nil {
			return exitError, err
		}
		var cases []any
		if err := json.Unmarshal(b, &cases); err != nil {
			return exitError, fmt.Errorf("%s is not a JSON list of cases: %w", *casesPath, err)
		}
		body["cases"] = cases
	}
	var res simulateResult
	raw, err := c.call(http.MethodPost, "/policy/simulate", body, &res)
	if err != nil {
		return exitError, err
	}
	flips := 0
	if res.Replay != nil {
		for _, g := range append(res.Replay.AllowToDeny, res.Replay.DenyToAllow...) {
			flips += len(g.Flips)
		}
	}
	if c.json {
		c.raw(raw)
	} else {
		for _, r := range res.DryRun {
			c.line("case", r.Name, r.Effect, r.Rule)
		}
		if rep := res.Replay; rep != nil {
			c.line("replayed", rep.Replayed, "unchanged", rep.Unchanged, "truncated", rep.Truncated)
			for _, g := range append(rep.AllowToDeny, rep.DenyToAllow...) {
				for _, f := range g.Flips {
					c.line("flip", g.From+"->"+g.To, f.DecisionID, f.Subject, f.Target, g.WasRule, g.NowRule,
						f.DecidedAt.UTC().Format(time.RFC3339))
				}
			}
			for _, u := range rep.Unreplayable {
				c.line("unreplayable", u.DecisionID, u.Reason)
			}
		}
	}
	if *failOnFlip && flips > 0 {
		return exitRefused, nil
	}
	return exitOK, nil
}

type activation struct {
	Version   version  `json:"version"`
	Previous  int64    `json:"previous"`
	Unchanged bool     `json:"unchanged"`
	Rollback  bool     `json:"rollback"`
	Accepted  []string `json:"accepted"`
	Report    report   `json:"report"`
}

func (c *client) printActivation(a activation) {
	c.line("activated", a.Version.Version, "previous", a.Previous, "rollback", a.Rollback, "unchanged", a.Unchanged)
	for _, id := range a.Accepted {
		c.line("accepted", id)
	}
	c.printFindings(a.Report.Findings)
}

func (c *client) apply(args []string) (int, error) {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	var accept acceptFlags
	fs.Var(&accept, "accept", "accept a warning by its id (repeatable)")
	pos, err := parse(fs, args, 1)
	if err != nil {
		return exitError, err
	}
	source, err := readSource(pos[0])
	if err != nil {
		return exitError, err
	}
	var res struct {
		Upload struct {
			Version  version `json:"version"`
			Existing bool    `json:"existing"`
		} `json:"upload"`
		Activate activation `json:"activate"`
	}
	raw, err := c.call(http.MethodPost, "/policy/apply", map[string]any{"source": source, "accept": []string(accept)}, &res)
	if err != nil {
		return exitError, err
	}
	if c.json {
		c.raw(raw)
		return exitOK, nil
	}
	c.line("version", res.Upload.Version.Version, res.Upload.Version.Digest, "existing", res.Upload.Existing)
	c.printActivation(res.Activate)
	return exitOK, nil
}

func (c *client) activate(args []string) (int, error) {
	fs := flag.NewFlagSet("activate", flag.ContinueOnError)
	var accept acceptFlags
	fs.Var(&accept, "accept", "accept a warning by its id (repeatable)")
	pos, err := parse(fs, args, 1)
	if err != nil {
		return exitError, err
	}
	v, err := strconv.ParseInt(pos[0], 10, 64)
	if err != nil || v <= 0 {
		return exitError, fmt.Errorf("%q is not a version", pos[0])
	}
	var res activation
	raw, err := c.call(http.MethodPost, "/policy/bundles/"+strconv.FormatInt(v, 10)+"/activate",
		map[string]any{"accept": []string(accept)}, &res)
	if err != nil {
		return exitError, err
	}
	if c.json {
		c.raw(raw)
		return exitOK, nil
	}
	c.printActivation(res)
	return exitOK, nil
}

func (c *client) versions(args []string) (int, error) {
	fs := flag.NewFlagSet("versions", flag.ContinueOnError)
	if _, err := parse(fs, args, 0); err != nil {
		return exitError, err
	}
	var res struct {
		Versions []version `json:"versions"`
	}
	raw, err := c.call(http.MethodGet, "/policy/bundles", nil, &res)
	if err != nil {
		return exitError, err
	}
	if c.json {
		c.raw(raw)
		return exitOK, nil
	}
	for _, v := range res.Versions {
		state := "inactive"
		if v.Active {
			state = "active"
		}
		c.line("version", v.Version, v.Digest, v.UploadedBy, v.UploadedAt.UTC().Format(time.RFC3339), state)
	}
	return exitOK, nil
}

type story struct {
	DecisionID string    `json:"decision_id"`
	SessionID  string    `json:"session_id"`
	ProxyID    string    `json:"proxy_id"`
	Effect     string    `json:"effect"`
	DecidedAt  time.Time `json:"decided_at"`
	Rule       string    `json:"rule"`
	RuleLine   int       `json:"rule_line"`
	Mapping    struct {
		Version int  `json:"version"`
		Active  int  `json:"active"`
		Changed bool `json:"changed"`
	} `json:"mapping"`
	Obligations []string        `json:"obligations"`
	Grant       json.RawMessage `json:"grant"`
	Unserved    string          `json:"unserved"`
}

func (c *client) printStory(s story) {
	c.line("decision", s.DecisionID, s.Effect, s.Rule, s.RuleLine, "session", s.SessionID, "proxy", s.ProxyID,
		"decided_at", s.DecidedAt.UTC().Format(time.RFC3339))
	c.line("mapping", s.Mapping.Version, "active", s.Mapping.Active, "changed", s.Mapping.Changed)
	for _, o := range s.Obligations {
		c.line("obligation", o)
	}
	if len(s.Grant) > 0 {
		var compact bytes.Buffer
		_ = json.Compact(&compact, s.Grant)
		c.line("grant", compact.String())
	}
	if s.Unserved != "" {
		c.line("unserved", s.Unserved)
	}
}

func (c *client) explain(args []string) (int, error) {
	fs := flag.NewFlagSet("explain", flag.ContinueOnError)
	session := fs.String("session", "", "explain every decision taken for this session id")
	want := 1
	if len(args) > 0 && strings.HasPrefix(args[0], "-session") {
		want = 0
	}
	pos, err := parse(fs, args, want)
	if err != nil {
		return exitError, err
	}
	if *session != "" {
		var res struct {
			Decisions []story `json:"decisions"`
		}
		raw, err := c.call(http.MethodGet, "/sessions/"+url.PathEscape(*session)+"/decisions", nil, &res)
		if err != nil {
			return exitError, err
		}
		if c.json {
			c.raw(raw)
			return exitOK, nil
		}
		for _, s := range res.Decisions {
			c.printStory(s)
		}
		return exitOK, nil
	}
	var s story
	raw, err := c.call(http.MethodGet, "/decisions/"+url.PathEscape(pos[0]), nil, &s)
	if err != nil {
		return exitError, err
	}
	if c.json {
		c.raw(raw)
		return exitOK, nil
	}
	c.printStory(s)
	return exitOK, nil
}
