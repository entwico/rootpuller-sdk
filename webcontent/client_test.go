package webcontent_test

import (
	"bytes"
	"errors"
	"testing"

	rootpuller "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/apierror"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
	"github.com/entwico/rootpuller-sdk/webcontent"
)

func newClient(t *testing.T, fake *rootpullertest.WebContent) *rootpuller.Client {
	t.Helper()
	srv := rootpullertest.NewServer(t, fake)
	c, err := rootpuller.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestFetchBuffersEverything(t *testing.T) {
	markdown := bytes.Repeat([]byte("# heading\ntext\n"), 300_000) // ~4.2 MiB → 3 chunks
	c := newClient(t, &rootpullertest.WebContent{
		Artifacts: map[webcontent.ArtifactKind][]byte{
			webcontent.ArtifactKindExtractedMarkdown: markdown,
			webcontent.ArtifactKindExtractedText:     []byte("plain"),
		},
		KindErrors: map[webcontent.ArtifactKind]*webcontent.ContentError{
			webcontent.ArtifactKindScreenshotPNG: {Code: webcontent.ErrorCodeBrowserError, Message: "no browser"},
		},
	})

	result, err := c.WebContent().Fetch(t.Context(), &webcontent.FetchURLRequest{
		URL: "https://example.com/article",
		Jobs: []webcontent.ExtractionJob{
			{Kind: webcontent.ArtifactKindExtractedMarkdown},
			{Kind: webcontent.ArtifactKindExtractedText},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Page.FinalURL != "https://example.com/article" || result.Page.StatusCode != 200 {
		t.Errorf("unexpected page metadata: %+v", result.Page)
	}
	if !bytes.Equal(result.Artifacts[webcontent.ArtifactKindExtractedMarkdown], markdown) {
		t.Errorf("markdown artifact mismatch: got %d bytes, want %d",
			len(result.Artifacts[webcontent.ArtifactKindExtractedMarkdown]), len(markdown))
	}
	if string(result.Artifacts[webcontent.ArtifactKindExtractedText]) != "plain" {
		t.Errorf("text artifact = %q", result.Artifacts[webcontent.ArtifactKindExtractedText])
	}
	ke := result.KindErrors[webcontent.ArtifactKindScreenshotPNG]
	if ke == nil || ke.Code != webcontent.ErrorCodeBrowserError {
		t.Errorf("kind error = %+v", ke)
	}
}

func TestFetchURLTerminalError(t *testing.T) {
	c := newClient(t, &rootpullertest.WebContent{
		FetchError: &webcontent.ContentError{
			Code:       webcontent.ErrorCodePaywall,
			Message:    "subscription required",
			HTTPStatus: 402,
			Retryable:  false,
		},
	})

	var lastErr error
	for _, err := range c.WebContent().FetchURL(t.Context(), &webcontent.FetchURLRequest{URL: "https://example.com"}) {
		lastErr = err
	}
	var ce *webcontent.ContentError
	if !errors.As(lastErr, &ce) {
		t.Fatalf("err = %#v, want *webcontent.ContentError", lastErr)
	}
	if ce.Code != webcontent.ErrorCodePaywall || ce.HTTPStatus != 402 {
		t.Errorf("unexpected content error: %+v", ce)
	}
}

func TestFetchURLMissingTerminal(t *testing.T) {
	c := newClient(t, &rootpullertest.WebContent{OmitTerminal: true})

	var lastErr error
	for _, err := range c.WebContent().FetchURL(t.Context(), &webcontent.FetchURLRequest{URL: "https://example.com"}) {
		lastErr = err
	}
	if !errors.Is(lastErr, apierror.ErrMissingTerminal) {
		t.Fatalf("err = %v, want ErrMissingTerminal", lastErr)
	}
}

func TestExtractContentRoundTrip(t *testing.T) {
	html := bytes.Repeat([]byte("<p>hello</p>"), 500_000) // ~5.7 MiB → multi-chunk upload
	var gotKind webcontent.ArtifactKind
	var gotLen int
	c := newClient(t, &rootpullertest.WebContent{
		ExtractFunc: func(kind webcontent.ArtifactKind, uploaded []byte) (map[webcontent.ArtifactKind][]byte, error) {
			gotKind = kind
			gotLen = len(uploaded)
			return map[webcontent.ArtifactKind][]byte{
				webcontent.ArtifactKindExtractedMarkdown: []byte("hello"),
			}, nil
		},
	})

	result, err := c.WebContent().ExtractContent(t.Context(), &webcontent.ExtractRequest{
		Jobs:     []webcontent.ExtractionJob{{Kind: webcontent.ArtifactKindExtractedMarkdown}},
		Document: webcontent.HTMLDocument{Kind: webcontent.ArtifactKindHTMLRendered, Content: bytes.NewReader(html)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotKind != webcontent.ArtifactKindHTMLRendered {
		t.Errorf("server saw kind %q, want html-rendered", gotKind)
	}
	if gotLen != len(html) {
		t.Errorf("server got %d bytes, want %d", gotLen, len(html))
	}
	if string(result.Artifacts[webcontent.ArtifactKindExtractedMarkdown]) != "hello" {
		t.Errorf("unexpected artifacts: %v", result.Artifacts)
	}
	if result.Summary.TotalBytes != int64(len(html)) {
		t.Errorf("summary total bytes = %d, want %d", result.Summary.TotalBytes, len(html))
	}
}

func TestExtractContentTerminalContentError(t *testing.T) {
	c := newClient(t, &rootpullertest.WebContent{
		ExtractFunc: func(webcontent.ArtifactKind, []byte) (map[webcontent.ArtifactKind][]byte, error) {
			return nil, &webcontent.ContentError{Code: webcontent.ErrorCodeExtractionNoContent}
		},
	})

	_, err := c.WebContent().ExtractContent(t.Context(), &webcontent.ExtractRequest{
		Document: webcontent.HTMLDocument{Content: bytes.NewReader([]byte("<p>x</p>"))},
	})
	var ce *webcontent.ContentError
	if !errors.As(err, &ce) || ce.Code != webcontent.ErrorCodeExtractionNoContent {
		t.Fatalf("err = %#v, want ContentError EXTRACTION_NO_CONTENT", err)
	}
}

func TestExtractContentInvalidKindFailsLocally(t *testing.T) {
	c, err := rootpuller.New("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.WebContent().ExtractContent(t.Context(), &webcontent.ExtractRequest{
		Document: webcontent.HTMLDocument{Kind: webcontent.ArtifactKindExtractedMarkdown, Content: bytes.NewReader(nil)},
	})
	if !errors.Is(err, apierror.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}
