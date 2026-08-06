package webcontent

import (
	"fmt"
	"time"

	webcontentpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/webcontent"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
)

// RedirectHop is one hop of a redirect chain.
type RedirectHop struct {
	URL        string
	StatusCode int
	Location   string
}

func redirectChainFromProto(hops []*webcontentpb.RedirectHop) []RedirectHop {
	if len(hops) == 0 {
		return nil
	}

	out := make([]RedirectHop, len(hops))
	for i, h := range hops {
		out[i] = RedirectHop{URL: h.GetUrl(), StatusCode: int(h.GetStatusCode()), Location: h.GetLocation()}
	}

	return out
}

// ContentError is the typed domain error of the webcontent and scrape
// services: why a page could not be fetched or extracted. It surfaces as
// the iteration error of event streams (terminal error frames) and inside
// KindErrorEvent for per-artifact failures.
type ContentError struct {
	Code               ErrorCode
	Message            string
	HTTPStatus         int // 0 when not applicable
	ChallengeSignature string
	Retryable          bool
	Throttled          bool
	RetryAfter         time.Duration // 0 when unknown
	RedirectChain      []RedirectHop
}

func (e *ContentError) Error() string {
	msg := fmt.Sprintf("rootpuller webcontent: %s", e.Code)
	if e.HTTPStatus != 0 {
		msg += fmt.Sprintf(" (HTTP %d)", e.HTTPStatus)
	}

	if e.Message != "" {
		msg += ": " + e.Message
	}

	return msg
}

func contentErrorFromProto(pb *webcontentpb.ErrorDetail) *ContentError {
	if pb == nil {
		return nil
	}

	return &ContentError{
		Code:               errorCodeFrom(pb.GetCode()),
		Message:            pb.GetMessage(),
		HTTPStatus:         int(pb.GetHttpStatus()),
		ChallengeSignature: pb.GetChallengeSignature(),
		Retryable:          pb.GetRetryable(),
		Throttled:          pb.GetThrottled(),
		RetryAfter:         protoconv.MsToDuration(int64(pb.GetRetryAfterMs())),
		RedirectChain:      redirectChainFromProto(pb.GetRedirectChain()),
	}
}

// PageInfo is the page-declared metadata trafilatura recovered.
type PageInfo struct {
	Title       *string
	Author      *string
	URL         *string // page-declared canonical URL
	Hostname    *string
	Description *string
	SiteName    *string
	Date        *string // ISO 8601 when available
	Categories  []string
	Tags        []string
	License     *string
	Language    *string // page-declared; see ExtractionMetadata.DetectedLanguage
	Image       *string
	PageType    *string
}

// PageMetadata describes the fetched page.
type PageMetadata struct {
	RequestedURL     string
	FinalURL         string // post-redirect; persist this for provenance
	StatusCode       int
	Headers          []Header
	ContentType      string
	ContentLength    int64 // 0 if unknown
	FetchedAt        time.Time
	EngineUsed       Engine
	EnginesAttempted []Engine
	RedirectChain    []RedirectHop
	ETag             *string
	LastModified     *string
	NotModified      *bool
}

// ExtractionMetadata describes the extraction result as a whole.
type ExtractionMetadata struct {
	PageInfo         PageInfo
	DetectedLanguage *string
}

// Summary is the terminal accounting frame of a fetch/extract stream.
type Summary struct {
	TotalBytes      int64
	FetchDuration   time.Duration // 0 for extract-only streams
	ExtractDuration time.Duration // 0 for fetch-only streams
	KindsSucceeded  []ArtifactKind
	KindsFailed     []ArtifactKind
}

// Event is one frame of a fetch/extract event stream. Concrete types:
// FetchProgressEvent, PageMetadataEvent, ArtifactChunkEvent,
// ExtractionProgressEvent, ExtractionMetadataEvent, KindErrorEvent,
// DoneEvent.
type Event interface{ isEvent() }

// FetchProgressEvent reports fetch progress.
type FetchProgressEvent struct {
	Stage         string
	BytesReceived *int64 // nil when the engine cannot report it
	Elapsed       time.Duration
}

func (FetchProgressEvent) isEvent() {}

// PageMetadataEvent carries the page metadata (once per page).
type PageMetadataEvent struct {
	Metadata PageMetadata
}

func (PageMetadataEvent) isEvent() {}

// ArtifactChunkEvent carries one chunk of an output artifact. Last marks
// the final chunk of that artifact kind; SHA256 (hex, of the whole
// artifact) is set only on the last chunk.
type ArtifactChunkEvent struct {
	Kind   ArtifactKind
	Data   []byte
	Last   bool
	SHA256 string
}

func (ArtifactChunkEvent) isEvent() {}

// ExtractionProgressEvent reports extraction progress.
type ExtractionProgressEvent struct {
	Stage   string
	Elapsed time.Duration
}

func (ExtractionProgressEvent) isEvent() {}

// ExtractionMetadataEvent carries the extraction metadata (once).
type ExtractionMetadataEvent struct {
	Metadata ExtractionMetadata
}

func (ExtractionMetadataEvent) isEvent() {}

// KindErrorEvent reports that one requested artifact kind failed while
// the rest of the stream continues.
type KindErrorEvent struct {
	Kind ArtifactKind
	Err  *ContentError
}

func (KindErrorEvent) isEvent() {}

// DoneEvent is the successful terminal frame.
type DoneEvent struct {
	Summary Summary
}

func (DoneEvent) isEvent() {}

func pageMetadataFromProto(pb *webcontentpb.PageMetadata) PageMetadata {
	if pb == nil {
		return PageMetadata{}
	}

	attempted := make([]Engine, 0, len(pb.GetEnginesAttempted()))
	for _, e := range pb.GetEnginesAttempted() {
		attempted = append(attempted, engineFrom(e))
	}

	return PageMetadata{
		RequestedURL:     pb.GetRequestedUrl(),
		FinalURL:         pb.GetFinalUrl(),
		StatusCode:       int(pb.GetStatusCode()),
		Headers:          headersFromProto(pb.GetHeaders()),
		ContentType:      pb.GetContentType(),
		ContentLength:    pb.GetContentLength(),
		FetchedAt:        time.UnixMilli(pb.GetFetchedAtUnixMs()),
		EngineUsed:       engineFrom(pb.GetEngineUsed()),
		EnginesAttempted: attempted,
		RedirectChain:    redirectChainFromProto(pb.GetRedirectChain()),
		ETag:             pb.Etag,
		LastModified:     pb.LastModified,
		NotModified:      pb.NotModified,
	}
}

func extractionMetadataFromProto(pb *webcontentpb.ExtractionMetadata) ExtractionMetadata {
	if pb == nil {
		return ExtractionMetadata{}
	}

	info := pb.GetPageInfo()

	meta := ExtractionMetadata{DetectedLanguage: pb.DetectedLanguage}
	if info != nil {
		meta.PageInfo = PageInfo{
			Title:       info.Title,
			Author:      info.Author,
			URL:         info.Url,
			Hostname:    info.Hostname,
			Description: info.Description,
			SiteName:    info.Sitename,
			Date:        info.Date,
			Categories:  info.GetCategories(),
			Tags:        info.GetTags(),
			License:     info.License,
			Language:    info.Language,
			Image:       info.Image,
			PageType:    info.Pagetype,
		}
	}

	return meta
}

func summaryFromProto(pb *webcontentpb.Summary) Summary {
	if pb == nil {
		return Summary{}
	}

	return Summary{
		TotalBytes:      pb.GetTotalBytes(),
		FetchDuration:   protoconv.MsToDuration(pb.GetFetchDurationMs()),
		ExtractDuration: protoconv.MsToDuration(pb.GetExtractDurationMs()),
		KindsSucceeded:  artifactKindsFrom(pb.GetKindsSucceeded()),
		KindsFailed:     artifactKindsFrom(pb.GetKindsFailed()),
	}
}

func fetchProgressFromProto(pb *webcontentpb.FetchProgress) FetchProgressEvent {
	return FetchProgressEvent{
		Stage:         pb.GetStage(),
		BytesReceived: pb.BytesReceived,
		Elapsed:       protoconv.MsToDuration(pb.GetElapsedMs()),
	}
}

func extractionProgressFromProto(pb *webcontentpb.ExtractionProgress) ExtractionProgressEvent {
	return ExtractionProgressEvent{
		Stage:   pb.GetStage(),
		Elapsed: protoconv.MsToDuration(pb.GetElapsedMs()),
	}
}

func artifactChunkFromProto(pb *webcontentpb.Artifact) ArtifactChunkEvent {
	return ArtifactChunkEvent{
		Kind:   artifactKindFrom(pb.GetKind()),
		Data:   pb.GetChunk(),
		Last:   pb.GetLastChunk(),
		SHA256: pb.GetSha256(),
	}
}

func kindErrorFromProto(pb *webcontentpb.KindError) KindErrorEvent {
	return KindErrorEvent{
		Kind: artifactKindFrom(pb.GetKind()),
		Err:  contentErrorFromProto(pb.GetError()),
	}
}
