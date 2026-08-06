package rootpullertest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"

	"connectrpc.com/connect"

	webcontentpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/webcontent"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/webcontent/webcontentconnect"
	"github.com/entwico/rootpuller-sdk/webcontent"
)

// CrawlPageFixture is one page a Scrape fake's Crawl emits. When Err is
// set the page ends with an error frame; otherwise its artifacts are
// chunked out and the page ends with done.
type CrawlPageFixture struct {
	PageID    int64
	URL       string
	Artifacts map[webcontent.ArtifactKind][]byte
	Err       *webcontent.ContentError
}

// Scrape is a facade-typed fake ScrapeService.
//
// Crawl emits the Pages with their frames deliberately interleaved
// across pages (metadata for all pages first, then artifact chunks in
// round-robin, then terminals) to exercise client-side PageID demuxing,
// plus one crawl progress frame. Map returns MapURLs in batches of
// MapBatchSize (default 2) followed by a done summary.
type Scrape struct {
	Pages        []CrawlPageFixture
	MapURLs      []string
	MapBatchSize int
	MapWarning   *string

	// SessionFunc handles one FetchSession instruction. It returns the
	// artifacts for the page, or a ContentError ending that instruction
	// with an error frame. A nil SessionFunc echoes the URL as an
	// extracted-markdown artifact. Instructions are processed
	// concurrently and their response frames interleave on the wire,
	// like the real server.
	SessionFunc func(url string) (map[webcontent.ArtifactKind][]byte, *webcontent.ContentError)
}

func (f *Scrape) register(mux *http.ServeMux) {
	mux.Handle(webcontentconnect.NewScrapeServiceHandler(&scrapeHandler{fake: f}))
}

type scrapeHandler struct {
	webcontentconnect.UnimplementedScrapeServiceHandler

	fake *Scrape
}

// Scrape-specific protocol-violation sentinels.
var (
	errClosedBeforeInit     = errors.New("client closed stream before sending Init")
	errFirstMessageNotInit  = errors.New("first message must be Init")
	errSessionReinitialised = errors.New("session already initialised")
	errNilInstruction       = errors.New("instruction must not be nil")
)

func (h *scrapeHandler) Crawl(_ context.Context, _ *connect.Request[webcontentpb.CrawlRequest], stream *connect.ServerStream[webcontentpb.CrawlResponse]) error {
	sendPage := func(pe *webcontentpb.CrawlResponse_PageEvent) error {
		return stream.Send(&webcontentpb.CrawlResponse{
			Message: &webcontentpb.CrawlResponse_Page{Page: pe},
		})
	}

	// Page metadata for every page first.
	for _, page := range h.fake.Pages {
		if page.Err != nil {
			continue
		}

		if err := sendPage(&webcontentpb.CrawlResponse_PageEvent{
			PageId: page.PageID,
			Event: &webcontentpb.CrawlResponse_PageEvent_PageMetadata{
				PageMetadata: &webcontentpb.PageMetadata{RequestedUrl: page.URL, FinalUrl: page.URL, StatusCode: 200},
			},
		}); err != nil {
			return err
		}
	}
	// One crawl-progress frame in the middle.
	if err := stream.Send(&webcontentpb.CrawlResponse{
		Message: &webcontentpb.CrawlResponse_Progress_{Progress: &webcontentpb.CrawlResponse_Progress{
			PagesEmitted: 0, PagesQueued: int64(len(h.fake.Pages)),
		}},
	}); err != nil {
		return err
	}
	// Artifact chunks round-robin across pages to force real demuxing.
	type chunkItem struct {
		pageID int64
		art    *webcontentpb.Artifact
	}

	var perPage [][]chunkItem

	for _, page := range h.fake.Pages {
		if page.Err != nil {
			continue
		}

		var items []chunkItem

		for kind, data := range page.Artifacts {
			_ = sendArtifactChunks(artifactKindToProtoKind(kind), data, func(a *webcontentpb.Artifact) error {
				items = append(items, chunkItem{pageID: page.PageID, art: a})

				return nil
			})
		}

		perPage = append(perPage, items)
	}

	for i := 0; ; i++ {
		sent := false

		for _, items := range perPage {
			if i < len(items) {
				if err := sendPage(&webcontentpb.CrawlResponse_PageEvent{
					PageId: items[i].pageID,
					Event:  &webcontentpb.CrawlResponse_PageEvent_Artifact{Artifact: items[i].art},
				}); err != nil {
					return err
				}

				sent = true
			}
		}

		if !sent {
			break
		}
	}
	// Terminals last.
	for _, page := range h.fake.Pages {
		event := &webcontentpb.CrawlResponse_PageEvent{PageId: page.PageID}
		if page.Err != nil {
			event.Event = &webcontentpb.CrawlResponse_PageEvent_Error{Error: contentErrorToProto(page.Err)}
		} else {
			event.Event = &webcontentpb.CrawlResponse_PageEvent_Done{Done: &webcontentpb.Summary{}}
		}

		if err := sendPage(event); err != nil {
			return err
		}
	}

	return nil
}

func (h *scrapeHandler) FetchSession(_ context.Context, stream *connect.BidiStream[webcontentpb.FetchSessionRequest, webcontentpb.FetchSessionResponse]) error {
	first, err := stream.Receive()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return invalidArgument(errClosedBeforeInit)
		}

		return err
	}

	if first.GetInit() == nil {
		return invalidArgument(errFirstMessageNotInit)
	}

	sessionFunc := h.fake.SessionFunc

	var sendMu sync.Mutex

	send := func(resp *webcontentpb.FetchSessionResponse) error {
		sendMu.Lock()
		defer sendMu.Unlock()

		return stream.Send(resp)
	}

	var wg sync.WaitGroup
	defer wg.Wait()

	for {
		req, rerr := stream.Receive()
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				return nil
			}

			return rerr
		}

		if req.GetInit() != nil {
			return invalidArgument(errSessionReinitialised)
		}

		instr := req.GetInstruction()
		if instr == nil {
			return invalidArgument(errNilInstruction)
		}

		wg.Go(func() {
			cid := instr.GetCorrelationId()
			url := instr.GetUrl()

			var (
				artifacts map[webcontent.ArtifactKind][]byte
				cerr      *webcontent.ContentError
			)
			if sessionFunc != nil {
				artifacts, cerr = sessionFunc(url)
			} else {
				// Default: echo the URL as an extracted-markdown artifact.
				artifacts = map[webcontent.ArtifactKind][]byte{webcontent.ArtifactKindExtractedMarkdown: []byte(url)}
			}

			if cerr != nil {
				_ = send(&webcontentpb.FetchSessionResponse{
					CorrelationId: cid,
					Message:       &webcontentpb.FetchSessionResponse_Error{Error: contentErrorToProto(cerr)},
				})

				return
			}

			_ = send(&webcontentpb.FetchSessionResponse{
				CorrelationId: cid,
				Message: &webcontentpb.FetchSessionResponse_PageMetadata{
					PageMetadata: &webcontentpb.PageMetadata{RequestedUrl: url, FinalUrl: url, StatusCode: 200},
				},
			})

			for kind, data := range artifacts {
				_ = sendArtifactChunks(artifactKindToProtoKind(kind), data, func(a *webcontentpb.Artifact) error {
					return send(&webcontentpb.FetchSessionResponse{
						CorrelationId: cid,
						Message:       &webcontentpb.FetchSessionResponse_Artifact{Artifact: a},
					})
				})
			}

			_ = send(&webcontentpb.FetchSessionResponse{
				CorrelationId: cid,
				Message:       &webcontentpb.FetchSessionResponse_Done{Done: &webcontentpb.Summary{}},
			})
		})
	}
}

func (h *scrapeHandler) Map(_ context.Context, _ *connect.Request[webcontentpb.MapRequest], stream *connect.ServerStream[webcontentpb.MapResponse]) error {
	batchSize := h.fake.MapBatchSize
	if batchSize <= 0 {
		batchSize = 2
	}

	urls := h.fake.MapURLs
	for len(urls) > 0 {
		n := min(len(urls), batchSize)
		if err := stream.Send(&webcontentpb.MapResponse{
			Message: &webcontentpb.MapResponse_Batch{Batch: &webcontentpb.MapResponse_UrlBatch{Urls: urls[:n]}},
		}); err != nil {
			return err
		}

		urls = urls[n:]
	}

	return stream.Send(&webcontentpb.MapResponse{
		Message: &webcontentpb.MapResponse_Done{Done: &webcontentpb.MapResponse_Summary{
			SitemapsProcessed: 1,
			Warning:           h.fake.MapWarning,
		}},
	})
}
