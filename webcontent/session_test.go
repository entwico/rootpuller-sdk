package webcontent_test

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/entwico/rootpuller-sdk/rootpullertest"
	"github.com/entwico/rootpuller-sdk/webcontent"
)

var (
	errForeignContent      = errors.New("got foreign or corrupted content")
	errMetadataURLMismatch = errors.New("metadata URL mismatch")
)

func TestSessionConcurrentFetches(t *testing.T) {
	t.Parallel()

	// The fake handles instructions concurrently and interleaves their
	// response frames, so correct results prove correlation-ID demuxing.
	svc := newScrapeService(t, &rootpullertest.Scrape{
		SessionFunc: func(url string) (map[webcontent.ArtifactKind][]byte, *webcontent.ContentError) {
			return map[webcontent.ArtifactKind][]byte{
				webcontent.ArtifactKindExtractedMarkdown: []byte(strings.Repeat(url+"|", 200_000)), // ~multi-chunk
			}, nil
		},
	})

	session, err := svc.OpenSession(t.Context(), &webcontent.SessionOptions{
		Jobs: []webcontent.ExtractionJob{{Kind: webcontent.ArtifactKindExtractedMarkdown}},
	})
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		if err := session.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	const workers = 4

	var wg sync.WaitGroup

	errs := make([]error, workers)
	for i := range workers {
		wg.Go(func() {
			url := fmt.Sprintf("https://example.com/page-%d", i)

			result, ferr := session.Fetch(t.Context(), url, nil)
			if ferr != nil {
				errs[i] = ferr

				return
			}

			markdown := string(result.Artifacts[webcontent.ArtifactKindExtractedMarkdown])
			if !strings.HasPrefix(markdown, url+"|") || strings.Contains(markdown, "page-"+strconv.Itoa((i+1)%workers)+"|") {
				errs[i] = fmt.Errorf("page %d: %w", i, errForeignContent)

				return
			}

			if result.Page.FinalURL != url {
				errs[i] = fmt.Errorf("page %d metadata URL = %q: %w", i, result.Page.FinalURL, errMetadataURLMismatch)
			}
		})
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("worker %d: %v", i, err)
		}
	}
}

func TestSessionInstructionError(t *testing.T) {
	t.Parallel()

	svc := newScrapeService(t, &rootpullertest.Scrape{
		SessionFunc: func(url string) (map[webcontent.ArtifactKind][]byte, *webcontent.ContentError) {
			if strings.Contains(url, "blocked") {
				return nil, &webcontent.ContentError{Code: webcontent.ErrorCodeBlockedCloudflare, Retryable: false}
			}

			return map[webcontent.ArtifactKind][]byte{webcontent.ArtifactKindExtractedText: []byte("ok")}, nil
		},
	})

	session, err := svc.OpenSession(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = session.Close() }()

	// The failing instruction errors...
	_, err = session.Fetch(t.Context(), "https://example.com/blocked", nil)

	var ce *webcontent.ContentError
	if !errors.As(err, &ce) || ce.Code != webcontent.ErrorCodeBlockedCloudflare {
		t.Fatalf("err = %#v, want ContentError BLOCKED_CLOUDFLARE", err)
	}
	// ...while the session stays usable.
	result, err := session.Fetch(t.Context(), "https://example.com/fine", nil)
	if err != nil {
		t.Fatal(err)
	}

	if string(result.Artifacts[webcontent.ArtifactKindExtractedText]) != "ok" {
		t.Errorf("unexpected artifacts: %v", result.Artifacts)
	}
}

func TestSessionFetchAfterClose(t *testing.T) {
	t.Parallel()

	svc := newScrapeService(t, &rootpullertest.Scrape{})

	session, err := svc.OpenSession(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := session.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = session.Fetch(t.Context(), "https://example.com", nil)
	if !errors.Is(err, webcontent.ErrSessionClosed) {
		t.Fatalf("err = %v, want ErrSessionClosed", err)
	}
}
