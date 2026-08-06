package search

import (
	"fmt"
	"strings"

	"connectrpc.com/connect"

	"github.com/entwico/rootpuller-sdk/apierror"
	searchpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/search"
)

// Provider selects the search backend. The zero value lets the gateway
// pick its configured default backend.
type Provider string

const (
	ProviderDefault Provider = ""
	ProviderBrave   Provider = "brave"  // Brave Search API: independent index, fast, privacy-friendly
	ProviderSerper  Provider = "serper" // Serper.dev: Google SERP results
)

var providerToProto = map[Provider]searchpb.Provider{
	ProviderDefault: searchpb.Provider_PROVIDER_UNSPECIFIED,
	ProviderBrave:   searchpb.Provider_PROVIDER_BRAVE,
	ProviderSerper:  searchpb.Provider_PROVIDER_SERPER,
}

func (p Provider) toProto() (searchpb.Provider, error) {
	v, ok := providerToProto[p]
	if !ok {
		return 0, invalidArgument(fmt.Sprintf("unknown provider %q", p))
	}
	return v, nil
}

func providerFromProto(v searchpb.Provider) Provider {
	switch v {
	case searchpb.Provider_PROVIDER_UNSPECIFIED:
		return ProviderDefault
	case searchpb.Provider_PROVIDER_BRAVE:
		return ProviderBrave
	case searchpb.Provider_PROVIDER_SERPER:
		return ProviderSerper
	default:
		// Unknown value from a newer server: preserve it in the same
		// style as the constants above.
		name := strings.TrimPrefix(v.String(), "PROVIDER_")
		return Provider(strings.ReplaceAll(strings.ToLower(name), "_", "-"))
	}
}

// Type is the kind of search to perform. The zero value keeps the server
// default (web).
type Type string

const (
	TypeDefault Type = ""
	TypeWeb     Type = "web"    // general web search
	TypeNews    Type = "news"   // news articles, ranked by recency and relevance
	TypeImages  Type = "images" // image search
	TypeVideos  Type = "videos" // video search
)

func (t Type) toProto() (*searchpb.SearchType, error) {
	switch t {
	case TypeDefault:
		return nil, nil
	case TypeWeb:
		v := searchpb.SearchType_SEARCH_TYPE_WEB
		return &v, nil
	case TypeNews:
		v := searchpb.SearchType_SEARCH_TYPE_NEWS
		return &v, nil
	case TypeImages:
		v := searchpb.SearchType_SEARCH_TYPE_IMAGES
		return &v, nil
	case TypeVideos:
		v := searchpb.SearchType_SEARCH_TYPE_VIDEOS
		return &v, nil
	default:
		return nil, invalidArgument(fmt.Sprintf("unknown search type %q", t))
	}
}

// Freshness restricts results to pages discovered within the given
// period. The zero value applies no freshness filter.
type Freshness string

const (
	FreshnessDefault Freshness = ""
	FreshnessDay     Freshness = "day"
	FreshnessWeek    Freshness = "week"
	FreshnessMonth   Freshness = "month"
	FreshnessYear    Freshness = "year"
)

func (f Freshness) toProto() (*searchpb.Freshness, error) {
	switch f {
	case FreshnessDefault:
		return nil, nil
	case FreshnessDay:
		v := searchpb.Freshness_FRESHNESS_DAY
		return &v, nil
	case FreshnessWeek:
		v := searchpb.Freshness_FRESHNESS_WEEK
		return &v, nil
	case FreshnessMonth:
		v := searchpb.Freshness_FRESHNESS_MONTH
		return &v, nil
	case FreshnessYear:
		v := searchpb.Freshness_FRESHNESS_YEAR
		return &v, nil
	default:
		return nil, invalidArgument(fmt.Sprintf("unknown freshness %q", f))
	}
}

func invalidArgument(msg string) error {
	return apierror.New(connect.CodeInvalidArgument, msg, "", 0, nil)
}

// Request configures Search. Nil pointer and zero-value enum fields keep
// the provider or gateway defaults noted per field.
type Request struct {
	Provider Provider
	// Query is the search text, plain. Required.
	Query string
	Type  Type
	// MaxResults caps the number of results; the gateway clamps it to
	// the provider's supported range (provider default, typically 10,
	// when nil).
	MaxResults *int
	// Country localizes results, ISO 3166-1 alpha-2 (e.g. "DE");
	// provider default when nil.
	Country *string
	// Language is the result language, ISO 639-1 (e.g. "de"); provider
	// default when nil.
	Language  *string
	Freshness Freshness
}

// Result is one search hit. Results come back in provider ranking order.
type Result struct {
	Title string
	// URL is the canonical URL: the page for web/news, the media file
	// for images, the watch page for videos.
	URL string
	// Snippet is a short plain-text extract; may be empty for image
	// results.
	Snippet string
	// PublishedAt is the publication date (ISO 8601, date only) when the
	// provider reports one; mostly present for news results.
	PublishedAt *string
	// ThumbnailURL is a preview thumbnail, only set for image and video
	// results.
	ThumbnailURL *string
	// SourceURL is the web page hosting the media, only set for image
	// results (where URL points at the image file itself).
	SourceURL *string
}

func resultFromProto(r *searchpb.SearchResult) Result {
	if r == nil {
		return Result{}
	}
	return Result{
		Title:        r.GetTitle(),
		URL:          r.GetUrl(),
		Snippet:      r.GetSnippet(),
		PublishedAt:  r.PublishedAt,
		ThumbnailURL: r.ThumbnailUrl,
		SourceURL:    r.SourceUrl,
	}
}

// Response is the outcome of Search: the provider that actually served
// the query and its results.
type Response struct {
	Provider Provider
	Results  []Result
}
