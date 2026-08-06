// Package search wraps com.entwico.rootpuller.search.SearchService: web,
// news, image, and video search through pluggable providers (Brave,
// Serper) behind one provider-neutral surface.
package search

import (
	"context"

	"connectrpc.com/connect"

	searchpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/search"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/search/searchconnect"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// Client calls the SearchService. Obtain one from
// rootpuller.Client.Search.
type Client struct {
	rpc searchconnect.SearchServiceClient
}

// NewFromCore is the internal constructor used by rootpuller.New.
func NewFromCore(core *transport.Core) *Client {
	return &Client{rpc: searchconnect.NewSearchServiceClient(core.HTTPClient, core.BaseURL, core.ClientOpts...)}
}

// Search calls SearchService/Search: runs the query against the selected
// provider and returns the hits in provider ranking order.
func (c *Client) Search(ctx context.Context, req *Request) (*Response, error) {
	provider, err := req.Provider.toProto()
	if err != nil {
		return nil, err
	}

	searchType, err := req.Type.toProto()
	if err != nil {
		return nil, err
	}

	freshness, err := req.Freshness.toProto()
	if err != nil {
		return nil, err
	}

	msg := &searchpb.SearchRequest{
		Provider:   provider,
		Query:      req.Query,
		Type:       searchType,
		MaxResults: protoconv.Int32Ptr(req.MaxResults),
		Country:    req.Country,
		Language:   req.Language,
		Freshness:  freshness,
	}

	resp, err := c.rpc.Search(ctx, connect.NewRequest(msg))
	if err != nil {
		return nil, transport.WrapError(err, searchconnect.SearchServiceSearchProcedure)
	}

	results := resp.Msg.GetResults()

	out := &Response{
		Provider: providerFromProto(resp.Msg.GetProvider()),
		Results:  make([]Result, len(results)),
	}
	for i, r := range results {
		out.Results[i] = resultFromProto(r)
	}

	return out, nil
}
