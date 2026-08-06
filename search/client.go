// Package search wraps com.entwico.rootpuller.search.SearchService: web,
// news, image, and video search through pluggable providers (Brave,
// Serper) behind one provider-neutral surface.
package search

import (
	"context"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	searchpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/search"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/search/searchconnect"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// Service calls the SearchService.
type Service struct {
	rpc               searchconnect.SearchServiceClient
	defaultProvider   Provider
	defaultMaxResults int
}

// Option configures a Service at construction.
type Option func(*Service)

// WithDefaultProvider sets the search backend used when a call's Options
// leave Provider empty.
func WithDefaultProvider(p Provider) Option {
	return func(s *Service) { s.defaultProvider = p }
}

// WithDefaultMaxResults sets the result cap used when a call's Options
// leave MaxResults zero.
func WithDefaultMaxResults(n int) Option {
	return func(s *Service) { s.defaultMaxResults = n }
}

// NewService builds a SearchService client on the sdk connection.
func NewService(sdk *rootpullersdk.Client, opts ...Option) *Service {
	core := sdk.TransportCore()

	s := &Service{rpc: searchconnect.NewSearchServiceClient(core.HTTPClient, core.BaseURL, core.ClientOpts...)}
	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Options tunes a Search call. Nil keeps all defaults.
type Options struct {
	// Provider selects the search backend; the zero value falls back to
	// the service default (WithDefaultProvider), then the gateway's
	// configured backend.
	Provider Provider
	// Type is the kind of search; the zero value keeps the server
	// default (web).
	Type Type
	// MaxResults caps the number of results; the gateway clamps it to
	// the provider's supported range. Zero falls back to the service
	// default (WithDefaultMaxResults), then the provider default
	// (typically 10).
	MaxResults int
	// Country localizes results, ISO 3166-1 alpha-2 (e.g. "DE");
	// provider default when empty.
	Country string
	// Language is the result language, ISO 639-1 (e.g. "de"); provider
	// default when empty.
	Language string
	// Freshness restricts results to pages discovered within the given
	// period; the zero value applies no freshness filter.
	Freshness Freshness
}

// Search calls SearchService/Search: runs the query against the selected
// provider and returns the hits in provider ranking order.
func (s *Service) Search(ctx context.Context, query string, opts *Options) (*Response, error) {
	if opts == nil {
		opts = &Options{}
	}

	prov := opts.Provider
	if prov == "" {
		prov = s.defaultProvider
	}

	provider, err := prov.toProto()
	if err != nil {
		return nil, err
	}

	searchType, err := opts.Type.toProto()
	if err != nil {
		return nil, err
	}

	freshness, err := opts.Freshness.toProto()
	if err != nil {
		return nil, err
	}

	msg := &searchpb.SearchRequest{
		Provider:  provider,
		Query:     query,
		Type:      searchType,
		Freshness: freshness,
	}

	maxResults := opts.MaxResults
	if maxResults == 0 {
		maxResults = s.defaultMaxResults
	}

	if maxResults > 0 {
		msg.MaxResults = protoconv.Int32Ptr(&maxResults)
	}

	if opts.Country != "" {
		msg.Country = &opts.Country
	}

	if opts.Language != "" {
		msg.Language = &opts.Language
	}

	resp, err := s.rpc.Search(ctx, connect.NewRequest(msg))
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
