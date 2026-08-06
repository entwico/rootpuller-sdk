package search_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	searchpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/search"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/search/searchconnect"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
	"github.com/entwico/rootpuller-sdk/search"
)

func newService(t *testing.T, baseURL string, opts ...search.Option) *search.Service {
	t.Helper()

	sdk, err := rootpullersdk.New(baseURL)
	if err != nil {
		t.Fatal(err)
	}

	return search.NewService(sdk, opts...)
}

func TestSearchRoundTrip(t *testing.T) {
	t.Parallel()

	var gotQuery string

	srv := rootpullertest.NewServer(t, &rootpullertest.Search{
		SearchFunc: func(query string) ([]search.Result, error) {
			gotQuery = query

			return []search.Result{
				{
					Title:       "Chunking news",
					URL:         "https://example.com/news",
					Snippet:     "fresh chunks",
					PublishedAt: new("2026-08-01"),
				},
				{Title: "Plain hit", URL: "https://example.com/plain"},
			}, nil
		},
	})

	svc := newService(t, srv.URL)

	resp, err := svc.Search(t.Context(), "chunking", &search.Options{
		Provider:   search.ProviderBrave,
		Type:       search.TypeNews,
		MaxResults: 5,
		Country:    "DE",
		Language:   "de",
		Freshness:  search.FreshnessWeek,
	})
	if err != nil {
		t.Fatal(err)
	}

	if gotQuery != "chunking" {
		t.Errorf("server saw query %q, want %q", gotQuery, "chunking")
	}
	// The fake echoes the request's provider back.
	if resp.Provider != search.ProviderBrave {
		t.Errorf("Provider = %q, want %q", resp.Provider, search.ProviderBrave)
	}

	if len(resp.Results) != 2 {
		t.Fatalf("got %d results, want 2", len(resp.Results))
	}

	first := resp.Results[0]
	if first.Title != "Chunking news" || first.URL != "https://example.com/news" || first.Snippet != "fresh chunks" {
		t.Errorf("unexpected first result: %+v", first)
	}

	if first.PublishedAt == nil || *first.PublishedAt != "2026-08-01" {
		t.Errorf("PublishedAt = %v, want 2026-08-01", first.PublishedAt)
	}

	second := resp.Results[1]
	if second.PublishedAt != nil || second.ThumbnailURL != nil || second.SourceURL != nil {
		t.Errorf("unset optional fields must stay nil: %+v", second)
	}
}

// capturingSearch records the proto request of each Search call.
type capturingSearch struct {
	searchconnect.UnimplementedSearchServiceHandler

	requests chan *searchpb.SearchRequest
}

func (h *capturingSearch) Search(_ context.Context, req *connect.Request[searchpb.SearchRequest]) (*connect.Response[searchpb.SearchResponse], error) {
	h.requests <- req.Msg

	return connect.NewResponse(&searchpb.SearchResponse{}), nil
}

func newCapturingServer(t *testing.T) (*rootpullertest.Server, chan *searchpb.SearchRequest) {
	t.Helper()

	handler := &capturingSearch{requests: make(chan *searchpb.SearchRequest, 1)}
	mux := http.NewServeMux()
	mux.Handle(searchconnect.NewSearchServiceHandler(handler))

	return rootpullertest.NewServerWithMux(t, mux), handler.requests
}

func TestSearchOptionalFieldsRoundTrip(t *testing.T) {
	t.Parallel()

	srv, requests := newCapturingServer(t)
	svc := newService(t, srv.URL)

	// All optional fields set.
	_, err := svc.Search(t.Context(), "q", &search.Options{
		Provider:   search.ProviderSerper,
		Type:       search.TypeImages,
		MaxResults: 7,
		Country:    "DE",
		Language:   "de",
		Freshness:  search.FreshnessMonth,
	})
	if err != nil {
		t.Fatal(err)
	}

	msg := <-requests
	if msg.GetProvider() != searchpb.Provider_PROVIDER_SERPER {
		t.Errorf("Provider = %v, want PROVIDER_SERPER", msg.GetProvider())
	}

	if msg.Type == nil || msg.GetType() != searchpb.SearchType_SEARCH_TYPE_IMAGES {
		t.Errorf("Type = %v, want SEARCH_TYPE_IMAGES", msg.Type)
	}

	if msg.MaxResults == nil || msg.GetMaxResults() != 7 {
		t.Errorf("MaxResults = %v, want 7", msg.MaxResults)
	}

	if msg.Country == nil || msg.GetCountry() != "DE" {
		t.Errorf("Country = %v, want DE", msg.Country)
	}

	if msg.Language == nil || msg.GetLanguage() != "de" {
		t.Errorf("Language = %v, want de", msg.Language)
	}

	if msg.Freshness == nil || msg.GetFreshness() != searchpb.Freshness_FRESHNESS_MONTH {
		t.Errorf("Freshness = %v, want FRESHNESS_MONTH", msg.Freshness)
	}

	// Nil options: optional proto fields must stay unset.
	_, err = svc.Search(t.Context(), "q", nil)
	if err != nil {
		t.Fatal(err)
	}

	msg = <-requests
	if msg.GetProvider() != searchpb.Provider_PROVIDER_UNSPECIFIED {
		t.Errorf("Provider = %v, want PROVIDER_UNSPECIFIED", msg.GetProvider())
	}

	if msg.Type != nil || msg.MaxResults != nil || msg.Country != nil || msg.Language != nil || msg.Freshness != nil {
		t.Errorf("optional fields must be nil for nil options: %+v", msg)
	}
}

func TestServiceDefaults(t *testing.T) {
	t.Parallel()

	srv, requests := newCapturingServer(t)
	svc := newService(t, srv.URL,
		search.WithDefaultProvider(search.ProviderBrave),
		search.WithDefaultMaxResults(3))

	// The construction-time defaults apply when Options leave the fields
	// zero...
	if _, err := svc.Search(t.Context(), "q", nil); err != nil {
		t.Fatal(err)
	}

	msg := <-requests
	if msg.GetProvider() != searchpb.Provider_PROVIDER_BRAVE {
		t.Errorf("Provider = %v, want service default PROVIDER_BRAVE", msg.GetProvider())
	}

	if msg.MaxResults == nil || msg.GetMaxResults() != 3 {
		t.Errorf("MaxResults = %v, want service default 3", msg.MaxResults)
	}

	// ...and per-call values win over the defaults.
	_, err := svc.Search(t.Context(), "q", &search.Options{
		Provider:   search.ProviderSerper,
		MaxResults: 9,
	})
	if err != nil {
		t.Fatal(err)
	}

	msg = <-requests
	if msg.GetProvider() != searchpb.Provider_PROVIDER_SERPER {
		t.Errorf("Provider = %v, want per-call PROVIDER_SERPER", msg.GetProvider())
	}

	if msg.MaxResults == nil || msg.GetMaxResults() != 9 {
		t.Errorf("MaxResults = %v, want per-call 9", msg.MaxResults)
	}
}

func TestInvalidEnumsFailLocally(t *testing.T) {
	t.Parallel()

	// No server: local validation must fail before any dial.
	svc := newService(t, "http://127.0.0.1:1")

	options := map[string]*search.Options{
		"provider":  {Provider: search.Provider("bogus")},
		"type":      {Type: search.Type("bogus")},
		"freshness": {Freshness: search.Freshness("bogus")},
	}
	for name, opts := range options {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := svc.Search(t.Context(), "q", opts)
			if !errors.Is(err, rootpullersdk.ErrInvalidArgument) {
				t.Fatalf("err = %v, want ErrInvalidArgument", err)
			}
		})
	}
}
