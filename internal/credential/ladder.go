// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package credential

import (
	"fmt"
	"strconv"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/policy/model"
)

// THE ONE RENDERER OF A LADDER ENTRY.
//
// Every `target_auth_ladder` entry this server sends is rendered here, from the
// policy model, and the decision path is what calls it. There used to be two:
// this package carried a second one for `brokered-certificate` that put the
// certificate, its serial and the CA bundle on the entry, which is the shape
// 0011 asked upstream for and upstream did not take (Hoplock/proxy#68).
//
// WHY THE ENTRY CARRIES POLICY AND NOTHING ELSE. The entry rides the authorize
// decision, which is cacheable and served to every connection it covers: a
// certificate on it would be replayed past its own expiry. It also cannot exist
// yet at that point, because it is signed over a key the proxy generates after
// the route is decided. So a `brokered-certificate` entry names `username`, and
// at most `key_type` and `lifetime_seconds`; the artifacts come back from
// POST /v1/credentials/certificate, once per session (proxy PLAN §5.4). A proxy
// that implements the method refuses a parameter it does not know before it
// generates anything, so an entry carrying `certificate`, `certificate_serial`
// or `ca_public_keys` would fail every session closed — and
// TestABrokeredCertificateEntryCarriesPolicyOnly holds that it never does.

// LadderEntry renders one authored ladder entry as the contract's TargetAuth.
//
// It refuses an entry with no username rather than sending one: `username` is
// required on every method the contract defines and is never defaulted to the
// identity's login, a client-typed string the proxy must not base an
// authorization decision on. The compiler refuses such an entry first; this is
// the decision path's own check, and its error is an outage (M11), never a deny.
func LadderEntry(e model.CredentialEntry) (contract.TargetAuth, error) {
	username := e.Username.Resolve("")
	if username == "" {
		return contract.TargetAuth{}, fmt.Errorf(
			"credential: a %s ladder entry must name the account to log in as", e.Method)
	}
	params := make(map[string]string, 4+len(e.DeviceFields))
	// Required on EVERY method. The engine resolved it against the subject;
	// what travels is a concrete account name, because that is what the
	// target's own audit trail and file ownership will be made of.
	params[contract.ParamUsername] = username
	if e.KeyType != "" {
		params[contract.ParamKeyType] = e.KeyType
	}
	if e.CredentialRef != "" {
		params[contract.ParamCredentialRef] = e.CredentialRef
	}
	if e.Platform != "" {
		params[contract.ParamPlatform] = e.Platform
	}
	if e.CredentialKind != "" {
		params[contract.ParamCredentialKind] = string(e.CredentialKind)
	}
	if e.ExpiryPosture != "" {
		params[contract.ParamExpiryPosture] = string(e.ExpiryPosture)
	}
	if e.LifetimeSeconds != 0 {
		params[contract.ParamLifetimeSeconds] = strconv.Itoa(int(e.LifetimeSeconds))
	}
	// The open namespace, carried through as DATA this server does not
	// interpret. `device_field.vdom` on a FortiGate scopes the provisioned
	// administrator to one virtual domain, and its ABSENCE makes that
	// administrator global — the strongest account the device has — so a
	// field is never dropped on the way through and never invented here.
	for name, value := range e.DeviceFields {
		params[contract.DeviceFieldPrefix+name] = value
	}
	return contract.TargetAuth{Method: contract.TargetAuthMethod(e.Method), Params: params}, nil
}
