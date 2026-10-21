// Package outbound builds the HTTP clients this binary points at somebody
// else's server, and refuses every address that is not somebody else's.
//
// One thing reaches a studio today: the manifest fetcher reads a document a
// studio wrote, and dials it from inside our own network. The guard belongs
// to no one caller and is here, once, so the next thing that dials out gets
// it for free.
package outbound

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// maxHops caps the redirect chain. A server that has moved is ordinary; one
// that has moved four times is a loop or somebody walking us somewhere.
const maxHops = 3

// Client dials only public addresses unless allowPrivate says otherwise, which
// only a laptop wants: there the fixtures are another container and every
// address in the network is private.
func Client(timeout time.Duration, allowPrivate bool) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}

	// On the address rather than the URL: by now the name is resolved, so a
	// redirect and a name that resolves differently twice are both caught.
	if !allowPrivate {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("outbound: %s is not an address", host)
			}
			if !public(ip) {
				return fmt.Errorf("outbound: %s is not a public address", ip)
			}
			return nil
		}
	}

	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{DialContext: dialer.DialContext},
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= maxHops {
				return fmt.Errorf("outbound: more than %d redirects", maxHops)
			}
			return nil
		},
	}
}

func public(ip net.IP) bool {
	// An IPv4 address written the IPv6 way is the same address. Unwrapping it
	// first means ::ffff:169.254.169.254 cannot walk past the IPv4 checks.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}

	switch {
	case ip.IsLoopback(), ip.IsPrivate(), ip.IsUnspecified(),
		ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast(),
		ip.IsInterfaceLocalMulticast(), ip.IsMulticast():
		return false
	}

	// What the standard library has no name for: a cloud's unrouted range, and
	// reserved ranges no real publisher is on.
	for _, cidr := range reserved {
		if cidr.Contains(ip) {
			return false
		}
	}
	return true
}

var reserved = func() []*net.IPNet {
	blocks := []string{
		"100.64.0.0/10",   // carrier-grade NAT, and what some clouds route internally
		"192.0.0.0/24",    // IETF protocol assignments
		"192.0.2.0/24",    // documentation
		"198.18.0.0/15",   // benchmarking
		"198.51.100.0/24", // documentation
		"203.0.113.0/24",  // documentation
		"255.255.255.255/32",
		// Not ::ffff:0:0/96: against a 4-byte address it collapses to 0.0.0.0/0.
		// public() unwraps that form instead.
		"64:ff9b::/96",  // IPv4/IPv6 translation — an IPv4 address in disguise
		"2001:db8::/32", // documentation
		"fc00::/7",      // unique local, which IsPrivate covers, kept for the v6 reader
	}

	out := make([]*net.IPNet, 0, len(blocks))
	for _, b := range blocks {
		_, cidr, err := net.ParseCIDR(b)
		if err != nil {
			panic("outbound: " + b + " is not a CIDR: " + err.Error())
		}
		out = append(out, cidr)
	}
	return out
}()
