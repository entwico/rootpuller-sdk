// Package protoconv converts between the exported common facade types and
// the generated com.entwico.rootpuller.common proto messages. Service
// packages keep their own service-specific conversions; only types shared
// across services live here.
package protoconv

import (
	"time"

	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"

	"github.com/entwico/rootpuller-sdk/common"
)

// Int32Ptr narrows a facade *int to the proto *int32 optional form.
func Int32Ptr(p *int) *int32 {
	if p == nil {
		return nil
	}
	v := int32(*p)
	return &v
}

// DurationToMsPtr converts an optional duration to proto optional
// milliseconds.
func DurationToMsPtr(p *time.Duration) *int32 {
	if p == nil {
		return nil
	}
	v := int32(p.Milliseconds())
	return &v
}

// DurationToSecondsPtr converts an optional duration to proto optional
// whole seconds.
func DurationToSecondsPtr(p *time.Duration) *int32 {
	if p == nil {
		return nil
	}
	v := int32(*p / time.Second)
	return &v
}

// MsToDuration converts proto milliseconds to a duration.
func MsToDuration(ms int64) time.Duration { return time.Duration(ms) * time.Millisecond }

// IntFromInt32 widens a proto int32 to a facade int.
func IntFromInt32(v int32) int { return int(v) }

func ToProtoNormalize(n *common.NormalizeOptions) *commonpb.NormalizeOptions {
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

func FromProtoTextChunk(c *commonpb.TextChunk) common.TextChunk {
	if c == nil {
		return common.TextChunk{}
	}
	return common.TextChunk{
		Text:       c.GetText(),
		StartIndex: int(c.GetStartIndex()),
		EndIndex:   int(c.GetEndIndex()),
		TokenCount: int(c.GetTokenCount()),
	}
}

func FromProtoTextChunks(chunks []*commonpb.TextChunk) []common.TextChunk {
	out := make([]common.TextChunk, len(chunks))
	for i, c := range chunks {
		out[i] = FromProtoTextChunk(c)
	}
	return out
}

func FromProtoUsage(u *commonpb.Usage) common.Usage {
	if u == nil {
		return common.Usage{}
	}
	return common.Usage{
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

func FromProtoPoint(p *commonpb.Point) common.Point {
	if p == nil {
		return common.Point{}
	}
	return common.Point{X: p.GetX(), Y: p.GetY()}
}

func FromProtoImageSize(s *commonpb.ImageSize) common.ImageSize {
	if s == nil {
		return common.ImageSize{}
	}
	return common.ImageSize{Width: int(s.GetWidth()), Height: int(s.GetHeight())}
}

func FromProtoBoundingBox(b *commonpb.BoundingBox) common.BoundingBox {
	if b == nil {
		return common.BoundingBox{}
	}
	return common.BoundingBox{
		Position: FromProtoPoint(b.GetPosition()),
		Size:     FromProtoImageSize(b.GetSize()),
	}
}

var imageFormatToProto = map[common.ImageFormat]commonpb.ImageFormat{
	common.ImageFormatUnspecified: commonpb.ImageFormat_IMAGE_FORMAT_UNSPECIFIED,
	common.ImageFormatJPG:         commonpb.ImageFormat_IMAGE_FORMAT_JPG,
	common.ImageFormatPNG:         commonpb.ImageFormat_IMAGE_FORMAT_PNG,
	common.ImageFormatWebP:        commonpb.ImageFormat_IMAGE_FORMAT_WEBP,
}

// ToProtoImageFormat maps a facade format to the proto enum; unknown
// values report ok=false so the caller can fail with InvalidArgument
// before any RPC.
func ToProtoImageFormat(f common.ImageFormat) (commonpb.ImageFormat, bool) {
	v, ok := imageFormatToProto[f]
	return v, ok
}
