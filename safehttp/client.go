package safehttp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const defaultClientTimeout = 30 * time.Second

var ErrUnsafeDestination = errors.New("unsafe HTTP destination")

type Resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

// Policy defines which network destinations an outbound HTTP client may reach.
// Literal loopback addresses are an explicit escape hatch for local Runtime and
// connector development; DNS names that resolve to loopback remain blocked.
type Policy struct {
	AllowLiteralLoopback bool
	AllowedHosts         []string
	Resolver             Resolver
	Dialer               *net.Dialer
}

type validator struct {
	allowLiteralLoopback bool
	allowedHosts         map[string]struct{}
	resolver             Resolver
	dialer               interface {
		DialContext(context.Context, string, string) (net.Conn, error)
	}
}

type validatingTransport struct {
	base      http.RoundTripper
	validator *validator
}

func NewClient(policy Policy) *http.Client {
	validated := newValidator(policy)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = validated.dialContext
	return &http.Client{
		Transport: &validatingTransport{base: transport, validator: validated},
		Timeout:   defaultClientTimeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			allowLoopback := len(via) > 0 && validated.isAllowedLiteralLoopback(via[0].URL)
			return validated.validateURL(request.Context(), request.URL, allowLoopback)
		},
	}
}

func ValidateURL(ctx context.Context, target *url.URL, policy Policy) error {
	return newValidator(policy).validateURL(ctx, target, policy.AllowLiteralLoopback)
}

func newValidator(policy Policy) *validator {
	resolver := policy.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	dialer := policy.Dialer
	if dialer == nil {
		dialer = &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	}
	hosts := make(map[string]struct{}, len(policy.AllowedHosts))
	for _, host := range policy.AllowedHosts {
		if normalized := normalizeHost(host); normalized != "" {
			hosts[normalized] = struct{}{}
		}
	}
	return &validator{allowLiteralLoopback: policy.AllowLiteralLoopback, allowedHosts: hosts, resolver: resolver, dialer: dialer}
}

func (t *validatingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil {
		return nil, errors.New("nil HTTP request")
	}
	if err := t.validator.validateURL(request.Context(), request.URL, t.validator.allowLiteralLoopback); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(request)
}

func (v *validator) validateURL(ctx context.Context, target *url.URL, allowLiteralLoopback bool) error {
	if target == nil {
		return unsafeDestination("URL is missing")
	}
	scheme := strings.ToLower(strings.TrimSpace(target.Scheme))
	if scheme != "http" && scheme != "https" {
		return unsafeDestination("scheme %q is not allowed", target.Scheme)
	}
	host := normalizeHost(target.Hostname())
	if host == "" {
		return unsafeDestination("host is missing")
	}
	if _, ok := v.allowedHosts[host]; ok {
		return nil
	}
	if literal, err := netip.ParseAddr(host); err == nil {
		literal = literal.Unmap()
		if allowLiteralLoopback && literal.IsLoopback() {
			return nil
		}
		return validateAddress(literal)
	}
	addresses, err := v.resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("resolve outbound HTTP host %q: %w", host, err)
	}
	if len(addresses) == 0 {
		return unsafeDestination("host %q has no IP addresses", host)
	}
	for _, address := range addresses {
		if err := validateAddress(address.Unmap()); err != nil {
			return fmt.Errorf("host %q: %w", host, err)
		}
	}
	return nil
}

func (v *validator) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	normalized := normalizeHost(host)
	if _, ok := v.allowedHosts[normalized]; ok {
		return v.dialer.DialContext(ctx, network, address)
	}
	if literal, parseErr := netip.ParseAddr(normalized); parseErr == nil {
		literal = literal.Unmap()
		if !(v.allowLiteralLoopback && literal.IsLoopback()) {
			if err := validateAddress(literal); err != nil {
				return nil, err
			}
		}
		return v.dialer.DialContext(ctx, network, net.JoinHostPort(literal.String(), port))
	}
	addresses, err := v.resolver.LookupNetIP(ctx, "ip", normalized)
	if err != nil {
		return nil, err
	}
	for _, candidate := range addresses {
		candidate = candidate.Unmap()
		if err := validateAddress(candidate); err != nil {
			return nil, fmt.Errorf("host %q: %w", normalized, err)
		}
	}
	for _, candidate := range addresses {
		connection, dialErr := v.dialer.DialContext(ctx, network, net.JoinHostPort(candidate.Unmap().String(), port))
		if dialErr == nil {
			return connection, nil
		}
		err = dialErr
	}
	if err == nil {
		err = unsafeDestination("host %q has no IP addresses", normalized)
	}
	return nil, err
}

func (v *validator) isAllowedLiteralLoopback(target *url.URL) bool {
	if !v.allowLiteralLoopback || target == nil {
		return false
	}
	address, err := netip.ParseAddr(normalizeHost(target.Hostname()))
	return err == nil && address.Unmap().IsLoopback()
}

func validateAddress(address netip.Addr) error {
	if !address.IsValid() || address.IsUnspecified() || address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() || !address.IsGlobalUnicast() || isCarrierGradeNAT(address) {
		return unsafeDestination("IP address %q is not public", address)
	}
	return nil
}

func isCarrierGradeNAT(address netip.Addr) bool {
	prefix := netip.MustParsePrefix("100.64.0.0/10")
	return prefix.Contains(address)
}

func normalizeHost(host string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
}

func unsafeDestination(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnsafeDestination, fmt.Sprintf(format, args...))
}
