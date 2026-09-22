// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// A real out-of-band second factor (M7, PLAN §6).
//
// It is a PUSH provider: this server asks an MFA service to put a prompt in
// front of the person, and then asks how it went. That shape is chosen because
// it is the one the contract already describes — the proxy relays and polls, so
// the factor must be something that resolves asynchronously — and because it is
// the shape every commercial MFA service offers.
//
// WHAT IT DELIBERATELY IS NOT. It is not TOTP. A code the user types has to
// arrive on the credential channel, and on this contract that channel is the
// password field: a TOTP factor would therefore be a password with a second
// meaning, which is how "enter your password, then your password again"
// happens. TOTP belongs to whatever enrolls it, not to the poll-based seam.
//
// EVERYTHING ABOVE THE PROVIDER SEAM STAYS ABOVE IT. Challenge lifetime, poll
// rate, single use, the poll budget and expiry-as-deny are [Service]'s and are
// the same whichever provider is in play (PLAN §6). This file's whole job is
// "start one" and "how is it going".

// PushConfig configures the provider for a deployment.
//
// The shared secret is NOT here. `SecretEnv` names the environment variable it
// is read from, for the same reason an OIDC client secret is not a row: a
// secret in a config struct is a secret in every log line that ever prints one.
type PushConfig struct {
	// Name is the provider name stored on an enrollment and on every
	// challenge, so a challenge issued by one provider is never polled
	// against another. Empty takes DefaultPushProviderName.
	Name string `yaml:"name"`
	// BeginURL and PollURL are the MFA service's endpoints.
	BeginURL string `yaml:"begin_url"`
	PollURL  string `yaml:"poll_url"`
	// SecretEnv names the environment variable holding the shared secret
	// that request signatures are computed with.
	SecretEnv string `yaml:"secret_env"`
	// Timeout bounds one call to the service. Zero takes
	// DefaultPushTimeout.
	Timeout time.Duration `yaml:"timeout"`
}

// Defaults for the push provider.
const (
	// DefaultPushProviderName is what an enrollment names when the
	// deployment does not rename it.
	DefaultPushProviderName = "push"
	// DefaultPushTimeout bounds one call. It is shorter than the default
	// challenge TTL on purpose: a provider that has not answered inside
	// this is one the poll gives up on, and the next poll tries again.
	DefaultPushTimeout = 5 * time.Second
	// pushSignatureHeader carries the request signature.
	pushSignatureHeader = "X-Hoplock-Signature"
	// maxPushResponseBytes bounds what is read from the service.
	maxPushResponseBytes = 1 << 16
)

// PushMFA is an out-of-band push provider.
type PushMFA struct {
	cfg    PushConfig
	client *http.Client
	secret []byte
}

// NewPushMFA builds the provider, reading its shared secret from the
// environment.
//
// It refuses to build without one. A provider that would silently send
// unsigned requests is a provider whose endpoint anybody on the network can
// drive: "approve challenge X" is a request worth forging.
func NewPushMFA(cfg PushConfig) (*PushMFA, error) {
	if cfg.BeginURL == "" || cfg.PollURL == "" {
		return nil, fmt.Errorf("identity: a push MFA provider needs a begin url and a poll url")
	}
	secret, err := secretFromEnv(cfg.SecretEnv)
	if err != nil {
		return nil, err
	}
	if cfg.Name == "" {
		cfg.Name = DefaultPushProviderName
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultPushTimeout
	}
	return &PushMFA{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.Timeout},
		secret: []byte(secret),
	}, nil
}

// WithPushHTTPClient overrides the client. Tests point it at an httptest
// server.
func (p *PushMFA) WithPushHTTPClient(c *http.Client) *PushMFA {
	if c != nil {
		p.client = c
	}
	return p
}

// Name returns the provider name.
func (p *PushMFA) Name() string { return p.cfg.Name }

// pushEnrollment is the subject's enrollment row, as this provider reads it.
//
// It is opaque to everything above [MFAProvider], which is what lets a second
// provider store something completely different in the same column.
type pushEnrollment struct {
	// Device is the provider's handle on the person's enrolled device.
	Device string `json:"device"`
	// Prompt overrides the prompt shown to the user.
	Prompt string `json:"prompt"`
}

type pushBeginRequest struct {
	Device  string `json:"device"`
	Subject string `json:"subject"`
	Login   string `json:"login"`
	Prompt  string `json:"prompt"`
}

type pushBeginResponse struct {
	Ref         string `json:"ref"`
	Prompt      string `json:"prompt"`
	PollAfterMS int    `json:"poll_after_ms"`
	TTLSeconds  int    `json:"ttl_seconds"`
	Error       string `json:"error"`
}

type pushPollRequest struct {
	Device string `json:"device"`
	Ref    string `json:"ref"`
	Polls  int    `json:"polls"`
}

type pushPollResponse struct {
	Result string `json:"result"`
	Error  string `json:"error"`
}

// Begin asks the service to put a prompt in front of the person.
func (p *PushMFA) Begin(ctx context.Context, id Identity, config json.RawMessage) (MFATerms, error) {
	enrollment, err := parsePushEnrollment(config)
	if err != nil {
		return MFATerms{}, err
	}

	var resp pushBeginResponse
	if err := p.call(ctx, p.cfg.BeginURL, pushBeginRequest{
		Device:  enrollment.Device,
		Subject: id.Subject,
		Login:   id.Login,
		Prompt:  enrollment.Prompt,
	}, &resp); err != nil {
		return MFATerms{}, err
	}
	if resp.Error != "" {
		return MFATerms{}, fmt.Errorf("identity: the MFA service refused to start a challenge (%s)", resp.Error)
	}
	if resp.Ref == "" {
		return MFATerms{}, fmt.Errorf("identity: the MFA service started a challenge without a reference")
	}

	prompt := firstNonEmpty(resp.Prompt, enrollment.Prompt, "Approve the sign-in request on your device")
	return MFATerms{
		Ref:       resp.Ref,
		Prompt:    prompt,
		PollAfter: time.Duration(resp.PollAfterMS) * time.Millisecond,
		TTL:       time.Duration(resp.TTLSeconds) * time.Second,
	}, nil
}

// Poll asks how the challenge is going.
//
// An error is an OUTAGE — the provider could not be asked — and never a
// refusal. A refusal is [MFARefused], and the difference is M11 one layer down:
// an MFA service that is down must not deny everybody's login.
func (p *PushMFA) Poll(ctx context.Context, config json.RawMessage, ref string, polls int) (MFAResult, error) {
	enrollment, err := parsePushEnrollment(config)
	if err != nil {
		return MFAPending, err
	}

	var resp pushPollResponse
	if err := p.call(ctx, p.cfg.PollURL, pushPollRequest{
		Device: enrollment.Device,
		Ref:    ref,
		Polls:  polls,
	}, &resp); err != nil {
		return MFAPending, err
	}
	if resp.Error != "" {
		return MFAPending, fmt.Errorf("identity: the MFA service could not report on a challenge (%s)", resp.Error)
	}

	switch strings.ToLower(strings.TrimSpace(resp.Result)) {
	case "approved":
		return MFAApproved, nil
	case "refused", "denied":
		return MFARefused, nil
	case "pending", "":
		return MFAPending, nil
	}
	// A result this build does not recognise is an OUTAGE rather than a
	// guess. Coercing an unknown answer to "pending" holds the session open
	// forever; coercing it to "refused" denies a person who may well have
	// approved.
	return MFAPending, fmt.Errorf("identity: the MFA service answered %q, which this build does not recognise", resp.Result)
}

func parsePushEnrollment(config json.RawMessage) (pushEnrollment, error) {
	var e pushEnrollment
	if len(config) == 0 {
		return pushEnrollment{}, fmt.Errorf("identity: this subject's push enrollment is empty")
	}
	dec := json.NewDecoder(bytes.NewReader(config))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		return pushEnrollment{}, fmt.Errorf("identity: this subject's push enrollment could not be read")
	}
	if e.Device == "" {
		return pushEnrollment{}, fmt.Errorf("identity: this subject's push enrollment names no device")
	}
	return e, nil
}

// call signs and sends one request.
//
// The signature is HMAC-SHA256 over the exact body bytes, so the service can
// tell a request this server made from one somebody else made. It is a
// deployment-shared secret rather than a per-request nonce because the
// endpoints are the MFA vendor's and this is the mechanism they offer.
func (p *PushMFA) call(ctx context.Context, endpoint string, body, into any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("identity: an MFA request could not be encoded")
	}

	mac := hmac.New(sha256.New, p.secret)
	mac.Write(payload)
	signature := hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("identity: the MFA service's endpoint is unusable")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set(pushSignatureHeader, signature)

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("identity: the MFA service could not be reached")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("identity: the MFA service answered %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxPushResponseBytes))
	if err != nil {
		return fmt.Errorf("identity: the MFA service's response could not be read")
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("identity: the MFA service's response was not the JSON this server expects")
	}
	return nil
}

// SignPushRequest recomputes the signature a service should expect. It exists
// so a test — and a vendor writing the far end — can verify against the same
// code this server signs with.
func SignPushRequest(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
