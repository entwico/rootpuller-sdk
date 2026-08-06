package webcontent

import (
	"fmt"

	"connectrpc.com/connect"

	"github.com/entwico/rootpuller-sdk/internal/apierr"
	webcontentpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/webcontent"
)

func invalidArgument(msg string) error {
	return apierr.New(connect.CodeInvalidArgument, msg, "", 0, nil)
}

// Engine selects the fetch engine. The zero value lets FetchURL
// auto-escalate (basic → dynamic → stealthy) and defaults to basic
// elsewhere.
type Engine string

const (
	EngineAuto     Engine = ""
	EngineBasic    Engine = "basic"
	EngineDynamic  Engine = "dynamic"
	EngineStealthy Engine = "stealthy"
)

var engineToProto = map[Engine]webcontentpb.FetcherOptions_Engine{
	EngineAuto:     webcontentpb.FetcherOptions_ENGINE_UNSPECIFIED,
	EngineBasic:    webcontentpb.FetcherOptions_ENGINE_BASIC,
	EngineDynamic:  webcontentpb.FetcherOptions_ENGINE_DYNAMIC,
	EngineStealthy: webcontentpb.FetcherOptions_ENGINE_STEALTHY,
}

var engineFromProto = map[webcontentpb.FetcherOptions_Engine]Engine{
	webcontentpb.FetcherOptions_ENGINE_UNSPECIFIED: EngineAuto,
	webcontentpb.FetcherOptions_ENGINE_BASIC:       EngineBasic,
	webcontentpb.FetcherOptions_ENGINE_DYNAMIC:     EngineDynamic,
	webcontentpb.FetcherOptions_ENGINE_STEALTHY:    EngineStealthy,
}

func engineFrom(v webcontentpb.FetcherOptions_Engine) Engine {
	if e, ok := engineFromProto[v]; ok {
		return e
	}

	return Engine(v.String())
}

// Method is the HTTP method for the basic fetch engine. The zero value
// keeps the server default (GET).
type Method string

const (
	MethodDefault Method = ""
	MethodGet     Method = "get"
	MethodPost    Method = "post"
	MethodPut     Method = "put"
	MethodDelete  Method = "delete"
)

var methodToProto = map[Method]webcontentpb.FetcherOptions_Basic_Method{
	MethodGet:    webcontentpb.FetcherOptions_Basic_METHOD_GET,
	MethodPost:   webcontentpb.FetcherOptions_Basic_METHOD_POST,
	MethodPut:    webcontentpb.FetcherOptions_Basic_METHOD_PUT,
	MethodDelete: webcontentpb.FetcherOptions_Basic_METHOD_DELETE,
}

// ScreenshotFormat selects the screenshot encoding. The zero value keeps
// the server default (PNG).
type ScreenshotFormat string

const (
	ScreenshotFormatDefault ScreenshotFormat = ""
	ScreenshotFormatPNG     ScreenshotFormat = "png"
	ScreenshotFormatJPEG    ScreenshotFormat = "jpeg"
	ScreenshotFormatWebP    ScreenshotFormat = "webp"
)

var screenshotFormatToProto = map[ScreenshotFormat]webcontentpb.ScreenshotOptions_Format{
	ScreenshotFormatDefault: webcontentpb.ScreenshotOptions_FORMAT_UNSPECIFIED,
	ScreenshotFormatPNG:     webcontentpb.ScreenshotOptions_FORMAT_PNG,
	ScreenshotFormatJPEG:    webcontentpb.ScreenshotOptions_FORMAT_JPEG,
	ScreenshotFormatWebP:    webcontentpb.ScreenshotOptions_FORMAT_WEBP,
}

// ArtifactKind identifies a produced (or uploaded) artifact type.
type ArtifactKind string

const (
	ArtifactKindUnspecified       ArtifactKind = ""
	ArtifactKindHTMLRaw           ArtifactKind = "html-raw"
	ArtifactKindHTMLPrerender     ArtifactKind = "html-prerender"
	ArtifactKindHTMLRendered      ArtifactKind = "html-rendered"
	ArtifactKindExtractedText     ArtifactKind = "extracted-txt"
	ArtifactKindExtractedMarkdown ArtifactKind = "extracted-markdown"
	ArtifactKindExtractedJSON     ArtifactKind = "extracted-json"
	ArtifactKindExtractedXML      ArtifactKind = "extracted-xml"
	ArtifactKindExtractedCSV      ArtifactKind = "extracted-csv"
	ArtifactKindScreenshotPNG     ArtifactKind = "screenshot-png"
	ArtifactKindScreenshotJPEG    ArtifactKind = "screenshot-jpeg"
	ArtifactKindScreenshotWebP    ArtifactKind = "screenshot-webp"
)

var artifactKindToProto = map[ArtifactKind]webcontentpb.Artifact_Kind{
	ArtifactKindUnspecified:       webcontentpb.Artifact_KIND_UNSPECIFIED,
	ArtifactKindHTMLRaw:           webcontentpb.Artifact_KIND_HTML_RAW,
	ArtifactKindHTMLPrerender:     webcontentpb.Artifact_KIND_HTML_PRERENDER,
	ArtifactKindHTMLRendered:      webcontentpb.Artifact_KIND_HTML_RENDERED,
	ArtifactKindExtractedText:     webcontentpb.Artifact_KIND_EXTRACTED_TXT,
	ArtifactKindExtractedMarkdown: webcontentpb.Artifact_KIND_EXTRACTED_MARKDOWN,
	ArtifactKindExtractedJSON:     webcontentpb.Artifact_KIND_EXTRACTED_JSON,
	ArtifactKindExtractedXML:      webcontentpb.Artifact_KIND_EXTRACTED_XML,
	ArtifactKindExtractedCSV:      webcontentpb.Artifact_KIND_EXTRACTED_CSV,
	ArtifactKindScreenshotPNG:     webcontentpb.Artifact_KIND_SCREENSHOT_PNG,
	ArtifactKindScreenshotJPEG:    webcontentpb.Artifact_KIND_SCREENSHOT_JPEG,
	ArtifactKindScreenshotWebP:    webcontentpb.Artifact_KIND_SCREENSHOT_WEBP,
}

var artifactKindFromProto = func() map[webcontentpb.Artifact_Kind]ArtifactKind {
	m := make(map[webcontentpb.Artifact_Kind]ArtifactKind, len(artifactKindToProto))
	for k, v := range artifactKindToProto {
		m[v] = k
	}

	return m
}()

func artifactKindFrom(v webcontentpb.Artifact_Kind) ArtifactKind {
	if k, ok := artifactKindFromProto[v]; ok {
		return k
	}

	return ArtifactKind(v.String())
}

func (k ArtifactKind) toProto() (webcontentpb.Artifact_Kind, error) {
	v, ok := artifactKindToProto[k]
	if !ok {
		return 0, invalidArgument(fmt.Sprintf("unknown artifact kind %q", k))
	}

	return v, nil
}

func artifactKindsFrom(vs []webcontentpb.Artifact_Kind) []ArtifactKind {
	if len(vs) == 0 {
		return nil
	}

	out := make([]ArtifactKind, len(vs))
	for i, v := range vs {
		out[i] = artifactKindFrom(v)
	}

	return out
}

// ErrorCode classifies a ContentError. Values mirror the proto
// ErrorDetail.Code names, e.g. "PAYWALL", "BLOCKED_CLOUDFLARE",
// "FETCH_TIMEOUT".
type ErrorCode string

const (
	ErrorCodeUnspecified            ErrorCode = ""
	ErrorCodeFetchNetworkError      ErrorCode = "FETCH_NETWORK_ERROR"
	ErrorCodeFetchTimeout           ErrorCode = "FETCH_TIMEOUT"
	ErrorCodeFetchProxyError        ErrorCode = "FETCH_PROXY_ERROR"
	ErrorCodeFetchTooManyRedirects  ErrorCode = "FETCH_TOO_MANY_REDIRECTS"
	ErrorCodeFetchRedirectLoop      ErrorCode = "FETCH_REDIRECT_LOOP"
	ErrorCodeHTTPNotFound           ErrorCode = "HTTP_NOT_FOUND"
	ErrorCodeHTTPForbidden          ErrorCode = "HTTP_FORBIDDEN"
	ErrorCodeHTTPUnauthorized       ErrorCode = "HTTP_UNAUTHORIZED"
	ErrorCodeHTTPRateLimited        ErrorCode = "HTTP_RATE_LIMITED"
	ErrorCodeHTTPServerError        ErrorCode = "HTTP_SERVER_ERROR"
	ErrorCodeHTTPOther              ErrorCode = "HTTP_OTHER"
	ErrorCodeBlockedCloudflare      ErrorCode = "BLOCKED_CLOUDFLARE"
	ErrorCodeBlockedGeneric         ErrorCode = "BLOCKED_GENERIC"
	ErrorCodeGeoBlocked             ErrorCode = "GEO_BLOCKED"
	ErrorCodeLoginWall              ErrorCode = "LOGIN_WALL"
	ErrorCodePaywall                ErrorCode = "PAYWALL"
	ErrorCodeNonHTMLContent         ErrorCode = "NON_HTML_CONTENT"
	ErrorCodePageTooLarge           ErrorCode = "PAGE_TOO_LARGE"
	ErrorCodeEmptyBody              ErrorCode = "EMPTY_BODY"
	ErrorCodeBlockedRobots          ErrorCode = "BLOCKED_ROBOTS"
	ErrorCodeBrowserError           ErrorCode = "BROWSER_ERROR"
	ErrorCodeExtractionNoContent    ErrorCode = "EXTRACTION_NO_CONTENT"
	ErrorCodeExtractionFormatFailed ErrorCode = "EXTRACTION_FORMAT_FAILED"
	ErrorCodeInternal               ErrorCode = "INTERNAL"
)

func errorCodeFrom(v webcontentpb.ErrorDetail_Code) ErrorCode {
	if v == webcontentpb.ErrorDetail_CODE_UNSPECIFIED {
		return ErrorCodeUnspecified
	}

	name := v.String() // e.g. "CODE_PAYWALL"

	const prefix = "CODE_"
	if len(name) > len(prefix) && name[:len(prefix)] == prefix {
		return ErrorCode(name[len(prefix):])
	}

	return ErrorCode(name)
}
