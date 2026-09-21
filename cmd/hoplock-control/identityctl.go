// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/hoplock/control/internal/config"
	"github.com/hoplock/control/internal/credential"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

// The operator commands for identity and the certificate authority (0011).
//
// WHY A CLI AND NOT ONLY THE API. Three of these are the bootstrap: a
// deployment with no principals has nobody who can call an authenticated route,
// so the first token and the first role binding cannot come from the API without
// a chicken-and-egg problem. The rest are here because they are the commands an
// operator needs during an incident, when the console may be exactly what is
// broken.
//
// `cmd/policyctl` (0014) is the terminal client for the north-bound API and will
// carry the operations that have an API. These stay: a bootstrap command that
// talks to the API is a bootstrap command that cannot run first.

// runIdentity dispatches the `identity` subcommands.
func runIdentity(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("identity: a command is required (%s)", strings.Join(identityCommands, ", "))
	}
	switch args[0] {
	case "token-issue":
		return runTokenIssue(args[1:], stdout, stderr)
	case "role-bind":
		return runRoleBind(args[1:], stdout, stderr)
	case "roles":
		return runRoleList(stdout)
	case "mapping-put":
		return runMappingPut(args[1:], stdout, stderr)
	case "mapping-show":
		return runMappingShow(args[1:], stdout, stderr)
	case "connector-put":
		return runConnectorPut(args[1:], stdout, stderr)
	}
	return fmt.Errorf("identity: unknown command %q (known: %s)", args[0], strings.Join(identityCommands, ", "))
}

var identityCommands = []string{
	"token-issue", "role-bind", "roles", "mapping-put", "mapping-show", "connector-put",
}

// runTokenIssue mints a scoped north-bound API token.
//
// THE SECRET IS PRINTED ONCE AND NEVER STORED. It is written to stdout and to
// nothing else: not the log, not an audit record, not an error. An audit record
// of a credential is a credential in the audit store, which is the one place in
// this system designed never to forget anything.
func runTokenIssue(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("hoplock-control identity token-issue", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", defaultConfigPath, "path to the YAML configuration file")
	name := fs.String("name", "", "what an operator recognises this token by (required)")
	scope := fs.String("scope", "", "tenant=role[,role] — repeatable with semicolons for several tenants (required)")
	ttl := fs.Duration("ttl", 0, "how long the token lives; zero means it does not expire")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" || *scope == "" {
		return fmt.Errorf("identity token-issue: --name and --scope are both required")
	}

	scopes, err := parseScopes(*scope)
	if err != nil {
		return err
	}

	cfg, st, closeStore, err := openForIdentity(*configPath)
	if err != nil {
		return err
	}
	defer closeStore()

	federation, err := identity.NewFederation(st,
		identity.WithFederationLogger(quietLogger()))
	if err != nil {
		return err
	}

	cred, principal, err := federation.IssueToken(context.Background(), store.Tenant(cfg.Tenant),
		identity.TokenRequest{DisplayName: *name, Scopes: scopes, TTL: *ttl})
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "principal %s issued in tenant %s\n", principal.ID, cfg.Tenant)
	for _, t := range principal.ScopedTenants() {
		fmt.Fprintf(stdout, "  %s: %s\n", t, strings.Join(principal.RolesIn(t).Strings(), ", "))
	}
	if *ttl > 0 {
		fmt.Fprintf(stdout, "  expires in %s\n", *ttl)
	}
	// The one line that matters, and the only place this value will ever
	// appear.
	fmt.Fprintf(stdout, "\ntoken (shown once):\n%s\n", cred.String())
	return nil
}

// parseScopes reads `tenant=role,role;tenant=role`.
//
// A scope naming a tenant other than the issuing one is delegated
// administration, which Enterprise governs (its E11). The MECHANISM is here
// because the queries are here, and this command is the only thing that writes
// one — deliberately, so that it takes an operator on the box rather than an API
// call.
func parseScopes(spec string) (map[store.Tenant]identity.RoleSet, error) {
	out := map[store.Tenant]identity.RoleSet{}
	for clause := range strings.SplitSeq(spec, ";") {
		clause = strings.TrimSpace(clause)
		if clause == "" {
			continue
		}
		tenant, roles, ok := strings.Cut(clause, "=")
		if !ok {
			return nil, fmt.Errorf("--scope: %q is not tenant=role[,role]", clause)
		}
		tenant = strings.TrimSpace(tenant)
		if tenant == "" {
			return nil, fmt.Errorf("--scope: %q names no tenant", clause)
		}
		var codes []string
		for r := range strings.SplitSeq(roles, ",") {
			if r = strings.TrimSpace(r); r != "" {
				codes = append(codes, r)
			}
		}
		set, err := identity.ParseRoleSet(codes)
		if err != nil {
			return nil, err
		}
		if len(set) == 0 {
			return nil, fmt.Errorf("--scope: %q names no roles, so the token could reach nothing", clause)
		}
		out[store.Tenant(tenant)] = set
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--scope: at least one tenant=role clause is required")
	}
	return out, nil
}

// runRoleBind grants a role to a subject or a group, in one tenant.
func runRoleBind(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("hoplock-control identity role-bind", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", defaultConfigPath, "path to the YAML configuration file")
	tenantFlag := fs.String("tenant", "", "the tenant the role is granted in; empty takes the configured tenant")
	subject := fs.String("subject", "", "the subject to grant to")
	group := fs.String("group", "", "the group to grant to")
	role := fs.String("role", "", "the role to grant (required)")
	remove := fs.Bool("remove", false, "remove the binding instead of adding it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *role == "" {
		return fmt.Errorf("identity role-bind: --role is required")
	}
	if (*subject == "") == (*group == "") {
		return fmt.Errorf("identity role-bind: exactly one of --subject and --group is required")
	}
	parsed, err := identity.ParseRole(*role)
	if err != nil {
		return err
	}

	cfg, st, closeStore, err := openForIdentity(*configPath)
	if err != nil {
		return err
	}
	defer closeStore()

	tenant := store.Tenant(cfg.Tenant)
	if *tenantFlag != "" {
		tenant = store.Tenant(*tenantFlag)
	}

	binding := store.RoleBinding{
		SubjectID: *subject,
		GroupID:   *group,
		Role:      string(parsed),
		GrantedBy: "cli",
	}
	ctx := context.Background()
	if *remove {
		if err := st.RoleBindings().Unbind(ctx, tenant, binding); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "removed %s in tenant %s\n", parsed, tenant)
		return nil
	}
	if err := st.RoleBindings().Bind(ctx, tenant, binding); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "granted %s in tenant %s\n", parsed, tenant)
	return nil
}

// runRoleList prints the fixed role set and what each role may do.
//
// It reads the same tables the middleware checks against, so it cannot drift
// from what is enforced — which is the point of the role set being code rather
// than rows.
func runRoleList(stdout io.Writer) error {
	for _, role := range identity.AllRoles {
		fmt.Fprintf(stdout, "%s\n", role)
		for _, p := range role.Permissions() {
			fmt.Fprintf(stdout, "  %s\n", p)
		}
	}
	return nil
}

// runMappingPut validates a claim mapping and makes it the active version.
func runMappingPut(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("hoplock-control identity mapping-put", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", defaultConfigPath, "path to the YAML configuration file")
	tenantFlag := fs.String("tenant", "", "the tenant to write for; empty takes the configured tenant")
	file := fs.String("file", "", "path to the mapping document (required)")
	dryRun := fs.Bool("dry-run", false, "validate and print, storing nothing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" {
		return fmt.Errorf("identity mapping-put: --file is required")
	}
	document, err := os.ReadFile(*file)
	if err != nil {
		return err
	}

	// A DRY RUN NEEDS NO DATABASE. Validation is pure, which is what lets a
	// mapping be checked in CI beside the policy bundle it feeds.
	if *dryRun {
		mapping, err := identity.ParseMapping(document)
		if err != nil {
			return err
		}
		return printMapping(stdout, mapping, "valid")
	}

	cfg, st, closeStore, err := openForIdentity(*configPath)
	if err != nil {
		return err
	}
	defer closeStore()

	tenant := store.Tenant(cfg.Tenant)
	if *tenantFlag != "" {
		tenant = store.Tenant(*tenantFlag)
	}
	federation, err := identity.NewFederation(st, identity.WithFederationLogger(quietLogger()))
	if err != nil {
		return err
	}
	mapping, err := federation.PutMapping(context.Background(), tenant, document, "cli")
	if err != nil {
		return err
	}
	return printMapping(stdout, mapping, fmt.Sprintf("stored and activated as version %d", mapping.Version))
}

// runMappingShow prints the active mapping.
func runMappingShow(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("hoplock-control identity mapping-show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", defaultConfigPath, "path to the YAML configuration file")
	tenantFlag := fs.String("tenant", "", "the tenant to read; empty takes the configured tenant")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, st, closeStore, err := openForIdentity(*configPath)
	if err != nil {
		return err
	}
	defer closeStore()

	tenant := store.Tenant(cfg.Tenant)
	if *tenantFlag != "" {
		tenant = store.Tenant(*tenantFlag)
	}
	federation, err := identity.NewFederation(st, identity.WithFederationLogger(quietLogger()))
	if err != nil {
		return err
	}
	mapping, err := federation.ActiveMapping(context.Background(), tenant)
	if err != nil {
		return err
	}
	return printMapping(stdout, mapping, fmt.Sprintf("active version %d", mapping.Version))
}

func printMapping(stdout io.Writer, mapping *identity.Mapping, status string) error {
	fmt.Fprintf(stdout, "claim mapping: %s\n", status)
	fmt.Fprintf(stdout, "  digest: %s\n", mapping.Digest)
	fmt.Fprintf(stdout, "  attributes it can produce: %s\n", orNone(mapping.MappedAttributeNames()))
	fmt.Fprintf(stdout, "  groups it can produce: %s\n", orNone(mapping.MappedGroupNames()))
	return nil
}

func orNone(values []string) string {
	if len(values) == 0 {
		return "(none)"
	}
	return strings.Join(values, ", ")
}

// runConnectorPut writes a federation connector.
//
// The configuration document is a file rather than flags, because it is a
// document: a connector has a dozen fields and half of them are URLs. What is
// NOT in it is the client secret — the document names an environment variable,
// and a document that tried to carry one would be a secret in a file and then in
// a database row.
func runConnectorPut(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("hoplock-control identity connector-put", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", defaultConfigPath, "path to the YAML configuration file")
	tenantFlag := fs.String("tenant", "", "the tenant to write for; empty takes the configured tenant")
	name := fs.String("name", "", "the connector's name (required)")
	kind := fs.String("kind", "", "oidc or saml (required)")
	display := fs.String("display-name", "", "what a login page shows")
	file := fs.String("file", "", "path to the connector's JSON configuration (required)")
	disabled := fs.Bool("disabled", false, "write the connector but do not accept logins through it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" || *kind == "" || *file == "" {
		return fmt.Errorf("identity connector-put: --name, --kind and --file are all required")
	}
	connectorKind := store.ConnectorKind(*kind)
	if connectorKind != store.ConnectorOIDC && connectorKind != store.ConnectorSAML {
		return fmt.Errorf("identity connector-put: --kind must be oidc or saml")
	}
	document, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	if !json.Valid(document) {
		return fmt.Errorf("identity connector-put: %s is not JSON", *file)
	}

	// THE CONFIGURATION IS VALIDATED BEFORE IT IS STORED, by building the
	// broker. A connector row that cannot produce a broker is a login page
	// entry that fails at the worst moment, and the error here names the field
	// rather than the connector.
	row := store.Connector{
		Name:        *name,
		Kind:        connectorKind,
		DisplayName: *display,
		Enabled:     !*disabled,
		Config:      document,
	}
	if _, err := (identity.DefaultBrokerFactory{}).Broker(context.Background(), "", row); err != nil {
		return err
	}

	cfg, st, closeStore, err := openForIdentity(*configPath)
	if err != nil {
		return err
	}
	defer closeStore()

	tenant := store.Tenant(cfg.Tenant)
	if *tenantFlag != "" {
		tenant = store.Tenant(*tenantFlag)
	}
	if err := st.Connectors().Upsert(context.Background(), tenant, row); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "connector %s (%s) written for tenant %s, enabled=%t\n",
		*name, *kind, tenant, row.Enabled)
	return nil
}

// runCA dispatches the `ca` subcommands.
func runCA(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("ca: a command is required (show, rotate, issue, certificates)")
	}
	switch args[0] {
	case "show":
		return runCAShow(args[1:], stdout, stderr)
	case "rotate":
		return runCARotate(args[1:], stdout, stderr)
	case "issue":
		return runCAIssue(args[1:], stdout, stderr)
	case "certificates":
		return runCACertificates(args[1:], stdout, stderr)
	}
	return fmt.Errorf("ca: unknown command %q (known: show, rotate, issue, certificates)", args[0])
}

func runCAShow(args []string, stdout, stderr io.Writer) error {
	fs, configPath, tenantFlag := caFlags("show", stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	ca, tenant, closeStore, err := openCA(*configPath, *tenantFlag)
	if err != nil {
		return err
	}
	defer closeStore()

	info, err := ca.Ensure(context.Background(), tenant)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "tenant %s certificate authority\n", tenant)
	fmt.Fprintf(stdout, "  active key: %s (%s, custodian %s, created %s)\n",
		info.ActiveKeyID, info.Algorithm, info.Custodian, info.CreatedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(stdout, "\n# TrustedUserCAKeys — publish ALL of these to every target\n")
	for _, k := range info.TrustBundle {
		fmt.Fprintf(stdout, "%s\n", k.PublicKey)
	}
	return nil
}

func runCARotate(args []string, stdout, stderr io.Writer) error {
	fs, configPath, tenantFlag := caFlags("rotate", stderr)
	comment := fs.String("comment", "", "why this rotation happened")
	compromise := fs.Bool("compromise", false,
		"treat the retired key as compromised: it leaves the trust bundle now and every outstanding certificate it signed is revoked")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ca, tenant, closeStore, err := openCA(*configPath, *tenantFlag)
	if err != nil {
		return err
	}
	defer closeStore()

	result, err := ca.Rotate(context.Background(), tenant, credential.RotateRequest{
		Comment:    *comment,
		Compromise: *compromise,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "tenant %s rotated: %s -> %s\n", tenant, result.PreviousKeyID, result.NewKeyID)
	if *compromise {
		fmt.Fprintf(stdout, "  the retired key left the trust bundle at %s\n",
			result.TrustedUntil.UTC().Format(time.RFC3339))
		fmt.Fprintf(stdout, "  %d outstanding certificates were revoked\n", result.RevokedCertificates)
	} else {
		fmt.Fprintf(stdout, "  the retired key stays trusted until %s, so certificates it signed keep working\n",
			result.TrustedUntil.UTC().Format(time.RFC3339))
		fmt.Fprintf(stdout, "  nothing was revoked\n")
	}
	fmt.Fprintf(stdout, "\n# TrustedUserCAKeys — publish ALL of these to every target\n")
	for _, k := range result.Info.TrustBundle {
		fmt.Fprintf(stdout, "%s\n", k.PublicKey)
	}
	return nil
}

// runCAIssue signs one certificate.
//
// IT EXISTS FOR TESTING AND FOR AN OPERATOR PROVING THE CA WORKS, and it is a
// command rather than an endpoint for exactly that reason: a certificate is
// minted on the decision path for one session, and an API that minted one on
// request would be a way to get a credential without a decision. A command needs
// a shell on the box and the database credential, which is not a privilege
// escalation path — it is already the highest privilege there is.
func runCAIssue(args []string, stdout, stderr io.Writer) error {
	fs, configPath, tenantFlag := caFlags("issue", stderr)
	subject := fs.String("subject", "", "the subject the certificate is for (required)")
	principals := fs.String("principals", "", "comma-separated logins the certificate is valid for (required)")
	target := fs.String("target", "", "the target it was minted to reach (required)")
	port := fs.Int("port", 0, "the target port")
	session := fs.String("session", "", "the session id it belongs to")
	sourceAddress := fs.String("source-address", "", "restrict the certificate to this address")
	keyFile := fs.String("public-key", "", "path to the public key to sign, in authorized_keys form (required)")
	validFor := fs.Duration("valid-for", 0, "how long it lives; zero takes the configured default")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *subject == "" || *principals == "" || *target == "" || *keyFile == "" {
		return fmt.Errorf("ca issue: --subject, --principals, --target and --public-key are all required")
	}
	blob, err := os.ReadFile(*keyFile)
	if err != nil {
		return err
	}
	wire, err := credential.ParseAuthorizedKey(blob)
	if err != nil {
		return err
	}

	ca, tenant, closeStore, err := openCA(*configPath, *tenantFlag)
	if err != nil {
		return err
	}
	defer closeStore()

	ctx := context.Background()
	if _, err := ca.Ensure(ctx, tenant); err != nil {
		return err
	}

	var logins []string
	for p := range strings.SplitSeq(*principals, ",") {
		if p = strings.TrimSpace(p); p != "" {
			logins = append(logins, p)
		}
	}
	slices.Sort(logins)

	issued, err := ca.Issue(ctx, tenant, credential.IssueRequest{
		SubjectID:     *subject,
		Principals:    slices.Compact(logins),
		Target:        *target,
		TargetPort:    *port,
		SessionID:     *session,
		SourceAddress: *sourceAddress,
		PublicKey:     wire,
		ValidFor:      *validFor,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "serial %d, key id %s, signed by %s\n", issued.Serial, issued.KeyID, issued.CAKeyID)
	fmt.Fprintf(stdout, "valid %s .. %s for %s on %s\n",
		issued.ValidAfter.UTC().Format(time.RFC3339),
		issued.ValidBefore.UTC().Format(time.RFC3339),
		strings.Join(issued.Principals, ","), issued.Target)
	fmt.Fprintf(stdout, "\n%s\n", issued.Certificate)
	return nil
}

func runCACertificates(args []string, stdout, stderr io.Writer) error {
	fs, configPath, tenantFlag := caFlags("certificates", stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	ca, tenant, closeStore, err := openCA(*configPath, *tenantFlag)
	if err != nil {
		return err
	}
	defer closeStore()

	rows, err := ca.Outstanding(context.Background(), tenant)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Fprintf(stdout, "tenant %s has no outstanding certificates\n", tenant)
		return nil
	}
	for _, c := range rows {
		fmt.Fprintf(stdout, "%d\t%s\t%s\t%s\texpires %s\n",
			c.Serial, c.SubjectID, strings.Join(c.Principals, ","), c.Target,
			c.ValidBefore.UTC().Format(time.RFC3339))
	}
	return nil
}

func caFlags(name string, stderr io.Writer) (*flag.FlagSet, *string, *string) {
	fs := flag.NewFlagSet("hoplock-control ca "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", defaultConfigPath, "path to the YAML configuration file")
	tenant := fs.String("tenant", "", "the tenant to act in; empty takes the configured tenant")
	return fs, configPath, tenant
}

// openForIdentity opens the store for a command, returning a closer rather than
// deferring inside a helper.
func openForIdentity(configPath string) (*config.Config, *store.Store, func(), error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, nil, nil, err
	}
	st, err := store.Open(context.Background(), cfg.Database.DSN)
	if err != nil {
		return nil, nil, nil, err
	}
	return cfg, st, st.Close, nil
}

// openCA opens the store and builds the certificate authority.
//
// A deployment with no key-encryption key gets an ERROR here rather than the nil
// the listener tolerates: a person typing `ca rotate` is asking for a certificate
// authority, and answering "there is none" without saying why would send them to
// read code.
func openCA(configPath, tenantFlag string) (*credential.CA, store.Tenant, func(), error) {
	cfg, st, closeStore, err := openForIdentity(configPath)
	if err != nil {
		return nil, "", nil, err
	}
	ca, err := buildCA(cfg, st, quietLogger())
	if err != nil {
		closeStore()
		return nil, "", nil, err
	}
	if ca == nil {
		closeStore()
		return nil, "", nil, fmt.Errorf(
			"this deployment has no certificate authority: set credential.key_encryption_key_env to a variable holding 32 base64 bytes")
	}
	tenant := store.Tenant(cfg.Tenant)
	if tenantFlag != "" {
		tenant = store.Tenant(tenantFlag)
	}
	return ca, tenant, closeStore, nil
}

// quietLogger discards the structured log for a one-shot command. A CLI that
// prints its own output does not also want the daemon's log on stderr.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}
