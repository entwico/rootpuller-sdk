package assetia

import (
	"fmt"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/internal/apierr"
	assetiapb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/assetia"
	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
)

// ImageMetadata describes a decoded image.
type ImageMetadata struct {
	Size   rootpullersdk.ImageSize
	Format rootpullersdk.ImageFormat
	// ColorSpace is the pixel color space, e.g. "srgb" or "cmyk".
	ColorSpace    string
	HasAlpha      bool
	HasICCProfile bool
	Channels      int
	// EXIFOrientation is the EXIF orientation tag: 1=normal, 3=180
	// degrees, 6=90 degrees CW, 8=90 degrees CCW; 0 when absent.
	EXIFOrientation int
	// Extra holds decoder-specific extras (EXIF tags, ...), nil when the
	// server reported none.
	Extra map[string]any
}

// VideoMetadata describes a probed video.
type VideoMetadata struct {
	Size rootpullersdk.ImageSize
	// Codec is the video codec name, e.g. "h264", "hevc", "av1".
	Codec     string
	Duration  time.Duration
	FrameRate float64
	// AudioCodec is the audio codec name, empty when the container has no
	// audio track.
	AudioCodec string
	// Extra holds prober-specific extras (bitrates, streams, ...), nil
	// when the server reported none.
	Extra map[string]any
}

// MediaMetadata is the metadata of either media kind: exactly one of
// Image and Video is non-nil.
type MediaMetadata struct {
	Image *ImageMetadata
	Video *VideoMetadata
}

// Resize is a target box; the media is fitted within it preserving
// aspect ratio. A zero Width or Height is derived from the other side.
type Resize struct {
	Width  int
	Height int
}

func (r *Resize) toProto() *assetiapb.Resize {
	if r == nil {
		return nil
	}

	return &assetiapb.Resize{
		Width:  protoconv.ClampInt32(int64(r.Width)),
		Height: protoconv.ClampInt32(int64(r.Height)),
	}
}

// Crop is the pixel region extracted from the source image before any
// other step.
type Crop struct {
	Left   int
	Top    int
	Width  int
	Height int
}

func (c *Crop) toProto() *assetiapb.TransformImageRequest_Crop {
	if c == nil {
		return nil
	}

	return &assetiapb.TransformImageRequest_Crop{
		Left:   protoconv.ClampInt32(int64(c.Left)),
		Top:    protoconv.ClampInt32(int64(c.Top)),
		Width:  protoconv.ClampInt32(int64(c.Width)),
		Height: protoconv.ClampInt32(int64(c.Height)),
	}
}

// Watermark overlays an image onto the transformed result. The watermark
// image is streamed to the server after the source image.
type Watermark struct {
	// Image is the watermark image to overlay.
	Image rootpullersdk.Upload
	// Opacity is the overlay opacity, 1-100 (0 = server default 100).
	Opacity int
}

// Output tunes the transformed image's encoding.
type Output struct {
	// Format selects the output encoding (unspecified = keep the source
	// format).
	Format rootpullersdk.ImageFormat
	// Quality is the jpg/webp/avif quality, 1-100 (0 = encoder default).
	Quality int
	// CompressionLevel is the png compression, 0-9. Nil keeps the encoder
	// default; 0 is meaningful.
	CompressionLevel *int
	// Lossless enables webp/avif lossless mode.
	Lossless bool
	// Interlace enables jpg progressive encoding.
	Interlace bool
}

func (o *Output) toProto() (*assetiapb.TransformImageRequest_Output, error) {
	format, ok := protoconv.ToProtoImageFormat(o.Format)
	if !ok {
		return nil, invalidArgument(fmt.Sprintf("unknown image format %q", o.Format))
	}

	msg := &assetiapb.TransformImageRequest_Output{
		Format:           format,
		CompressionLevel: protoconv.Int32Ptr(o.CompressionLevel),
	}
	if o.Quality > 0 {
		msg.Quality = protoconv.Int32Ptr(&o.Quality)
	}

	if o.Lossless {
		msg.Lossless = &o.Lossless
	}

	if o.Interlace {
		msg.Interlace = &o.Interlace
	}

	return msg, nil
}

// TransformImageOptions tunes a TransformImage call. Nil keeps all
// defaults (the image is re-encoded unchanged). Operations apply in
// fixed order: crop, rotate, resize, watermark, encode.
type TransformImageOptions struct {
	// Crop extracts a pixel region before any other step.
	Crop *Crop
	// Rotate rotates clockwise by the given degrees, a multiple of 90
	// (0 = no rotation).
	Rotate int
	// Resize fits the image within the given box, preserving aspect
	// ratio.
	Resize *Resize
	// Watermark overlays a watermark image.
	Watermark *Watermark
	// Output tunes the result encoding.
	Output *Output
}

func (o *TransformImageOptions) toProto() (*assetiapb.TransformImageRequest_Params, error) {
	msg := &assetiapb.TransformImageRequest_Params{
		Crop:   o.Crop.toProto(),
		Resize: o.Resize.toProto(),
	}
	if o.Rotate != 0 {
		msg.Rotate = protoconv.Int32Ptr(&o.Rotate)
	}

	if o.Watermark != nil {
		wm := &assetiapb.TransformImageRequest_Watermark{}
		if o.Watermark.Opacity > 0 {
			wm.Opacity = protoconv.Int32Ptr(&o.Watermark.Opacity)
		}

		msg.Watermark = wm
	}

	if o.Output != nil {
		out, err := o.Output.toProto()
		if err != nil {
			return nil, err
		}

		msg.Output = out
	}

	return msg, nil
}

// TransformImageResult is the outcome of a TransformImage call: the
// transformed image and the metadata the server reported for it.
type TransformImageResult struct {
	File rootpullersdk.File
	// Original describes the source image as the server decoded it.
	Original ImageMetadata
	// Result describes the transformed image.
	Result ImageMetadata
	// ProcessingTime is the server-side processing duration.
	ProcessingTime time.Duration
}

// H265Preset selects the x265 encoder speed/quality trade-off. The zero
// value keeps the server default (medium).
type H265Preset string

// The x265 presets, from fastest to slowest.
const (
	H265PresetDefault   H265Preset = ""
	H265PresetUltraFast H265Preset = "ultrafast"
	H265PresetSuperFast H265Preset = "superfast"
	H265PresetVeryFast  H265Preset = "veryfast"
	H265PresetFaster    H265Preset = "faster"
	H265PresetFast      H265Preset = "fast"
	H265PresetMedium    H265Preset = "medium"
	H265PresetSlow      H265Preset = "slow"
	H265PresetSlower    H265Preset = "slower"
	H265PresetVerySlow  H265Preset = "veryslow"
)

var h265PresetToProto = map[H265Preset]assetiapb.TransformVideoRequest_H265_Preset{
	H265PresetDefault:   assetiapb.TransformVideoRequest_H265_PRESET_UNSPECIFIED,
	H265PresetUltraFast: assetiapb.TransformVideoRequest_H265_PRESET_ULTRAFAST,
	H265PresetSuperFast: assetiapb.TransformVideoRequest_H265_PRESET_SUPERFAST,
	H265PresetVeryFast:  assetiapb.TransformVideoRequest_H265_PRESET_VERYFAST,
	H265PresetFaster:    assetiapb.TransformVideoRequest_H265_PRESET_FASTER,
	H265PresetFast:      assetiapb.TransformVideoRequest_H265_PRESET_FAST,
	H265PresetMedium:    assetiapb.TransformVideoRequest_H265_PRESET_MEDIUM,
	H265PresetSlow:      assetiapb.TransformVideoRequest_H265_PRESET_SLOW,
	H265PresetSlower:    assetiapb.TransformVideoRequest_H265_PRESET_SLOWER,
	H265PresetVerySlow:  assetiapb.TransformVideoRequest_H265_PRESET_VERYSLOW,
}

// toProto maps a facade preset to the proto optional enum, nil when
// unspecified so the server default applies.
func (p H265Preset) toProto() (*assetiapb.TransformVideoRequest_H265_Preset, error) {
	v, ok := h265PresetToProto[p]
	if !ok {
		return nil, invalidArgument(fmt.Sprintf("unknown H.265 preset %q", p))
	}

	if v == assetiapb.TransformVideoRequest_H265_PRESET_UNSPECIFIED {
		return nil, nil
	}

	return &v, nil
}

// Codec selects the target video codec for TransformVideo: H265 or AV1.
type Codec interface{ isCodec() }

// H265 selects the H.265 (HEVC) target codec.
type H265 struct {
	// Preset is the encoder speed/quality trade-off.
	Preset H265Preset
	// CRF is the constant rate factor, 0-51, 51 = worst (default 28).
	// Nil keeps the server default; 0 (lossless) is meaningful.
	CRF *int
}

func (H265) isCodec() {}

func (c H265) toProto() (*assetiapb.TransformVideoRequest_H265, error) {
	preset, err := c.Preset.toProto()
	if err != nil {
		return nil, err
	}

	return &assetiapb.TransformVideoRequest_H265{Preset: preset, Crf: protoconv.Int32Ptr(c.CRF)}, nil
}

// AV1 selects the AV1 target codec.
type AV1 struct {
	// Preset is the encoder speed/quality trade-off, 0-13 (default 8).
	// Nil keeps the server default; 0 (slowest/best) is meaningful.
	Preset *int
	// CRF is the constant rate factor, 0-63, 63 = worst (default 50).
	// Nil keeps the server default; 0 is meaningful.
	CRF *int
}

func (AV1) isCodec() {}

func (c AV1) toProto() *assetiapb.TransformVideoRequest_AV1 {
	return &assetiapb.TransformVideoRequest_AV1{Preset: protoconv.Int32Ptr(c.Preset), Crf: protoconv.Int32Ptr(c.CRF)}
}

// Progress is a transcoding progress report, emitted periodically while
// the encoder runs.
type Progress struct {
	// Percentage is the overall progress, 0-100.
	Percentage float32
	// FPS is the current encoding speed in frames per second (0 =
	// unreported).
	FPS float64
}

func progressFromProto(pb *assetiapb.TranscodeProgress) Progress {
	return Progress{Percentage: pb.GetPercentage(), FPS: pb.GetFps()}
}

// TransformVideoOptions tunes a TransformVideo call. Nil keeps all
// defaults (remux keeping the source codec).
type TransformVideoOptions struct {
	// Resize fits the video within the given box, preserving aspect
	// ratio.
	Resize *Resize
	// Codec is the target codec: H265 or AV1. Nil keeps the source codec
	// (resize-only / remux).
	Codec Codec
	// OnProgress, when set, receives transcoding progress as the server
	// reports it. Called synchronously from the receive loop.
	OnProgress func(Progress)
}

func (o *TransformVideoOptions) toProto() (*assetiapb.TransformVideoRequest_Params, error) {
	msg := &assetiapb.TransformVideoRequest_Params{Resize: o.Resize.toProto()}

	switch c := o.Codec.(type) {
	case nil:
	case H265:
		h265, err := c.toProto()
		if err != nil {
			return nil, err
		}

		msg.Codec = &assetiapb.TransformVideoRequest_Params_H265{H265: h265}
	case AV1:
		msg.Codec = &assetiapb.TransformVideoRequest_Params_Av1{Av1: c.toProto()}
	default:
		return nil, invalidArgument(fmt.Sprintf("TransformVideoOptions.Codec must be H265 or AV1, got %T", o.Codec))
	}

	return msg, nil
}

// TransformVideoResult is the outcome of a TransformVideo call: the
// transcoded video and the metadata the server reported for it.
type TransformVideoResult struct {
	File rootpullersdk.File
	// Original describes the source video as the server probed it.
	Original VideoMetadata
	// Result describes the transcoded video.
	Result VideoMetadata
	// ProcessingTime is the server-side processing duration.
	ProcessingTime time.Duration
}

// PreviewOptions tunes a GeneratePreview call. Nil keeps all defaults.
type PreviewOptions struct {
	// Resize fits the preview within the given box, preserving aspect
	// ratio.
	Resize *Resize
	// Format selects the preview encoding (unspecified = server default
	// webp).
	Format rootpullersdk.ImageFormat
	// FrameTime is the timestamp of the frame to capture from video
	// sources (0 = first frame).
	FrameTime time.Duration
}

func (o *PreviewOptions) toProto() (*assetiapb.GeneratePreviewRequest_Params, error) {
	format, ok := protoconv.ToProtoImageFormat(o.Format)
	if !ok {
		return nil, invalidArgument(fmt.Sprintf("unknown image format %q", o.Format))
	}

	msg := &assetiapb.GeneratePreviewRequest_Params{Resize: o.Resize.toProto(), Format: format}
	if o.FrameTime > 0 {
		ms := o.FrameTime.Milliseconds()
		msg.FrameTimeMs = &ms
	}

	return msg, nil
}

// PreviewResult is the outcome of a GeneratePreview call: the preview
// image and the metadata the server reported for it.
type PreviewResult struct {
	File rootpullersdk.File
	// Source describes the source media as the server decoded it.
	Source MediaMetadata
	// Result describes the preview image.
	Result ImageMetadata
	// ProcessingTime is the server-side processing duration.
	ProcessingTime time.Duration
}

var imageFormatFromProto = map[commonpb.ImageFormat]rootpullersdk.ImageFormat{
	commonpb.ImageFormat_IMAGE_FORMAT_UNSPECIFIED: rootpullersdk.ImageFormatUnspecified,
	commonpb.ImageFormat_IMAGE_FORMAT_JPG:         rootpullersdk.ImageFormatJPG,
	commonpb.ImageFormat_IMAGE_FORMAT_PNG:         rootpullersdk.ImageFormatPNG,
	commonpb.ImageFormat_IMAGE_FORMAT_WEBP:        rootpullersdk.ImageFormatWebP,
	commonpb.ImageFormat_IMAGE_FORMAT_AVIF:        rootpullersdk.ImageFormatAVIF,
}

// fromProtoExtra maps the optional google.protobuf.Struct extras to a
// plain map; a nil proto struct stays a nil map.
func fromProtoExtra(s *structpb.Struct) map[string]any {
	if s == nil {
		return nil
	}

	return s.AsMap()
}

func fromProtoImageMetadata(pb *assetiapb.ImageMetadata) ImageMetadata {
	return ImageMetadata{
		Size:            protoconv.FromProtoImageSize(pb.GetSize()),
		Format:          imageFormatFromProto[pb.GetFormat()],
		ColorSpace:      pb.GetColorSpace(),
		HasAlpha:        pb.GetHasAlpha(),
		HasICCProfile:   pb.GetHasIccProfile(),
		Channels:        int(pb.GetChannels()),
		EXIFOrientation: int(pb.GetExifOrientation()),
		Extra:           fromProtoExtra(pb.GetExtra()),
	}
}

func fromProtoVideoMetadata(pb *assetiapb.VideoMetadata) VideoMetadata {
	return VideoMetadata{
		Size:       protoconv.FromProtoImageSize(pb.GetSize()),
		Codec:      pb.GetCodec(),
		Duration:   protoconv.MsToDuration(pb.GetDurationMs()),
		FrameRate:  pb.GetFrameRate(),
		AudioCodec: pb.GetAudioCodec(),
		Extra:      fromProtoExtra(pb.GetExtra()),
	}
}

func fromProtoMediaMetadata(pb *assetiapb.MediaMetadata) MediaMetadata {
	var m MediaMetadata

	switch v := pb.GetMetadata().(type) {
	case *assetiapb.MediaMetadata_Image:
		img := fromProtoImageMetadata(v.Image)
		m.Image = &img
	case *assetiapb.MediaMetadata_Video:
		vid := fromProtoVideoMetadata(v.Video)
		m.Video = &vid
	}

	return m
}

func invalidArgument(msg string) error {
	return apierr.New(connect.CodeInvalidArgument, msg, "", 0, nil)
}
