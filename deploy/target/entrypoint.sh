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

exec /usr/sbin/sshd -D -e
