// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package declarative is Control's own access-context provider (M16, M15): a
// probe and a push mapping configured rather than coded, sufficient for most
// scanners and ITSM systems. It is how a self-hosting customer integrates a
// system nobody has heard of without writing Go — and it is not a stub
// standing in for the product; packaged vendor integrations add packaging and
// support, not capability.
//
// It reads and asserts; it never decides. Everything that makes a push safe to
// act on and a probe safe to wait for — scope, replay, skew, the ceiling, the
// budget — is internal/accessctx's, and applies to this provider exactly as it
// does to anyone else's.
package declarative

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/config"
)

// maxAnswerBytes bounds a probe's answer. An answer larger than this is not
// one this provider will parse, and saying so is cheaper than holding it.
const maxAnswerBytes = 1 << 20

// RegistrationPrefix is the provider name every declarative integration is
// registered under, followed by its own name: the registry listing then shows
// one row per configured system, and that the code behind it is Control's.
const RegistrationPrefix = ext.ProviderControl + "/declarative/"

// Provider is one configured integration.
type Provider struct {
	name   string
	probe  *probe
	push   *push
	client *http.Client
	now    func() time.Time
}

var _ ext.AccessContextProvider = (*Provider)(nil)

type probe struct {
	url        template
	host       string
	method     string
	headers    map[string]string
	body       template
	auth       func(*http.Request)
	timeout    time.Duration
	ttl        time.Duration
	absent     []int
	confirm    []assertion
	reference  *jsonPath
	start      *jsonPath
	end        *jsonPath
	additional *jsonPath
	facts      map[string]jsonPath
}

type assertion struct {
	path      jsonPath
	equals    *template
	notEquals *template
	oneOf     []template
	exists    *bool
	contains  *template
}

type push struct {
	id, subject, targets, end                       jsonPath
	reference, scope, start, issued, additionalPath *jsonPath
}

// Options are what a provider needs from the process building it.
type Options struct {
	// Getenv reads a secret's environment variable. Nil is os.LookupEnv.
	Getenv func(string) (string, bool)
	// Now is the clock an answer's ObservedAt is read from. Tests use it.
	Now func() time.Time
}

// New builds one configured integration, checking everything configuration
// loading could not: the URL's shape and egress rules, every path and
// template, and that every secret it names is present. A problem is a
// start-up error naming the provider and the key.
func New(c config.DeclarativeProviderConfig, eg config.EgressConfig, o Options) (*Provider, error) {
	if o.Getenv == nil {
		o.Getenv = os.LookupEnv
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	fail := func(key string, err error) error {
		return fmt.Errorf("access_context.providers[%s].%s: %w", c.Name, key, err)
	}
	var allow []netip.Prefix
	for _, a := range eg.AllowCIDRs {
		p, err := netip.ParsePrefix(strings.TrimSpace(a))
		if err != nil {
			return nil, fmt.Errorf("access_context.egress.allow_cidrs: %w", err)
		}
		allow = append(allow, p.Masked())
	}
	pr := &Provider{name: c.Name, client: egress{allow: allow}.client(), now: o.Now}

	if c.Probe != nil {
		p, err := buildProbe(*c.Probe, o.Getenv)
		if err != nil {
			var keyed *keyError
			if errors.As(err, &keyed) {
				return nil, fail("probe."+keyed.key, keyed.err)
			}
			return nil, fail("probe", err)
		}
		pr.probe = p
	}
	if c.Push != nil {
		p, err := buildPush(*c.Push)
		if err != nil {
			var keyed *keyError
			if errors.As(err, &keyed) {
				return nil, fail("push."+keyed.key, keyed.err)
			}
			return nil, fail("push", err)
		}
		pr.push = p
	}
	if pr.probe == nil && pr.push == nil {
		return nil, fail("probe", fmt.Errorf("a provider probes, takes pushes, or both"))
	}
	return pr, nil
}

type keyError struct {
	key string
	err error
}

func (e *keyError) Error() string { return e.key + ": " + e.err.Error() }

func keyed(key string, err error) error { return &keyError{key: key, err: err} }

func buildProbe(c config.DeclarativeProbeConfig, getenv func(string) (string, bool)) (*probe, error) {
	p := &probe{
		method:  strings.ToUpper(c.Method),
		headers: maps.Clone(c.Headers),
		timeout: c.Timeout,
		ttl:     c.TTL,
		absent:  slices.Clone(c.AbsentStatus),
	}
	if p.method == "" {
		p.method = http.MethodGet
	}
	if len(p.absent) == 0 {
		p.absent = []int{http.StatusNotFound}
	}

	u, err := parseTemplate(c.URL)
	if err != nil {
		return nil, keyed("url", err)
	}
	host, err := checkURL(u)
	if err != nil {
		return nil, keyed("url", err)
	}
	p.url, p.host = u, host

	if c.Body != "" {
		if p.body, err = parseTemplate(c.Body); err != nil {
			return nil, keyed("body", err)
		}
		if !json.Valid([]byte(p.body.render(nil, func(string) string { return sentinel }))) {
			return nil, keyed("body", fmt.Errorf("is not JSON once filled in; a placeholder belongs inside a JSON string"))
		}
	}
	for k, v := range p.headers {
		if strings.ContainsAny(k+v, "\r\n") || strings.Contains(v, "{{") {
			return nil, keyed("headers", fmt.Errorf("%s is sent as written: no line breaks, and no placeholders", k))
		}
	}

	if p.auth, err = buildAuth(c.Auth, getenv); err != nil {
		return nil, keyed("auth", err)
	}
	for i, a := range c.Confirm {
		as, err := buildAssertion(a)
		if err != nil {
			return nil, keyed(fmt.Sprintf("confirm[%d]", i), err)
		}
		p.confirm = append(p.confirm, as)
	}
	if len(p.confirm) == 0 {
		return nil, keyed("confirm", fmt.Errorf("needs at least one assertion"))
	}
	for key, raw := range map[string]string{
		"reference": c.Reference, "window_start": c.WindowStart, "window_end": c.WindowEnd,
		"additional_context": c.AdditionalContext,
	} {
		if raw == "" {
			continue
		}
		path, err := parsePath(raw)
		if err != nil {
			return nil, keyed(key, err)
		}
		switch key {
		case "reference":
			p.reference = &path
		case "window_start":
			p.start = &path
		case "window_end":
			p.end = &path
		case "additional_context":
			p.additional = &path
		}
	}
	p.facts = map[string]jsonPath{}
	for code, raw := range c.Assertions {
		path, err := parsePath(raw)
		if err != nil {
			return nil, keyed("assertions."+code, err)
		}
		p.facts[code] = path
	}
	return p, nil
}

// checkURL refuses a URL whose destination a value could move: the scheme, the
// host and the port must be fixed text, and https is required except to a
// loopback host. It returns the host every rendered URL must still have.
func checkURL(t template) (string, error) {
	u, err := url.Parse(t.sample())
	if err != nil {
		return "", err
	}
	switch {
	case u.Opaque != "" || u.Host == "":
		return "", fmt.Errorf("is not an absolute URL")
	case u.User != nil:
		return "", fmt.Errorf("carries credentials; name them under auth, from the environment")
	case u.Fragment != "":
		return "", fmt.Errorf("has a fragment, which no server is ever sent")
	case strings.Contains(strings.ToLower(u.Scheme+u.Host), sentinel):
		return "", fmt.Errorf("puts a placeholder in its scheme, host or port; a template fills a path or a query, never where the request goes")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !loopbackHost(u.Hostname()) {
			return "", fmt.Errorf("is http to a host that is not loopback; use https")
		}
	default:
		return "", fmt.Errorf("scheme %q is not https", u.Scheme)
	}
	return strings.ToLower(u.Host), nil
}

func loopbackHost(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(h)
	return err == nil && ip.IsLoopback()
}

func buildAuth(a config.DeclarativeAuthConfig, getenv func(string) (string, bool)) (func(*http.Request), error) {
	secret := func(env string) (string, error) {
		if env == "" {
			return "", fmt.Errorf("names no environment variable for its secret")
		}
		v, ok := getenv(env)
		if !ok || v == "" {
			return "", fmt.Errorf("environment variable %s is not set", env)
		}
		return v, nil
	}
	switch a.Type {
	case "", "none":
		return func(*http.Request) {}, nil
	case "bearer":
		tok, err := secret(a.TokenEnv)
		if err != nil {
			return nil, err
		}
		return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }, nil
	case "basic":
		pw, err := secret(a.PasswordEnv)
		if err != nil {
			return nil, err
		}
		if a.Username == "" {
			return nil, fmt.Errorf("basic authentication names a username")
		}
		user := a.Username
		return func(r *http.Request) { r.SetBasicAuth(user, pw) }, nil
	case "header":
		v, err := secret(a.ValueEnv)
		if err != nil {
			return nil, err
		}
		h := http.CanonicalHeaderKey(strings.TrimSpace(a.Header))
		if h == "" || strings.ContainsAny(h, " \r\n:") {
			return nil, fmt.Errorf("header authentication names a header")
		}
		return func(r *http.Request) { r.Header.Set(h, v) }, nil
	}
	return nil, fmt.Errorf("%q is not an authentication type", a.Type)
}

func buildAssertion(a config.DeclarativeAssertion) (assertion, error) {
	path, err := parsePath(a.Path)
	if err != nil {
		return assertion{}, err
	}
	out := assertion{path: path, exists: a.Exists}
	value := func(s *string) (*template, error) {
		if s == nil {
			return nil, nil
		}
		t, err := parseTemplate(*s)
		return &t, err
	}
	if out.equals, err = value(a.Equals); err != nil {
		return assertion{}, err
	}
	if out.notEquals, err = value(a.NotEquals); err != nil {
		return assertion{}, err
	}
	if out.contains, err = value(a.Contains); err != nil {
		return assertion{}, err
	}
	for _, v := range a.OneOf {
		t, err := parseTemplate(v)
		if err != nil {
			return assertion{}, err
		}
		out.oneOf = append(out.oneOf, t)
	}
	n := 0
	for _, set := range []bool{out.equals != nil, out.notEquals != nil, len(out.oneOf) > 0, out.exists != nil, out.contains != nil} {
		if set {
			n++
		}
	}
	if n != 1 {
		return assertion{}, fmt.Errorf("names exactly one of equals, not_equals, one_of, exists, contains")
	}
	return out, nil
}

func buildPush(c config.DeclarativePushConfig) (*push, error) {
	p := &push{}
	required := map[string]*jsonPath{"id": &p.id, "subject": &p.subject, "targets": &p.targets, "window_end": &p.end}
	for key, dst := range required {
		raw := map[string]string{"id": c.ID, "subject": c.Subject, "targets": c.Targets, "window_end": c.WindowEnd}[key]
		path, err := parsePath(raw)
		if err != nil {
			return nil, keyed(key, err)
		}
		*dst = path
	}
	optional := map[string]**jsonPath{
		"reference": &p.reference, "scope": &p.scope, "window_start": &p.start,
		"issued_at": &p.issued, "additional_context": &p.additionalPath,
	}
	raws := map[string]string{
		"reference": c.Reference, "scope": c.Scope, "window_start": c.WindowStart,
		"issued_at": c.IssuedAt, "additional_context": c.AdditionalContext,
	}
	for key, dst := range optional {
		if raws[key] == "" {
			continue
		}
		path, err := parsePath(raws[key])
		if err != nil {
			return nil, keyed(key, err)
		}
		*dst = &path
	}
	return p, nil
}

// Describe implements ext.AccessContextProvider.
func (p *Provider) Describe() ext.AccessContextInfo {
	return ext.AccessContextInfo{Name: p.name, Probes: p.probe != nil, Pushes: p.push != nil}
}

// Probe implements ext.AccessContextProvider: it asks the configured endpoint
// about one access, and reads the answer through the configured assertions.
func (p *Provider) Probe(ctx context.Context, q ext.AccessContextQuery) (ext.AccessEvidence, error) {
	if p.probe == nil {
		return ext.AccessEvidence{}, p.errorf("Probe", ext.KindDisabled, "this provider does not probe")
	}
	pr := p.probe
	values := map[string]string{
		"tenant":          string(q.Tenant),
		"subject.id":      q.Subject.ID,
		"target.hostname": q.Target.Hostname,
		"target.zone":     q.Target.Zone,
		"reference":       q.ExternalRef,
		"at":              q.At.UTC().Format(time.RFC3339),
	}
	if len(q.Privileges) > 0 {
		values["scope"] = q.Privileges[0]
	}

	target := pr.url.render(values, urlEscape)
	u, err := url.Parse(target)
	if err != nil || strings.ToLower(u.Host) != pr.host {
		// Unreachable by construction — checkURL fixed the host — and
		// checked anyway, because this is the line between a template and
		// an SSRF.
		return ext.AccessEvidence{}, p.errorf("Probe", ext.KindInternal, "the rendered URL does not go where the configuration says")
	}
	var body io.Reader
	if !pr.body.empty() {
		body = strings.NewReader(pr.body.render(values, jsonEscape))
	}

	ctx, cancel := context.WithTimeout(ctx, pr.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, pr.method, u.String(), body)
	if err != nil {
		return ext.AccessEvidence{}, p.errorf("Probe", ext.KindInternal, "building the request: %v", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "hoplock-control/declarative")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range pr.headers {
		req.Header.Set(k, v)
	}
	pr.auth(req)

	resp, err := p.client.Do(req)
	if err != nil {
		return ext.AccessEvidence{}, p.transportError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswerBytes+1))
	if err != nil {
		return ext.AccessEvidence{}, p.transportError(err)
	}
	observed := p.now().UTC()

	switch {
	case slices.Contains(pr.absent, resp.StatusCode):
		return ext.AccessEvidence{State: ext.WindowNotConfirmed, ObservedAt: observed, TTL: pr.ttl,
			Assertions: map[string]string{"http_status": strconv.Itoa(resp.StatusCode)}}, nil
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return ext.AccessEvidence{}, p.errorf("Probe", ext.KindUnavailable, "the endpoint answered HTTP %d", resp.StatusCode)
	case len(raw) > maxAnswerBytes:
		return ext.AccessEvidence{}, p.errorf("Probe", ext.KindMalformed, "the answer is larger than %d bytes", maxAnswerBytes)
	}
	doc, err := decodeJSON(raw)
	if err != nil {
		return ext.AccessEvidence{}, p.errorf("Probe", ext.KindMalformed, "the answer is not JSON: %v", err)
	}

	ev := ext.AccessEvidence{State: ext.WindowConfirmed, ObservedAt: observed, TTL: pr.ttl, Assertions: map[string]string{}}
	for code, path := range pr.facts {
		if vs := path.values(doc); len(vs) > 0 {
			ev.Assertions[code] = text(vs[0])
		}
	}
	for _, a := range pr.confirm {
		if !a.holds(doc, values) {
			ev.State = ext.WindowNotConfirmed
			ev.Assertions["unmet"] = a.path.raw
			return ev, nil
		}
	}

	if pr.reference != nil {
		ref, ok := scalar(*pr.reference, doc)
		if !ok {
			return ext.AccessEvidence{}, p.errorf("Probe", ext.KindMalformed, "%s names no single reference", pr.reference.raw)
		}
		ev.Reference = ref
	}
	if ev.Window.NotBefore, err = instantAt(pr.start, doc); err != nil {
		return ext.AccessEvidence{}, p.errorf("Probe", ext.KindMalformed, "window_start: %v", err)
	}
	if ev.Window.NotAfter, err = instantAt(pr.end, doc); err != nil {
		return ext.AccessEvidence{}, p.errorf("Probe", ext.KindMalformed, "window_end: %v", err)
	}
	if ev.AdditionalContext, err = additionalAt(pr.additional, doc); err != nil {
		return ext.AccessEvidence{}, p.errorf("Probe", ext.KindMalformed, "additional_context: %v", err)
	}
	return ev, nil
}

func (a assertion) holds(doc any, values map[string]string) bool {
	vs := a.path.flatValues(doc)
	has := func(want string) bool {
		for _, v := range vs {
			if text(v) == want {
				return true
			}
		}
		return false
	}
	render := func(t *template) string { return t.render(values, func(s string) string { return s }) }
	switch {
	case a.exists != nil:
		return (len(vs) > 0) == *a.exists
	case a.equals != nil:
		return has(render(a.equals))
	case a.notEquals != nil:
		return len(vs) > 0 && !has(render(a.notEquals))
	case a.contains != nil:
		return has(render(a.contains))
	default:
		for _, t := range a.oneOf {
			if has(render(&t)) {
				return true
			}
		}
		return false
	}
}

// Interpret implements ext.AccessContextProvider: it reads a push through the
// configured mapping. It judges nothing — whether the window it reads may
// become a grant is decided after it returns.
func (p *Provider) Interpret(_ context.Context, in ext.AccessContextPush) (ext.WindowAssertion, error) {
	if p.push == nil {
		return ext.WindowAssertion{}, p.errorf("Interpret", ext.KindDisabled, "this provider takes no pushes")
	}
	doc, err := decodeJSON(in.Body)
	if err != nil {
		return ext.WindowAssertion{}, p.errorf("Interpret", ext.KindMalformed, "the push is not JSON: %v", err)
	}
	m := p.push
	var a ext.WindowAssertion
	var ok bool
	if a.ID, ok = scalar(m.id, doc); !ok {
		return ext.WindowAssertion{}, p.errorf("Interpret", ext.KindMalformed, "id: %s names no single value", m.id.raw)
	}
	if a.Subject, ok = scalar(m.subject, doc); !ok {
		return ext.WindowAssertion{}, p.errorf("Interpret", ext.KindMalformed, "subject: %s names no single value", m.subject.raw)
	}
	for _, v := range m.targets.flatValues(doc) {
		s, isString := v.(string)
		if !isString {
			return ext.WindowAssertion{}, p.errorf("Interpret", ext.KindMalformed, "targets: %s holds something that is not a hostname", m.targets.raw)
		}
		a.Targets = append(a.Targets, s)
	}
	if m.reference != nil {
		if a.Reference, ok = scalar(*m.reference, doc); !ok {
			return ext.WindowAssertion{}, p.errorf("Interpret", ext.KindMalformed, "reference: %s names no single value", m.reference.raw)
		}
	}
	if m.scope != nil {
		if a.Scope, ok = scalar(*m.scope, doc); !ok {
			return ext.WindowAssertion{}, p.errorf("Interpret", ext.KindMalformed, "scope: %s names no single value", m.scope.raw)
		}
	}
	if a.Window.NotBefore, err = instantAt(m.start, doc); err != nil {
		return ext.WindowAssertion{}, p.errorf("Interpret", ext.KindMalformed, "window_start: %v", err)
	}
	if a.Window.NotAfter, err = instantAt(&m.end, doc); err != nil {
		return ext.WindowAssertion{}, p.errorf("Interpret", ext.KindMalformed, "window_end: %v", err)
	}
	if a.IssuedAt, err = instantAt(m.issued, doc); err != nil {
		return ext.WindowAssertion{}, p.errorf("Interpret", ext.KindMalformed, "issued_at: %v", err)
	}
	if a.AdditionalContext, err = additionalAt(m.additionalPath, doc); err != nil {
		return ext.WindowAssertion{}, p.errorf("Interpret", ext.KindMalformed, "additional_context: %v", err)
	}
	return a, nil
}

// instantAt reads a time: RFC 3339 text, or a number of Unix seconds. An
// absent path is the zero time; a present value that is neither is an error.
func instantAt(path *jsonPath, doc any) (time.Time, error) {
	if path == nil {
		return time.Time{}, nil
	}
	vs := path.values(doc)
	switch {
	case len(vs) == 0:
		return time.Time{}, nil
	case len(vs) > 1:
		return time.Time{}, fmt.Errorf("%s names more than one value", path.raw)
	}
	switch v := vs[0].(type) {
	case string:
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			return time.Time{}, fmt.Errorf("%s is not an RFC 3339 instant", path.raw)
		}
		return t.UTC(), nil
	case json.Number:
		f, err := v.Float64()
		if err != nil || f <= 0 || f > 1e11 {
			return time.Time{}, fmt.Errorf("%s is not a number of Unix seconds", path.raw)
		}
		sec := int64(f)
		return time.Unix(sec, int64((f-float64(sec))*1e9)).UTC(), nil
	case nil:
		return time.Time{}, nil
	}
	return time.Time{}, fmt.Errorf("%s is not a time", path.raw)
}

// additionalAt reads `additional_context`: a JSON string or a JSON object,
// verbatim, and nothing else — a number, a list or a boolean is refused rather
// than coerced, because it is copied into every session record for an auditor.
func additionalAt(path *jsonPath, doc any) (json.RawMessage, error) {
	if path == nil {
		return nil, nil
	}
	vs := path.values(doc)
	switch {
	case len(vs) == 0:
		return nil, nil
	case len(vs) > 1:
		return nil, fmt.Errorf("%s names more than one value", path.raw)
	}
	switch vs[0].(type) {
	case string, map[string]any:
	case nil:
		return nil, nil
	default:
		return nil, fmt.Errorf("%s is neither a JSON string nor a JSON object", path.raw)
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(vs[0]); err != nil {
		return nil, err
	}
	return json.RawMessage(bytes.TrimSpace(b.Bytes())), nil
}

// transportError classifies a failed exchange: a deadline is a timeout the
// decision record names as one; everything else is an unreachable system.
func (p *Provider) transportError(err error) error {
	var refused *errEgressRefused
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return &ext.Error{Point: ext.PointAccessContextProvider, Provider: p.name, Op: "Probe",
			Kind: ext.KindUnavailable, Err: fmt.Errorf("%w: %w", ext.ErrUnavailable, context.DeadlineExceeded)}
	case errors.As(err, &refused):
		return p.errorf("Probe", ext.KindUnavailable, "%v", refused)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &ext.Error{Point: ext.PointAccessContextProvider, Provider: p.name, Op: "Probe",
			Kind: ext.KindUnavailable, Err: fmt.Errorf("%w: %w", ext.ErrUnavailable, context.DeadlineExceeded)}
	}
	return p.errorf("Probe", ext.KindUnavailable, "the endpoint could not be reached: %v", err)
}

func (p *Provider) errorf(op string, kind ext.Kind, format string, args ...any) error {
	return ext.Errorf(ext.PointAccessContextProvider, p.name, op, kind, format, args...)
}
