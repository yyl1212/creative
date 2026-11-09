package release

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testCommit = "0123456789abcdef0123456789abcdef01234567"

func write(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	dist, catalog := filepath.Join(root, "dist"), filepath.Join(root, "catalog.json")
	write(t, root, "catalog.json", `[{"need_id":7,"slug":"demo"}]`)
	write(t, dist, "en/tools/demo/index.html", "English demo")
	write(t, dist, "zh/tools/demo/index.html", "中文演示")
	write(t, dist, "_astro/main.js", "console.log('demo');")
	return dist, catalog
}

func prepare(t *testing.T, dist, catalog, origin, environment string) Manifest {
	t.Helper()
	m, err := Prepare(dist, catalog, testCommit, origin, environment)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestReleaseVerification(t *testing.T) {
	for _, change := range []string{"unchanged", "catalog", "build", "remote marker", "remote page", "remote asset", "network", "preview", "redirect", "local marker", "manifest path", "missing digest", "extra digest", "marker digest", "missing product", "secret path", "missing page", "cookies", "large body", "large marker", "status error", "cancelled"} {
		t.Run(change, func(t *testing.T) {
			dist, catalog := fixture(t)
			var marker []byte
			files := map[string]string{"/en/tools/demo/index.html": "English demo", "/zh/tools/demo/index.html": "中文演示", "/_astro/main.js": "console.log('demo');"}
			requests := 0
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("credentials sent")
				}
				if change == "redirect" {
					http.Redirect(w, r, "/private", http.StatusFound)
					return
				}
				if change == "status error" {
					http.Error(w, "temporary failure", http.StatusServiceUnavailable)
					return
				}
				if r.URL.Path == "/.well-known/creative-release.json" {
					_, _ = w.Write(marker)
					return
				}
				if change == "large body" {
					_, _ = w.Write([]byte(strings.Repeat("x", (32<<20)+1)))
					return
				}
				if body, ok := files[r.URL.Path]; ok {
					_, _ = w.Write([]byte(body))
					return
				}
				http.NotFound(w, r)
			}))
			defer srv.Close()
			env := "production"
			if change == "preview" {
				env = "preview"
			}
			m := prepare(t, dist, catalog, srv.URL, env)
			var err error
			marker, err = json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			client := srv.Client()
			switch change {
			case "catalog":
				write(t, filepath.Dir(catalog), filepath.Base(catalog), `[{"need_id":8,"slug":"demo"}]`)
			case "build":
				write(t, dist, "_astro/main.js", "changed")
			case "remote marker":
				remote := m
				remote.Commit = strings.Repeat("f", 40)
				marker, _ = json.Marshal(remote)
			case "remote page":
				files["/en/tools/demo/index.html"] = "changed"
			case "remote asset":
				files["/_astro/main.js"] = "changed"
			case "network":
				srv.Close()
			case "local marker":
				write(t, dist, ".well-known/creative-release.json", `{}`)
			case "manifest path":
				m.Files["../secret.txt"] = strings.Repeat("a", 64)
			case "missing digest":
				delete(m.Files, "_astro/main.js")
			case "extra digest":
				m.Files["missing.txt"] = strings.Repeat("a", 64)
			case "marker digest":
				m.Files[".well-known/creative-release.json"] = strings.Repeat("a", 64)
			case "missing product":
				m.Products = nil
			case "large marker":
				marker = []byte(strings.Repeat(" ", (4<<20)+1))
			case "secret path":
				write(t, dist, ".env", "SENTINEL=not-a-real-secret")
			case "missing page":
				if err := os.Remove(filepath.Join(dist, "zh/tools/demo/index.html")); err != nil {
					t.Fatal(err)
				}
			case "cookies":
				client.Jar, _ = cookiejar.New(nil)
				u, _ := url.Parse(srv.URL)
				client.Jar.SetCookies(u, []*http.Cookie{{Name: "session", Value: "sentinel"}})
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if change == "cancelled" {
				cancel()
			}
			err = Verify(ctx, dist, catalog, m, client)
			if change == "unchanged" || change == "cookies" {
				if err != nil {
					t.Fatal(err)
				}
				if err := Verify(context.Background(), dist, catalog, m, client); err != nil {
					t.Fatalf("repeat verification: %v", err)
				}
			} else if err == nil {
				t.Fatal("unsafe release accepted")
			}
			if change == "redirect" && requests != 1 {
				t.Fatalf("redirect followed: %d requests", requests)
			}
			if change == "preview" && requests != 0 {
				t.Fatal("preview contacted network")
			}
		})
	}
}

func TestPrepareManifest(t *testing.T) {
	dist, catalog := fixture(t)
	m := prepare(t, dist, catalog, "https://example.org", "production")
	if m.Schema == "" || m.Commit != testCommit || m.Origin != "https://example.org" || m.Environment != "production" || len(m.Products) != 1 || m.Products[0] != (Product{NeedID: 7, Slug: "demo"}) || len(m.Files) != 3 {
		t.Fatalf("incomplete manifest: %+v", m)
	}
	if m.Files["en/tools/demo/index.html"] != fmt.Sprintf("%x", sha256.Sum256([]byte("English demo"))) {
		t.Fatal("wrong file digest")
	}
	read, err := ReadManifest(filepath.Join(dist, ".well-known/creative-release.json"))
	if err != nil {
		t.Fatal(err)
	}
	if read.Commit != testCommit {
		t.Fatal("manifest not written")
	}
	second := prepare(t, dist, catalog, "https://example.org", "production")
	a, _ := json.Marshal(m)
	b, _ := json.Marshal(second)
	if string(a) != string(b) {
		t.Fatal("prepare is not idempotent")
	}
}

func TestPrepareRejectsInvalidInput(t *testing.T) {
	for _, raw := range []string{`[]`, `null`, `[{"need_id":0,"slug":"demo"}]`, `[{"need_id":-1,"slug":"demo"}]`, `[{"need_id":1,"slug":"../demo"}]`, `[{"need_id":1,"slug":"Demo"}]`, `[{"need_id":1,"slug":"demo","token":"sentinel"}]`, `[{"need_id":1,"need_id":7,"slug":"demo"}]`, `[{"NEED_ID":7,"slug":"demo"}]`, `[{"need_id":7,"slug":"demo"},{"need_id":7,"slug":"other"}]`, `[{"need_id":7,"slug":"demo"},{"need_id":8,"slug":"demo"}]`} {
		t.Run(raw, func(t *testing.T) {
			dist, catalog := fixture(t)
			write(t, filepath.Dir(catalog), filepath.Base(catalog), raw)
			if _, err := Prepare(dist, catalog, testCommit, "https://example.org", "production"); err == nil {
				t.Fatal("invalid catalog accepted")
			}
		})
	}
	for _, origin := range []string{"http://example.org", "https://u:p@example.org", "https://example.org/path", "https://example.org?key=secret", "https://example.org#fragment", "https://example.org/", "https://"} {
		t.Run(origin, func(t *testing.T) {
			dist, catalog := fixture(t)
			if _, err := Prepare(dist, catalog, testCommit, origin, "production"); err == nil {
				t.Fatal("invalid origin accepted")
			}
		})
	}
	dist, catalog := fixture(t)
	if _, err := Prepare(dist, catalog, "HEAD", "https://example.org", "production"); err == nil {
		t.Fatal("symbolic commit accepted")
	}
	if _, err := Prepare(dist, catalog, testCommit, "https://example.org", "staging"); err == nil {
		t.Fatal("unknown environment accepted")
	}
}

func TestPrepareRejectsPrivateFilesAndLinks(t *testing.T) {
	for _, name := range []string{".env", ".git/config", "backup.sqlite", "database.db", "server.key", "private/notes.txt", "admin/index.html", "secrets.json", "config.json", "assets/main.js.map", "assets/a%2fb.js", "assets/a\\b.js", ".well-known/../.env"} {
		t.Run(name, func(t *testing.T) {
			dist, catalog := fixture(t)
			write(t, dist, name, "synthetic private sentinel")
			if _, err := Prepare(dist, catalog, testCommit, "https://example.org", "production"); err == nil {
				t.Fatal("private file accepted")
			}
		})
	}
	for _, name := range []string{"asset.txt", ".well-known", "en/tools/demo/index.html", "dist-root", "catalog"} {
		t.Run(name, func(t *testing.T) {
			dist, catalog := fixture(t)
			outside := t.TempDir()
			write(t, outside, "sentinel.txt", "outside")
			link, target := filepath.Join(dist, name), filepath.Join(outside, "sentinel.txt")
			if name == ".well-known" {
				target = outside
			}
			if name == "en/tools/demo/index.html" {
				if err := os.Remove(link); err != nil {
					t.Fatal(err)
				}
			}
			if name == "dist-root" {
				link = filepath.Join(outside, "dist-link")
				target, dist = dist, link
			}
			if name == "catalog" {
				link = filepath.Join(outside, "catalog-link")
				target, catalog = catalog, link
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			if _, err := Prepare(dist, catalog, testCommit, "https://example.org", "production"); err == nil {
				t.Fatal("symlink accepted")
			}
		})
	}
}

func TestReleaseDefaultNetworkPolicy(t *testing.T) {
	for _, origin := range []string{"https://127.0.0.1", "https://localhost", "https://host.local", "https://10.0.0.1", "https://100.64.0.1", "https://192.0.2.1", "https://[::ffff:127.0.0.1]", "https://[2001:db8::1]"} {
		t.Run(origin, func(t *testing.T) {
			dist, catalog := fixture(t)
			m := prepare(t, dist, catalog, origin, "production")
			if err := Verify(context.Background(), dist, catalog, m, nil); err == nil {
				t.Fatal("private origin accepted")
			}
		})
	}
	for _, ips := range [][]netip.Addr{nil, {netip.MustParseAddr("127.0.0.1")}, {netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("10.0.0.1")}, {netip.MustParseAddr("93.184.216.34")}} {
		called := false
		resolve := func(context.Context, string, string) ([]netip.Addr, error) { return ips, nil }
		dial := func(_ context.Context, network, address string) (net.Conn, error) {
			called = true
			if network != "tcp" || address != "93.184.216.34:443" {
				t.Fatalf("unvalidated dial target: %s %s", network, address)
			}
			return nil, errors.New("offline dial sentinel")
		}
		if _, err := publicDial(context.Background(), "tcp", "example.org:443", resolve, dial); err == nil {
			t.Fatal("network failure ignored")
		}
		if called != (len(ips) == 1 && ips[0].String() == "93.184.216.34") {
			t.Fatalf("unexpected dial for %v", ips)
		}
	}
}

func TestReleaseIgnoresEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:31337")
	t.Setenv("https_proxy", "http://127.0.0.1:31337")
	transport := newTransport()
	defer transport.CloseIdleConnections()
	called := false
	transport.DialContext = func(_ context.Context, _, address string) (net.Conn, error) {
		called = true
		if address != "example.org:443" {
			t.Errorf("proxy dial attempted: %s", address)
		}
		return nil, errors.New("offline sentinel")
	}
	client := &http.Client{Transport: transport}
	if _, err := client.Get("https://example.org"); err == nil || !called {
		t.Fatalf("expected offline direct dial: %v", err)
	}
}

func TestPrepareRequiresBothProductPages(t *testing.T) {
	dist, catalog := fixture(t)
	if err := os.Remove(filepath.Join(dist, "zh/tools/demo/index.html")); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(dist, catalog, testCommit, "https://example.org", "production"); err == nil {
		t.Fatal("missing Chinese product page accepted")
	}
	if _, err := os.Stat(filepath.Join(dist, ".well-known/creative-release.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed prepare wrote marker: %v", err)
	}
}

func TestReadManifestRejectsAmbiguousJSON(t *testing.T) {
	dist, catalog := fixture(t)
	m := prepare(t, dist, catalog, "https://example.org", "production")
	raw, _ := json.Marshal(m)
	for _, invalid := range []string{
		string(raw) + ` {}`,
		strings.Replace(string(raw), `"schema":`, `"schema":"old","schema":`, 1),
		strings.Replace(string(raw), `"schema":`, `"SCHEMA":`, 1),
		strings.Replace(string(raw), `"need_id":7`, `"need_id":7,"secret":"sentinel"`, 1),
	} {
		write(t, dist, ".well-known/creative-release.json", invalid)
		if _, err := ReadManifest(filepath.Join(dist, ".well-known/creative-release.json")); err == nil {
			t.Fatal("ambiguous manifest accepted")
		}
	}
}
