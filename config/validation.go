package config

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// blockedNetworks lists IP ranges that webhook destinations must never use.
// Besides RFC1918 space it covers loopback, link-local, CGNAT, multicast,
// documentation and otherwise reserved ranges so a webhook cannot be used to
// reach the server itself, the host metadata service, or internal services.
var blockedNetworks = mustParseCIDRs([]string{
	// IPv4
	"0.0.0.0/8",
	"10.0.0.0/8",
	"100.64.0.0/10",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.0.0.0/24",
	"192.0.2.0/24",
	"192.88.99.0/24",
	"192.168.0.0/16",
	"198.18.0.0/15",
	"198.51.100.0/24",
	"203.0.113.0/24",
	"224.0.0.0/4",
	"240.0.0.0/4",
	"255.255.255.255/32",
	// IPv6
	"::/128",
	"::1/128",
	// IPv4-mapped addresses are normalized with To4() and matched against
	// the IPv4 ranges above; listing ::ffff:0:0/96 here would match every
	// IPv4 address (IPNet.Contains converts v4 addresses to v6).
	"64:ff9b::/96",
	"64:ff9b:1::/48",
	"100::/64",
	"2001:db8::/32",
	"fc00::/7",
	"fe80::/10",
	"ff00::/8",
})

func mustParseCIDRs(entries []string) []*net.IPNet {
	networks := make([]*net.IPNet, 0, len(entries))
	for _, entry := range entries {
		_, network, err := net.ParseCIDR(entry)
		if err != nil {
			panic("invalid built-in CIDR " + entry)
		}
		networks = append(networks, network)
	}
	return networks
}

// IsBlockedIP reports whether ip is in a range webhooks must not connect to.
// IPv4-mapped IPv6 addresses are normalized to IPv4 before matching.
func IsBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, network := range blockedNetworks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// ValidateWebhookURL rejects URLs that cannot safely be used as webhook
// destinations. It allows http(s) only, forbids embedded credentials and
// local hostnames, and refuses addresses in private/special IP ranges.
func ValidateWebhookURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("webhook URL must not be empty")
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid webhook URL %q: %w", raw, err)
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("webhook URL %q must use http or https", raw)
	}
	if parsed.User != nil {
		return fmt.Errorf("webhook URL %q must not contain credentials", raw)
	}

	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("webhook URL %q must include a host", raw)
	}

	lowerHost := strings.ToLower(strings.TrimSuffix(host, "."))
	if lowerHost == "localhost" ||
		strings.HasSuffix(lowerHost, ".localhost") ||
		strings.HasSuffix(lowerHost, ".local") ||
		strings.HasSuffix(lowerHost, ".internal") {
		return fmt.Errorf("webhook URL %q uses a local hostname", raw)
	}

	if ip := net.ParseIP(host); ip != nil {
		if IsBlockedIP(ip) {
			return fmt.Errorf("webhook URL %q targets a disallowed IP address", raw)
		}
		return nil
	}

	// For DNS names, reject the URL when resolution succeeds and any answer is
	// in a blocked range. A resolution failure is not fatal here: the
	// dispatcher resolves again at dial time and refuses blocked addresses,
	// which also protects against DNS rebinding between the two lookups.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil
	}
	for _, addr := range addrs {
		if IsBlockedIP(addr.IP) {
			return fmt.Errorf("webhook URL %q resolves to a disallowed IP address", raw)
		}
	}
	return nil
}

// ValidateWebhookURLs validates every configured webhook URL.
func ValidateWebhookURLs(webhooks []string) error {
	for i, webhook := range webhooks {
		if err := ValidateWebhookURL(webhook); err != nil {
			return fmt.Errorf("webhooks[%d]: %w", i, err)
		}
	}
	return nil
}

// parseTrustedProxies parses trusted_proxies entries. Each entry may be a
// CIDR or a single IP address; bare addresses become host routes.
func parseTrustedProxies(entries []string) ([]*net.IPNet, error) {
	if len(entries) == 0 {
		return nil, nil
	}

	networks := make([]*net.IPNet, 0, len(entries))
	for i, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			return nil, fmt.Errorf("trusted_proxies[%d] must not be empty", i)
		}

		if strings.Contains(entry, "/") {
			_, network, err := net.ParseCIDR(entry)
			if err != nil {
				return nil, fmt.Errorf("trusted_proxies[%d]: invalid CIDR %q", i, entry)
			}
			networks = append(networks, network)
			continue
		}

		ip := net.ParseIP(entry)
		if ip == nil {
			return nil, fmt.Errorf("trusted_proxies[%d]: invalid IP address %q", i, entry)
		}
		bits := 32
		if ip.To4() == nil {
			bits = 128
		}
		mask := net.CIDRMask(bits, bits)
		networks = append(networks, &net.IPNet{IP: ip.Mask(mask), Mask: mask})
	}
	return networks, nil
}

// placeholderAdminTokens are example/default values that must never be
// accepted as a real admin credential.
var placeholderAdminTokens = map[string]bool{
	"admin":                         true,
	"admin-token":                   true,
	"changeme":                      true,
	"change-me":                     true,
	"my-super-secret-admin-token":   true,
	"your-super-secret-admin-token": true,
	"your-admin-token":              true,
	"password":                      true,
	"secret":                        true,
	"test":                          true,
	"token":                         true,
}

// ValidateAdminToken rejects short, empty and well-known placeholder tokens.
func ValidateAdminToken(token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("admin_token is required")
	}
	if len(token) < 16 {
		return fmt.Errorf("admin_token must be at least 16 characters")
	}
	lower := strings.ToLower(token)
	if placeholderAdminTokens[lower] || strings.HasPrefix(lower, "replace_") {
		return fmt.Errorf("admin_token is a placeholder; choose a long random value")
	}
	return nil
}
