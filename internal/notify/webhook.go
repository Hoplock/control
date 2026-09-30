// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The deployment's outbound webhook: Control's core answer at the Notifier seam.
//
// A request is a JSON POST of one notification — codes and structured
// attributes, never a rendered sentence, because the console holds the only
// string catalogue (M21) and every receiver renders the same event its own way.
// When a secret is configured, the body is signed, so a receiver can tell this
// server from anybody else who learned the URL.

// Headers a webhook request carries.
const (
	// HeaderEvent is the notification's kind, e.g. `grant.created`.
	HeaderEvent = "X-Hoplock-Event"
	// HeaderDelivery is the delivery id. It is the same on every retry, so
	// a receiver de-duplicates on it.
	HeaderDelivery = "X-Hoplock-Delivery"
	// HeaderTimestamp is when this attempt was signed, in Unix seconds. A
	// receiver refuses one too old to be fresh, which is what stops a
	// captured request being replayed later.
	HeaderTimestamp = "X-Hoplock-Timestamp"
	// HeaderSignature is `v1=<hex HMAC-SHA256(secret, timestamp + "." + body)>`,
	// present only when a secret is configured.
	HeaderSignature = "X-Hoplock-Signature"
)

// DefaultWebhookTimeout bounds one webhook attempt.
const DefaultWebhookTimeout = 5 * time.Second

// Webhook posts notifications to one URL.
type Webhook struct {
	endpoint *url.URL
	secret   []byte
	client   *http.Client
	now      func() time.Time
}

// WebhookOptions configures a Webhook.
type WebhookOptions struct {
	// URL is where to post. See ParseWebhookURL for what is accepted.
	URL string
	// Secret signs every request. It is read by the caller from the
	// environment variable the configuration names — never from the
	// configuration file, which is not where a secret belongs.
	Secret []byte
	// Timeout bounds one attempt. Zero takes DefaultWebhookTimeout.
	Timeout time.Duration
	// Transport overrides the HTTP transport. Tests use it.
	Transport http.RoundTripper
	// Now overrides the signing clock. Tests use it.
	Now func() time.Time
}

// NewWebhook builds a webhook destination.
func NewWebhook(o WebhookOptions) (*Webhook, error) {
	endpoint, err := ParseWebhookURL(o.URL)
	if err != nil {
		return nil, err
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = DefaultWebhookTimeout
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	return &Webhook{
		endpoint: endpoint,
		secret:   o.Secret,
		now:      now,
		client: &http.Client{
			Timeout:   timeout,
			Transport: o.Transport,
			// A redirect is refused rather than followed: a 307 re-sends
			// the body, signature and all, to wherever the receiver said —
			// and "wherever" was not configured by this deployment.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// ParseWebhookURL accepts an https URL, or an http one to a loopback host —
// automation on the same machine — and nothing else. Notifications name
// subjects, scopes and reasons, and that is not something to send in the clear
// across a network. Credentials in the URL are refused: they would be in the
// configuration file and in every log line that named the URL.
func ParseWebhookURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("notify: the webhook URL must be an absolute http(s) URL")
	}
	if u.User != nil {
		return nil, fmt.Errorf("notify: the webhook URL carries credentials; sign with notify.webhook_secret_env instead")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !loopback(u.Hostname()) {
			return nil, fmt.Errorf("notify: a webhook over plain http is accepted only to a loopback host")
		}
	default:
		return nil, fmt.Errorf("notify: the webhook URL must be https (or http to a loopback host)")
	}
	return u, nil
}

func loopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Name implements Destination.
func (w *Webhook) Name() string { return "webhook" }

// Signed reports whether requests carry a signature.
func (w *Webhook) Signed() bool { return len(w.secret) > 0 }

// Host is the webhook's scheme and host, which is safe to log. The path and
// query are not logged: somebody will one day put a token in one.
func (w *Webhook) Host() string { return w.endpoint.Scheme + "://" + w.endpoint.Host }

// payload is the body a webhook receives.
type payload struct {
	DeliveryID string            `json:"delivery_id"`
	Tenant     string            `json:"tenant"`
	Kind       string            `json:"kind"`
	Severity   string            `json:"severity"`
	SubjectID  string            `json:"subject_id,omitempty"`
	TargetID   string            `json:"target_id,omitempty"`
	SessionID  string            `json:"session_id,omitempty"`
	DecisionID string            `json:"decision_id,omitempty"`
	Attributes map[string]string `json:"attributes"`
	OccurredAt string            `json:"occurred_at"`
	Link       string            `json:"link,omitempty"`
	DedupeKey  string            `json:"dedupe_key,omitempty"`
}

// Deliver implements Destination: one POST.
func (w *Webhook) Deliver(ctx context.Context, d Delivery) error {
	n := d.Notification
	attrs := n.Attributes
	if attrs == nil {
		attrs = map[string]string{}
	}
	body, err := json.Marshal(payload{
		DeliveryID: d.ID,
		Tenant:     string(n.Tenant),
		Kind:       n.Kind,
		Severity:   n.Severity.String(),
		SubjectID:  n.SubjectID,
		TargetID:   n.TargetID,
		SessionID:  n.SessionID,
		DecisionID: n.DecisionID,
		Attributes: attrs,
		OccurredAt: n.OccurredAt.UTC().Format(time.RFC3339Nano),
		Link:       n.Link,
		DedupeKey:  n.DedupeKey,
	})
	if err != nil {
		return fmt.Errorf("%w: encoding: %v", ErrPermanent, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPermanent, err)
	}
	stamp := strconv.FormatInt(w.now().Unix(), 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "hoplock-control")
	req.Header.Set(HeaderEvent, n.Kind)
	req.Header.Set(HeaderDelivery, d.ID)
	req.Header.Set(HeaderTimestamp, stamp)
	if w.Signed() {
		req.Header.Set(HeaderSignature, Sign(w.secret, stamp, body))
	}

	resp, err := w.client.Do(req)
	if err != nil {
		// The URL may carry a token in its path or query, and url.Error
		// prints the whole URL. Report the host only.
		return fmt.Errorf("posting to %s failed: %v", w.Host(), unwrapURLError(err))
	}
	// Drain a little so the connection can be reused, and no more: the
	// receiver's body is not this server's business.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	_ = resp.Body.Close()

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusRequestTimeout, resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode >= 500:
		return fmt.Errorf("%s answered %d", w.Host(), resp.StatusCode)
	default:
		// The receiver answered and refused. Asking again gets the same
		// refusal, so it is not asked again.
		return fmt.Errorf("%w: %s answered %d", ErrPermanent, w.Host(), resp.StatusCode)
	}
}

// Sign computes the signature header value a receiver verifies:
// `v1=` + hex(HMAC-SHA256(secret, timestamp + "." + body)).
func Sign(secret []byte, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

// unwrapURLError drops url.Error's URL, keeping the cause.
func unwrapURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}
