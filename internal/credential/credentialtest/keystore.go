// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package credentialtest is a key store for tests: what a host registers as its
// ext.KeyStore, held in memory, with the two ways a real one fails that the
// certificate authority must tell apart — a key it does not hold, and a store
// that cannot be asked (0062).
//
// It is test support, imported only by tests, as `storetest` is. Nothing that
// ships imports it.
package credentialtest

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"sync"
	"time"

	"github.com/hoplock/control/ext"
)

// Provider is the registration the memory store is registered under.
const Provider = "example/test-keys"

// KeyStore is an ext.KeyStore in memory, standing in for an HSM a host
// registers. It makes Ed25519 keys only.
type KeyStore struct {
	custodian string

	mu   sync.Mutex
	keys map[ext.KeyRef]key
	down bool
}

type key struct {
	info ext.KeyInfo
	priv ed25519.PrivateKey
}

// New returns an empty store whose keys say custodian is where they live.
func New(custodian string) *KeyStore {
	return &KeyStore{custodian: custodian, keys: map[ext.KeyRef]key{}}
}

// SetDown makes every call answer ext.KindUnavailable while down is true: the
// store exists and cannot be asked.
func (m *KeyStore) SetDown(down bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.down = down
}

// Put places a freshly made key under ref without the caller learning its
// private half: a store that holds SOME key under a name, which need not be the
// key anything else recorded there.
func (m *KeyStore) Put(ref ext.KeyRef) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.keys[ref] = key{info: ext.KeyInfo{
		Ref: ref, Algorithm: ext.KeyAlgorithmEd25519, Public: pub, CreatedAt: time.Now().UTC(), Custodian: m.custodian,
	}, priv: priv}
}

// Len is how many keys the store holds, across tenants.
func (m *KeyStore) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.keys)
}

// Refs is every ref the store holds for a tenant.
func (m *KeyStore) Refs(tenant ext.Tenant) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for ref := range m.keys {
		if ref.Tenant == tenant {
			out = append(out, ref.Name)
		}
	}
	return out
}

func (m *KeyStore) unavailable(op string) error {
	return ext.Errorf(ext.PointKeyStore, Provider, op, ext.KindUnavailable, "the test key store is down")
}

// Generate implements ext.KeyStore.
func (m *KeyStore) Generate(_ context.Context, ref ext.KeyRef, algo ext.KeyAlgorithm) (ext.KeyInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.down {
		return ext.KeyInfo{}, m.unavailable("Generate")
	}
	if algo != ext.KeyAlgorithmEd25519 {
		return ext.KeyInfo{}, ext.Errorf(ext.PointKeyStore, Provider, "Generate", ext.KindInvalid, "Ed25519 only")
	}
	if _, ok := m.keys[ref]; ok {
		return ext.KeyInfo{}, ext.Errorf(ext.PointKeyStore, Provider, "Generate", ext.KindConflict, "key %q exists", ref.Name)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return ext.KeyInfo{}, err
	}
	info := ext.KeyInfo{Ref: ref, Algorithm: algo, Public: pub, CreatedAt: time.Now().UTC(), Custodian: m.custodian}
	m.keys[ref] = key{info: info, priv: priv}
	return info, nil
}

func (m *KeyStore) find(op string, ref ext.KeyRef) (key, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.down {
		return key{}, m.unavailable(op)
	}
	k, ok := m.keys[ref]
	if !ok {
		return key{}, ext.Errorf(ext.PointKeyStore, Provider, op, ext.KindNotFound, "no key %q", ref.Name)
	}
	return k, nil
}

// Describe implements ext.KeyStore.
func (m *KeyStore) Describe(_ context.Context, ref ext.KeyRef) (ext.KeyInfo, error) {
	k, err := m.find("Describe", ref)
	return k.info, err
}

// Signer implements ext.KeyStore.
func (m *KeyStore) Signer(_ context.Context, ref ext.KeyRef) (crypto.Signer, error) {
	k, err := m.find("Signer", ref)
	if err != nil {
		return nil, err
	}
	return k.priv, nil
}

// List implements ext.KeyStore.
func (m *KeyStore) List(_ context.Context, tenant ext.Tenant) ([]ext.KeyInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.down {
		return nil, m.unavailable("List")
	}
	var out []ext.KeyInfo
	for ref, k := range m.keys {
		if ref.Tenant == tenant {
			out = append(out, k.info)
		}
	}
	return out, nil
}

// Destroy implements ext.KeyStore.
func (m *KeyStore) Destroy(_ context.Context, ref ext.KeyRef) error {
	if _, err := m.find("Destroy", ref); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.keys, ref)
	return nil
}

var _ ext.KeyStore = (*KeyStore)(nil)
