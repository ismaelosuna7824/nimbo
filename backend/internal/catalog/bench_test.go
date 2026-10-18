package catalog

import (
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model/modeltest"
)

func benchCatalog(b *testing.B) *Catalog {
	b.Helper()
	c, err := Load()
	if err != nil {
		b.Fatal(err)
	}
	return c
}

func BenchmarkValidate500(b *testing.B) {
	c := benchCatalog(b)
	wf := modeltest.LargeWorkflow(500)
	hclcodec.Normalize(wf)
	// The first call decompresses the aws schema; measure steady state.
	if d := c.Validate(wf); len(d) > 0 {
		for _, x := range d {
			if x.Severity == model.SeverityError {
				b.Fatalf("error diagnostic: %+v", x)
			}
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		c.Validate(wf)
	}
}

func BenchmarkSearch(b *testing.B) {
	c := benchCatalog(b)
	for _, bc := range []struct {
		name string
		q    Query
	}{
		{"empty", Query{}},
		{"word", Query{Text: "bucket"}},
		{"multiword", Query{Text: "aws lambda function"}},
		{"prefix", Query{Text: "sec gr", Cloud: model.CloudAWS}},
		{"supported", Query{Text: "queue", SupportedOnly: true}},
	} {
		c.Search(bc.q) // build summaries outside the timer
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				c.Search(bc.q)
			}
		})
	}
}
