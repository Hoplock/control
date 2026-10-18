// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/store"
)

// Signed retention (0024, PLAN §7, M8).
//
// WHAT THE SEAL IS FOR. Retention deletes a stream's oldest records and writes
// where the chain now starts (`audit_retention`). Unsigned, that row was trusted
// as it stood: a writer of the database alone could delete a prefix, insert the
// row a lawful pass would have written — both values readable beforehand — and
// the chain verified without one later record being rewritten. A retained
// capture was the same, one `UPDATE` away. So every pass now signs what it
// wrote with a per-tenant key the database does not hold in usable form, and
// the verifier refuses a marker or a retained capture whose signature does not
// check.
//
// WHAT IT DOES NOT DEFEND AGAINST, SAID AS PLAINLY AS THE CHAIN SAYS ITS OWN.
// Whoever holds the key — the key-encryption key and the database together, or
// the key store — or controls this server can sign anything, so the seal moves
// the line from "can write the database" to "can sign as this tenant", and no
// further. Truncating a stream's newest records is still invisible to the chain,
// sealed or not. Both need an anchor published outside this system, which PLAN
// §7 names as future work.
//
// THE CHECK IS AGAINST THE PRIVATE HALF, NEVER A STORED PUBLIC HALF. A public
// key in a database row is replaced together with the marker it vouches for, by
// the same writer. So the verifier asks the key store for the key's signer and
// checks against its Public(): for Control's software custodian that is derived
// from the private half inside AES-256-GCM ciphertext under a key-encryption key
// the database never sees, and for an HSM it is the device's answer. The public
// key column the software custodian also stores is never read here.
//
// ONE KEY PER TENANT, BECAUSE A DEPARTING TENANT'S CHAIN MUST VERIFY ON ITS OWN
// (M18 point 4). The key's ref always carries the marker's own tenant; a marker
// names only the key's name, so it can never be checked against another
// tenant's key.

// RetentionKeyName is the role part of a retention key's name in the key store.
// The generation is appended, because rotation creates a new name and never
// replaces a key markers are still signed with (ext.KeyStore.Generate).
const RetentionKeyName = "audit-retention"

// The versions inside the signed bytes. They are inside them, not beside them,
// for the reason bodyVersion is: a later encoding must not verify under this
// reading. They also keep the two statements apart — a prefix's signature can
// never check as a capture watermark's, nor either as anything else a tenant's
// keys sign (an SSH certificate starts with a four-byte length, never a digit).
const (
	retentionVersion        = "hoplock.retention/1"
	captureRetentionVersion = "hoplock.capture-retention/1"
)

// RetentionMessage is what a prefix pass signs: the tenant, the stream, the
// sequence the pass retained through and that record's hash, versioned and
// length-prefixed as a chain link is.
func RetentionMessage(tenant store.Tenant, stream string, throughSeq int64, throughHash string) []byte {
	var b bytes.Buffer
	writeParts(&b, retentionVersion, string(tenant), stream, strconv.FormatInt(throughSeq, 10), throughHash)
	return b.Bytes()
}

// CaptureRetentionMessage is what a capture pass signs: the tenant and the
// instant before which its captures may be gone. The instant is rendered in UTC
// to the microsecond, which is what the database keeps, so the bytes the
// verifier rebuilds from the stored row are the bytes that were signed.
func CaptureRetentionMessage(tenant store.Tenant, before time.Time) []byte {
	var b bytes.Buffer
	writeParts(&b, captureRetentionVersion, string(tenant), captureInstant(before).Format(time.RFC3339Nano))
	return b.Bytes()
}

// captureInstant is an instant as the database stores it.
func captureInstant(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }

// ErrRetentionKeyNotConfigured is a retention pass on a deployment with no key
// store: no key-encryption key and no registered ext.KeyStore. The pass is
// refused before anything is deleted, because a marker nothing signed is one a
// writer of the database could have written.
var ErrRetentionKeyNotConfigured = errors.New(
	"audit: no key store is configured to sign retention: set credential.key_encryption_key_env, or register an ext.KeyStore")

// RetentionUnverifiedError is a pass refused because where it would start does
// not verify: the marker it would move, or the captures already retained, carry
// no seal that holds, or the stream no longer starts where its marker says. A
// pass signs where it leaves the stream; signing over a start nobody checked
// would launder whatever made it so.
type RetentionUnverifiedError struct {
	// Stream is the stream refused, empty for a capture pass.
	Stream string
	Detail string
}

func (e *RetentionUnverifiedError) Error() string {
	if e.Stream == "" {
		return "audit: capture retention does not verify: " + e.Detail
	}
	return fmt.Sprintf("audit: stream %s does not verify where retention would start: %s", e.Stream, e.Detail)
}

// IsRetentionUnverified reports whether err is a *RetentionUnverifiedError.
func IsRetentionUnverified(err error) bool {
	var e *RetentionUnverifiedError
	return errors.As(err, &e)
}

// RetentionKeys signs retention seals and checks them, through ext.KeyStore:
// Control's software custodian or a host's, with no second code path (M15).
type RetentionKeys struct {
	keys ext.KeyStore
	now  func() time.Time
}

// NewRetentionKeys builds the signer over a key store. A nil key store is no
// signer at all, which is what a deployment without one has.
func NewRetentionKeys(ks ext.KeyStore) *RetentionKeys {
	if ks == nil {
		return nil
	}
	return &RetentionKeys{keys: ks, now: time.Now}
}

// IsRetentionKey reports whether a key store name is a retention key's.
func IsRetentionKey(name string) bool {
	gen, ok := strings.CutPrefix(name, RetentionKeyName+":")
	return ok && gen != ""
}

// Current names the key new seals are signed with: the newest generation the
// tenant holds, created on first use. Generations sort by name, oldest first,
// so the newest is the greatest — which a key store's own creation times, which
// a host's store may not keep, are never asked to decide.
func (k *RetentionKeys) Current(ctx context.Context, tenant store.Tenant) (ext.KeyRef, error) {
	ref, ok, err := k.Newest(ctx, tenant)
	if err != nil || ok {
		return ref, err
	}
	info, err := k.generate(ctx, tenant, "")
	if err != nil {
		return ext.KeyRef{}, err
	}
	return info.Ref, nil
}

// Rotate makes a new generation, which signs every seal from now on. Older
// generations stay in the key store, so a marker signed before the rotation
// names a key that still checks it.
func (k *RetentionKeys) Rotate(ctx context.Context, tenant store.Tenant) (ext.KeyInfo, error) {
	ref, _, err := k.Newest(ctx, tenant)
	if err != nil {
		return ext.KeyInfo{}, err
	}
	return k.generate(ctx, tenant, ref.Name)
}

// Newest names the tenant's newest retention key without creating one; false
// when it has none yet.
func (k *RetentionKeys) Newest(ctx context.Context, tenant store.Tenant) (ext.KeyRef, bool, error) {
	infos, err := k.keys.List(ctx, ext.Tenant(tenant))
	if err != nil {
		return ext.KeyRef{}, false, err
	}
	var best string
	for _, info := range infos {
		if IsRetentionKey(info.Ref.Name) && info.Ref.Name > best {
			best = info.Ref.Name
		}
	}
	return ext.KeyRef{Tenant: ext.Tenant(tenant), Name: best}, best != "", nil
}

// generationLayout names a generation by the instant it was made, fixed-width
// to the nanosecond, so names sort as their instants do.
const generationLayout = "20060102T150405.000000000Z"

// generate makes a generation named for the instant it was made — never before
// the newest, so it sorts after it and signs from now on even when this
// server's clock is behind the one that made the newest — plus enough entropy
// that two nodes making the first key together do not collide.
func (k *RetentionKeys) generate(ctx context.Context, tenant store.Tenant, after string) (ext.KeyInfo, error) {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return ext.KeyInfo{}, fmt.Errorf("audit: a retention key name could not be generated")
	}
	at := k.now().UTC()
	if gen, ok := strings.CutPrefix(after, RetentionKeyName+":"); ok {
		stamp, _, _ := strings.Cut(gen, "-")
		if prev, err := time.Parse(generationLayout, stamp); err == nil && !at.After(prev) {
			at = prev.Add(time.Nanosecond)
		}
	}
	name := RetentionKeyName + ":" + at.Format(generationLayout) + "-" + hex.EncodeToString(buf)
	if name <= after {
		return ext.KeyInfo{}, fmt.Errorf(
			"audit: tenant %s's newest retention key %s does not name its instant as this build does; a new one would never sign", tenant, after)
	}
	return k.keys.Generate(ctx, ext.KeyRef{Tenant: ext.Tenant(tenant), Name: name}, ext.KeyAlgorithmEd25519)
}

// Public answers a retention key's public half and custodian, taken from the
// key store's signer — never from a stored public half, for the reason the
// verifier never reads one.
func (k *RetentionKeys) Public(ctx context.Context, tenant store.Tenant, name string) (ed25519.PublicKey, string, error) {
	ref := ext.KeyRef{Tenant: ext.Tenant(tenant), Name: name}
	signer, err := k.keys.Signer(ctx, ref)
	if err != nil {
		return nil, "", err
	}
	pub, ok := signer.Public().(ed25519.PublicKey)
	if !ok {
		return nil, "", fmt.Errorf("audit: tenant %s's retention key %s is not an Ed25519 key", tenant, name)
	}
	custodian := ""
	if info, err := k.keys.Describe(ctx, ref); err == nil {
		custodian = info.Custodian
	}
	return pub, custodian, nil
}

// sign seals a message with the tenant's current key. The signature is checked
// before it is returned, so a key store that signs wrongly is an outage now
// rather than a marker that never verifies.
func (k *RetentionKeys) sign(ctx context.Context, tenant store.Tenant, msg []byte) (store.RetentionSeal, error) {
	ref, err := k.Current(ctx, tenant)
	if err != nil {
		return store.RetentionSeal{}, err
	}
	signer, err := k.keys.Signer(ctx, ref)
	if err != nil {
		return store.RetentionSeal{}, err
	}
	pub, ok := signer.Public().(ed25519.PublicKey)
	if !ok {
		return store.RetentionSeal{}, fmt.Errorf("audit: tenant %s's retention key %s is not an Ed25519 key", tenant, ref.Name)
	}
	sig, err := signer.Sign(rand.Reader, msg, crypto.Hash(0))
	if err != nil {
		return store.RetentionSeal{}, fmt.Errorf("audit: tenant %s's retention key %s did not sign: %w", tenant, ref.Name, err)
	}
	if !ed25519.Verify(pub, msg, sig) {
		return store.RetentionSeal{}, fmt.Errorf("audit: tenant %s's retention key %s signed something its public half does not check", tenant, ref.Name)
	}
	return store.RetentionSeal{KeyName: ref.Name, Signature: base64.StdEncoding.EncodeToString(sig)}, nil
}

// check answers why a seal does not hold over msg, or "" when it does. An error
// is an outage — the key store could not be asked — and never a verdict.
//
// A nil receiver is a verifier with no key store, which can check nothing: it
// says so rather than trusting what it cannot check.
func (k *RetentionKeys) check(ctx context.Context, tenant store.Tenant, seal store.RetentionSeal, msg []byte) (string, error) {
	switch {
	case seal.Signature == "":
		return "it carries no signature: it was written before v0.11.0, or by something other than a retention pass", nil
	case !IsRetentionKey(seal.KeyName):
		return fmt.Sprintf("it names key %q, which is not a retention key", seal.KeyName), nil
	case k == nil:
		return "no key store is configured, so its signature cannot be checked: set credential.key_encryption_key_env, or register an ext.KeyStore", nil
	}
	signer, err := k.keys.Signer(ctx, ext.KeyRef{Tenant: ext.Tenant(tenant), Name: seal.KeyName})
	if ext.IsNotFound(err) {
		return fmt.Sprintf("it names key %s, which this key store does not hold: the key was destroyed, or another key store signed it", seal.KeyName), nil
	}
	if err != nil {
		return "", err
	}
	pub, ok := signer.Public().(ed25519.PublicKey)
	if !ok {
		return fmt.Sprintf("key %s is not an Ed25519 key", seal.KeyName), nil
	}
	sig, err := base64.StdEncoding.DecodeString(seal.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return "its signature is not an Ed25519 signature", nil
	}
	if !ed25519.Verify(pub, msg, sig) {
		return fmt.Sprintf("its signature does not check under key %s", seal.KeyName), nil
	}
	return "", nil
}

// checkPrefixStart is a prefix pass's check, under the stream's append lock:
// the marker the pass would move must carry a seal that holds, and the stream
// must start where the marker — or, with none, sequence 1 — says it does.
func (k *RetentionKeys) checkPrefixStart(ctx context.Context, tenant store.Tenant, stream string, prior *store.AuditRetention, first store.AuditRecord) error {
	if prior == nil {
		if first.ChainSeq != 1 || first.PrevHash != "" {
			return &RetentionUnverifiedError{Stream: stream, Detail: fmt.Sprintf(
				"it has no marker and starts at sequence %d: records before it were removed by something other than retention", first.ChainSeq)}
		}
		return nil
	}
	problem, err := k.check(ctx, tenant, sealOf(*prior), RetentionMessage(tenant, stream, prior.ThroughSeq, prior.ThroughHash))
	if err != nil {
		return err
	}
	if problem != "" {
		return &RetentionUnverifiedError{Stream: stream, Detail: fmt.Sprintf("its marker through %d: %s", prior.ThroughSeq, problem)}
	}
	if first.ChainSeq != prior.ThroughSeq+1 || first.PrevHash != prior.ThroughHash {
		return &RetentionUnverifiedError{Stream: stream, Detail: fmt.Sprintf(
			"its marker is through %d, and the stream starts at sequence %d, which does not chain onto it", prior.ThroughSeq, first.ChainSeq)}
	}
	return nil
}

// checkCaptureStart is a capture pass's check: the tenant's watermark must carry
// a seal that holds, and no capture may already be marked retained outside it.
func (k *RetentionKeys) checkCaptureStart(ctx context.Context, tenant store.Tenant, prior *store.CaptureRetention, outside int64) error {
	if prior != nil {
		problem, err := k.check(ctx, tenant, store.RetentionSeal{KeyName: prior.KeyName, Signature: prior.Signature},
			CaptureRetentionMessage(tenant, prior.Before))
		if err != nil {
			return err
		}
		if problem != "" {
			return &RetentionUnverifiedError{Detail: "its watermark: " + problem}
		}
	}
	if outside > 0 {
		return &RetentionUnverifiedError{Detail: fmt.Sprintf(
			"%d capture(s) are marked retained outside any signed capture pass", outside)}
	}
	return nil
}

func sealOf(m store.AuditRetention) store.RetentionSeal {
	return store.RetentionSeal{KeyName: m.KeyName, Signature: m.Signature}
}
