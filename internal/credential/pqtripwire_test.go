// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package credential_test

import (
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// classicalSignatures is every host-key and public-key algorithm name
// golang.org/x/crypto/ssh offered when this test was written (0063), with the
// certificate names among them: none is post-quantum. A name x/crypto/ssh adds
// later is not in it, and the tripwire below fails until somebody classifies it.
var classicalSignatures = map[string]bool{
	ssh.KeyAlgoED25519: true, ssh.KeyAlgoSKED25519: true,
	ssh.KeyAlgoECDSA256: true, ssh.KeyAlgoECDSA384: true, ssh.KeyAlgoECDSA521: true, ssh.KeyAlgoSKECDSA256: true,
	ssh.KeyAlgoRSA: true, ssh.KeyAlgoRSASHA256: true, ssh.KeyAlgoRSASHA512: true,
	// DSA, by name: x/crypto/ssh still offers it among its insecure
	// algorithms, and its constants are deprecated.
	"ssh-dss": true,

	ssh.CertAlgoED25519v01: true, ssh.CertAlgoSKED25519v01: true,
	ssh.CertAlgoECDSA256v01: true, ssh.CertAlgoECDSA384v01: true, ssh.CertAlgoECDSA521v01: true, ssh.CertAlgoSKECDSA256v01: true,
	ssh.CertAlgoRSAv01: true, ssh.CertAlgoRSASHA256v01: true, ssh.CertAlgoRSASHA512v01: true,
	"ssh-dss-cert-v01@openssh.com": true,
}

// postQuantumMarkers are fragments a post-quantum signature's SSH name is
// likely to carry. They are a hint for the failure message, not the guard: the
// guard is that every name is classified, so a post-quantum name that matches
// none of these still fails the build.
var postQuantumMarkers = []string{"mldsa", "ml-dsa", "slhdsa", "slh-dsa", "sphincs", "falcon", "fn-dsa", "dilithium", "lms", "xmss"}

// THE TRIPWIRE FOR THE SSH CERTIFICATE GAP (0063), in the shape of 0011's for
// brokered certificates: a test that fails the build on the day an upstream
// fact changes, and says what to do.
//
// The fact: OpenSSH defines no post-quantum signature for certificates, and
// x/crypto/ssh implements none, so ext.KeyAlgorithm has no post-quantum member
// — the CA could generate such a key and never sign a certificate with it.
// Target certificates live for minutes (0011), so a signature is worth forging
// only for minutes; the post-quantum work with a deadline is key exchange,
// which the proxy→target floor (`pq-hybrid-kex`) and this server's own
// listeners (internal/tlsconf) already carry.
//
// When this fails because x/crypto/ssh gained a post-quantum signature:
//  1. add the member to ext.KeyAlgorithm (ext/identity.go) and its String code
//     — a MINOR, and a `### Breaking` entry, because Enterprise's key stores
//     implement Generate over that enum (M15, M23);
//  2. add its branch to the software custodian's generateKey
//     (internal/extdefault/keystore.go) and make sure the CA signs with it
//     (internal/credential/ca.go): exhaustive names every switch that forgot;
//  3. check that OpenSSH on a target accepts the certificate type — the proxy
//     presents what this CA signs, so a type sshd refuses is an outage on
//     every brokered-certificate route;
//  4. then change this test to pin the new name rather than deleting it.
//
// When it fails because of a new classical name, add it to
// classicalSignatures.
func TestNoPostQuantumSSHSignatureExistsYet(t *testing.T) {
	var offered []string
	for _, set := range []ssh.Algorithms{ssh.SupportedAlgorithms(), ssh.InsecureAlgorithms()} {
		offered = append(offered, set.HostKeys...)
		offered = append(offered, set.PublicKeyAuths...)
	}
	if len(offered) == 0 {
		t.Fatal("x/crypto/ssh reports no signature algorithms; this tripwire is reading the wrong list")
	}
	for _, name := range offered {
		lower := strings.ToLower(name)
		for _, marker := range postQuantumMarkers {
			if strings.Contains(lower, marker) {
				t.Errorf("x/crypto/ssh now offers %q, which looks like a POST-QUANTUM SIGNATURE. The CA's algorithm "+
					"vocabulary can now take a post-quantum member: follow the steps on this test "+
					"(ext.KeyAlgorithm, the software custodian, the CA, a target's sshd), then pin the name here.", name)
			}
		}
		if !classicalSignatures[name] {
			t.Errorf("x/crypto/ssh now offers %q, which this tripwire has not classified. If it is a post-quantum "+
				"signature, follow the steps on this test; if it is classical, add it to classicalSignatures.", name)
		}
	}
}
