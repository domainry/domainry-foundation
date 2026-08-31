package safehttp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

type resolverFunc func(context.Context, string, string) ([]netip.Addr, error)

func (f resolverFunc) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	return f(ctx, network, host)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type dialerFunc func(context.Context, string, string) (net.Conn, error)

func (f dialerFunc) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return f(ctx, network, address)
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	target, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func TestValidateURLCoversStructuralLiteralAndResolverBoundaries(t *testing.T) {
	resolveErr := errors.New("resolver unavailable")
	policy := Policy{Resolver: resolverFunc(func(_ context.Context, _, host string) ([]netip.Addr, error) {
		switch host {
		case "error.example":
			return nil, resolveErr
		case "empty.example":
			return nil, nil
		case "public.example":
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		default:
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		}
	})}
	tests := []struct {
		name   string
		target *url.URL
		policy Policy
		unsafe bool
		cause  error
	}{
		{name: "missing URL", target: nil, policy: policy, unsafe: true},
		{name: "scheme", target: mustURL(t, "ftp://public.example/file"), policy: policy, unsafe: true},
		{name: "missing host", target: mustURL(t, "https:///path"), policy: policy, unsafe: true},
		{name: "loopback denied", target: mustURL(t, "http://127.0.0.1"), policy: policy, unsafe: true},
		{name: "loopback allowed", target: mustURL(t, "http://127.0.0.1"), policy: Policy{AllowLiteralLoopback: true}},
		{name: "public literal", target: mustURL(t, "https://8.8.8.8"), policy: policy},
		{name: "public literal with loopback escape enabled", target: mustURL(t, "https://8.8.8.8"), policy: Policy{AllowLiteralLoopback: true}},
		{name: "resolver error", target: mustURL(t, "https://error.example"), policy: policy, cause: resolveErr},
		{name: "empty DNS", target: mustURL(t, "https://empty.example"), policy: policy, unsafe: true},
		{name: "public DNS", target: mustURL(t, "https://public.example"), policy: policy},
		{name: "DNS loopback", target: mustURL(t, "https://loopback.example"), policy: policy, unsafe: true},
		{name: "normalized allowlist", target: mustURL(t, "https://INTERNAL.EXAMPLE./path"), policy: Policy{AllowedHosts: []string{" internal.example. "}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateURL(t.Context(), test.target, test.policy)
			if test.unsafe && !errors.Is(err, ErrUnsafeDestination) {
				t.Fatalf("error=%v, want unsafe destination", err)
			}
			if test.cause != nil && !errors.Is(err, test.cause) {
				t.Fatalf("error=%v, want cause %v", err, test.cause)
			}
			if !test.unsafe && test.cause == nil && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidatingTransportRejectsBeforeBaseAndForwardsSafeRequest(t *testing.T) {
	called := 0
	baseErr := errors.New("base transport failed")
	transport := &validatingTransport{
		validator: newValidator(Policy{Resolver: resolverStub{"public.example": {netip.MustParseAddr("8.8.8.8")}}}),
		base: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			called++
			if request.URL.Path == "/error" {
				return nil, baseErr
			}
			return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody, Request: request}, nil
		}),
	}
	if _, err := transport.RoundTrip(nil); err == nil || called != 0 {
		t.Fatalf("nil request err=%v called=%d", err, called)
	}
	private, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://10.0.0.1", nil)
	if _, err := transport.RoundTrip(private); !errors.Is(err, ErrUnsafeDestination) || called != 0 {
		t.Fatalf("private request err=%v called=%d", err, called)
	}
	safe, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://public.example/ok", nil)
	response, err := transport.RoundTrip(safe)
	if err != nil || response.StatusCode != http.StatusNoContent || called != 1 {
		t.Fatalf("response=%#v err=%v called=%d", response, err, called)
	}
	failing, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://public.example/error", nil)
	if _, err := transport.RoundTrip(failing); !errors.Is(err, baseErr) || called != 2 {
		t.Fatalf("base error=%v called=%d", err, called)
	}
}

func TestClientRedirectPolicyCoversLimitAndLoopbackOrigin(t *testing.T) {
	client := NewClient(Policy{AllowLiteralLoopback: true})
	if client.Timeout != defaultClientTimeout {
		t.Fatalf("timeout=%v", client.Timeout)
	}
	via := make([]*http.Request, 10)
	for index := range via {
		via[index], _ = http.NewRequest(http.MethodGet, "http://127.0.0.1/start", nil)
	}
	redirect, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1/next", nil)
	if err := client.CheckRedirect(redirect, via); err == nil || !strings.Contains(err.Error(), "10 redirects") {
		t.Fatalf("redirect limit error=%v", err)
	}
	if err := client.CheckRedirect(redirect, via[:1]); err != nil {
		t.Fatalf("literal loopback redirect rejected: %v", err)
	}
	public, _ := http.NewRequest(http.MethodGet, "https://8.8.8.8/next", nil)
	if err := client.CheckRedirect(public, nil); err != nil {
		t.Fatalf("public redirect rejected: %v", err)
	}
}

func TestDialContextCoversAddressParsingValidationAndDialOutcomes(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	validator := newValidator(Policy{AllowLiteralLoopback: true, Dialer: &net.Dialer{Timeout: time.Second}})
	if _, err := validator.dialContext(t.Context(), "tcp", "missing-port"); err == nil {
		t.Fatal("address without port accepted")
	}
	connection, err := validator.dialContext(t.Context(), "tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("loopback dial failed: %v", err)
	}
	connection.Close()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	allowed := newValidator(Policy{AllowedHosts: []string{"localhost"}, Dialer: &net.Dialer{Timeout: time.Second}})
	connection, err = allowed.dialContext(t.Context(), "tcp", net.JoinHostPort("LOCALHOST", port))
	if err != nil {
		t.Fatalf("explicitly allowed host dial failed: %v", err)
	}
	connection.Close()
	if _, err := validator.dialContext(t.Context(), "tcp", "10.0.0.1:80"); !errors.Is(err, ErrUnsafeDestination) {
		t.Fatalf("private literal dial error=%v", err)
	}

	resolveErr := errors.New("lookup failed")
	validator = newValidator(Policy{Resolver: resolverFunc(func(_ context.Context, _, host string) ([]netip.Addr, error) {
		if host == "error.example" {
			return nil, resolveErr
		}
		if host == "empty.example" {
			return nil, nil
		}
		if host == "private.example" {
			return []netip.Addr{netip.MustParseAddr("192.168.1.2")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("1.1.1.1")}, nil
	}), Dialer: &net.Dialer{Timeout: time.Millisecond}})
	if _, err := validator.dialContext(t.Context(), "tcp", "error.example:443"); !errors.Is(err, resolveErr) {
		t.Fatalf("resolver error=%v", err)
	}
	if _, err := validator.dialContext(t.Context(), "tcp", "private.example:443"); !errors.Is(err, ErrUnsafeDestination) {
		t.Fatalf("private DNS error=%v", err)
	}
	if _, err := validator.dialContext(t.Context(), "tcp", "empty.example:443"); !errors.Is(err, ErrUnsafeDestination) {
		t.Fatalf("empty DNS error=%v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := validator.dialContext(cancelled, "tcp", "public.example:443"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled public dial error=%v", err)
	}
	successValidator := newValidator(Policy{Resolver: resolverStub{"public.example": {netip.MustParseAddr("8.8.8.8")}}})
	successValidator.dialer = dialerFunc(func(context.Context, string, string) (net.Conn, error) {
		connection, peer := net.Pipe()
		_ = peer.Close()
		return connection, nil
	})
	connection, err = successValidator.dialContext(t.Context(), "tcp", "public.example:443")
	if err != nil {
		t.Fatalf("public DNS dial failed: %v", err)
	}
	_ = connection.Close()
	connection, err = successValidator.dialContext(t.Context(), "tcp", "8.8.8.8:443")
	if err != nil {
		t.Fatalf("public literal dial failed: %v", err)
	}
	_ = connection.Close()
}

func TestAddressAndLoopbackHelpersCoverAllClasses(t *testing.T) {
	for _, raw := range []string{"invalid IP", "0.0.0.0", "127.0.0.1", "10.0.0.1", "169.254.1.1", "224.0.0.1", "239.0.0.1", "255.255.255.255", "100.64.0.1"} {
		address, _ := netip.ParseAddr(raw)
		if err := validateAddress(address); !errors.Is(err, ErrUnsafeDestination) {
			t.Fatalf("address %q error=%v", raw, err)
		}
	}
	if err := validateAddress(netip.MustParseAddr("8.8.8.8")); err != nil {
		t.Fatalf("public address rejected: %v", err)
	}
	if !isCarrierGradeNAT(netip.MustParseAddr("100.127.255.254")) || isCarrierGradeNAT(netip.MustParseAddr("100.128.0.1")) {
		t.Fatal("carrier-grade NAT boundary mismatch")
	}
	validator := newValidator(Policy{})
	if validator.isAllowedLiteralLoopback(nil) || validator.isAllowedLiteralLoopback(mustURL(t, "http://127.0.0.1")) {
		t.Fatal("disabled loopback policy allowed target")
	}
	validator = newValidator(Policy{AllowLiteralLoopback: true})
	if validator.isAllowedLiteralLoopback(nil) || validator.isAllowedLiteralLoopback(mustURL(t, "https://example.com")) || !validator.isAllowedLiteralLoopback(mustURL(t, "http://[::1]")) {
		t.Fatal("loopback target classification mismatch")
	}
}

func TestNewValidatorIgnoresBlankAllowedHosts(t *testing.T) {
	validator := newValidator(Policy{AllowedHosts: []string{"  "}})
	if len(validator.allowedHosts) != 0 {
		t.Fatalf("allowed hosts = %#v", validator.allowedHosts)
	}
}
