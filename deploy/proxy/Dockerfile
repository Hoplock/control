# The real Hoplock Proxy, at the commit deploy/PROXY_REVISION pins; the binary
# is built on the host by deploy/build.sh.
# BASE_IMAGE is a build argument for one reason: a builder behind an egress
# proxy can point apt at it with a base of its own. Every image here is built and
# tested on the default.
ARG BASE_IMAGE=debian:stable-slim
FROM ${BASE_IMAGE}
RUN mkdir -p /var/lib/hoplock/logbuf
COPY bin/hoplock-proxy /usr/local/bin/hoplock-proxy
ENTRYPOINT ["/usr/local/bin/hoplock-proxy"]
