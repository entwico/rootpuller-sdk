// Package protoconv converts between the exported common facade types and
// the generated com.entwico.rootpuller.common proto messages. Service
// packages keep their own service-specific conversions; only types shared
// across services live here.
package protoconv

import (
	"math"
	"time"

	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
)

// ClampInt32 narrows v to int32, clamping to the int32 range instead of
// wrapping on overflow.
func ClampInt32(v int64) int32 {
	if v > math.MaxInt32 {
		return math.MaxInt32
	}

	if v < math.MinInt32 {
		return math.MinInt32
	}

	return int32(v)
}

// Int32Ptr narrows a facade *int to the proto *int32 optional form,
// clamping to the int32 range.
func Int32Ptr(p *int) *int32 {
	if p == nil {
		return nil
	}

	v := ClampInt32(int64(*p))

	return &v
}

// DurationToMsPtr converts an optional duration to proto optional
// milliseconds, clamping to the int32 range.
func DurationToMsPtr(p *time.Duration) *int32 {
	if p == nil {
		return nil
	}

	v := ClampInt32(p.Milliseconds())

	return &v
}

// DurationToSecondsPtr converts an optional duration to proto optional
// whole seconds, clamping to the int32 range.
func DurationToSecondsPtr(p *time.Duration) *int32 {
	if p == nil {
		return nil
	}

	v := ClampInt32(int64(*p / time.Second))

	return &v
}

// MsToDuration converts proto milliseconds to a duration.
func MsToDuration(ms int64) time.Duration { return time.Duration(ms) * time.Millisecond }

// IntFromInt32 widens a proto int32 to a facade int.
func IntFromInt32(v int32) int { return int(v) }

func ToProtoNormalize(n *rootpullersdk.NormalizeOptions) *commonpb.NormalizeOptions {
	if n == nil {
		return nil
	}

	return &commonpb.NormalizeOptions{
		NormalizeWhitespace: n.NormalizeWhitespace,
		NoLineBreaks:        n.NoLineBreaks,
		FixUnicode:          n.FixUnicode,
		ToAscii:             n.ToASCII,
		Lower:               n.Lower,
		NoUrls:              n.NoURLs,
		NoEmails:            n.NoEmails,
		NoPhoneNumbers:      n.NoPhoneNumbers,
		NoNumbers:           n.NoNumbers,
		NoCurrencySymbols:   n.NoCurrencySymbols,
		NoPunctuation:       n.NoPunctuation,
		NoEmoji:             n.NoEmoji,
	}
}

func FromProtoTextChunk(c *commonpb.TextChunk) rootpullersdk.TextChunk {
	if c == nil {
		return rootpullersdk.TextChunk{}
	}

	return rootpullersdk.TextChunk{
		Text:       c.GetText(),
		StartIndex: int(c.GetStartIndex()),
		EndIndex:   int(c.GetEndIndex()),
		TokenCount: int(c.GetTokenCount()),
	}
}

func FromProtoTextChunks(chunks []*commonpb.TextChunk) []rootpullersdk.TextChunk {
	out := make([]rootpullersdk.TextChunk, len(chunks))
	for i, c := range chunks {
		out[i] = FromProtoTextChunk(c)
	}

	return out
}

func FromProtoUsage(u *commonpb.Usage) rootpullersdk.Usage {
	if u == nil {
		return rootpullersdk.Usage{}
	}

	return rootpullersdk.Usage{
		InputTokens:             int(u.GetInputTokens()),
		CachedInputTokens:       int(u.GetCachedInputTokens()),
		OutputTokens:            int(u.GetOutputTokens()),
		EstimatedCostMicros:     u.GetEstimatedCostMicros(),
		CurrencyCode:            u.GetCurrencyCode(),
		InputPricePerMTok:       u.GetInputPricePerMtok(),
		OutputPricePerMTok:      u.GetOutputPricePerMtok(),
		CachedInputPricePerMTok: u.GetCachedInputPricePerMtok(),
	}
}

func FromProtoPoint(p *commonpb.Point) rootpullersdk.Point {
	if p == nil {
		return rootpullersdk.Point{}
	}

	return rootpullersdk.Point{X: p.GetX(), Y: p.GetY()}
}

func FromProtoImageSize(s *commonpb.ImageSize) rootpullersdk.ImageSize {
	if s == nil {
		return rootpullersdk.ImageSize{}
	}

	return rootpullersdk.ImageSize{Width: int(s.GetWidth()), Height: int(s.GetHeight())}
}

func FromProtoBoundingBox(b *commonpb.BoundingBox) rootpullersdk.BoundingBox {
	if b == nil {
		return rootpullersdk.BoundingBox{}
	}

	return rootpullersdk.BoundingBox{
		Position: FromProtoPoint(b.GetPosition()),
		Size:     FromProtoImageSize(b.GetSize()),
	}
}

var imageFormatToProto = map[rootpullersdk.ImageFormat]commonpb.ImageFormat{
	rootpullersdk.ImageFormatUnspecified: commonpb.ImageFormat_IMAGE_FORMAT_UNSPECIFIED,
	rootpullersdk.ImageFormatJPG:         commonpb.ImageFormat_IMAGE_FORMAT_JPG,
	rootpullersdk.ImageFormatPNG:         commonpb.ImageFormat_IMAGE_FORMAT_PNG,
	rootpullersdk.ImageFormatWebP:        commonpb.ImageFormat_IMAGE_FORMAT_WEBP,
}

// ToProtoImageFormat maps a facade format to the proto enum; unknown
// values report ok=false so the caller can fail with InvalidArgument
// before any RPC.
func ToProtoImageFormat(f rootpullersdk.ImageFormat) (commonpb.ImageFormat, bool) {
	v, ok := imageFormatToProto[f]

	return v, ok
}
