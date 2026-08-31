package safehttp

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"testing"
)

type resolverStub map[string][]netip.Addr

func (r resolverStub) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	return r[host], nil
}

func TestValidateURLRejectsPrivateAndMixedDNSAnswers(t *testing.T) {
	policy := Policy{Resolver: resolverStub{
		"private.example": {netip.MustParseAddr("10.0.0.8")},
		"mixed.example":   {netip.MustParseAddr("203.0.113.8"), netip.MustParseAddr("192.168.1.8")},
	}}
	for _, raw := range []string{"http://10.0.0.1/path", "https://169.254.169.254/latest/meta-data", "https://private.example", "https://mixed.example"} {
		target, _ := url.Parse(raw)
		if err := ValidateURL(t.Context(), target, policy); !errors.Is(err, ErrUnsafeDestination) {
			t.Fatalf("target %q error = %v", raw, err)
		}
	}
}

func TestValidateURLAllowsPublicAndExplicitHosts(t *testing.T) {
	policy := Policy{Resolver: resolverStub{"public.example": {netip.MustParseAddr("8.8.8.8")}}, AllowedHosts: []string{"internal.example"}}
	for _, raw := range []string{"https://public.example/path", "https://internal.example/path"} {
		target, _ := url.Parse(raw)
		if err := ValidateURL(t.Context(), target, policy); err != nil {
			t.Fatalf("target %q error = %v", raw, err)
		}
	}
}

func TestRedirectCannotMovePublicRequestToLoopback(t *testing.T) {
	client := NewClient(Policy{AllowLiteralLoopback: true, Resolver: resolverStub{"public.example": {netip.MustParseAddr("8.8.8.8")}}})
	initial, _ := http.NewRequest(http.MethodGet, "https://public.example/start", nil)
	redirect, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1/admin", nil)
	if err := client.CheckRedirect(redirect, []*http.Request{initial}); !errors.Is(err, ErrUnsafeDestination) {
		t.Fatalf("redirect error = %v", err)
	}
}
