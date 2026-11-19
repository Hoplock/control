# A target: a real sshd and a real account database, with what each credential
# method needs from a target and nothing more.
#
#   ephemeral-user (D6)       root, reachable with the management key — the
#                             privileged provisioning account
#   brokered-key (D6a)        netadmin, which ALREADY EXISTS and has no
#                             provisioning rights: the appliance stand-in. A
#                             scenario that finds it modified has found a bug.
#   brokered-certificate      causer, which trusts this server's tenant CA through
#   (Hoplock/proxy#68)        TrustedUserCAKeys and has no authorized_keys at all
#
# debian:stable-slim's OpenSSH is 9.9 or later, so it offers
# mlkem768x25519-sha256 and a modern host key: the default algorithm profile is
# the secure set (Hoplock/proxy#66), and a target speaking less would fail every
# scenario here as an outage that has nothing to do with what it tests. It is
# also what the algorithm-floor scenario's met floor rests on, and the version
# that scenario records (`ssh -V` in this image) is the evidence it names.
# BASE_IMAGE is a build argument for one reason: a builder behind an egress
# proxy can point apt at it with a base of its own. Every image here is built and
# tested on the default.
ARG BASE_IMAGE=debian:stable-slim
FROM ${BASE_IMAGE}
RUN apt-get update \
 && apt-get install -y --no-install-recommends openssh-server procps \
 && rm -rf /var/lib/apt/lists/*
RUN mkdir -p /run/sshd /root/.ssh /etc/ssh/sshd_config.d && chmod 700 /root/.ssh \
 && useradd -m -s /bin/sh netadmin \
 && mkdir -p /home/netadmin/.ssh && chmod 700 /home/netadmin/.ssh \
 && chown -R netadmin:netadmin /home/netadmin \
 && useradd -m -s /bin/sh causer \
 && sed -i 's/^#\?PermitRootLogin.*/PermitRootLogin prohibit-password/' /etc/ssh/sshd_config \
 && (grep -q '^Include /etc/ssh/sshd_config.d/\*.conf' /etc/ssh/sshd_config \
     || sed -i '1i Include /etc/ssh/sshd_config.d/*.conf' /etc/ssh/sshd_config) \
 && sed -i '/pam_motd/d' /etc/pam.d/sshd
# pam_motd is removed because on some bases it writes into the home of whoever
# logs in, and "the standing accounts are byte-identical afterwards" is a claim
# about the proxy, not about the message of the day.
COPY target/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod 755 /usr/local/bin/entrypoint.sh
EXPOSE 22 2222 2223
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
