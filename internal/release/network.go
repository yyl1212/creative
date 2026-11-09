package release

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

var excludedNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"), netip.MustParsePrefix("3fff::/20"),
}

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.Zone() != "" {
		return false
	}
	if ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	for _, prefix := range excludedNetworks {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

func ValidateProductionOrigin(raw string) error {
	u, err := parseOrigin(raw)
	if err != nil {
		return err
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if port := u.Port(); port != "" && port != "443" {
		return errors.New("生产 origin 只允许 HTTPS 默认端口")
	}
	if !strings.Contains(host, ".") && !strings.Contains(host, ":") || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") || strings.Contains(host, "%") {
		return errors.New("生产 origin 必须为公网域名或地址")
	}
	if ip, err := netip.ParseAddr(host); err == nil && !publicIP(ip) {
		return errors.New("生产 origin 不允许非公网地址")
	}
	return nil
}

func newTransport() *http.Transport {
	return &http.Transport{
		Proxy:                  nil,
		DisableCompression:     true,
		DisableKeepAlives:      true,
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  10 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return publicDial(ctx, network, address, net.DefaultResolver.LookupNetIP, (&net.Dialer{Timeout: 10 * time.Second}).DialContext)
		},
	}
}

func publicDial(ctx context.Context, network, address string, resolve func(context.Context, string, string) ([]netip.Addr, error), dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := resolve(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, errors.New("生产域名没有地址")
	}
	for _, ip := range ips {
		if !publicIP(ip) {
			return nil, errors.New("生产域名解析包含非公网地址")
		}
	}
	// 固定连接到本次验证的 IP，避免第二次 DNS 解析产生重绑定。
	return dial(ctx, network, net.JoinHostPort(ips[0].Unmap().String(), port))
}
