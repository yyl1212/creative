package source

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/publicsuffix"
)

type Block struct {
	Text        string    `json:"text"`
	Author      string    `json:"author"`
	AuthorRef   string    `json:"author_ref"`
	PublishedAt time.Time `json:"published_at"`
}
type Document struct {
	URL       string    `json:"url"`
	Site      string    `json:"site"`
	Hash      string    `json:"hash"`
	FetchedAt time.Time `json:"fetched_at"`
	Text      string    `json:"text"`
	Blocks    []Block   `json:"blocks"`
	Raw       []byte    `json:"-"`
}

func Normalize(s string) string { return strings.Join(strings.Fields(s), " ") }
func (d Document) Bind(quote string) (Block, error) {
	q := Normalize(quote)
	var match Block
	n := 0
	if q == "" {
		return match, ErrUnavailable
	}
	for _, b := range d.Blocks {
		if strings.Contains(b.Text, q) {
			match = b
			n++
		}
	}
	if n != 1 || match.Author == "" || match.AuthorRef == "" || match.PublishedAt.IsZero() || len(match.Text) > 16000 {
		return Block{}, ErrUnavailable
	}
	return match, nil
}
func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func excludedNode(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	switch n.Data {
	case "script", "style", "noscript", "template", "blockquote", "pre", "code":
		return true
	}
	for _, a := range n.Attr {
		if a.Key == "hidden" {
			return true
		}
	}
	style := strings.ToLower(strings.ReplaceAll(attr(n, "style"), " ", ""))
	return attr(n, "aria-hidden") == "true" || strings.Contains(style, "display:none") || strings.Contains(style, "visibility:hidden")
}
func authored(n *html.Node) bool {
	return n.Type == html.ElementNode && (n.Data == "article" || strings.HasSuffix(attr(n, "itemtype"), "/Comment") || strings.HasSuffix(attr(n, "itemtype"), "/DiscussionForumPosting"))
}
func visible(n *html.Node, root *html.Node) string {
	if excludedNode(n) || (n != root && authored(n)) {
		return ""
	}
	if n.Type == html.TextNode {
		return n.Data + " "
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(visible(c, root))
	}
	return b.String()
}
func Extract(raw []byte, finalURL string) (Document, error) {
	d := Document{URL: finalURL, Hash: fmt.Sprintf("%x", sha256.Sum256(raw)), FetchedAt: time.Now().UTC(), Raw: raw}
	u, e := url.Parse(finalURL)
	if e != nil {
		return d, e
	}
	d.Site, e = publicsuffix.EffectiveTLDPlusOne(u.Hostname())
	if e != nil {
		return d, ErrUnavailable
	}
	tree, e := html.Parse(bytes.NewReader(raw))
	if e != nil {
		return d, e
	}
	var roots []*html.Node
	var body *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if excludedNode(n) {
			return
		}
		if n.Data == "body" {
			body = n
		}
		if authored(n) {
			roots = append(roots, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(tree)
	if len(roots) == 0 && body != nil {
		roots = []*html.Node{body}
	}
	for _, root := range roots {
		b := Block{Text: Normalize(visible(root, root))}
		authors := map[string]string{}
		dates := map[string]bool{}
		var meta func(*html.Node)
		meta = func(n *html.Node) {
			if excludedNode(n) || (n != root && authored(n)) {
				return
			}
			if n.Type == html.ElementNode {
				if strings.Contains(" "+attr(n, "rel")+" ", " author ") || attr(n, "itemprop") == "author" {
					name := Normalize(visible(n, n))
					ref := attr(n, "href")
					if ref == "" {
						ref = name
					} else if link, er := url.Parse(ref); er == nil {
						ref = u.ResolveReference(link).String()
					}
					if name != "" {
						authors[name] = ref
					}
				}
				if n.Data == "time" || attr(n, "itemprop") == "datePublished" {
					v := attr(n, "datetime")
					if v == "" {
						v = attr(n, "content")
					}
					if v != "" {
						dates[v] = true
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				meta(c)
			}
		}
		meta(root)
		if len(authors) == 1 {
			for name, ref := range authors {
				b.Author = name
				b.AuthorRef = ref
			}
		}
		if len(dates) == 1 {
			for v := range dates {
				b.PublishedAt, _ = time.Parse(time.RFC3339, v)
			}
		}
		d.Blocks = append(d.Blocks, b)
		d.Text += b.Text + "\n"
	}
	if len(d.Text) > 128<<10 {
		return Document{}, ErrUnavailable
	}
	return d, nil
}
