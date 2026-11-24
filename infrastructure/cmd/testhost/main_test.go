package main

import "testing"

func TestMobileDNSProviderSelection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config map[string]string
		want   string
	}{
		{"wizard Cloudflare", map[string]string{"dnsprovider": "cloudflare"}, "cloudflare"},
		{"legacy wizard seeded Route 53", map[string]string{"dnsprovider": "cloudflare", "mobileDNSProvider": "route53"}, "cloudflare"},
		{"wizard Route 53", map[string]string{"dnsprovider": "route53"}, "route53"},
		{"explicit override", map[string]string{"dnsprovider": "cloudflare", "mobileDNSProviderOverride": "route53"}, "route53"},
		{"legacy dedicated zone", map[string]string{"dnsprovider": "cloudflare", "mobileDNSProvider": "route53", "mobileDNSZone": "demo.example.test"}, "route53"},
		{"legacy Cloudflare override", map[string]string{"dnsprovider": "route53", "mobileDNSProvider": "cloudflare"}, "cloudflare"},
		{"legacy without wizard selection", map[string]string{"mobileDNSProvider": "route53"}, "route53"},
		{"standalone without mobile settings", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := mobileSettings(func(key string) string { return tc.config[key] })
			if got.DNSProvider != tc.want {
				t.Fatalf("mobile DNS provider = %q, want %q", got.DNSProvider, tc.want)
			}
		})
	}
}
