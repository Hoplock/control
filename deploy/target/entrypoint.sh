#!/bin/sh
# Installs the run's key material and starts sshd (deploy/target/Dockerfile).
#
# Everything is mounted at run time — /material from deploy/keys, /ca from the
# bundle `hoplock-control ca show` printed at start-up (deploy/compose.yaml) —
# so regenerating keys never needs an image rebuild.
set -eu

install_key() {
	src=$1 dest=$2 owner=$3
	cat "$src" > "$dest"
	chmod 600 "$dest"
	chown "$owner" "$dest" "$(dirname "$dest")"
}

install_key /material/management_key.pub /root/.ssh/authorized_keys root:root
install_key /material/brokered_key.pub /home/netadmin/.ssh/authorized_keys netadmin:netadmin

# The tenant CA's trust bundle: every key `ca show` prints, the retired ones
# included, which is what that command says to publish.
grep '^ssh-' /ca/trusted_user_ca_keys > /etc/ssh/hoplock_user_ca.pub
chmod 644 /etc/ssh/hoplock_user_ca.pub

ssh-keygen -A >/dev/null

# The four directives below are the proxy topology's, for its reasons
# (Hoplock Proxy deploy/README.md, "Four things the target image must not
# change"): a decrypting proxy is ONE source address to every target it fronts,
# so sshd's per-source penalties would score every failure against it. Each is
# applied only if this sshd understands it.
conf=/etc/ssh/sshd_config.d/hoplock.conf
printf '%s\n' "TrustedUserCAKeys /etc/ssh/hoplock_user_ca.pub" > "$conf"
for directive in \
	"PerSourcePenalties no" \
	"PerSourceMaxStartups none" \
	"MaxStartups 200:30:400" \
	"MaxSessions 100"
do
	printf '%s\n' "$directive" >> "$conf"
	if ! /usr/sbin/sshd -t >/dev/null 2>&1; then
		echo "target: this sshd does not understand \"$directive\"; skipping it" >&2
		sed -i '$d' "$conf"
	fi
done

# Two more sshds on the same host, with the same accounts, keys and settings —
# only the key exchange differs — for the algorithm-floor scenario (phase 0063;
# Hoplock Proxy deploy/target/entrypoint.sh does the same on 2222). A route
# whose floor is pq-hybrid-kex that fails against either can only have failed
# on the floor, and the no-floor routes to the same two ports prove it.
#
#   2222  classical key exchanges only: no hybrid of any kind.
#   2223  sntrup761x25519-sha512@openssh.com as its ONLY hybrid, beside the same
#         classical list — what OpenSSH 9.0 to 9.8 offer by default. The proxy's
#         SSH library implements no sntrup761, so this sshd does not meet
#         pq-hybrid-kex either, and the scenario asserts that it does not.
#
# Each list is written out rather than derived, and started before the main
# sshd and fatal if it will not start (`set -e`): a missing listener would fail
# a floor scenario as "the target could not be reached", which reads like a
# network fault.
classical="curve25519-sha256,curve25519-sha256@libssh.org,ecdh-sha2-nistp256,ecdh-sha2-nistp384,ecdh-sha2-nistp521,diffie-hellman-group16-sha512,diffie-hellman-group14-sha256"
/usr/sbin/sshd -p 2222 -o "KexAlgorithms=$classical" \
	-o PidFile=/run/sshd-classical.pid -E /var/log/sshd-classical.log
/usr/sbin/sshd -p 2223 -o "KexAlgorithms=sntrup761x25519-sha512@openssh.com,$classical" \
	-o PidFile=/run/sshd-sntrup.pid -E /var/log/sshd-sntrup.log

exec /usr/sbin/sshd -D -e
