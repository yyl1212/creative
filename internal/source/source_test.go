package source

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestSafeSourceFetch(t *testing.T) {
	for _, raw := range []string{"http://localhost/a", "https://127.0.0.1", "https://[::ffff:127.0.0.1]", "https://example.com:8080", "https://a:b@example.com", "file:///etc/passwd", "https://100.64.1.2", "https://192.0.2.1", "https://[2001:db8::1]"} {
		if _, err := New().Fetch(context.Background(), raw); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("accepted %s: %v", raw, err)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "http://127.0.0.1/secret", 302)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/large" {
			_, _ = w.Write([]byte(strings.Repeat("x", (2<<20)+1)))
			return
		}
		_, _ = w.Write([]byte(`<article><a rel="author" href="/u/alice">Alice</a><time datetime="2026-10-01T00:00:00Z"></time><p>I will pay USD 20 for batch export.</p></article>`))
	}))
	defer srv.Close()
	f := New()
	f.resolve = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}
	f.dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr != "93.184.216.34:80" {
			t.Errorf("not pinned: %s", addr)
		}
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	for _, path := range []string{"/redirect", "/large"} {
		if _, err := f.Fetch(context.Background(), "http://example.com"+path); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("accepted %s", path)
		}
	}
	doc, err := f.Fetch(context.Background(), "http://example.com/")
	if err != nil || doc.Hash == "" || doc.FetchedAt.IsZero() {
		t.Fatalf("fetch: %+v %v", doc, err)
	}
	f.resolve = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("127.0.0.1")}, nil
	}
	if _, err := f.Fetch(context.Background(), "http://example.com/"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("mixed DNS accepted")
	}
}

func TestQuoteAuthorBinding(t *testing.T) {
	html := `<article><a rel="author" href="/u/alice">Alice</a><time datetime="2026-10-01T00:00:00Z"></time><p>I will pay USD 20.</p><blockquote>Copied payment.</blockquote><div hidden>Hidden payment.</div><script>Script payment.</script><article><a rel="author" href="/u/bob">Bob</a><time datetime="2026-10-02T00:00:00Z"></time>Bob pays.</article></article>`
	d, err := Extract([]byte(html), "https://example.com/post")
	if err != nil {
		t.Fatal(err)
	}
	b, err := d.Bind("I will pay USD 20.")
	if err != nil || b.Author != "Alice" || b.AuthorRef != "https://example.com/u/alice" {
		t.Fatalf("binding %+v %v", b, err)
	}
	b, err = d.Bind("Bob pays.")
	if err != nil || b.Author != "Bob" {
		t.Fatalf("nested author %+v %v", b, err)
	}
	for _, q := range []string{"Copied payment.", "Hidden payment.", "Script payment.", "missing"} {
		if _, err = d.Bind(q); err == nil {
			t.Fatal("bound " + q)
		}
	}
	d, _ = Extract([]byte(`<article><a rel="author">A</a><a rel="author">B</a><time datetime="2026-10-01T00:00:00Z"></time>ambiguous</article>`), "https://example.com")
	if _, err = d.Bind("ambiguous"); err == nil {
		t.Fatal("ambiguous authors accepted")
	}
}

func TestSiteCanonicalization(t *testing.T) {
	for _, pair := range [][2]string{{"EXAMPLE.COM.", "example.com"}, {"例子.中国", "xn--fsqu00a.xn--fiqs8s"}} {
		a, e := SiteKey(pair[0])
		if e != nil {
			t.Fatal(e)
		}
		b, e := SiteKey(pair[1])
		if e != nil || a != b {
			t.Fatalf("same site differs %q %q %v", a, b, e)
		}
	}
}
