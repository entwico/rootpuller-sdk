package chef_test

import (
	"errors"
	"reflect"
	"testing"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/chef"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
)

func newService(t *testing.T, baseURL string) *chef.Service {
	t.Helper()

	sdk, err := rootpullersdk.New(baseURL)
	if err != nil {
		t.Fatal(err)
	}

	return chef.NewService(sdk)
}

func TestProcessTextRoundTrip(t *testing.T) {
	t.Parallel()

	var gotText string

	srv := rootpullertest.NewServer(t, &rootpullertest.Chef{
		ProcessTextFunc: func(text string) (*chef.TextResult, error) {
			gotText = text

			return &chef.TextResult{Content: "cleaned: " + text, ID: "doc-42"}, nil
		},
	})

	result, err := newService(t, srv.URL).ProcessText(t.Context(), "raw text")
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
	t.Parallel()

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

	result, err := newService(t, srv.URL).ProcessText(t.Context(), "x")
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(result.Metadata, metadata) {
		t.Errorf("Metadata = %#v, want %#v", result.Metadata, metadata)
	}
}

func TestProcessTableRoundTrip(t *testing.T) {
	t.Parallel()

	tables := []chef.MarkdownTable{
		{Content: "| a |\n| 1 |", Alias: new("table-0"), StartIndex: 4, EndIndex: 15, TokenCount: 6},
		{Content: "| b |\n| 2 |", StartIndex: 20, EndIndex: 31, TokenCount: 6},
	}
	srv := rootpullertest.NewServer(t, &rootpullertest.Chef{Tables: tables})

	got, err := newService(t, srv.URL).ProcessTable(t.Context(), "a,b\n1,2",
		&chef.TableOptions{SourceType: chef.TableSourceTypeCSV})
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(got, tables) {
		t.Errorf("tables = %#v, want %#v", got, tables)
	}
}

func TestProcessMarkdownRoundTrip(t *testing.T) {
	t.Parallel()

	var gotText string

	want := &chef.MarkdownResult{
		Content: "processed",
		ID:      "md-7",
		Tables: []chef.MarkdownTable{
			{Content: "| x |", Alias: new("t0"), StartIndex: 1, EndIndex: 6, TokenCount: 3},
		},
		CodeBlocks: []chef.CodeBlock{
			{Content: "fmt.Println()", Language: new("go"), StartIndex: 10, EndIndex: 23, TokenCount: 4},
			{Content: "plain", StartIndex: 30, EndIndex: 35, TokenCount: 1},
		},
		Images: []chef.ImageRef{
			{URL: "https://example.com/a.png", AltText: new("diagram"), StartIndex: 40, EndIndex: 60},
		},
		Chunks: []rootpullersdk.TextChunk{
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

	got, err := newService(t, srv.URL).ProcessMarkdown(t.Context(), "# heading",
		&chef.MarkdownOptions{Tokenizer: "word"})
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
	t.Parallel()

	tokenizers := []chef.TokenizerInfo{
		{ID: "character", Type: "builtin", Description: "Character tokenizer"},
		{ID: "gpt2", Type: "huggingface", Description: "GPT-2 BPE"},
	}
	srv := rootpullertest.NewServer(t, &rootpullertest.Chef{Tokenizers: tokenizers})

	got, err := newService(t, srv.URL).ListTokenizers(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(got, tokenizers) {
		t.Errorf("tokenizers = %#v, want %#v", got, tokenizers)
	}
}

func TestInvalidTableSourceTypeFailsLocally(t *testing.T) {
	t.Parallel()

	// No server: local validation must fail before any dial.
	svc := newService(t, "http://127.0.0.1:1")

	_, err := svc.ProcessTable(t.Context(), "a,b", &chef.TableOptions{SourceType: chef.TableSourceType("bogus")})
	if !errors.Is(err, rootpullersdk.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}
