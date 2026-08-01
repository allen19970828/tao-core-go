package security

import (
	"net"
	"testing"
)

func TestOutboundPolicyURLValidation(t *testing.T) {
	policy, err := NewOutboundPolicy([]string{"hooks.example.com", "*.trusted.example"})
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}
	for _, rawURL := range []string{
		"http://hooks.example.com/callback",
		"https://user:pass@hooks.example.com/callback",
		"https://evil.example/callback",
		"https://trusted.example/callback",
	} {
		if _, err := policy.ValidateURL(rawURL); err == nil {
			t.Fatalf("expected %q to be rejected", rawURL)
		}
	}
	for _, rawURL := range []string{"https://hooks.example.com/callback#fragment", "https://api.trusted.example/callback"} {
		parsed, err := policy.ValidateURL(rawURL)
		if err != nil {
			t.Fatalf("expected %q to be accepted: %v", rawURL, err)
		}
		if parsed.Fragment != "" {
			t.Fatal("expected URL fragment to be removed")
		}
	}
}

func TestOutboundPolicyRejectsSpecialUseIPRanges(t *testing.T) {
	for _, address := range []string{
		"0.0.0.1", "10.0.0.1", "100.64.0.1", "127.0.0.1", "169.254.169.254",
		"172.16.0.1", "192.0.2.1", "192.168.1.1", "198.18.0.1", "198.51.100.1",
		"203.0.113.1", "224.0.0.1", "240.0.0.1", "::1", "2001:db8::1", "fc00::1", "fe80::1",
	} {
		if safePublicIP(net.ParseIP(address)) {
			t.Fatalf("expected special-use address %s to be rejected", address)
		}
	}
	if !safePublicIP(net.ParseIP("8.8.8.8")) || !safePublicIP(net.ParseIP("2606:4700:4700::1111")) {
		t.Fatal("expected public resolver addresses to be accepted")
	}
}

func TestOutboundPolicyRejectsLocalHostnameEvenWhenAllowlisted(t *testing.T) {
	policy, err := NewOutboundPolicy([]string{"localhost", "service.local"})
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}
	for _, rawURL := range []string{"https://localhost/hook", "https://service.local/hook"} {
		if _, err := policy.ValidateURL(rawURL); err == nil {
			t.Fatalf("expected %q to be rejected", rawURL)
		}
	}
}

func TestOutboundPolicyRejectsEmptyAllowlist(t *testing.T) {
	if _, err := NewOutboundPolicy(nil); err == nil {
		t.Fatal("expected an empty allowlist to be rejected")
	}
}
