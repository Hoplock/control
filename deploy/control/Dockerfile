# This server in the end-to-end topology: the e2e host binary deploy/build.sh
# builds (test/e2e/host — hoplock-control exactly, started as a host starts it).
# BASE_IMAGE is a build argument for one reason: a builder behind an egress
# proxy can point apt at it with a base of its own. Every image here is built and
# tested on the default.
ARG BASE_IMAGE=debian:stable-slim
FROM ${BASE_IMAGE}
COPY bin/hoplock-control /usr/local/bin/hoplock-control
ENTRYPOINT ["/usr/local/bin/hoplock-control"]
