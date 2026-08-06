package webcontent_test

import (
	"bytes"
	"errors"
	"slices"
	"testing"

	rootpuller "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/apierror"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
	"github.com/entwico/rootpuller-sdk/webcontent"
)

func newScrapeClient(t *testing.T, fake *rootpullertest.Scrape) *rootpuller.Client {
	t.Helper()
	srv := rootpullertest.NewServer(t, fake)
	c, err := rootpuller.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCrawlPagesDemux(t *testing.T) {
	pageA := bytes.Repeat([]byte("aaaa"), 1<<20) // 4 MiB → multiple chunks
	pageB := bytes.Repeat([]byte("bb"), 1<<20)   // 2 MiB
	c := newScrapeClient(t, &rootpullertest.Scrape{
		Pages: []rootpullertest.CrawlPageFixture{
			{PageID: 1, URL: "https://example.com/a", Artifacts: map[webcontent.ArtifactKind][]byte{
				webcontent.ArtifactKindExtractedMarkdown: pageA,
			}},
			{PageID: 2, URL: "https://example.com/b", Artifacts: map[webcontent.ArtifactKind][]byte{
				webcontent.ArtifactKindExtractedMarkdown: pageB,
			}},
			{PageID: 3, Err: &webcontent.ContentError{Code: webcontent.ErrorCodeHTTPNotFound, HTTPStatus: 404}},
		},
	})

	results := map[int64]webcontent.CrawlPage{}
	for page, err := range c.Scrape().CrawlPages(t.Context(), &webcontent.CrawlRequest{
		Seed: webcontent.PageSeeds{URLs: []string{"https://example.com/a", "https://example.com/b"}},
		Jobs: []webcontent.ExtractionJob{{Kind: webcontent.ArtifactKindExtractedMarkdown}},
	}) {
		if err != nil {
			t.Fatal(err)
		}
		results[page.PageID] = page
	}
	if len(results) != 3 {
		t.Fatalf("got %d pages, want 3", len(results))
	}
	// The fake interleaves chunks across pages, so correct reassembly
	// proves the PageID demux.
	if !bytes.Equal(results[1].Result.Artifacts[webcontent.ArtifactKindExtractedMarkdown], pageA) {
		t.Error("page 1 artifact corrupted by interleaving")
	}
	if !bytes.Equal(results[2].Result.Artifacts[webcontent.ArtifactKindExtractedMarkdown], pageB) {
		t.Error("page 2 artifact corrupted by interleaving")
	}
	if results[1].Result.Page.FinalURL != "https://example.com/a" {
		t.Errorf("page 1 metadata = %+v", results[1].Result.Page)
	}
	if results[3].Err == nil || results[3].Err.Code != webcontent.ErrorCodeHTTPNotFound {
		t.Errorf("page 3 error = %+v", results[3].Err)
	}
}

func TestCrawlRawEventsIncludeProgress(t *testing.T) {
	c := newScrapeClient(t, &rootpullertest.Scrape{
		Pages: []rootpullertest.CrawlPageFixture{{PageID: 1, URL: "https://example.com"}},
	})

	sawProgress := false
	sawDone := false
	for event, err := range c.Scrape().Crawl(t.Context(), &webcontent.CrawlRequest{
		Seed: webcontent.PageSeeds{URLs: []string{"https://example.com"}},
	}) {
		if err != nil {
			t.Fatal(err)
		}
		switch e := event.(type) {
		case webcontent.CrawlProgressEvent:
			sawProgress = true
			if e.PagesQueued != 1 {
				t.Errorf("PagesQueued = %d, want 1", e.PagesQueued)
			}
		case webcontent.PageEvent:
			if _, ok := e.Event.(webcontent.DoneEvent); ok {
				sawDone = true
			}
		}
	}
	if !sawProgress || !sawDone {
		t.Fatalf("sawProgress=%v sawDone=%v, want both", sawProgress, sawDone)
	}
}

func TestCrawlWithoutSeedFailsLocally(t *testing.T) {
	c, err := rootpuller.New("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	var lastErr error
	for _, err := range c.Scrape().Crawl(t.Context(), &webcontent.CrawlRequest{}) {
		lastErr = err
	}
	if !errors.Is(lastErr, apierror.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", lastErr)
	}
}

func TestMapURLs(t *testing.T) {
	warning := "sitemap sparse"
	c := newScrapeClient(t, &rootpullertest.Scrape{
		MapURLs:      []string{"https://e.com/1", "https://e.com/2", "https://e.com/3", "https://e.com/4", "https://e.com/5"},
		MapBatchSize: 2,
		MapWarning:   &warning,
	})

	urls, summary, err := c.Scrape().MapURLs(t.Context(), &webcontent.MapRequest{URL: "https://e.com"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(urls, []string{"https://e.com/1", "https://e.com/2", "https://e.com/3", "https://e.com/4", "https://e.com/5"}) {
		t.Errorf("urls = %v", urls)
	}
	if summary == nil || summary.SitemapsProcessed != 1 || summary.Warning == nil || *summary.Warning != warning {
		t.Errorf("summary = %+v", summary)
	}
}
