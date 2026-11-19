// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

// The archive and the key store the audit view's retention is checked against
// (0057), registered in the host's registry as a host registers its own (M15):
//
//   - `e2e-archive`, an archive that is behind and stays behind: every batch
//     it is handed is held until Control's deadline for the attempt ends,
//     which the fixture's configuration sets past the length of a run
//     (`audit.archive.attempt_timeout`). So it confirms nothing, and nothing
//     it holds is failing — the archive is registered and behind, and
//     retention waits on it (M8): a pass by sequence is refused with
//     `retention_not_archived`, and a pass by instant deletes as far as the
//     archive holds, which is nothing;
//   - `e2e-keys`, a key store in memory: retention is signed, and is refused
//     without one (0024). Control's CA keeps its keys in it too (0062); no
//     screen reads the CA, and a key made in a run before this process is
//     one it does not hold, which start-up logs and issuance refuses, so it
//     changes nothing else the console shows.
//
// Neither binds a route.

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"sync"
	"time"

	"github.com/hoplock/control/ext"
)

const (
	archiveProvider  = "e2e-archive"
	keyStoreProvider = "e2e-keys"
)

// holdingArchive holds every batch until the attempt's deadline, and then
// confirms nothing.
type holdingArchive struct{}

func (holdingArchive) Archive(ctx context.Context, _ []ext.AuditRecord) error {
	<-ctx.Done()
	return ext.Errorf(ext.PointArchiveStore, archiveProvider, "Archive", ext.KindUnavailable,
		"the fixture's archive holds every batch: %v", ctx.Err())
}

func (holdingArchive) ArchiveCaptures(ctx context.Context, _ []ext.Capture) error {
	<-ctx.Done()
	return ext.Errorf(ext.PointArchiveStore, archiveProvider, "ArchiveCaptures", ext.KindUnavailable,
		"the fixture's archive holds every batch: %v", ctx.Err())
}

func (holdingArchive) Search(context.Context, ext.ArchiveQuery) (ext.ArchivePage, error) {
	return ext.ArchivePage{}, nil
}

// memoryKeys keeps Ed25519 keys in memory for the life of the fixture's
// server, which is one run.
type memoryKeys struct {
	mu   sync.Mutex
	keys map[ext.KeyRef]memoryKey
}

type memoryKey struct {
	info ext.KeyInfo
	priv ed25519.PrivateKey
}

func newMemoryKeys() *memoryKeys { return &memoryKeys{keys: map[ext.KeyRef]memoryKey{}} }

func (m *memoryKeys) Generate(_ context.Context, ref ext.KeyRef, algo ext.KeyAlgorithm) (ext.KeyInfo, error) {
	if algo != ext.KeyAlgorithmEd25519 {
		return ext.KeyInfo{}, ext.Errorf(ext.PointKeyStore, keyStoreProvider, "Generate", ext.KindInvalid,
			"the fixture's key store makes Ed25519 keys only")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.keys[ref]; ok {
		return ext.KeyInfo{}, ext.Errorf(ext.PointKeyStore, keyStoreProvider, "Generate", ext.KindConflict, "key %q exists", ref.Name)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return ext.KeyInfo{}, err
	}
	info := ext.KeyInfo{Ref: ref, Algorithm: algo, Public: pub, CreatedAt: time.Now().UTC(), Custodian: "memory"}
	m.keys[ref] = memoryKey{info: info, priv: priv}
	return info, nil
}

func (m *memoryKeys) find(op string, ref ext.KeyRef) (memoryKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[ref]
	if !ok {
		return memoryKey{}, ext.Errorf(ext.PointKeyStore, keyStoreProvider, op, ext.KindNotFound, "no key %q", ref.Name)
	}
	return k, nil
}

func (m *memoryKeys) Describe(_ context.Context, ref ext.KeyRef) (ext.KeyInfo, error) {
	k, err := m.find("Describe", ref)
	return k.info, err
}

func (m *memoryKeys) Signer(_ context.Context, ref ext.KeyRef) (crypto.Signer, error) {
	k, err := m.find("Signer", ref)
	if err != nil {
		return nil, err
	}
	return k.priv, nil
}

func (m *memoryKeys) List(_ context.Context, tenant ext.Tenant) ([]ext.KeyInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ext.KeyInfo
	for ref, k := range m.keys {
		if ref.Tenant == tenant {
			out = append(out, k.info)
		}
	}
	return out, nil
}

func (m *memoryKeys) Destroy(_ context.Context, ref ext.KeyRef) error {
	if _, err := m.find("Destroy", ref); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.keys, ref)
	return nil
}
