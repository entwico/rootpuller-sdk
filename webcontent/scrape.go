package webcontent

// This file wraps com.entwico.rootpuller.webcontent.ScrapeService, which
// shares the webcontent proto package and event vocabulary. The scrape
// client therefore lives here rather than in a package of its own.

import (
	"context"
	"iter"
	"time"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/internal/apierr"
	webcontentpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/webcontent"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/webcontent/webcontentconnect"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
	"github.com/entwico/rootpuller-sdk/internal/streamio"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// ScrapeService calls the ScrapeService: multi-page crawling, sitemap
// mapping, and interactive fetch sessions.
type ScrapeService struct {
	rpc webcontentconnect.ScrapeServiceClient
	bot string
}

// NewScrapeService builds a ScrapeService client on the sdk connection.
// It accepts the same options as NewService (e.g. WithBot).
func NewScrapeService(sdk *rootpullersdk.Client, opts ...Option) *ScrapeService {
	core := sdk.TransportCore()

	s := &ScrapeService{rpc: webcontentconnect.NewScrapeServiceClient(core.HTTPClient, core.BaseURL, core.ClientOpts...)}
	for _, opt := range opts {
		opt.applyScrape(s)
	}

	return s
}

// LinkExtraction controls which links a crawl follows.
type LinkExtraction struct {
	CSSSelectors   []string
	XPathSelectors []string
	AllowURLRegex  []string // whitelist; empty disables
	DenyURLRegex   []string // applied after allow
	DenyExtensions []string
}

func (l *LinkExtraction) toProto() *webcontentpb.LinkExtraction {
	if l == nil {
		return nil
	}

	return &webcontentpb.LinkExtraction{
		CssSelectors:   l.CSSSelectors,
		XpathSelectors: l.XPathSelectors,
		AllowUrlRegex:  l.AllowURLRegex,
		DenyUrlRegex:   l.DenyURLRegex,
		DenyExtensions: l.DenyExtensions,
	}
}

// Seed provides the crawl start set. Implementations: PageSeeds,
// SitemapSeeds.
type Seed interface{ isSeed() }

// PageSeeds starts a crawl from explicit page URLs.
type PageSeeds struct {
	URLs []string
}

func (PageSeeds) isSeed() {}

// SitemapSeeds starts a crawl from sitemaps.
type SitemapSeeds struct {
	// URLs are sitemap URLs to process directly.
	URLs []string
	// DiscoverFrom are site URLs whose robots.txt/sitemap.xml are probed.
	DiscoverFrom   []string
	Follow         *LinkExtraction
	AlternateLinks *bool
}

func (SitemapSeeds) isSeed() {}

// CrawlRules bounds a crawl.
type CrawlRules struct {
	AllowedDomains       []string
	DenyDomains          []string
	MaxDepth             *int // 0 = seeds only; default 1
	MaxPages             *int
	ObeyRobotsTxt        *bool
	PerDomainConcurrency *int
	DownloadDelay        *time.Duration // per-origin minimum delay
	LinkExtraction       *LinkExtraction
	NormalizeURLs        *bool
	PathPrefixes         []string
}

// CrawlOptions tunes Crawl and CrawlPages. Nil keeps all defaults.
type CrawlOptions struct {
	Fetcher    *FetcherOptions
	Rules      *CrawlRules
	Jobs       []ExtractionJob
	Screenshot *ScreenshotOptions
}

func (o *CrawlOptions) toProto(seed Seed) (*webcontentpb.CrawlRequest, error) {
	if o == nil {
		o = &CrawlOptions{}
	}

	fetcher, err := o.Fetcher.toProto()
	if err != nil {
		return nil, err
	}

	jobs, err := jobsToProto(o.Jobs)
	if err != nil {
		return nil, err
	}

	screenshot, err := o.Screenshot.toProto()
	if err != nil {
		return nil, err
	}

	msg := &webcontentpb.CrawlRequest{Fetcher: fetcher, Jobs: jobs, Screenshot: screenshot}

	switch seed := seed.(type) {
	case PageSeeds:
		msg.Seed = &webcontentpb.CrawlRequest_Pages{Pages: &webcontentpb.CrawlRequest_PageSeeds{Urls: seed.URLs}}
	case SitemapSeeds:
		msg.Seed = &webcontentpb.CrawlRequest_Sitemap{Sitemap: &webcontentpb.CrawlRequest_SitemapSeeds{
			Urls:           seed.URLs,
			DiscoverFrom:   seed.DiscoverFrom,
			Follow:         seed.Follow.toProto(),
			AlternateLinks: seed.AlternateLinks,
		}}
	case nil:
		return nil, invalidArgument("crawl needs a seed (PageSeeds or SitemapSeeds)")
	default:
		return nil, invalidArgument("unknown seed type")
	}

	if rules := o.Rules; rules != nil {
		msg.Rules = &webcontentpb.CrawlRequest_Rules{
			AllowedDomains:       rules.AllowedDomains,
			DenyDomains:          rules.DenyDomains,
			MaxDepth:             protoconv.Int32Ptr(rules.MaxDepth),
			MaxPages:             protoconv.Int32Ptr(rules.MaxPages),
			ObeyRobotsTxt:        rules.ObeyRobotsTxt,
			PerDomainConcurrency: protoconv.Int32Ptr(rules.PerDomainConcurrency),
			DownloadDelayMs:      protoconv.DurationToMsPtr(rules.DownloadDelay),
			LinkExtraction:       rules.LinkExtraction.toProto(),
			NormalizeUrls:        rules.NormalizeURLs,
			PathPrefixes:         rules.PathPrefixes,
		}
	}

	return msg, nil
}

// CrawlEvent is one frame of a crawl stream. Concrete types: PageEvent,
// CrawlProgressEvent.
type CrawlEvent interface{ isCrawlEvent() }

// PageEvent carries one page-scoped event. Event is nil exactly when Err
// is set: a terminal error frame for that page (the crawl continues).
// A DoneEvent marks the page's successful completion.
type PageEvent struct {
	PageID int64
	Event  Event
	Err    *ContentError
}

func (PageEvent) isCrawlEvent() {}

// CrawlProgressEvent reports crawl-wide progress.
type CrawlProgressEvent struct {
	PagesEmitted    int64
	PagesQueued     int64
	PagesFailed     int64
	MaxDepthReached int
}

func (CrawlProgressEvent) isCrawlEvent() {}

// Crawl calls ScrapeService/Crawl: crawls from the seeds and streams
// page-scoped events (demultiplex by PageID) interleaved with crawl
// progress. The stream ends when the crawl completes; per-page failures
// arrive as PageEvent.Err, not as iteration errors. For per-page buffered
// results use CrawlPages. A nil opts keeps all defaults.
func (s *ScrapeService) Crawl(ctx context.Context, seed Seed, opts *CrawlOptions) iter.Seq2[CrawlEvent, error] {
	ctx = transport.EnsureBot(ctx, s.bot)

	msg, err := opts.toProto(seed)
	if err != nil {
		return errSeq[CrawlEvent](err)
	}

	stream, serr := s.rpc.Crawl(ctx, connect.NewRequest(msg))
	if serr != nil {
		return errSeq[CrawlEvent](transport.WrapError(serr, webcontentconnect.ScrapeServiceCrawlProcedure))
	}

	return streamio.EventSeq(stream, webcontentconnect.ScrapeServiceCrawlProcedure, nil,
		func(pb *webcontentpb.CrawlResponse) (CrawlEvent, bool, error) {
			switch m := pb.GetMessage().(type) {
			case *webcontentpb.CrawlResponse_Page:
				return pageEventFromProto(m.Page), false, nil
			case *webcontentpb.CrawlResponse_Progress_:
				return CrawlProgressEvent{
					PagesEmitted:    m.Progress.GetPagesEmitted(),
					PagesQueued:     m.Progress.GetPagesQueued(),
					PagesFailed:     m.Progress.GetPagesFailed(),
					MaxDepthReached: int(m.Progress.GetMaxDepthReached()),
				}, false, nil
			default:
				return nil, false, apierr.New(connect.CodeInternal, "unknown Crawl response frame", webcontentconnect.ScrapeServiceCrawlProcedure, 0, nil)
			}
		})
}

func pageEventFromProto(pb *webcontentpb.CrawlResponse_PageEvent) PageEvent {
	event := PageEvent{PageID: pb.GetPageId()}
	switch e := pb.GetEvent().(type) {
	case *webcontentpb.CrawlResponse_PageEvent_FetchProgress:
		event.Event = fetchProgressFromProto(e.FetchProgress)
	case *webcontentpb.CrawlResponse_PageEvent_PageMetadata:
		event.Event = PageMetadataEvent{Metadata: pageMetadataFromProto(e.PageMetadata)}
	case *webcontentpb.CrawlResponse_PageEvent_Artifact:
		event.Event = artifactChunkFromProto(e.Artifact)
	case *webcontentpb.CrawlResponse_PageEvent_ExtractionProgress:
		event.Event = extractionProgressFromProto(e.ExtractionProgress)
	case *webcontentpb.CrawlResponse_PageEvent_ExtractionMetadata:
		event.Event = ExtractionMetadataEvent{Metadata: extractionMetadataFromProto(e.ExtractionMetadata)}
	case *webcontentpb.CrawlResponse_PageEvent_KindError:
		event.Event = kindErrorFromProto(e.KindError)
	case *webcontentpb.CrawlResponse_PageEvent_Error:
		event.Err = contentErrorFromProto(e.Error)
	case *webcontentpb.CrawlResponse_PageEvent_Done:
		event.Event = DoneEvent{Summary: summaryFromProto(e.Done)}
	}

	return event
}

// CrawlPage is one completed page of CrawlPages: either Result or Err is
// set.
type CrawlPage struct {
	PageID int64
	Result *FetchResult
	Err    *ContentError
}

// CrawlPages calls ScrapeService/Crawl and yields each page fully
// buffered once its terminal frame arrives. Crawl progress frames are
// dropped; use Crawl for full control. A nil opts keeps all defaults.
func (s *ScrapeService) CrawlPages(ctx context.Context, seed Seed, opts *CrawlOptions) iter.Seq2[CrawlPage, error] {
	return func(yield func(CrawlPage, error) bool) {
		pending := map[int64]*FetchResult{}

		for event, err := range s.Crawl(ctx, seed, opts) {
			if err != nil {
				yield(CrawlPage{}, err)

				return
			}

			page, ok := event.(PageEvent)
			if !ok {
				continue
			}

			if page.Err != nil {
				delete(pending, page.PageID)

				if !yield(CrawlPage{PageID: page.PageID, Err: page.Err}, nil) {
					return
				}

				continue
			}

			result, ok := pending[page.PageID]
			if !ok {
				result = &FetchResult{}
				pending[page.PageID] = result
			}

			result.Apply(page.Event)

			if _, done := page.Event.(DoneEvent); done {
				delete(pending, page.PageID)

				if !yield(CrawlPage{PageID: page.PageID, Result: result}, nil) {
					return
				}
			}
		}
	}
}

// MapOptions tunes MapURLs. Nil keeps all defaults.
type MapOptions struct {
	// Limit caps the returned URLs (0 keeps the server default, 5000;
	// max 50000).
	Limit int
	// NormalizeURLs canonicalises the returned URLs.
	NormalizeURLs bool
	PathPrefixes  []string
	// IncludeSubdomains also returns URLs on subdomains of the url's host.
	IncludeSubdomains bool
}

// MapSummary is the terminal accounting of a MapURLs call.
type MapSummary struct {
	SitemapsProcessed int
	Warning           *string // set when results look sparse or empty
}

// MapURLs calls ScrapeService/Map: discovers the site's URLs from its
// sitemaps, buffered into one slice. A nil opts keeps all defaults.
func (s *ScrapeService) MapURLs(ctx context.Context, url string, opts *MapOptions) ([]string, *MapSummary, error) {
	ctx = transport.EnsureBot(ctx, s.bot)
	procedure := webcontentconnect.ScrapeServiceMapProcedure

	if opts == nil {
		opts = &MapOptions{}
	}

	msg := &webcontentpb.MapRequest{
		Url:          url,
		PathPrefixes: opts.PathPrefixes,
	}
	if opts.Limit > 0 {
		msg.Limit = protoconv.Int32Ptr(&opts.Limit)
	}

	if opts.NormalizeURLs {
		msg.NormalizeUrls = &opts.NormalizeURLs
	}

	if opts.IncludeSubdomains {
		msg.IncludeSubdomains = &opts.IncludeSubdomains
	}

	stream, err := s.rpc.Map(ctx, connect.NewRequest(msg))
	if err != nil {
		return nil, nil, transport.WrapError(err, procedure)
	}

	var (
		urls    []string
		summary *MapSummary
	)

	seq := streamio.EventSeq(stream, procedure, apierr.ErrMissingTerminal,
		func(pb *webcontentpb.MapResponse) ([]string, bool, error) {
			switch m := pb.GetMessage().(type) {
			case *webcontentpb.MapResponse_Batch:
				return m.Batch.GetUrls(), false, nil
			case *webcontentpb.MapResponse_Done:
				summary = &MapSummary{
					SitemapsProcessed: int(m.Done.GetSitemapsProcessed()),
					Warning:           m.Done.Warning,
				}

				return nil, true, nil
			default:
				return nil, false, apierr.New(connect.CodeInternal, "unknown Map response frame", procedure, 0, nil)
			}
		})
	for batch, err := range seq {
		if err != nil {
			return nil, nil, err
		}

		urls = append(urls, batch...)
	}

	return urls, summary, nil
}
