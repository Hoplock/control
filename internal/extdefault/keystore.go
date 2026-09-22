// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package extdefault

import (
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"fmt"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/store"
)

// Control's own key custody: the CORE behaviour at `ext.PointKeyStore`.
//
// The point's catalogue entry says WhenAbsentCore — "Control holds its own
// software keys and signs with them" — so this is not registered as a default
// in the registry; it is what the certificate authority uses when nothing has
// been registered. An organisation with an HSM registers an `ext.KeyStore` and
// this file stops being reached, with no second code path anywhere above it,
// because the seam is `crypto.Signer` and an HSM-backed signer works everywhere
// a software one does.
//
// THE PRIVATE HALF IS NEVER STORED IN THE CLEAR. It is AES-256-GCM ciphertext
// under a key-encryption key the process is configured with and the database
// never sees, and this store REFUSES TO EXIST without one. That refusal is the
// design: a CA private key sitting in plaintext in a Postgres row is not a
// default worth shipping, and an option to disable encryption is an option
// somebody will find in a hurry during an incident.

// KeyEncryptionKeySize is the key-encryption key's length in bytes: AES-256.
const KeyEncryptionKeySize = 32

// SoftwareKeyStore holds Control's own keys in the database, encrypted.
type SoftwareKeyStore struct {
	keys store.SoftwareKeyRepository
	aead cipher.AEAD
	now  func() time.Time
}

// NewSoftwareKeyStore builds the store over a key-encryption key.
//
// kek must be exactly KeyEncryptionKeySize bytes. A short key is refused rather
// than stretched: stretching it here would let a deployment believe it had
// 256-bit custody when it had whatever the operator typed.
func NewSoftwareKeyStore(st *store.Store, kek []byte) (*SoftwareKeyStore, error) {
	if st == nil {
		return nil, fmt.Errorf("extdefault: a store is required")
	}
	if len(kek) != KeyEncryptionKeySize {
		return nil, fmt.Errorf(
			"extdefault: the key-encryption key must be %d bytes; this deployment supplied %d",
			KeyEncryptionKeySize, len(kek))
	}
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, fmt.Errorf("extdefault: the key-encryption key could not be used")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("extdefault: the key-encryption key could not be used")
	}
	return &SoftwareKeyStore{keys: st.SoftwareKeys(), aead: aead, now: time.Now}, nil
}

// Generate creates a key at ref and returns what it made.
//
// A ref that already exists is an ErrConflict: key rotation creates a new name,
// it does not silently replace a key certificates are still chained to.
func (s *SoftwareKeyStore) Generate(ctx context.Context, ref ext.KeyRef, algo ext.KeyAlgorithm) (ext.KeyInfo, error) {
	if err := checkRef("Generate", ref); err != nil {
		return ext.KeyInfo{}, err
	}
	if _, err := s.keys.Get(ctx, store.Tenant(ref.Tenant), ref.Name); err == nil {
		return ext.KeyInfo{}, ext.Errorf(ext.PointKeyStore, ext.ProviderControl, "Generate",
			ext.KindConflict, "key %s/%s already exists", ref.Tenant, ref.Name)
	} else if !store.IsNotFound(err) {
		return ext.KeyInfo{}, keyStoreError("Generate", err)
	}

	priv, pub, err := generateKey(algo)
	if err != nil {
		return ext.KeyInfo{}, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return ext.KeyInfo{}, ext.Errorf(ext.PointKeyStore, ext.ProviderControl, "Generate",
			ext.KindInternal, "the generated key could not be encoded")
	}
	sealed, err := s.seal(der)
	if err != nil {
		return ext.KeyInfo{}, err
	}
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return ext.KeyInfo{}, ext.Errorf(ext.PointKeyStore, ext.ProviderControl, "Generate",
			ext.KindInternal, "the generated public key could not be encoded")
	}

	row := store.SoftwareKey{
		Name:       ref.Name,
		Algorithm:  algo.String(),
		PublicKey:  pubDER,
		PrivateKey: sealed,
	}
	if err := s.keys.Insert(ctx, store.Tenant(ref.Tenant), row); err != nil {
		return ext.KeyInfo{}, keyStoreError("Generate", err)
	}
	return ext.KeyInfo{
		Ref:       ref,
		Algorithm: algo,
		Public:    pub,
		CreatedAt: s.now(),
		Custodian: CustodianSoftware,
	}, nil
}

// CustodianSoftware is what `ext.KeyInfo.Custodian` says for a key this store
// holds. It is recorded so that an auditor asking "where is the CA key" has an
// answer that does not depend on asking an engineer.
const CustodianSoftware = "software"

// Describe returns a key's public half and provenance.
func (s *SoftwareKeyStore) Describe(ctx context.Context, ref ext.KeyRef) (ext.KeyInfo, error) {
	if err := checkRef("Describe", ref); err != nil {
		return ext.KeyInfo{}, err
	}
	row, err := s.keys.Get(ctx, store.Tenant(ref.Tenant), ref.Name)
	if err != nil {
		return ext.KeyInfo{}, keyStoreError("Describe", err)
	}
	pub, err := x509.ParsePKIXPublicKey(row.PublicKey)
	if err != nil {
		return ext.KeyInfo{}, ext.Errorf(ext.PointKeyStore, ext.ProviderControl, "Describe",
			ext.KindInternal, "the stored public key could not be decoded")
	}
	return ext.KeyInfo{
		Ref:       ref,
		Algorithm: parseAlgorithm(row.Algorithm),
		Public:    pub,
		CreatedAt: row.CreatedAt,
		Custodian: CustodianSoftware,
	}, nil
}

// Signer returns a signer for the key's private half.
func (s *SoftwareKeyStore) Signer(ctx context.Context, ref ext.KeyRef) (crypto.Signer, error) {
	if err := checkRef("Signer", ref); err != nil {
		return nil, err
	}
	row, err := s.keys.Get(ctx, store.Tenant(ref.Tenant), ref.Name)
	if err != nil {
		return nil, keyStoreError("Signer", err)
	}
	der, err := s.open(row.PrivateKey)
	if err != nil {
		return nil, err
	}
	priv, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, ext.Errorf(ext.PointKeyStore, ext.ProviderControl, "Signer",
			ext.KindInternal, "the stored private key could not be decoded")
	}
	signer, ok := priv.(crypto.Signer)
	if !ok {
		return nil, ext.Errorf(ext.PointKeyStore, ext.ProviderControl, "Signer",
			ext.KindInternal, "the stored private key cannot sign")
	}
	return signer, nil
}

// List returns every key the tenant holds.
func (s *SoftwareKeyStore) List(ctx context.Context, tenant ext.Tenant) ([]ext.KeyInfo, error) {
	if tenant == "" {
		return nil, ext.Errorf(ext.PointKeyStore, ext.ProviderControl, "List",
			ext.KindInvalid, "tenant is required")
	}
	rows, err := s.keys.List(ctx, store.Tenant(tenant))
	if err != nil {
		return nil, keyStoreError("List", err)
	}
	out := make([]ext.KeyInfo, 0, len(rows))
	for _, row := range rows {
		pub, perr := x509.ParsePKIXPublicKey(row.PublicKey)
		if perr != nil {
			continue
		}
		out = append(out, ext.KeyInfo{
			Ref:       ext.KeyRef{Tenant: tenant, Name: row.Name},
			Algorithm: parseAlgorithm(row.Algorithm),
			Public:    pub,
			CreatedAt: row.CreatedAt,
			Custodian: CustodianSoftware,
		})
	}
	return out, nil
}

// Destroy removes a key. It is irreversible, and Control calls it only when an
// operator has said so explicitly.
func (s *SoftwareKeyStore) Destroy(ctx context.Context, ref ext.KeyRef) error {
	if err := checkRef("Destroy", ref); err != nil {
		return err
	}
	if err := s.keys.Delete(ctx, store.Tenant(ref.Tenant), ref.Name); err != nil {
		return keyStoreError("Destroy", err)
	}
	return nil
}

// seal encrypts a private key. The nonce is random per call and is prefixed to
// the ciphertext, which is what makes two generations of the same key
// indistinguishable to anybody reading the table.
func (s *SoftwareKeyStore) seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, ext.Errorf(ext.PointKeyStore, ext.ProviderControl, "seal",
			ext.KindInternal, "a nonce could not be generated")
	}
	return s.aead.Seal(nonce, nonce, plaintext, nil), nil
}

func (s *SoftwareKeyStore) open(sealed []byte) ([]byte, error) {
	size := s.aead.NonceSize()
	if len(sealed) <= size {
		return nil, ext.Errorf(ext.PointKeyStore, ext.ProviderControl, "open",
			ext.KindInternal, "the stored key material is truncated")
	}
	out, err := s.aead.Open(nil, sealed[:size], sealed[size:], nil)
	if err != nil {
		// THE LIKELIEST CAUSE IS THE WRONG KEY-ENCRYPTION KEY, and
		// saying so is the difference between a ten-minute fix and an
		// afternoon. It is an outage, never a denial.
		return nil, ext.Errorf(ext.PointKeyStore, ext.ProviderControl, "open",
			ext.KindInternal,
			"the stored key material did not decrypt; the key-encryption key this process was given is not the one it was written with")
	}
	return out, nil
}

func generateKey(algo ext.KeyAlgorithm) (crypto.PrivateKey, crypto.PublicKey, error) {
	switch algo {
	case ext.KeyAlgorithmEd25519:
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, nil, keyGenFailure(err)
		}
		return priv, pub, nil
	case ext.KeyAlgorithmECDSAP256:
		priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, nil, keyGenFailure(err)
		}
		return priv, priv.Public(), nil
	case ext.KeyAlgorithmRSA4096:
		priv, err := rsa.GenerateKey(rand.Reader, 4096)
		if err != nil {
			return nil, nil, keyGenFailure(err)
		}
		return priv, priv.Public(), nil
	}
	return nil, nil, ext.Errorf(ext.PointKeyStore, ext.ProviderControl, "Generate",
		ext.KindInvalid, "algorithm %d is not one this build generates", int(algo))
}

func keyGenFailure(err error) error {
	return ext.Errorf(ext.PointKeyStore, ext.ProviderControl, "Generate",
		ext.KindInternal, "a key could not be generated: %v", err)
}

// parseAlgorithm maps a stored code back to the enum. An unknown code answers
// Ed25519 rather than failing, because the algorithm is a diagnostic here —
// what signs is the stored key, which carries its own type.
func parseAlgorithm(code string) ext.KeyAlgorithm {
	for _, a := range []ext.KeyAlgorithm{
		ext.KeyAlgorithmEd25519, ext.KeyAlgorithmECDSAP256, ext.KeyAlgorithmRSA4096,
	} {
		if a.String() == code {
			return a
		}
	}
	return ext.KeyAlgorithmEd25519
}

func checkRef(op string, ref ext.KeyRef) error {
	if ref.Tenant == "" || ref.Name == "" {
		return ext.Errorf(ext.PointKeyStore, ext.ProviderControl, op,
			ext.KindInvalid, "a key ref needs a tenant and a name")
	}
	return nil
}

// keyStoreError translates a store failure into the ext error vocabulary,
// keeping absence distinct from failure (M11).
func keyStoreError(op string, err error) error {
	if store.IsNotFound(err) {
		return ext.Errorf(ext.PointKeyStore, ext.ProviderControl, op,
			ext.KindNotFound, "no such key")
	}
	if store.IsConflict(err) {
		return ext.Errorf(ext.PointKeyStore, ext.ProviderControl, op,
			ext.KindConflict, "that key already exists")
	}
	return ext.Errorf(ext.PointKeyStore, ext.ProviderControl, op,
		ext.KindInternal, "the key store could not be read: %v", err)
}
