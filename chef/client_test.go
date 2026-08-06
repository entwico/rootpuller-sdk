package chef_test

import (
	"errors"
	"reflect"
	"testing"

	"connectrpc.com/connect"

	"github.com/entwico/rootpuller-sdk/apierror"
	"github.com/entwico/rootpuller-sdk/chef"
	"github.com/entwico/rootpuller-sdk/common"
	"github.com/entwico/rootpuller-sdk/internal/transport"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
)

// newClient builds a chef client the same way rootpuller.New does. The
// aggregate client's Chef accessor is wired outside this package, so the
// tests go through NewFromCore directly.
func newClient(t *testing.T, baseURL string) *chef.Client {
	t.Helper()
	httpClient, err := transport.NewHTTPClient(baseURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	return chef.NewFromCore(&transport.Core{
		HTTPClient: httpClient,
		BaseURL:    baseURL,
		ClientOpts: []connect.ClientOption{connect.WithGRPC()},
	})
}

func ptr[T any](v T) *T { return &v }

func TestProcessTextRoundTrip(t *testing.T) {
	var gotText string
	srv := rootpullertest.NewServer(t, &rootpullertest.Chef{
		ProcessTextFunc: func(text string) (*chef.TextResult, error) {
			gotText = text
			return &chef.TextResult{Content: "cleaned: " + text, ID: "doc-42"}, nil
		},
	})

	result, err := newClient(t, srv.URL).ProcessText(t.Context(), "raw text")
	if err != nil {
		t.Fatal(err)
	}
	if gotText != "raw text" {
		t.Errorf("server saw text %q, want %q", gotText, "raw text")
	}
	if result.Content != "cleaned: raw text" {
		t.Errorf("Content = %q, want %q", result.Content, "cleaned: raw text")
	}
	if result.ID != "doc-42" {
		t.Errorf("ID = %q, want doc-42", result.ID)
	}
	if result.Metadata != nil {
		t.Errorf("Metadata = %#v, want nil (server sent no metadata)", result.Metadata)
	}
}

func TestProcessTextMetadataRoundTrip(t *testing.T) {
	// structpb only represents JSON values: numbers come back as float64,
	// lists as []any, nested objects as map[string]any.
	metadata := map[string]any{
		"source":   "unit-test",
		"score":    0.75,
		"verified": true,
		"tags":     []any{"alpha", "beta", 3.0},
		"nested": map[string]any{
			"depth": 2.0,
			"kind":  "table",
		},
	}
	srv := rootpullertest.NewServer(t, &rootpullertest.Chef{
		ProcessTextFunc: func(text string) (*chef.TextResult, error) {
			return &chef.TextResult{Content: text, ID: "doc-1", Metadata: metadata}, nil
		},
	})

	result, err := newClient(t, srv.URL).ProcessText(t.Context(), "x")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Metadata, metadata) {
		t.Errorf("Metadata = %#v, want %#v", result.Metadata, metadata)
	}
}

func TestProcessTableRoundTrip(t *testing.T) {
	tables := []chef.MarkdownTable{
		{Content: "| a |\n| 1 |", Alias: ptr("table-0"), StartIndex: 4, EndIndex: 15, TokenCount: 6},
		{Content: "| b |\n| 2 |", StartIndex: 20, EndIndex: 31, TokenCount: 6},
	}
	srv := rootpullertest.NewServer(t, &rootpullertest.Chef{Tables: tables})

	got, err := newClient(t, srv.URL).ProcessTable(t.Context(), "a,b\n1,2", chef.TableSourceTypeCSV)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, tables) {
		t.Errorf("tables = %#v, want %#v", got, tables)
	}
}

func TestProcessMarkdownRoundTrip(t *testing.T) {
	var gotText string
	want := &chef.MarkdownResult{
		Content: "processed",
		ID:      "md-7",
		Tables: []chef.MarkdownTable{
			{Content: "| x |", Alias: ptr("t0"), StartIndex: 1, EndIndex: 6, TokenCount: 3},
		},
		CodeBlocks: []chef.CodeBlock{
			{Content: "fmt.Println()", Language: ptr("go"), StartIndex: 10, EndIndex: 23, TokenCount: 4},
			{Content: "plain", StartIndex: 30, EndIndex: 35, TokenCount: 1},
		},
		Images: []chef.ImageRef{
			{URL: "https://example.com/a.png", AltText: ptr("diagram"), StartIndex: 40, EndIndex: 60},
		},
		Chunks: []common.TextChunk{
			{Text: "first", EndIndex: 5, TokenCount: 2},
			{Text: "second", StartIndex: 5, EndIndex: 11, TokenCount: 3},
		},
		Metadata: map[string]any{"pages": 2.0},
	}
	srv := rootpullertest.NewServer(t, &rootpullertest.Chef{
		ProcessMarkdownFunc: func(text string) (*chef.MarkdownResult, error) {
			gotText = text
			return want, nil
		},
	})

	got, err := newClient(t, srv.URL).ProcessMarkdown(t.Context(), &chef.MarkdownRequest{
		Text:      "# heading",
		Tokenizer: ptr("word"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotText != "# heading" {
		t.Errorf("server saw text %q, want %q", gotText, "# heading")
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("result = %#v, want %#v", got, want)
	}
}

func TestListTokenizers(t *testing.T) {
	tokenizers := []chef.TokenizerInfo{
		{ID: "character", Type: "builtin", Description: "Character tokenizer"},
		{ID: "gpt2", Type: "huggingface", Description: "GPT-2 BPE"},
	}
	srv := rootpullertest.NewServer(t, &rootpullertest.Chef{Tokenizers: tokenizers})

	got, err := newClient(t, srv.URL).ListTokenizers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, tokenizers) {
		t.Errorf("tokenizers = %#v, want %#v", got, tokenizers)
	}
}

func TestInvalidTableSourceTypeFailsLocally(t *testing.T) {
	// No server: local validation must fail before any dial.
	c := newClient(t, "http://127.0.0.1:1")
	_, err := c.ProcessTable(t.Context(), "a,b", chef.TableSourceType("bogus"))
	if !errors.Is(err, apierror.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}
