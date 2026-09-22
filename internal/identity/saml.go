// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"bytes"
	"compress/flate"
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/beevik/etree"
	dsig "github.com/russellhaering/goxmldsig"

	"github.com/hoplock/control/internal/store"
)

// The SAML broker: Web SSO, HTTP-Redirect out, HTTP-POST back, and a narrow
// profile stated rather than configured.
//
// WHAT THIS DELIBERATELY DOES NOT DO. No IdP-initiated login (a POST with no
// flow behind it is an assertion nobody asked for, and it is how SAML relay
// attacks work); no encrypted assertions in this phase; no artifact binding; no
// unsigned assertions, ever, under any configuration. Each omission is a
// smaller attack surface rather than a missing feature, and the one that would
// be a feature — encrypted assertions — is named in the learnings as such.
//
// THE XML SIGNATURE IS NOT HAND-ROLLED. `goxmldsig` does the canonicalisation
// and the reference processing, because exclusive canonicalisation is where
// every home-grown SAML implementation gets it wrong: the bug is silent, it
// only shows up against a particular IdP's namespace handling, and its
// consequence is accepting a forged assertion. What IS this file's job — and is
// where a SAML implementation gets it wrong second-most often — is checking
// that the signature covers THE ASSERTION THIS SERVER IS ABOUT TO TRUST, which
// is [SAMLBroker.validate] below.

// SAMLConfig is a connector's configuration document.
type SAMLConfig struct {
	// EntityID is this server's SP entity id, as registered with the IdP.
	EntityID string `json:"entity_id"`
	// ACSURL is this server's assertion consumer service, and the value the
	// Response's Destination must name.
	ACSURL string `json:"acs_url"`
	// IdPEntityID is the IdP's entity id, compared with the assertion's
	// Issuer.
	IdPEntityID string `json:"idp_entity_id"`
	// IdPSSOURL is where a login is sent.
	IdPSSOURL string `json:"idp_sso_url"`
	// IdPCertificates are the IdP's signing certificates, base64 DER (the
	// form a SAML metadata document carries them in). More than one so that
	// an IdP rotating its signing certificate does not need a maintenance
	// window.
	IdPCertificates []string `json:"idp_certificates"`
	// NameIDFormat is requested in the AuthnRequest. Empty requests none,
	// which lets the IdP choose its own.
	NameIDFormat string `json:"name_id_format"`
	// LoginAttribute names the SAML attribute carrying the username to seed
	// a new subject with. The NameID is the subject id, always.
	LoginAttribute string `json:"login_attribute"`
	// ClockSkew is how much clock difference the assertion's validity
	// window is allowed. Zero takes DefaultOIDCSkew — the same value, for
	// the same reason.
	ClockSkew time.Duration `json:"clock_skew"`
}

// SAMLBroker brokers one SAML connector.
type SAMLBroker struct {
	name  string
	cfg   SAMLConfig
	roots []*x509.Certificate
	now   func() time.Time
}

// NewSAMLBroker builds a broker for a connector.
func NewSAMLBroker(name string, cfg SAMLConfig, opts ...BrokerOption) (*SAMLBroker, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("identity: a SAML connector needs a name")
	}
	if cfg.EntityID == "" || cfg.ACSURL == "" || cfg.IdPSSOURL == "" {
		return nil, fmt.Errorf("identity: connector %q needs an entity id, an ACS url and an IdP SSO url", name)
	}
	if len(cfg.IdPCertificates) == 0 {
		// A connector with no signing certificate cannot verify
		// anything, and there is no configuration under which this
		// server accepts an unsigned assertion. Refusing at
		// construction is better than at the first login.
		return nil, fmt.Errorf("identity: connector %q must name at least one IdP signing certificate", name)
	}
	if cfg.ClockSkew <= 0 {
		cfg.ClockSkew = DefaultOIDCSkew
	}

	b := &SAMLBroker{name: name, cfg: cfg, now: time.Now}
	for _, raw := range cfg.IdPCertificates {
		der, err := base64.StdEncoding.DecodeString(stripPEMArmour(raw))
		if err != nil {
			return nil, fmt.Errorf("identity: connector %q has a signing certificate that is not base64 DER", name)
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, fmt.Errorf("identity: connector %q has a signing certificate that does not parse", name)
		}
		b.roots = append(b.roots, cert)
	}
	for _, opt := range opts {
		opt.applySAML(b)
	}
	return b, nil
}

// stripPEMArmour lets an operator paste either a bare base64 blob (as SAML
// metadata carries it) or a PEM block (as a download gives it).
func stripPEMArmour(v string) string {
	var b strings.Builder
	for line := range strings.SplitSeq(v, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "-----") {
			continue
		}
		b.WriteString(line)
	}
	return b.String()
}

// Name returns the connector's name.
func (b *SAMLBroker) Name() string { return b.name }

// Kind returns store.ConnectorSAML.
func (b *SAMLBroker) Kind() store.ConnectorKind { return store.ConnectorSAML }

// Begin builds the AuthnRequest and the redirect that carries it.
//
// The request id is the per-flow secret: the Response must name it in
// InResponseTo, which is what makes an assertion nobody asked for — an
// IdP-initiated login, or a replayed one — impossible to complete here.
func (b *SAMLBroker) Begin(_ context.Context, req BeginRequest) (Begun, error) {
	id, err := randomToken(16)
	if err != nil {
		return Begun{}, err
	}
	// A SAML ID is an XML ID and must not start with a digit.
	requestID := "_" + strings.ReplaceAll(strings.ReplaceAll(id, "-", ""), "_", "")

	authn := b.authnRequest(requestID)
	deflated, err := deflateRaw(authn)
	if err != nil {
		return Begun{}, err
	}

	u, err := url.Parse(b.cfg.IdPSSOURL)
	if err != nil {
		return Begun{}, fmt.Errorf("identity: connector %q has an unparseable SSO url", b.name)
	}
	q := u.Query()
	q.Set("SAMLRequest", base64.StdEncoding.EncodeToString(deflated))
	q.Set("RelayState", req.State)
	u.RawQuery = q.Encode()

	return Begun{RedirectURL: u.String(), RequestID: requestID}, nil
}

func (b *SAMLBroker) authnRequest(id string) []byte {
	var sb strings.Builder
	sb.WriteString(`<samlp:AuthnRequest xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol"`)
	sb.WriteString(` xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion"`)
	fmt.Fprintf(&sb, ` ID="%s" Version="2.0" IssueInstant="%s"`,
		xmlAttr(id), b.now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&sb, ` Destination="%s" AssertionConsumerServiceURL="%s"`,
		xmlAttr(b.cfg.IdPSSOURL), xmlAttr(b.cfg.ACSURL))
	sb.WriteString(` ProtocolBinding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-POST">`)
	fmt.Fprintf(&sb, `<saml:Issuer>%s</saml:Issuer>`, xmlText(b.cfg.EntityID))
	if b.cfg.NameIDFormat != "" {
		fmt.Fprintf(&sb, `<samlp:NameIDPolicy Format="%s" AllowCreate="true"/>`,
			xmlAttr(b.cfg.NameIDFormat))
	}
	sb.WriteString(`</samlp:AuthnRequest>`)
	return []byte(sb.String())
}

// Metadata returns this server's SP EntityDescriptor.
func (b *SAMLBroker) Metadata(context.Context) (string, []byte, error) {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	fmt.Fprintf(&sb, `<md:EntityDescriptor xmlns:md="urn:oasis:names:tc:SAML:2.0:metadata" entityID="%s">`,
		xmlAttr(b.cfg.EntityID))
	sb.WriteString(`<md:SPSSODescriptor protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol"`)
	// WantAssertionsSigned is true and AuthnRequestsSigned is false, and
	// both are facts about this implementation rather than settings: this
	// server always requires a signed assertion and does not sign its own
	// requests in this phase.
	sb.WriteString(` WantAssertionsSigned="true" AuthnRequestsSigned="false">`)
	if b.cfg.NameIDFormat != "" {
		fmt.Fprintf(&sb, `<md:NameIDFormat>%s</md:NameIDFormat>`, xmlText(b.cfg.NameIDFormat))
	}
	fmt.Fprintf(&sb, `<md:AssertionConsumerService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-POST" Location="%s" index="0" isDefault="true"/>`,
		xmlAttr(b.cfg.ACSURL))
	sb.WriteString(`</md:SPSSODescriptor></md:EntityDescriptor>`)
	return "application/samlmetadata+xml", []byte(sb.String()), nil
}

// Complete verifies the Response and turns the assertion into ours.
func (b *SAMLBroker) Complete(_ context.Context, req CompleteRequest) (Assertion, error) {
	encoded := req.Params.Get("SAMLResponse")
	if encoded == "" {
		return Assertion{}, refuse(RejectFederationProtocol,
			"this login could not be completed", "the callback carried no SAMLResponse")
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return Assertion{}, refuse(RejectFederationProtocol,
			"this login could not be completed", "the SAMLResponse is not base64")
	}
	if len(raw) > maxIdPResponseBytes {
		return Assertion{}, refuse(RejectFederationProtocol,
			"this login could not be completed", "the SAMLResponse is implausibly large")
	}

	doc := etree.NewDocument()
	if err := doc.ReadFromBytes(raw); err != nil {
		return Assertion{}, refuse(RejectFederationProtocol,
			"this login could not be completed", "the SAMLResponse is not XML")
	}
	response := doc.Root()
	if response == nil || response.Tag != "Response" {
		return Assertion{}, refuse(RejectFederationProtocol,
			"this login could not be completed", "the SAMLResponse is not a samlp:Response")
	}

	assertion, err := b.validate(response)
	if err != nil {
		return Assertion{}, err
	}
	return b.assertionFrom(response, assertion, req.Flow.RequestID)
}

// validate returns the ONE assertion whose integrity this server has verified.
//
// The rule it enforces is the rule SAML implementations get wrong: a valid
// signature somewhere in the document is not a signed assertion. Either the
// Response is signed (in which case its single child assertion is covered) or
// the Assertion itself is signed — and in both cases the element returned is
// the one the signature covered, so nothing downstream can read a second,
// unsigned assertion that was smuggled in beside it.
func (b *SAMLBroker) validate(response *etree.Element) (*etree.Element, error) {
	certStore := &dsig.MemoryX509CertificateStore{Roots: b.roots}
	ctx := dsig.NewDefaultValidationContext(certStore)
	ctx.Clock = dsig.NewFakeClockAt(b.now())

	// A signed Response: validate it, then take the assertion out of the
	// VALIDATED copy rather than out of the document we parsed.
	if validated, err := ctx.Validate(response); err == nil {
		assertions := validated.FindElements("./Assertion")
		if len(assertions) != 1 {
			return nil, refuse(RejectFederationAssertion,
				"this login could not be verified",
				fmt.Sprintf("the signed Response carries %d assertions", len(assertions)))
		}
		return assertions[0], nil
	}

	// Otherwise the Assertion must be signed in its own right. Exactly one
	// assertion may be present: a document with two is one where "which one
	// did the signature cover" has a wrong answer available.
	assertions := response.FindElements("./Assertion")
	if len(assertions) != 1 {
		return nil, refuse(RejectFederationSignature,
			"this login could not be verified",
			fmt.Sprintf("neither the Response nor a single Assertion is signed (%d assertions)", len(assertions)))
	}
	validated, err := ctx.Validate(assertions[0])
	if err != nil {
		return nil, refuse(RejectFederationSignature,
			"this login could not be verified", "the assertion's signature did not verify: "+err.Error())
	}
	return validated, nil
}

// assertionFrom checks the conditions and extracts the attributes.
func (b *SAMLBroker) assertionFrom(response, assertion *etree.Element, expectRequestID string) (Assertion, error) {
	now := b.now()
	skew := b.cfg.ClockSkew

	if dest := response.SelectAttrValue("Destination", ""); dest != "" && dest != b.cfg.ACSURL {
		return Assertion{}, refuse(RejectFederationAudience,
			"this login was not issued for this server",
			fmt.Sprintf("the Response's Destination is %q", dest))
	}
	if code := statusCode(response); code != "" && code != "urn:oasis:names:tc:SAML:2.0:status:Success" {
		return Assertion{}, refuse(RejectFederationNotAllowed,
			"the identity provider refused this login", "status "+code)
	}
	// InResponseTo is checked on the Response AND on the SubjectConfirmation
	// below. Both carry it and an IdP-initiated assertion carries neither,
	// which is exactly the case this server does not serve.
	if got := response.SelectAttrValue("InResponseTo", ""); got != expectRequestID {
		return Assertion{}, refuse(RejectFederationState,
			"this login could not be completed",
			fmt.Sprintf("the Response answers request %q, not the one this flow issued", got))
	}
	if issuer := childText(assertion, "Issuer"); b.cfg.IdPEntityID != "" && issuer != b.cfg.IdPEntityID {
		return Assertion{}, refuse(RejectFederationAssertion,
			"this login could not be verified", fmt.Sprintf("the assertion names issuer %q", issuer))
	}

	subjectEl := assertion.FindElement("./Subject")
	if subjectEl == nil {
		return Assertion{}, refuse(RejectFederationSubject,
			"the identity provider did not identify you", "the assertion carries no Subject")
	}
	nameID := strings.TrimSpace(elementText(subjectEl.FindElement("./NameID")))
	if nameID == "" {
		return Assertion{}, refuse(RejectFederationSubject,
			"the identity provider did not identify you", "the assertion carries no NameID")
	}

	confirmed := false
	for _, scd := range subjectEl.FindElements("./SubjectConfirmation/SubjectConfirmationData") {
		if got := scd.SelectAttrValue("InResponseTo", ""); got != "" && got != expectRequestID {
			continue
		}
		if recipient := scd.SelectAttrValue("Recipient", ""); recipient != "" && recipient != b.cfg.ACSURL {
			continue
		}
		if err := withinWindow(scd, now, skew); err != nil {
			continue
		}
		confirmed = true
		break
	}
	if !confirmed {
		return Assertion{}, refuse(RejectFederationAssertion,
			"this login could not be verified",
			"no SubjectConfirmationData confirms this request, this recipient and this instant")
	}

	if conditions := assertion.FindElement("./Conditions"); conditions != nil {
		if err := withinWindow(conditions, now, skew); err != nil {
			return Assertion{}, refuse(RejectFederationExpired,
				"this login has expired; please try again", err.Error())
		}
		if audiences := conditions.FindElements("./AudienceRestriction/Audience"); len(audiences) > 0 {
			ok := false
			for _, a := range audiences {
				if strings.TrimSpace(elementText(a)) == b.cfg.EntityID {
					ok = true
					break
				}
			}
			if !ok {
				return Assertion{}, refuse(RejectFederationAudience,
					"this login was not issued for this server",
					"the assertion's AudienceRestriction does not name this server")
			}
		}
	}

	single := map[string]string{}
	multi := map[string][]string{}
	for _, attr := range assertion.FindElements("./AttributeStatement/Attribute") {
		name := attr.SelectAttrValue("Name", "")
		if name == "" {
			continue
		}
		var values []string
		for _, v := range attr.FindElements("./AttributeValue") {
			values = append(values, strings.TrimSpace(elementText(v)))
		}
		switch len(values) {
		case 0:
		case 1:
			single[name] = values[0]
		default:
			multi[name] = values
		}
	}

	login := single[b.cfg.LoginAttribute]
	if b.cfg.LoginAttribute == "" {
		login = ""
	}

	return Assertion{
		Connector:   b.name,
		Subject:     nameID,
		Login:       login,
		DisplayName: single["displayName"],
		Email:       single["email"],
		Claims:      single,
		MultiClaims: multi,
	}, nil
}

// withinWindow checks NotBefore/NotOnOrAfter on whichever element carries them.
func withinWindow(el *etree.Element, now time.Time, skew time.Duration) error {
	if v := el.SelectAttrValue("NotBefore", ""); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return fmt.Errorf("NotBefore %q is not an instant", v)
		}
		if now.Add(skew).Before(t) {
			return fmt.Errorf("the assertion is not valid until %s", v)
		}
	}
	if v := el.SelectAttrValue("NotOnOrAfter", ""); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return fmt.Errorf("NotOnOrAfter %q is not an instant", v)
		}
		if !now.Add(-skew).Before(t) {
			return fmt.Errorf("the assertion expired at %s", v)
		}
	}
	return nil
}

func statusCode(response *etree.Element) string {
	el := response.FindElement("./Status/StatusCode")
	if el == nil {
		return ""
	}
	return el.SelectAttrValue("Value", "")
}

func childText(el *etree.Element, tag string) string {
	return strings.TrimSpace(elementText(el.FindElement("./" + tag)))
}

func elementText(el *etree.Element) string {
	if el == nil {
		return ""
	}
	return el.Text()
}

// deflateRaw is the DEFLATE encoding the HTTP-Redirect binding specifies: raw,
// with no zlib header.
func deflateRaw(in []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.DefaultCompression)
	if err != nil {
		return nil, fmt.Errorf("identity: a SAML request could not be compressed")
	}
	if _, err := w.Write(in); err != nil {
		return nil, fmt.Errorf("identity: a SAML request could not be compressed")
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("identity: a SAML request could not be compressed")
	}
	return buf.Bytes(), nil
}

// xmlAttr and xmlText escape a value into an attribute or a text node. They are
// used rather than encoding/xml's marshaller because the documents here are
// small and fixed, and a struct-tagged marshaller for SAML's namespace
// handling is more code than the escaping is.
func xmlAttr(v string) string {
	var b bytes.Buffer
	if err := xml.EscapeText(&b, []byte(v)); err != nil {
		return ""
	}
	return b.String()
}

func xmlText(v string) string { return xmlAttr(v) }

// ParseSAMLConfig decodes a connector's stored configuration.
func ParseSAMLConfig(raw json.RawMessage) (SAMLConfig, error) {
	var cfg SAMLConfig
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return SAMLConfig{}, fmt.Errorf("identity: this SAML connector's configuration could not be read")
	}
	return cfg, nil
}
