// Package source 抓取公开原文；每次连接固定到重新验证的公网地址。
package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

var ErrUnavailable = errors.New("source_unavailable")

type Fetcher struct {
	resolve func(context.Context, string) ([]netip.Addr, error)
	dial    func(context.Context, string, string) (net.Conn, error)
}

func New() *Fetcher {
	return &Fetcher{resolve: func(ctx context.Context, h string) ([]netip.Addr, error) {
		return net.DefaultResolver.LookupNetIP(ctx, "ip", h)
	}, dial: (&net.Dialer{Timeout: 10 * time.Second}).DialContext}
}

var excluded = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"), netip.MustParsePrefix("3fff::/20"),
}

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.Zone() != "" {
		return false
	}
	if ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	for _, p := range excluded {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}
func validURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.Opaque != "" {
		return nil, ErrUnavailable
	}
	if p := u.Port(); p != "" && p != "80" && p != "443" {
		return nil, ErrUnavailable
	}
	h := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if !strings.Contains(h, ".") && !strings.Contains(h, ":") || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".internal") || strings.Contains(h, "%") {
		return nil, ErrUnavailable
	}
	if ip, e := netip.ParseAddr(h); e == nil && !publicIP(ip) {
		return nil, ErrUnavailable
	}
	return u, nil
}
func (f *Fetcher) Fetch(ctx context.Context, raw string) (Document, error) {
	var zero Document
	u, err := validURL(raw)
	if err != nil {
		return zero, err
	}
	tr := &http.Transport{Proxy: nil, DisableKeepAlives: true, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 10 * time.Second}
	defer tr.CloseIdleConnections()
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		h, p, e := net.SplitHostPort(address)
		if e != nil {
			return nil, ErrUnavailable
		}
		ips, e := f.resolve(ctx, h)
		if e != nil || len(ips) == 0 {
			return nil, ErrUnavailable
		}
		for _, ip := range ips {
			if !publicIP(ip) {
				return nil, ErrUnavailable
			}
		}
		return f.dial(ctx, network, net.JoinHostPort(ips[0].Unmap().String(), p))
	}
	client := &http.Client{Transport: tr, Timeout: 15 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return ErrUnavailable
		}
		_, e := validURL(r.URL.String())
		return e
	}}
	for attempt := 0; attempt < 3; attempt++ {
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if e != nil {
			return zero, ErrUnavailable
		}
		req.Header.Set("User-Agent", "Creative-SourceVerifier/1.0")
		req.Header.Set("Accept", "text/html, text/plain;q=0.8")
		resp, e := client.Do(req)
		if e != nil {
			if ctx.Err() != nil || errors.Is(e, ErrUnavailable) {
				return zero, ErrUnavailable
			}
			continue
		}
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			resp.Body.Close()
			if resp.Header.Get("Retry-After") != "" {
				return zero, ErrUnavailable
			}
			continue
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			return zero, ErrUnavailable
		}
		kind, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		if kind != "text/html" && kind != "text/plain" {
			resp.Body.Close()
			return zero, ErrUnavailable
		}
		b, e := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
		resp.Body.Close()
		if e != nil || len(b) > 2<<20 {
			return zero, ErrUnavailable
		}
		doc, e := Extract(b, resp.Request.URL.String())
		if e != nil {
			return zero, fmt.Errorf("%w: unsupported document", ErrUnavailable)
		}
		return doc, nil
	}
	return zero, ErrUnavailable
}
