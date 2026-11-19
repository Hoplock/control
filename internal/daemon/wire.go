// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"crypto/tls"
	"net"
	"net/http"

	"github.com/hoplock/control/internal/config"
	"github.com/hoplock/control/internal/tlsconf"
)

// listenerWire is each listener's own TLS: nil where its section has no `tls:`
// block and the listener serves plaintext. The two are built by one
// constructor and share nothing it returns (M2, M29).
type listenerWire struct {
	south, north *tls.Config
}

// buildWire builds both listeners' TLS from their own sections. An error is a
// block that cannot be honoured, and refuses the start.
func buildWire(cfg *config.Config) (listenerWire, error) {
	south, err := tlsconf.Build("south", cfg.South.TLS)
	if err != nil {
		return listenerWire{}, err
	}
	north, err := tlsconf.Build("north", cfg.North.TLS)
	if err != nil {
		return listenerWire{}, err
	}
	return listenerWire{south: south, north: north}, nil
}

// serveListener serves srv on ln: TLS when srv carries a TLS configuration,
// plaintext when it does not. The certificate is already in the
// configuration, so no file is named here — tlsconf read them at start-up.
func serveListener(srv *http.Server, ln net.Listener) error {
	if srv.TLSConfig != nil {
		return srv.ServeTLS(ln, "", "")
	}
	return srv.Serve(ln)
}
