package rootpullertest

import (
	"context"
	"net/http"

	"connectrpc.com/connect"

	searchpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/search"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/search/searchconnect"
	"github.com/entwico/rootpuller-sdk/search"
)

// Search is a facade-typed fake SearchService. SearchFunc receives the
// query and returns the hits; a nil SearchFunc returns one canned result
// for the query. The response echoes the request's provider.
type Search struct {
	SearchFunc func(query string) ([]search.Result, error)
}

func (f *Search) register(mux *http.ServeMux) {
	mux.Handle(searchconnect.NewSearchServiceHandler(&searchHandler{fake: f}))
}

type searchHandler struct {
	searchconnect.UnimplementedSearchServiceHandler

	fake *Search
}

func (h *searchHandler) Search(_ context.Context, req *connect.Request[searchpb.SearchRequest]) (*connect.Response[searchpb.SearchResponse], error) {
	searchFunc := h.fake.SearchFunc
	if searchFunc == nil {
		searchFunc = func(query string) ([]search.Result, error) {
			return []search.Result{{
				Title:   "Result for " + query,
				URL:     "https://example.com/",
				Snippet: query,
			}}, nil
		}
	}

	results, err := searchFunc(req.Msg.GetQuery())
	if err != nil {
		return nil, err
	}

	resp := &searchpb.SearchResponse{Provider: req.Msg.GetProvider()}
	for _, r := range results {
		resp.Results = append(resp.Results, &searchpb.SearchResult{
			Title:        r.Title,
			Url:          r.URL,
			Snippet:      r.Snippet,
			PublishedAt:  r.PublishedAt,
			ThumbnailUrl: r.ThumbnailURL,
			SourceUrl:    r.SourceURL,
		})
	}

	return connect.NewResponse(resp), nil
}
