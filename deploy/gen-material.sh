#!/bin/sh
# Generates every key the end-to-end topology uses into deploy/keys/ — the TLS
# certificate this server's listeners serve included — and renders
# deploy/control/seed.yaml from seed.template.yaml (deploy/README.md).
#
# Nothing here is committed: the seed names keys by their public blobs, so it
# cannot be written before the keys exist, and material inside an image or a
# commit is material shared by everyone who has either. Everything is mounted at
# run time, so regenerating never needs a rebuild.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
keys="$here/keys"

rm -rf "$keys"
mkdir -p "$keys/brokered"

gen() {
	ssh-keygen -q -t ed25519 -N "" -C "hoplock-e2e-$1" -f "$keys/$1"
}

# People. Each is a subject in the seed with this key as their only credential.
#   alice  sre: the routes most scenarios use
#   bob    no group: the deny scenario's user, and JIT's requester
#   carol  sre: the lock scenario's subject, so a lock never strands alice
#   svc    automation: restricted exec
for who in alice bob carol svc; do gen "user_$who"; done

# The proxies: an SSH host key each, and a chain identity each (PLAN §4). The
# chain identity is what the inner proxy authenticates with at /v1/auth/cert as
# a chain leg, so the seed registers both fingerprints.
for p in edge relay; do
	gen "hostkey_proxy_$p"
	gen "chain_proxy_$p"
done

# The target credentials. management_key reaches root on the targets for
# ephemeral-user (D6); brokered_key is the standing netadmin account's
# authorised key (D6a), handed to the proxies as appliance-fleet.key.
gen management_key
gen brokered_key
cp "$keys/brokered_key" "$keys/brokered/appliance-fleet.key"
chmod 600 "$keys/brokered/appliance-fleet.key"

# The relay registration's authorisation on the edge proxy (D11): the protected
# zone's proxy opens the registration with its chain identity, and the comment
# is the proxy id the edge proxy files it under.
sed 's/ hoplock-e2e-chain_proxy_relay$/ proxy-relay/' \
	"$keys/chain_proxy_relay.pub" > "$keys/relay_authorized_keys"

# This server's own TLS on both listeners (phase 0063, PLAN M29): a throwaway CA
# for the run, and one certificate for `control` — the name the proxies dial —
# and for 127.0.0.1, where the suite reaches the north-bound listener. The
# proxies trust the CA through SSL_CERT_FILE (deploy/compose.yaml), which the Go
# standard library reads on Linux, so nothing on the proxy is configured for it.
# The CA's private half is deleted as soon as the certificate is signed: nothing
# in the run signs anything else with it.
tls=$(mktemp -d)
trap 'rm -rf "$tls"' EXIT
cat > "$tls/openssl.cnf" <<'CNF'
[req]
distinguished_name = dn
prompt = no
[dn]
CN = hoplock-e2e TLS CA
[ca]
basicConstraints = critical,CA:TRUE
keyUsage = critical,keyCertSign,cRLSign
subjectKeyIdentifier = hash
[leaf]
basicConstraints = critical,CA:FALSE
keyUsage = critical,digitalSignature
extendedKeyUsage = serverAuth
subjectAltName = DNS:control,DNS:localhost,IP:127.0.0.1
authorityKeyIdentifier = keyid
CNF
openssl req -x509 -config "$tls/openssl.cnf" -extensions ca -days 7 -nodes \
	-newkey ec -pkeyopt ec_paramgen_curve:P-256 \
	-keyout "$tls/ca.key" -out "$keys/tls_ca.pem" 2>/dev/null
openssl req -new -config "$tls/openssl.cnf" -subj "/CN=control" -nodes \
	-newkey ec -pkeyopt ec_paramgen_curve:P-256 \
	-keyout "$keys/tls_control.key" -out "$tls/control.csr" 2>/dev/null
openssl x509 -req -in "$tls/control.csr" -CA "$keys/tls_ca.pem" -CAkey "$tls/ca.key" \
	-set_serial 2 -days 7 -extfile "$tls/openssl.cnf" -extensions leaf \
	-out "$keys/tls_control.pem" 2>/dev/null
chmod 600 "$keys/tls_control.key"

# The private halves stay 0600, which is what ssh insists on; every container
# reads them as root.
chmod 644 "$keys"/*.pub "$keys/relay_authorized_keys" "$keys/tls_ca.pem" "$keys/tls_control.pem"

# The key the tenant CA's private half is encrypted under at rest
# (credential.key_encryption_key_env). Compose reads deploy/.env, so the server
# gets it in its environment and it is never in a file the image carries.
printf 'HOPLOCK_KEK=%s\n' "$(head -c 32 /dev/urandom | base64)" > "$here/.env"

blob() { awk '{print $2}' "$keys/$1.pub"; }

sed \
	-e "s|@@KEY_ALICE@@|$(blob user_alice)|" \
	-e "s|@@KEY_BOB@@|$(blob user_bob)|" \
	-e "s|@@KEY_CAROL@@|$(blob user_carol)|" \
	-e "s|@@KEY_SVC@@|$(blob user_svc)|" \
	-e "s|@@CHAIN_EDGE@@|$(blob chain_proxy_edge)|" \
	-e "s|@@CHAIN_RELAY@@|$(blob chain_proxy_relay)|" \
	"$here/control/seed.template.yaml" > "$here/control/seed.yaml"

echo "e2e material generated in $keys"
