// Package release 生成并核验静态公开构建，不负责部署或改变业务状态。
package release

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/yyl1212/creative/internal/jsonutil"
)

const (
	schema       = "creative-release/v1"
	markerPath   = ".well-known/creative-release.json"
	maxJSONBytes = 4 << 20
	maxFileBytes = 32 << 20
	maxFiles     = 10000
)

type Product struct {
	NeedID int    `json:"need_id"`
	Slug   string `json:"slug"`
}

type Manifest struct {
	Schema      string            `json:"schema"`
	Commit      string            `json:"commit"`
	Origin      string            `json:"origin"`
	Environment string            `json:"environment"`
	Products    []Product         `json:"products"`
	Files       map[string]string `json:"files"`
}

var (
	commitPattern   = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
	slugPattern     = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	pathPartPattern = regexp.MustCompile(`^[a-zA-Z0-9_-][a-zA-Z0-9_.-]*$`)
)

// Prepare 读取外部 JSON 产品数组并原子写入构建标记；不访问网络。
func Prepare(dist, catalog, commit, origin, environment string) (Manifest, error) {
	m, err := buildManifest(dist, catalog, commit, origin, environment)
	if err != nil {
		return Manifest{}, err
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return Manifest{}, err
	}
	if len(raw) > maxJSONBytes {
		return Manifest{}, errors.New("发布标记过大")
	}
	root, err := openDist(dist)
	if err != nil {
		return Manifest{}, err
	}
	defer root.Close()
	if err = root.MkdirAll(".well-known", 0755); err != nil {
		return Manifest{}, err
	}
	info, err := root.Lstat(".well-known")
	if err != nil || !info.IsDir() {
		return Manifest{}, errors.New("标记目录不安全")
	}
	name := ".well-known/.creative-release-" + rand.Text() + ".tmp"
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return Manifest{}, err
	}
	defer root.Remove(name)
	_, err = f.Write(append(raw, '\n'))
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return Manifest{}, err
	}
	if closeErr != nil {
		return Manifest{}, closeErr
	}
	if err = root.Rename(name, markerPath); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// Verify 核对本地、清单及生产每个公开文件。client 非 nil 仅供受信任的离线测试注入；CLI 必须传 nil。
func Verify(ctx context.Context, dist, catalog string, manifest Manifest, client *http.Client) error {
	if manifest.Schema != schema || manifest.Environment != "production" {
		return errors.New("仅可核验 production 的当前格式标记")
	}
	expected, err := buildManifest(dist, catalog, manifest.Commit, manifest.Origin, manifest.Environment)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(expected, manifest) {
		return errors.New("构建、产品清单或发布标记不一致")
	}
	root, err := openDist(dist)
	if err != nil {
		return err
	}
	localFile, err := root.Open(markerPath)
	if err != nil {
		root.Close()
		return err
	}
	localRaw, err := readLimited(localFile, maxJSONBytes)
	localFile.Close()
	root.Close()
	if err != nil {
		return err
	}
	local, err := decodeManifest(localRaw)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(local, manifest) {
		return errors.New("本地发布标记已变更")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var c http.Client
	if client == nil {
		if err = ValidateProductionOrigin(manifest.Origin); err != nil {
			return err
		}
		transport := newTransport()
		defer transport.CloseIdleConnections()
		c.Transport = transport
	} else {
		c = *client
		c.Jar = nil
	}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if c.Timeout <= 0 || c.Timeout > 15*time.Second {
		c.Timeout = 15 * time.Second
	}
	raw, err := fetch(ctx, &c, manifest.Origin, markerPath, maxJSONBytes)
	if err != nil {
		return err
	}
	remote, err := decodeManifest(raw)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(remote, manifest) {
		return errors.New("生产标记与本地构建不一致")
	}
	names := make([]string, 0, len(manifest.Files))
	for name := range manifest.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		raw, err := fetch(ctx, &c, manifest.Origin, name, maxFileBytes)
		if err != nil {
			return err
		}
		if fmt.Sprintf("%x", sha256.Sum256(raw)) != manifest.Files[name] {
			return fmt.Errorf("生产文件摘要不一致：%s", name)
		}
	}
	return nil
}

// ReadManifest 严格读取本地标记；完整可信性仍由 Verify 检查。
func ReadManifest(name string) (Manifest, error) {
	raw, err := readFile(name, maxJSONBytes)
	if err != nil {
		return Manifest{}, err
	}
	return decodeManifest(raw)
}

func buildManifest(dist, catalog, commit, origin, environment string) (Manifest, error) {
	m := Manifest{Schema: schema, Commit: commit, Origin: origin, Environment: environment}
	if !commitPattern.MatchString(commit) || environment != "production" && environment != "preview" {
		return m, errors.New("提交须为 40 位十六进制，环境须为 production 或 preview")
	}
	if _, err := parseOrigin(origin); err != nil {
		return m, err
	}
	raw, err := readFile(catalog, maxJSONBytes)
	if err != nil {
		return m, err
	}
	m.Products, err = decodeProducts(raw)
	if err != nil {
		return m, err
	}
	m.Files, err = hashDist(dist)
	if err != nil {
		return m, err
	}
	for _, p := range m.Products {
		for _, lang := range []string{"en", "zh"} {
			if _, ok := m.Files[lang+"/tools/"+p.Slug+"/index.html"]; !ok {
				return m, fmt.Errorf("产品 %s 缺少 %s 页面", p.Slug, lang)
			}
		}
	}
	return m, nil
}

func decodeProducts(raw []byte) ([]Product, error) {
	if err := jsonutil.Check(raw); err != nil {
		return nil, err
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, errors.New("产品清单不能为空")
	}
	products := make([]Product, 0, len(entries))
	ids, slugs := map[int]bool{}, map[string]bool{}
	for _, entry := range entries {
		var p Product
		if len(entry) != 2 || entry["need_id"] == nil || entry["slug"] == nil {
			return nil, errors.New("产品清单只允许 need_id、slug")
		}
		if err := json.Unmarshal(entry["need_id"], &p.NeedID); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(entry["slug"], &p.Slug); err != nil {
			return nil, err
		}
		if p.NeedID <= 0 || !slugPattern.MatchString(p.Slug) || ids[p.NeedID] || slugs[p.Slug] {
			return nil, errors.New("产品 ID 或 slug 无效或重复")
		}
		ids[p.NeedID], slugs[p.Slug] = true, true
		products = append(products, p)
	}
	sort.Slice(products, func(i, j int) bool { return products[i].NeedID < products[j].NeedID })
	return products, nil
}

func decodeManifest(raw []byte) (Manifest, error) {
	var m Manifest
	if err := jsonutil.Check(raw); err != nil {
		return m, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return m, err
	}
	if len(fields) != 6 {
		return m, errors.New("发布标记字段不完整")
	}
	for _, key := range []string{"schema", "commit", "origin", "environment", "products", "files"} {
		if fields[key] == nil {
			return m, errors.New("发布标记字段无效")
		}
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, err
	}
	products, err := decodeProducts(fields["products"])
	if err != nil {
		return m, err
	}
	m.Products = products
	return m, nil
}

func parseOrigin(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return nil, errors.New("origin 必须为不带路径、凭据、查询或片段的 HTTPS 源")
	}
	return u, nil
}

func openDist(name string) (*os.Root, error) {
	info, err := os.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("构建目录必须是真实目录")
	}
	return os.OpenRoot(name)
}

func hashDist(dist string) (map[string]string, error) {
	root, err := openDist(dist)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	files := map[string]string{}
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == "." {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("构建不允许符号链接：%s", name)
		}
		if !publicPath(name, entry.IsDir()) {
			return fmt.Errorf("构建包含非公开路径：%s", name)
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("构建不允许特殊文件：%s", name)
		}
		if name == markerPath {
			return nil
		}
		if len(files) >= maxFiles {
			return errors.New("构建文件数量超限")
		}
		f, err := root.Open(name)
		if err != nil {
			return err
		}
		h := sha256.New()
		n, err := io.Copy(h, io.LimitReader(f, maxFileBytes+1))
		f.Close()
		if err != nil {
			return err
		}
		if n > maxFileBytes {
			return fmt.Errorf("构建文件过大：%s", name)
		}
		files[name] = fmt.Sprintf("%x", h.Sum(nil))
		return nil
	})
	return files, err
}

func publicPath(name string, dir bool) bool {
	for i, part := range strings.Split(name, "/") {
		if i == 0 && part == ".well-known" {
			continue
		}
		if !pathPartPattern.MatchString(part) {
			return false
		}
		switch strings.ToLower(strings.TrimSuffix(part, path.Ext(part))) {
		case "admin", "private", "secret", "secrets", "credential", "credentials", "token", "tokens", "config", "configuration", "backup", "backups", "database", "databases", "logs", "id_rsa", "id_ed25519", "package", "package-lock":
			return false
		}
	}
	if dir {
		return true
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".html", ".css", ".js", ".mjs", ".json", ".txt", ".xml", ".svg", ".png", ".jpg", ".jpeg", ".webp", ".gif", ".ico", ".avif", ".woff", ".woff2", ".ttf", ".otf", ".webmanifest", ".mp4", ".webm", ".mp3", ".ogg", ".wav", ".pdf":
		return true
	}
	return false
}

func readFile(name string, limit int64) ([]byte, error) {
	info, err := os.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("只允许普通文件")
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readLimited(f, limit)
}

func readLimited(r io.Reader, limit int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, errors.New("内容超限")
	}
	return raw, nil
}

func fetch(ctx context.Context, client *http.Client, origin, name string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/"+name, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Cache-Control", "no-cache")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("生产核验请求失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("生产核验状态异常：%s (%d)", name, resp.StatusCode)
	}
	return readLimited(resp.Body, limit)
}
