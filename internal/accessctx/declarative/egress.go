// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package declarative

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// EGRESS. A declarative provider is a URL an administrator wrote, filled from a
// request somebody else made — which is an SSRF primitive with an administrator
// holding the pen. Three things keep it pointed where it was pointed:
//
//  1. The host is fixed by the configuration. A template fills a path or a
//     query, never a scheme, a host or a port (checked at start-up).
//  2. Every connection is checked AT DIAL TIME against the address actually
//     dialled, not the name in the URL, so DNS that answers differently
//     tomorrow — or differently for the second lookup — reaches nothing new.
//     Loopback, private, link-local (the cloud metadata service), CGNAT,
//     benchmark, reserved, multicast and unspecified addresses are refused
//     unless `access_context.egress.allow_cidrs` admits them.
//  3. Redirects are not followed, and no environment proxy is used: either
//     would move the request somewhere the dial-time check never saw.

// blocked are the prefixes refused unless the allow-list names them, beyond
// what netip's own predicates already classify.
var blocked = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	// NAT64: a v6 address that is a private v4 address in disguise.
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
}

type egress struct {
	allow []netip.Prefix
}

// permitted reports whether a connection to ip may be made.
func (e egress) permitted(ip netip.Addr) bool {
	ip = ip.Unmap()
	for _, p := range e.allow {
		if p.Contains(ip) {
			return true
		}
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, p := range blocked {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// errEgressRefused is a connection the allow-list does not admit.
type errEgressRefused struct{ addr string }

func (e *errEgressRefused) Error() string {
	return fmt.Sprintf("egress to %s refused: the address is not public and access_context.egress.allow_cidrs does not admit it", e.addr)
}

// client is the HTTP client every declarative provider uses.
func (e egress) client() *http.Client {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return &errEgressRefused{addr: address}
			}
			ip, err := netip.ParseAddr(host)
			if err != nil || !e.permitted(ip) {
				return &errEgressRefused{addr: host}
			}
			return nil
		},
	}
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          32,
			MaxIdleConnsPerHost:   8,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ExpectContinueTimeout: time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
