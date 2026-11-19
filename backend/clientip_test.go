package main

import (
	"net/http/httptest"
	"testing"
)

func TestClientIP(t *testing.T) {
	cases := []struct{ xff, cf, want string }{
		{"203.0.113.9", "", "203.0.113.9"},                     // direct, no Cloudflare
		{"198.51.100.1, 172.68.10.20", "203.0.113.7", "203.0.113.7"}, // via Cloudflare edge: trust CF header
		{"203.0.113.50", "1.2.3.4", "203.0.113.50"},              // spoofed CF header from a non-Cloudflare peer
		{"2a06:98c1:3120::5", "2001:db8::1", "2001:db8::1"},      // Cloudflare over IPv6
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("X-Forwarded-For", c.xff)
		if c.cf != "" {
			r.Header.Set("CF-Connecting-IP", c.cf)
		}
		if got := clientIP(r); got != c.want {
			t.Errorf("xff=%q cf=%q: got %q, want %q", c.xff, c.cf, got, c.want)
		}
	}
}
