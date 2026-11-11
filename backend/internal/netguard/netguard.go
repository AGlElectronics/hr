// Package netguard keeps HR's outbound requests to URLs an admin typed in
// (the address lookup server) away from internal addresses (SSRF). A URL
// must be http(s) and every address it resolves to must be public, unless
// the host is on the allowlist (OUTBOUND_ALLOWED_HOSTS: host names or
// CIDRs, e.g. a self-hosted Photon inside the cluster). The check runs
// again when connecting, so DNS changes after the check do not get around
// it.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Guard decides which outbound addresses are allowed.
type Guard struct {
	hosts map[string]bool
	cidrs []netip.Prefix
	// AllowAll turns the guard off (development).
	AllowAll bool
	resolver *net.Resolver
}

// New parses the allowlist ("photon.photon.svc,10.20.0.0/16").
func New(allowlist []string, allowAll bool) (*Guard, error) {
	g := &Guard{hosts: map[string]bool{}, AllowAll: allowAll, resolver: net.DefaultResolver}
	for _, e := range allowlist {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		if strings.Contains(e, "/") {
			p, err := netip.ParsePrefix(e)
			if err != nil {
				return nil, fmt.Errorf("OUTBOUND_ALLOWED_HOSTS: %q is not a CIDR: %w", e, err)
			}
			g.cidrs = append(g.cidrs, p.Masked())
			continue
		}
		g.hosts[e] = true
	}
	return g, nil
}

// ErrBlocked is returned for a URL or address the guard refuses.
var ErrBlocked = errors.New("address not allowed")

func (g *Guard) allowedIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	for _, p := range g.cidrs {
		if p.Contains(ip) {
			return true
		}
	}
	return Public(ip)
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// Public reports whether ip is a public unicast address.
func Public(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsValid() && !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() &&
		!ip.IsInterfaceLocalMulticast() && !ip.IsMulticast() && !ip.IsUnspecified() && !cgnat.Contains(ip)
}

// CheckURL refuses non-http(s) URLs and URLs whose host resolves to an
// internal address.
func (g *Guard) CheckURL(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return fmt.Errorf("%w: must be an http(s) URL", ErrBlocked)
	}
	if u.User != nil {
		return fmt.Errorf("%w: must not contain a user name or password", ErrBlocked)
	}
	if g == nil || g.AllowAll {
		return nil
	}
	host := strings.ToLower(u.Hostname())
	if g.hosts[host] {
		return nil
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if !g.allowedIP(ip) {
			return fmt.Errorf("%w: %s is an internal address (add it to OUTBOUND_ALLOWED_HOSTS to allow it)", ErrBlocked, host)
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addrs, err := g.resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("%w: %s cannot be resolved", ErrBlocked, host)
	}
	for _, a := range addrs {
		if !g.allowedIP(a) {
			return fmt.Errorf("%w: %s resolves to the internal address %s (add it to OUTBOUND_ALLOWED_HOSTS to allow it)", ErrBlocked, host, a)
		}
	}
	return nil
}

// Client is an http.Client whose connections the guard checks: requests to
// allowlisted host names are dialled as usual, all others only to allowed
// addresses (also after redirects).
func (g *Guard) Client(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, _ := net.SplitHostPort(addr)
		d := *dialer
		if g != nil && !g.AllowAll && !g.hosts[strings.ToLower(host)] {
			d.Control = func(_, address string, _ syscall.RawConn) error {
				h, _, err := net.SplitHostPort(address)
				if err != nil {
					return err
				}
				ip, err := netip.ParseAddr(h)
				if err != nil || !g.allowedIP(ip) {
					return fmt.Errorf("%w: %s", ErrBlocked, address)
				}
				return nil
			}
		}
		return d.DialContext(ctx, network, addr)
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}
