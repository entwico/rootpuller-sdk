// Package webcontent wraps
// com.entwico.rootpuller.webcontent.WebContentService: fetching web pages
// with pluggable engines (basic HTTP, headless browser, anti-bot) and
// extracting structured content (Estratto/trafilatura) from fetched or
// uploaded HTML.
package webcontent

import (
	"context"
	"errors"
	"io"
	"iter"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/internal/apierr"
	webcontentpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/webcontent"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/webcontent/webcontentconnect"
	"github.com/entwico/rootpuller-sdk/internal/streamio"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// Service calls the WebContentService.
type Service struct {
	rpc webcontentconnect.WebContentServiceClient
	bot string
}

// Option configures a Service or a ScrapeService at construction. The
// same option values apply to both constructors.
type Option interface {
	applyWebContent(s *Service)
	applyScrape(s *ScrapeService)
}

// botOption implements Option for both service types.
type botOption struct{ name string }

func (o botOption) applyWebContent(s *Service) { s.bot = o.name }

func (o botOption) applyScrape(s *ScrapeService) { s.bot = o.name }

// WithBot sends the rootpuller-bot identity header on every call. A
// per-call rootpullersdk.ContextWithBot value still wins.
func WithBot(name string) Option { return botOption{name: name} }

// NewService builds a WebContentService client on the sdk connection.
func NewService(sdk *rootpullersdk.Client, opts ...Option) *Service {
	core := sdk.TransportCore()

	s := &Service{rpc: webcontentconnect.NewWebContentServiceClient(core.HTTPClient, core.BaseURL, core.ClientOpts...)}
	for _, opt := range opts {
		opt.applyWebContent(s)
	}

	return s
}

// FetchOptions tunes FetchURL and Fetch. Nil keeps all defaults.
type FetchOptions struct {
	Fetcher *FetcherOptions
	// Jobs are the extraction outputs to produce; empty fetches only.
	Jobs       []ExtractionJob
	Screenshot *ScreenshotOptions
}

func (o *FetchOptions) toProto(url string) (*webcontentpb.FetchUrlRequest, error) {
	if o == nil {
		o = &FetchOptions{}
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

	return &webcontentpb.FetchUrlRequest{Url: url, Fetcher: fetcher, Jobs: jobs, Screenshot: screenshot}, nil
}

func errSeq[E any](err error) iter.Seq2[E, error] {
	return func(yield func(E, error) bool) {
		var zero E
		yield(zero, err)
	}
}

// FetchURL calls WebContentService/FetchUrl: fetches the page and streams
// typed events (progress, page metadata, artifact chunks, per-kind
// errors) ending in a DoneEvent. A terminal error frame surfaces as the
// iteration error (*ContentError); a stream that ends without a terminal
// frame yields rootpullersdk.ErrMissingTerminal. For the buffered
// convenience form use Fetch. A nil opts keeps all defaults.
func (s *Service) FetchURL(ctx context.Context, url string, opts *FetchOptions) iter.Seq2[Event, error] {
	ctx = transport.EnsureBot(ctx, s.bot)

	msg, err := opts.toProto(url)
	if err != nil {
		return errSeq[Event](err)
	}

	stream, serr := s.rpc.FetchUrl(ctx, connect.NewRequest(msg))
	if serr != nil {
		return errSeq[Event](transport.WrapError(serr, webcontentconnect.WebContentServiceFetchUrlProcedure))
	}

	return streamio.EventSeq(stream, webcontentconnect.WebContentServiceFetchUrlProcedure, apierr.ErrMissingTerminal,
		func(pb *webcontentpb.FetchUrlResponse) (Event, bool, error) {
			switch m := pb.GetMessage().(type) {
			case *webcontentpb.FetchUrlResponse_FetchProgress:
				return fetchProgressFromProto(m.FetchProgress), false, nil
			case *webcontentpb.FetchUrlResponse_PageMetadata:
				return PageMetadataEvent{Metadata: pageMetadataFromProto(m.PageMetadata)}, false, nil
			case *webcontentpb.FetchUrlResponse_Artifact:
				return artifactChunkFromProto(m.Artifact), false, nil
			case *webcontentpb.FetchUrlResponse_ExtractionProgress:
				return extractionProgressFromProto(m.ExtractionProgress), false, nil
			case *webcontentpb.FetchUrlResponse_ExtractionMetadata:
				return ExtractionMetadataEvent{Metadata: extractionMetadataFromProto(m.ExtractionMetadata)}, false, nil
			case *webcontentpb.FetchUrlResponse_KindError:
				return kindErrorFromProto(m.KindError), false, nil
			case *webcontentpb.FetchUrlResponse_Error:
				return nil, true, contentErrorFromProto(m.Error)
			case *webcontentpb.FetchUrlResponse_Done:
				return DoneEvent{Summary: summaryFromProto(m.Done)}, true, nil
			default:
				return nil, false, apierr.New(connect.CodeInternal, "unknown FetchUrl response frame", webcontentconnect.WebContentServiceFetchUrlProcedure, 0, nil)
			}
		})
}

// FetchResult is the buffered outcome of a fetch/extract stream.
type FetchResult struct {
	Page       PageMetadata
	Extraction *ExtractionMetadata // nil when no extraction ran
	// Artifacts holds each produced artifact fully reassembled.
	Artifacts map[ArtifactKind][]byte
	// KindErrors holds per-artifact failures; the page itself succeeded.
	KindErrors map[ArtifactKind]*ContentError
	Summary    Summary
}

// Apply folds one event into the result. Progress events are ignored.
func (r *FetchResult) Apply(event Event) {
	switch e := event.(type) {
	case PageMetadataEvent:
		r.Page = e.Metadata
	case ExtractionMetadataEvent:
		meta := e.Metadata
		r.Extraction = &meta
	case ArtifactChunkEvent:
		if r.Artifacts == nil {
			r.Artifacts = map[ArtifactKind][]byte{}
		}

		if _, ok := r.Artifacts[e.Kind]; !ok {
			r.Artifacts[e.Kind] = []byte{}
		}

		r.Artifacts[e.Kind] = append(r.Artifacts[e.Kind], e.Data...)
	case KindErrorEvent:
		if r.KindErrors == nil {
			r.KindErrors = map[ArtifactKind]*ContentError{}
		}

		r.KindErrors[e.Kind] = e.Err
	case DoneEvent:
		r.Summary = e.Summary
	}
}

// CollectEvents drains an event stream into a FetchResult. It is the
// shared accumulator behind Fetch and the scrape helpers.
func CollectEvents(events iter.Seq2[Event, error]) (*FetchResult, error) {
	result := &FetchResult{}

	for event, err := range events {
		if err != nil {
			return nil, err
		}

		result.Apply(event)
	}

	return result, nil
}

// Fetch calls WebContentService/FetchUrl and buffers the whole stream
// into a FetchResult. Use FetchURL for incremental consumption. A nil
// opts keeps all defaults.
func (s *Service) Fetch(ctx context.Context, url string, opts *FetchOptions) (*FetchResult, error) {
	return CollectEvents(s.FetchURL(ctx, url, opts))
}

// HTMLDocument is the HTML input of ExtractContent.
type HTMLDocument struct {
	// Kind must be an HTML kind; the zero value defaults to
	// ArtifactKindHTMLRaw.
	Kind    ArtifactKind
	Content io.Reader
}

// ExtractOptions tunes ExtractContent. Nil keeps all defaults.
type ExtractOptions struct {
	Jobs []ExtractionJob
	// SourceURL improves relative-link resolution and metadata; empty
	// leaves it unset.
	SourceURL string
}

// ExtractResult is the outcome of ExtractContent.
type ExtractResult struct {
	Extraction *ExtractionMetadata
	Artifacts  map[ArtifactKind][]byte
	KindErrors map[ArtifactKind]*ContentError
	Summary    Summary
}

// ExtractContent calls WebContentService/ExtractContent: uploads an HTML
// document (init frame first, then chunks with an in-band last_chunk
// marker) and returns the extracted artifacts. The extractor buffers the
// whole document, so results arrive only after the upload completes. A
// nil opts keeps all defaults.
func (s *Service) ExtractContent(ctx context.Context, doc HTMLDocument, opts *ExtractOptions) (*ExtractResult, error) {
	ctx = transport.EnsureBot(ctx, s.bot)
	procedure := webcontentconnect.WebContentServiceExtractContentProcedure

	if opts == nil {
		opts = &ExtractOptions{}
	}

	kind := doc.Kind
	if kind == ArtifactKindUnspecified {
		kind = ArtifactKindHTMLRaw
	}

	switch kind {
	case ArtifactKindHTMLRaw, ArtifactKindHTMLPrerender, ArtifactKindHTMLRendered:
	case ArtifactKindUnspecified,
		ArtifactKindExtractedText, ArtifactKindExtractedMarkdown, ArtifactKindExtractedJSON,
		ArtifactKindExtractedXML, ArtifactKindExtractedCSV,
		ArtifactKindScreenshotPNG, ArtifactKindScreenshotJPEG, ArtifactKindScreenshotWebP:
		return nil, invalidArgument("document kind must be an HTML kind")
	default:
		return nil, invalidArgument("unknown document kind " + string(kind) + "; document kind must be an HTML kind")
	}

	kindPb, err := kind.toProto()
	if err != nil {
		return nil, err
	}

	jobs, err := jobsToProto(opts.Jobs)
	if err != nil {
		return nil, err
	}

	var sourceURL *string
	if opts.SourceURL != "" {
		sourceURL = &opts.SourceURL
	}

	stream := s.rpc.ExtractContent(ctx)
	frames := extractFrames(jobs, sourceURL, kindPb, doc.Content)

	result := &ExtractResult{}

	var terminalErr *ContentError

	sawTerminal := false

	err = streamio.UploadCollect(stream, procedure, frames, func(pb *webcontentpb.ExtractContentResponse) error {
		switch m := pb.GetMessage().(type) {
		case *webcontentpb.ExtractContentResponse_ExtractionMetadata:
			meta := extractionMetadataFromProto(m.ExtractionMetadata)
			result.Extraction = &meta
		case *webcontentpb.ExtractContentResponse_Artifact:
			if result.Artifacts == nil {
				result.Artifacts = map[ArtifactKind][]byte{}
			}

			k := artifactKindFrom(m.Artifact.GetKind())
			if _, ok := result.Artifacts[k]; !ok {
				result.Artifacts[k] = []byte{}
			}

			result.Artifacts[k] = append(result.Artifacts[k], m.Artifact.GetChunk()...)
		case *webcontentpb.ExtractContentResponse_KindError:
			if result.KindErrors == nil {
				result.KindErrors = map[ArtifactKind]*ContentError{}
			}

			e := kindErrorFromProto(m.KindError)
			result.KindErrors[e.Kind] = e.Err
		case *webcontentpb.ExtractContentResponse_Error:
			terminalErr = contentErrorFromProto(m.Error)
			sawTerminal = true
		case *webcontentpb.ExtractContentResponse_Done:
			result.Summary = summaryFromProto(m.Done)
			sawTerminal = true
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	if terminalErr != nil {
		return nil, terminalErr
	}

	if !sawTerminal {
		return nil, apierr.ErrMissingTerminal
	}

	return result, nil
}

// extractFrames yields the init frame, then the document as artifact
// chunks with last_chunk set on the final one (in-band terminator the
// extractor requires). An empty document still sends one empty last
// chunk.
func extractFrames(jobs []*webcontentpb.ExtractionJob, sourceURL *string, kind webcontentpb.Artifact_Kind, content io.Reader) iter.Seq2[*webcontentpb.ExtractContentRequest, error] {
	return func(yield func(*webcontentpb.ExtractContentRequest, error) bool) {
		if !yield(&webcontentpb.ExtractContentRequest{
			Message: &webcontentpb.ExtractContentRequest_Init_{Init: &webcontentpb.ExtractContentRequest_Init{
				Jobs:      jobs,
				SourceUrl: sourceURL,
			}},
		}, nil) {
			return
		}

		wrap := func(data []byte, last bool) *webcontentpb.ExtractContentRequest {
			return &webcontentpb.ExtractContentRequest{
				Message: &webcontentpb.ExtractContentRequest_Artifact{Artifact: &webcontentpb.Artifact{
					Kind:      kind,
					Chunk:     data,
					LastChunk: last,
				}},
			}
		}
		// One-chunk lookahead so the final chunk carries last_chunk.
		var pending []byte

		havePending := false

		buf := make([]byte, streamio.MaxChunkBytes)
		for {
			n, rerr := content.Read(buf)
			if n > 0 {
				if havePending {
					if !yield(wrap(pending, false), nil) {
						return
					}
				}

				pending = append(pending[:0], buf[:n]...)
				havePending = true
			}

			if rerr != nil {
				if errors.Is(rerr, io.EOF) {
					break
				}

				yield(nil, invalidArgument("reading HTML document: "+rerr.Error()))

				return
			}
		}

		if !havePending {
			pending = nil
		}

		yield(wrap(pending, true), nil)
	}
}
