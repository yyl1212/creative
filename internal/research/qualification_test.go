package research

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"testing"
	"time"

	"github.com/yyl1212/creative/internal/model"
	"github.com/yyl1212/creative/internal/source"
)

var fixtureNow = time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)

func ptr(s string) *string { return &s }
func qualificationFixture() (candidate, []BoundSource, model.Assessment) {
	c := candidate{ExternalID: "export", Title: "Export", TargetUser: "Bookkeepers", Job: "CSV invoice export", Constraints: []string{}}
	a := model.Assessment{SelfService: true, TaskKey: "invoice-csv-export", DistinctFromExisting: true, Evidence: []model.Evidence{}}
	var sources []BoundSource
	for i, author := range []string{"Alice", "Bob", "Cara"} {
		text := fmt.Sprintf("%s will pay USD 20 per month for export. CSV is unavailable.", author)
		if i == 2 {
			text = "I cannot export invoices."
		}
		url := fmt.Sprintf("https://site%d.example.org/post/%d", i%2, i)
		// 不同可注册站点，不能把同域子站算成独立站点。
		if i%2 == 1 {
			url = "https://second.example.net/post/1"
		}
		d := source.Document{URL: url, Site: []string{"example.org", "example.net", "example.org"}[i], Hash: fmt.Sprint(i), FetchedAt: fixtureNow, Blocks: []source.Block{{Author: author, AuthorRef: author, PublishedAt: fixtureNow.AddDate(0, -1, 0), Text: text}}}
		e := Evidence{ID: fmt.Sprint(i), URL: url, AuthorRef: ptr(author), PublishedAt: ptr(d.Blocks[0].PublishedAt.Format(time.RFC3339)), Quote: text, Signal: "explicit_wtp", Price: &Price{ptr("20"), ptr("USD"), ptr("per month")}, UnmetNeed: "CSV is unavailable."}
		m := model.Evidence{ID: e.ID, Relevant: true, Original: true, DirectUser: true, Independent: true, Signal: "explicit_wtp", PaymentQuote: fmt.Sprintf("%s will pay USD 20 per month for export.", author), UnmetQuote: e.UnmetNeed, PriceQuote: text, Conditions: []string{}}
		if i == 2 {
			e.Signal = "pain_only"
			e.Price = nil
			e.UnmetNeed = text
			m.Signal = "pain_only"
			m.PaymentQuote = ""
			m.UnmetQuote = text
			m.PriceQuote = ""
		}
		c.Evidence = append(c.Evidence, e)
		a.Evidence = append(a.Evidence, m)
		sources = append(sources, BoundSource{ID: e.ID, Document: d, Block: d.Blocks[0]})
	}
	return c, sources, a
}
func TestQualificationCorpus(t *testing.T) {
	var cases []struct {
		ID, Text, Signal, Pay, Unmet, Amount, Currency, Unit, Author, Date string
		Copied, Qualified                                                  bool
	}
	raw, err := os.ReadFile("testdata/verification-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 30 {
		t.Fatal("too few cases")
	}
	for _, tc := range cases {
		t.Run(tc.ID, func(t *testing.T) {
			c, bs, a := qualificationFixture()
			raw := fmt.Sprintf(`<article><a rel="author">%s</a><time datetime="%s"></time><p>%s</p></article>`, html.EscapeString(tc.Author), tc.Date, html.EscapeString(tc.Text))
			d, e := source.Extract([]byte(raw), c.Evidence[1].URL)
			if e != nil {
				t.Fatal(e)
			}
			b, e := d.Bind(tc.Text)
			if e != nil {
				t.Fatal(e)
			}
			if b.Author != tc.Author || b.PublishedAt.Format(time.RFC3339) != tc.Date {
				t.Fatal("source metadata changed")
			}
			d.FetchedAt = fixtureNow
			bs[1] = BoundSource{ID: "1", Document: d, Block: b}
			c.Evidence[1] = Evidence{ID: "1", URL: d.URL, AuthorRef: ptr(tc.Author), PublishedAt: ptr(tc.Date), Quote: tc.Text, Signal: tc.Signal, UnmetNeed: tc.Unmet}
			if tc.Amount != "" {
				c.Evidence[1].Price = &Price{ptr(tc.Amount), ptr(tc.Currency), ptr(tc.Unit)}
			}
			a.Evidence[1] = model.Evidence{ID: "1", Relevant: true, Original: !tc.Copied, DirectUser: true, Independent: true, Signal: tc.Signal, PaymentQuote: tc.Pay, UnmetQuote: tc.Unmet, PriceQuote: tc.Text}
			result := Evaluate(c, bs, a, fixtureNow)
			if (result.Outcome == "qualified") != tc.Qualified {
				t.Fatalf("want qualified=%v got %+v", tc.Qualified, result)
			}
		})
	}
	t.Logf("自制 %d 条来源字段/硬规则样本；模型语义为独立标注替身，不代表真实模型准确率", len(cases))
}
func TestQualificationExpiry(t *testing.T) {
	c, bs, a := qualificationFixture()
	d := Evaluate(c, bs, a, fixtureNow)
	if d.Outcome != "qualified" || !d.ValidUntil.Equal(fixtureNow.AddDate(0, 0, 30)) {
		t.Fatalf("%+v", d)
	}
	bs[0].Block.PublishedAt = fixtureNow.AddDate(-1, 0, 1)
	c.Evidence[0].PublishedAt = ptr(bs[0].Block.PublishedAt.Format(time.RFC3339))
	d = Evaluate(c, bs, a, fixtureNow)
	if !d.ValidUntil.Equal(fixtureNow.AddDate(0, 0, 1)) {
		t.Fatalf("expiry %+v", d)
	}
}
func TestEvidenceIndependence(t *testing.T) {
	for _, mutate := range []func(*candidate, []BoundSource, *model.Assessment){
		func(c *candidate, b []BoundSource, a *model.Assessment) {
			b[1].Block.Author = "ALICE"
			c.Evidence[1].AuthorRef = ptr("ALICE")
		},
		func(c *candidate, b []BoundSource, a *model.Assessment) { b[1].Document.Site = b[0].Document.Site },
		func(c *candidate, b []BoundSource, a *model.Assessment) { a.Evidence[1].Independent = false },
		func(c *candidate, b []BoundSource, a *model.Assessment) { a.Evidence[1].PaymentQuote = "fabricated" },
		func(c *candidate, b []BoundSource, a *model.Assessment) { c.Evidence[1].PublishedAt = nil },
		func(c *candidate, b []BoundSource, a *model.Assessment) { a.DistinctFromExisting = false },
	} {
		c, b, a := qualificationFixture()
		mutate(&c, b, &a)
		if d := Evaluate(c, b, a, fixtureNow); d.Outcome == "qualified" {
			t.Fatalf("insufficient qualified %+v", d)
		}
	}
}
