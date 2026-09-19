package rootpullertest

import (
	"context"
	"errors"
	"io"
	"net/http"

	"connectrpc.com/connect"

	webcontentpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/webcontent"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/webcontent/webcontentconnect"
	"github.com/entwico/rootpuller-sdk/webcontent"
)

// WebContent is a facade-typed fake WebContentService.
//
// FetchUrl streams: page metadata (Page or a canned default), the
// Artifacts in ≤2 MiB chunks, KindErrors, then done — or a terminal
// error frame when FetchError is set. OmitTerminal drops the terminal
// frame to simulate a protocol violation.
//
// ExtractContent enforces the init-first/last_chunk protocol and calls
// ExtractFunc with the reassembled upload; a nil ExtractFunc returns the
// upload as an extracted-markdown artifact.
type WebContent struct {
	Page         *webcontent.PageMetadata
	Artifacts    map[webcontent.ArtifactKind][]byte
	KindErrors   map[webcontent.ArtifactKind]*webcontent.ContentError
	FetchError   *webcontent.ContentError
	OmitTerminal bool

	ExtractFunc func(kind webcontent.ArtifactKind, html []byte) (map[webcontent.ArtifactKind][]byte, error)
}

func (f *WebContent) register(mux *http.ServeMux) {
	mux.Handle(webcontentconnect.NewWebContentServiceHandler(&webContentHandler{fake: f}))
}

type webContentHandler struct {
	webcontentconnect.UnimplementedWebContentServiceHandler

	fake *WebContent
}

// WebContent-specific protocol-violation sentinels.
var (
	errInitAfterInit          = errors.New("init received after stream already initialised")
	errFirstFrameNotInit      = errors.New("first frame must contain init")
	errUploadKindNotHTML      = errors.New("upload kind must be an HTML kind")
	errStreamClosedBeforeInit = errors.New("stream closed before init frame")
	errMissingLastChunk       = errors.New("upload ended without last_chunk")
)

func contentErrorToProto(e *webcontent.ContentError) *webcontentpb.ErrorDetail {
	code := webcontentpb.ErrorDetail_CODE_UNSPECIFIED

	if e.Code != "" {
		if v, ok := webcontentpb.ErrorDetail_Code_value["CODE_"+string(e.Code)]; ok {
			code = webcontentpb.ErrorDetail_Code(v)
		}
	}

	pb := &webcontentpb.ErrorDetail{Code: code, Message: e.Message}
	if e.HTTPStatus != 0 {
		status := int32(e.HTTPStatus) //nolint:gosec // HTTP status codes fit int32
		pb.HttpStatus = &status
	}

	if e.Retryable {
		pb.Retryable = &e.Retryable
	}

	if e.Throttled {
		pb.Throttled = &e.Throttled
	}

	if e.RetryAfter > 0 {
		ms := int32(e.RetryAfter.Milliseconds()) //nolint:gosec // test-fixture retry-after durations fit int32
		pb.RetryAfterMs = &ms
	}

	return pb
}

func artifactKindToProtoKind(k webcontent.ArtifactKind) webcontentpb.Artifact_Kind {
	name := map[webcontent.ArtifactKind]webcontentpb.Artifact_Kind{
		webcontent.ArtifactKindHTMLRaw:           webcontentpb.Artifact_KIND_HTML_RAW,
		webcontent.ArtifactKindHTMLPrerender:     webcontentpb.Artifact_KIND_HTML_PRERENDER,
		webcontent.ArtifactKindHTMLRendered:      webcontentpb.Artifact_KIND_HTML_RENDERED,
		webcontent.ArtifactKindExtractedText:     webcontentpb.Artifact_KIND_EXTRACTED_TXT,
		webcontent.ArtifactKindExtractedMarkdown: webcontentpb.Artifact_KIND_EXTRACTED_MARKDOWN,
		webcontent.ArtifactKindExtractedJSON:     webcontentpb.Artifact_KIND_EXTRACTED_JSON,
		webcontent.ArtifactKindExtractedXML:      webcontentpb.Artifact_KIND_EXTRACTED_XML,
		webcontent.ArtifactKindExtractedCSV:      webcontentpb.Artifact_KIND_EXTRACTED_CSV,
		webcontent.ArtifactKindScreenshotPNG:     webcontentpb.Artifact_KIND_SCREENSHOT_PNG,
		webcontent.ArtifactKindScreenshotJPEG:    webcontentpb.Artifact_KIND_SCREENSHOT_JPEG,
		webcontent.ArtifactKindScreenshotWebP:    webcontentpb.Artifact_KIND_SCREENSHOT_WEBP,
	}

	return name[k]
}

// isHTMLKind reports whether k is one of the HTML upload kinds accepted
// by ExtractContent.
func isHTMLKind(k webcontentpb.Artifact_Kind) bool {
	return k == webcontentpb.Artifact_KIND_HTML_RAW ||
		k == webcontentpb.Artifact_KIND_HTML_PRERENDER ||
		k == webcontentpb.Artifact_KIND_HTML_RENDERED
}

// sendArtifactChunks streams one artifact in ≤2 MiB chunks with
// last_chunk set on the final one, like the real server.
func sendArtifactChunks(kind webcontentpb.Artifact_Kind, data []byte, send func(*webcontentpb.Artifact) error) error {
	const chunkSize = 2 << 20
	for first := true; first || len(data) > 0; first = false {
		n := min(len(data), chunkSize)
		if err := send(&webcontentpb.Artifact{
			Kind:      kind,
			Chunk:     data[:n],
			LastChunk: n == len(data),
		}); err != nil {
			return err
		}

		data = data[n:]
	}

	return nil
}

//nolint:revive // FetchUrl implements the generated connect handler interface and cannot be renamed.
func (h *webContentHandler) FetchUrl(_ context.Context, req *connect.Request[webcontentpb.FetchUrlRequest], stream *connect.ServerStream[webcontentpb.FetchUrlResponse]) error {
	if h.fake.FetchError != nil {
		return stream.Send(&webcontentpb.FetchUrlResponse{
			Message: &webcontentpb.FetchUrlResponse_Error{Error: contentErrorToProto(h.fake.FetchError)},
		})
	}

	page := &webcontentpb.PageMetadata{
		RequestedUrl: req.Msg.GetUrl(),
		FinalUrl:     req.Msg.GetUrl(),
		StatusCode:   200,
		ContentType:  "text/html",
	}
	if h.fake.Page != nil {
		page.FinalUrl = h.fake.Page.FinalURL
		page.StatusCode = int32(h.fake.Page.StatusCode) //nolint:gosec // HTTP status codes fit int32
		page.ContentType = h.fake.Page.ContentType
	}

	if err := stream.Send(&webcontentpb.FetchUrlResponse{
		Message: &webcontentpb.FetchUrlResponse_PageMetadata{PageMetadata: page},
	}); err != nil {
		return err
	}

	for kind, data := range h.fake.Artifacts {
		if err := sendArtifactChunks(artifactKindToProtoKind(kind), data, func(a *webcontentpb.Artifact) error {
			return stream.Send(&webcontentpb.FetchUrlResponse{Message: &webcontentpb.FetchUrlResponse_Artifact{Artifact: a}})
		}); err != nil {
			return err
		}
	}

	for kind, cerr := range h.fake.KindErrors {
		if err := stream.Send(&webcontentpb.FetchUrlResponse{
			Message: &webcontentpb.FetchUrlResponse_KindError{KindError: &webcontentpb.KindError{
				Kind:  artifactKindToProtoKind(kind),
				Error: contentErrorToProto(cerr),
			}},
		}); err != nil {
			return err
		}
	}

	if h.fake.OmitTerminal {
		return nil
	}

	return stream.Send(&webcontentpb.FetchUrlResponse{
		Message: &webcontentpb.FetchUrlResponse_Done{Done: &webcontentpb.Summary{TotalBytes: 1}},
	})
}

func (h *webContentHandler) ExtractContent(_ context.Context, stream *connect.BidiStream[webcontentpb.ExtractContentRequest, webcontentpb.ExtractContentResponse]) error {
	var (
		initSeen bool
		kind     webcontentpb.Artifact_Kind
		html     []byte
		lastSeen bool
	)

	for {
		req, err := stream.Receive()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return err
		}

		switch m := req.GetMessage().(type) {
		case *webcontentpb.ExtractContentRequest_Init_:
			if initSeen {
				return invalidArgument(errInitAfterInit)
			}

			initSeen = true
		case *webcontentpb.ExtractContentRequest_Artifact:
			if !initSeen {
				return invalidArgument(errFirstFrameNotInit)
			}

			if !isHTMLKind(m.Artifact.GetKind()) {
				return invalidArgument(errUploadKindNotHTML)
			}

			kind = m.Artifact.GetKind()

			html = append(html, m.Artifact.GetChunk()...)
			if m.Artifact.GetLastChunk() {
				lastSeen = true
			}
		default:
			return invalidArgument(errUnexpectedVariant)
		}
	}

	if !initSeen {
		return invalidArgument(errStreamClosedBeforeInit)
	}

	if !lastSeen {
		return invalidArgument(errMissingLastChunk)
	}

	extract := h.fake.ExtractFunc
	if extract == nil {
		extract = func(_ webcontent.ArtifactKind, html []byte) (map[webcontent.ArtifactKind][]byte, error) {
			return map[webcontent.ArtifactKind][]byte{webcontent.ArtifactKindExtractedMarkdown: html}, nil
		}
	}

	facadeKind := map[webcontentpb.Artifact_Kind]webcontent.ArtifactKind{
		webcontentpb.Artifact_KIND_HTML_RAW:       webcontent.ArtifactKindHTMLRaw,
		webcontentpb.Artifact_KIND_HTML_PRERENDER: webcontent.ArtifactKindHTMLPrerender,
		webcontentpb.Artifact_KIND_HTML_RENDERED:  webcontent.ArtifactKindHTMLRendered,
	}[kind]

	artifacts, err := extract(facadeKind, html)
	if err != nil {
		var ce *webcontent.ContentError
		if errors.As(err, &ce) {
			return stream.Send(&webcontentpb.ExtractContentResponse{
				Message: &webcontentpb.ExtractContentResponse_Error{Error: contentErrorToProto(ce)},
			})
		}

		return err
	}

	for k, data := range artifacts {
		if err := sendArtifactChunks(artifactKindToProtoKind(k), data, func(a *webcontentpb.Artifact) error {
			return stream.Send(&webcontentpb.ExtractContentResponse{Message: &webcontentpb.ExtractContentResponse_Artifact{Artifact: a}})
		}); err != nil {
			return err
		}
	}

	if h.fake.OmitTerminal {
		return nil
	}

	return stream.Send(&webcontentpb.ExtractContentResponse{
		Message: &webcontentpb.ExtractContentResponse_Done{Done: &webcontentpb.Summary{TotalBytes: int64(len(html))}},
	})
}
