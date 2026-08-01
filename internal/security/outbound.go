package security

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

type OutboundPolicy struct {
	allowedHosts []string
	resolver     *net.Resolver
}

func NewOutboundPolicy(allowedHosts []string) (*OutboundPolicy, error) {
	normalized := make([]string, 0, len(allowedHosts))
	for _, host := range allowedHosts {
		host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
		if host != "" {
			normalized = append(normalized, host)
		}
	}
	if len(normalized) == 0 {
		return nil, errors.New("外連目的地主機允許清單不可為空")
	}
	return &OutboundPolicy{allowedHosts: normalized, resolver: net.DefaultResolver}, nil
}

func (p *OutboundPolicy) ValidateURL(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return nil, errors.New("外連 URL 必須是有效的 HTTPS URL")
	}
	if parsed.User != nil {
		return nil, errors.New("外連 URL 不可包含使用者資訊")
	}
	hostname := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if hostname == "localhost" || strings.HasSuffix(hostname, ".localhost") || strings.HasSuffix(hostname, ".local") {
		return nil, errors.New("外連 URL 不可指向本機名稱")
	}
	if !p.hostAllowed(hostname) {
		return nil, errors.New("外連 URL 主機不在允許清單")
	}
	if ip := net.ParseIP(hostname); ip != nil && !safePublicIP(ip) {
		return nil, errors.New("外連 URL 不可指向內網或特殊用途 IP")
	}
	parsed.Fragment = ""
	return parsed, nil
}

func (p *OutboundPolicy) HTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:                 nil,
		ForceAttemptHTTP2:     true,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConns:          20,
		MaxIdleConnsPerHost:   5,
	}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if !p.hostAllowed(host) {
			return nil, errors.New("外連目的地主機不在允許清單")
		}
		ips, err := p.resolve(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if safePublicIP(ip) {
				return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			}
		}
		return nil, errors.New("外連目的地只解析到內網或特殊用途 IP")
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("外連 HTTP redirect 已停用")
		},
	}
}

func (p *OutboundPolicy) resolve(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	addresses, err := p.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("無法解析外連主機: %w", err)
	}
	ips := make([]net.IP, 0, len(addresses))
	for _, address := range addresses {
		ips = append(ips, address.IP)
	}
	return ips, nil
}

func (p *OutboundPolicy) hostAllowed(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, allowed := range p.allowedHosts {
		if strings.HasPrefix(allowed, "*.") {
			suffix := strings.TrimPrefix(allowed, "*")
			if strings.HasSuffix(host, suffix) && host != strings.TrimPrefix(suffix, ".") {
				return true
			}
			continue
		}
		if host == allowed {
			return true
		}
	}
	return false
}

func safePublicIP(ip net.IP) bool {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	address = address.Unmap()
	for _, prefix := range specialUsePrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return address.IsGlobalUnicast()
}

var specialUsePrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}
