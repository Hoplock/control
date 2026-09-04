# `cmd/pdpconform`

The black-box contract conformance suite (PLAN M1). It drives *any*
implementation of the south-bound contract over HTTP and asserts the wire
behaviour the vendored `contract/` document specifies — it does not import this
server, and it must stay that way.

CI runs it against this server **and** against the Hoplock Proxy repository's
`cmd/mock-control`. Running it against the mock is what keeps it honest: a suite
only ever run against the implementation it was written beside tests agreement
with itself.

Built in **phase 0002**; `make conform` fails with that message until then.
