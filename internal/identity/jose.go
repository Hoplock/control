// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// Just enough JOSE to verify an OIDC ID token, and no more.
//
// It is hand-written for the reason `internal/contract`'s types are: what this
// server needs is a narrow, auditable subset — verify a compact JWS against a
// published JWKS — and a general JOSE library brings JWE, `none`, key
// wrapping, and a decade of algorithm confusion CVEs with it. The rules below
// are what those CVEs were about, so they are stated rather than configured:
//
//   - THE ALGORITHM COMES FROM THE KEY, NOT FROM THE TOKEN. The header's `alg`
//     selects among the algorithms the RESOLVED KEY can do; it can never turn
//     an RSA public key into an HMAC secret, which is the classic
//     "alg: HS256 signed with the RSA public key" forgery.
//   - `none` IS NOT AN ALGORITHM. There is no branch for it.
//   - SYMMETRIC ALGORITHMS ARE NOT SUPPORTED AT ALL. A JWKS publishes public
//     keys; an `oct` entry in one is either a misconfiguration or an attack,
//     and either way it is not something to verify against.

// Supported signature algorithms, as a closed set.
const (
	algRS256 = "RS256"
	algRS384 = "RS384"
	algRS512 = "RS512"
	algPS256 = "PS256"
	algPS384 = "PS384"
	algPS512 = "PS512"
	algES256 = "ES256"
	algES384 = "ES384"
	algES512 = "ES512"
	algEdDSA = "EdDSA"
)

// ErrNoVerifyingKey is returned when no key in the set matches the token's
// `kid` and algorithm. It is separated so a caller can refresh a cached JWKS
// once and retry — a signing key rotating is normal, not an attack.
var ErrNoVerifyingKey = errors.New("identity: no key in the set can verify this token")

// jwsHeader is the protected header, with only the fields that are read.
type jwsHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

// jwk is one key from a JWKS.
type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	Crv string `json:"crv"`
	N   string `json:"n"`
	E   string `json:"e"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// jwkSet is a JWKS document.
type jwkSet struct {
	Keys []jwk `json:"keys"`
}

// publicKey turns a JWK into a crypto public key, refusing anything symmetric.
func (k jwk) publicKey() (crypto.PublicKey, error) {
	switch k.Kty {
	case "RSA":
		n, err := b64uint(k.N)
		if err != nil {
			return nil, err
		}
		e, err := b64uint(k.E)
		if err != nil {
			return nil, err
		}
		if !e.IsInt64() || e.Int64() <= 0 || e.Int64() > 1<<31 {
			return nil, fmt.Errorf("identity: the JWKS carries an RSA key with an unusable exponent")
		}
		if n.BitLen() < 2048 {
			// An RSA key shorter than 2048 bits is not a key this
			// server will accept a signature from, whatever the IdP
			// thinks. It is refused here rather than at review time.
			return nil, fmt.Errorf("identity: the JWKS carries an RSA key shorter than 2048 bits")
		}
		return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
	case "EC":
		curve, err := curveFor(k.Crv)
		if err != nil {
			return nil, err
		}
		x, err := b64uint(k.X)
		if err != nil {
			return nil, err
		}
		y, err := b64uint(k.Y)
		if err != nil {
			return nil, err
		}
		if !curve.IsOnCurve(x, y) {
			return nil, fmt.Errorf("identity: the JWKS carries an EC key whose point is not on its curve")
		}
		return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
	case "OKP":
		if k.Crv != "Ed25519" {
			return nil, fmt.Errorf("identity: the JWKS carries an OKP key on an unsupported curve")
		}
		raw, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("identity: the JWKS carries a malformed Ed25519 key")
		}
		return ed25519.PublicKey(raw), nil
	default:
		// `oct` lands here, on purpose: see the note at the top.
		return nil, fmt.Errorf("identity: the JWKS carries a key type this server does not verify with")
	}
}

func curveFor(name string) (elliptic.Curve, error) {
	switch name {
	case "P-256":
		return elliptic.P256(), nil
	case "P-384":
		return elliptic.P384(), nil
	case "P-521":
		return elliptic.P521(), nil
	}
	return nil, fmt.Errorf("identity: the JWKS carries an EC key on an unsupported curve")
}

func b64uint(s string) (*big.Int, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(raw) == 0 {
		return nil, fmt.Errorf("identity: the JWKS carries a malformed big-endian integer")
	}
	return new(big.Int).SetBytes(raw), nil
}

// verifyJWS checks a compact JWS against a key set and returns the payload.
//
// It returns the RAW payload rather than decoded claims, so that the claim
// validation above it reads the same bytes the signature covered.
func verifyJWS(token string, set jwkSet) ([]byte, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("identity: the token is not a compact JWS")
	}
	headerRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("identity: the token header is not base64url")
	}
	var header jwsHeader
	if err := json.Unmarshal(headerRaw, &header); err != nil {
		return nil, fmt.Errorf("identity: the token header is not JSON")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("identity: the token payload is not base64url")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("identity: the token signature is not base64url")
	}
	signed := []byte(parts[0] + "." + parts[1])

	for _, k := range set.Keys {
		if header.Kid != "" && k.Kid != "" && header.Kid != k.Kid {
			continue
		}
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		pub, err := k.publicKey()
		if err != nil {
			continue
		}
		if verifySignature(pub, header.Alg, signed, signature) {
			return payload, nil
		}
	}
	return nil, ErrNoVerifyingKey
}

// verifySignature checks one signature against one key.
//
// The type switch is what enforces "the algorithm comes from the key": an
// `alg` naming an algorithm the key cannot do falls through to false rather
// than selecting a different primitive.
func verifySignature(pub crypto.PublicKey, alg string, signed, signature []byte) bool {
	switch key := pub.(type) {
	case *rsa.PublicKey:
		hash, digest, ok := digestFor(alg, signed, "RS", "PS")
		if !ok {
			return false
		}
		if strings.HasPrefix(alg, "PS") {
			return rsa.VerifyPSS(key, hash, digest, signature, &rsa.PSSOptions{
				SaltLength: rsa.PSSSaltLengthEqualsHash,
				Hash:       hash,
			}) == nil
		}
		return rsa.VerifyPKCS1v15(key, hash, digest, signature) == nil
	case *ecdsa.PublicKey:
		_, digest, ok := digestFor(alg, signed, "ES")
		if !ok {
			return false
		}
		// JWS ECDSA signatures are the fixed-width r||s form, never the
		// ASN.1 one, so they are split here rather than handed to
		// ecdsa.VerifyASN1.
		size := (key.Curve.Params().BitSize + 7) / 8
		if len(signature) != 2*size {
			return false
		}
		r := new(big.Int).SetBytes(signature[:size])
		s := new(big.Int).SetBytes(signature[size:])
		return ecdsa.Verify(key, digest, r, s)
	case ed25519.PublicKey:
		if alg != algEdDSA {
			return false
		}
		return ed25519.Verify(key, signed, signature)
	default:
		return false
	}
}

// digestFor maps an algorithm onto its hash, refusing one outside the given
// families. The family check is the second half of "the algorithm comes from
// the key": an RSA key never reaches the EC branch's hashes and vice versa.
func digestFor(alg string, signed []byte, families ...string) (crypto.Hash, []byte, bool) {
	family := false
	for _, f := range families {
		if strings.HasPrefix(alg, f) {
			family = true
			break
		}
	}
	if !family {
		return 0, nil, false
	}
	switch alg {
	case algRS256, algPS256, algES256:
		sum := sha256.Sum256(signed)
		return crypto.SHA256, sum[:], true
	case algRS384, algPS384, algES384:
		sum := sha512.Sum384(signed)
		return crypto.SHA384, sum[:], true
	case algRS512, algPS512, algES512:
		sum := sha512.Sum512(signed)
		return crypto.SHA512, sum[:], true
	}
	return 0, nil, false
}

// claimSet is an ID token's payload, decoded twice: once into the fields this
// server validates, and once into a flat map the claim mapping reads.
type claimSet struct {
	Issuer    string      `json:"iss"`
	Subject   string      `json:"sub"`
	Audience  audienceSet `json:"aud"`
	Expiry    int64       `json:"exp"`
	IssuedAt  int64       `json:"iat"`
	NotBefore int64       `json:"nbf"`
	Nonce     string      `json:"nonce"`
	AuthTime  int64       `json:"auth_time"`
	AZP       string      `json:"azp"`
}

// audienceSet decodes `aud`, which is a string or an array of strings.
type audienceSet []string

func (a *audienceSet) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*a = audienceSet{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return fmt.Errorf("identity: `aud` is neither a string nor an array of strings")
	}
	*a = many
	return nil
}

// flattenClaims splits a decoded JSON object into single- and multi-valued
// claims, so that a mapping reading `groups` gets a list and one reading
// `email` gets a string.
//
// A nested object is NOT flattened into dotted names. A claim whose value is
// an object is one this server has no vocabulary for, and inventing a dotted
// namespace here would be inventing exactly the transformation language the
// mapping deliberately does not have.
func flattenClaims(raw map[string]json.RawMessage) (map[string]string, map[string][]string) {
	single := map[string]string{}
	multi := map[string][]string{}
	for name, value := range raw {
		var s string
		if err := json.Unmarshal(value, &s); err == nil {
			single[name] = s
			continue
		}
		var list []string
		if err := json.Unmarshal(value, &list); err == nil {
			multi[name] = list
			continue
		}
		var b bool
		if err := json.Unmarshal(value, &b); err == nil {
			single[name] = fmt.Sprintf("%t", b)
			continue
		}
		var n json.Number
		if err := json.Unmarshal(value, &n); err == nil {
			single[name] = n.String()
			continue
		}
	}
	return single, multi
}

// s256 is the PKCE code challenge transform, and the only one this server
// offers: `plain` is a challenge that is its own answer.
func s256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
