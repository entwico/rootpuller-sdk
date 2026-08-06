package search_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"connectrpc.com/connect"

	"github.com/entwico/rootpuller-sdk/apierror"
	searchpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/search"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/search/searchconnect"
	"github.com/entwico/rootpuller-sdk/internal/transport"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
	"github.com/entwico/rootpuller-sdk/search"
)

// newClient dials baseURL the same way rootpuller.New does. The
// rootpuller.Client accessor for this service is wired separately, so the
// tests construct the service client directly from a transport.Core.
func newClient(t *testing.T, baseURL string) *search.Client {
	t.Helper()

	httpClient, err := transport.NewHTTPClient(baseURL, nil)
	if err != nil {
		t.Fatal(err)
	}

	return search.NewFromCore(&transport.Core{
		HTTPClient: httpClient,
		BaseURL:    baseURL,
		ClientOpts: []connect.ClientOption{connect.WithGRPC()},
	})
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

	c := newClient(t, srv.URL)

	resp, err := c.Search(t.Context(), &search.Request{
		Provider:   search.ProviderBrave,
		Query:      "chunking",
		Type:       search.TypeNews,
		MaxResults: new(5),
		Country:    new("DE"),
		Language:   new("de"),
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

func TestSearchOptionalFieldsRoundTrip(t *testing.T) {
	t.Parallel()

	handler := &capturingSearch{requests: make(chan *searchpb.SearchRequest, 1)}
	mux := http.NewServeMux()
	mux.Handle(searchconnect.NewSearchServiceHandler(handler))
	srv := rootpullertest.NewServerWithMux(t, mux)
	c := newClient(t, srv.URL)

	// All optional fields set.
	_, err := c.Search(t.Context(), &search.Request{
		Provider:   search.ProviderSerper,
		Query:      "q",
		Type:       search.TypeImages,
		MaxResults: new(7),
		Country:    new("DE"),
		Language:   new("de"),
		Freshness:  search.FreshnessMonth,
	})
	if err != nil {
		t.Fatal(err)
	}

	msg := <-handler.requests
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

	// Zero values: optional proto fields must stay unset.
	_, err = c.Search(t.Context(), &search.Request{Query: "q"})
	if err != nil {
		t.Fatal(err)
	}

	msg = <-handler.requests
	if msg.GetProvider() != searchpb.Provider_PROVIDER_UNSPECIFIED {
		t.Errorf("Provider = %v, want PROVIDER_UNSPECIFIED", msg.GetProvider())
	}

	if msg.Type != nil || msg.MaxResults != nil || msg.Country != nil || msg.Language != nil || msg.Freshness != nil {
		t.Errorf("optional fields must be nil for zero-value request: %+v", msg)
	}
}

func TestInvalidEnumsFailLocally(t *testing.T) {
	t.Parallel()

	// No server: local validation must fail before any dial.
	c := newClient(t, "http://127.0.0.1:1")

	requests := map[string]*search.Request{
		"provider":  {Query: "q", Provider: search.Provider("bogus")},
		"type":      {Query: "q", Type: search.Type("bogus")},
		"freshness": {Query: "q", Freshness: search.Freshness("bogus")},
	}
	for name, req := range requests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := c.Search(t.Context(), req)
			if !errors.Is(err, apierror.ErrInvalidArgument) {
				t.Fatalf("err = %v, want ErrInvalidArgument", err)
			}
		})
	}
}
