# The client node: a real OpenSSH client and nothing else. Every scenario's SSH
# invocation runs here, on a container attached only to the `edge` network, so
# "the target is reachable only through a proxy" is a property of the run.
# A real client rather than a Go library, because what the acceptance criteria
# are about is what a person at a terminal is told.
# BASE_IMAGE is a build argument for one reason: a builder behind an egress
# proxy can point apt at it with a base of its own. Every image here is built and
# tested on the default.
ARG BASE_IMAGE=debian:stable-slim
FROM ${BASE_IMAGE}
RUN apt-get update \
 && apt-get install -y --no-install-recommends openssh-client netcat-openbsd \
 && rm -rf /var/lib/apt/lists/*
ENTRYPOINT []
