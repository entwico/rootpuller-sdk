// Package facefixer wraps
// com.entwico.rootpuller.facefixer.FaceFixerService: CodeFormer-based
// face restoration, colorization, and inpainting.
package facefixer

import (
	"context"
	"fmt"
	"iter"
	"time"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/internal/apierr"
	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"
	facefixerpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/facefixer"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/facefixer/facefixerconnect"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
	"github.com/entwico/rootpuller-sdk/internal/streamio"
)

// Service calls the FaceFixerService.
type Service struct {
	rpc facefixerconnect.FaceFixerServiceClient
}

// Option configures a Service at construction.
type Option func(*Service)

// NewService builds a FaceFixerService client on the sdk connection.
func NewService(sdk *rootpullersdk.Client, opts ...Option) *Service {
	core := sdk.TransportCore()

	s := &Service{rpc: facefixerconnect.NewFaceFixerServiceClient(core.HTTPClient, core.BaseURL, core.ClientOpts...)}
	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Landmarks are the five facial keypoints detected for a face.
type Landmarks struct {
	LeftEye    rootpullersdk.Point
	RightEye   rootpullersdk.Point
	Nose       rootpullersdk.Point
	LeftMouth  rootpullersdk.Point
	RightMouth rootpullersdk.Point
}

// FaceInfo describes one face the server detected and processed.
type FaceInfo struct {
	// OriginalBBox is the face bounding box in the input image.
	OriginalBBox rootpullersdk.BoundingBox
	// Confidence is the detection confidence.
	Confidence float32
	// Landmarks are the detected facial keypoints, nil when the server
	// did not report them.
	Landmarks *Landmarks
	// RestoredBBox is the face bounding box in the output image, nil when
	// the server did not report it.
	RestoredBBox *rootpullersdk.BoundingBox
}

// Metadata describes a face-fixing result.
type Metadata struct {
	// FacesDetected is the total number of faces detected in the input.
	FacesDetected int
	// UpscaleFactor is the upscaling factor that was applied.
	UpscaleFactor int
	// FidelityWeight is the quality/fidelity balance that was applied.
	FidelityWeight float32
	// OnlyCenterFace reports whether only the center face was processed.
	OnlyCenterFace bool
	// EnhanceBackground reports whether the background was enhanced.
	EnhanceBackground bool
	// ProcessingTime is the server-side processing duration.
	ProcessingTime time.Duration
	// InputSize is the input image size.
	InputSize rootpullersdk.ImageSize
	// OutputSize is the output image size.
	OutputSize rootpullersdk.ImageSize
	// Model is the model name used.
	Model string
	// Operation is the operation performed: "restore", "colorize", or
	// "inpaint"; empty when unspecified.
	Operation string
	// Faces describes each processed face.
	Faces []FaceInfo
	// MaskProvided reports whether a mask was supplied; set only for the
	// inpaint operation, nil otherwise.
	MaskProvided *bool
}

var operationFromProto = map[facefixerpb.Operation]string{
	facefixerpb.Operation_OPERATION_UNSPECIFIED: "",
	facefixerpb.Operation_OPERATION_RESTORE:     "restore",
	facefixerpb.Operation_OPERATION_COLORIZE:    "colorize",
	facefixerpb.Operation_OPERATION_INPAINT:     "inpaint",
}

func fromProtoFaceInfo(pb *facefixerpb.FaceInfo) FaceInfo {
	orig := pb.GetOriginal()

	fi := FaceInfo{
		OriginalBBox: protoconv.FromProtoBoundingBox(orig.GetBbox()),
		Confidence:   orig.GetConfidence(),
	}
	if lm := orig.GetLandmarks(); lm != nil {
		fi.Landmarks = &Landmarks{
			LeftEye:    protoconv.FromProtoPoint(lm.GetLeftEye()),
			RightEye:   protoconv.FromProtoPoint(lm.GetRightEye()),
			Nose:       protoconv.FromProtoPoint(lm.GetNose()),
			LeftMouth:  protoconv.FromProtoPoint(lm.GetLeftMouth()),
			RightMouth: protoconv.FromProtoPoint(lm.GetRightMouth()),
		}
	}

	if restored := pb.GetRestored(); restored != nil {
		box := protoconv.FromProtoBoundingBox(restored.GetBbox())
		fi.RestoredBBox = &box
	}

	return fi
}

func fromProtoMetadata(pb *facefixerpb.RestoreMetadata) Metadata {
	m := Metadata{
		FacesDetected:     int(pb.GetFacesDetectedTotal()),
		UpscaleFactor:     int(pb.GetUpscaleFactor()),
		FidelityWeight:    pb.GetFidelityWeight(),
		OnlyCenterFace:    pb.GetOnlyCenterFace(),
		EnhanceBackground: pb.GetEnhanceBackground(),
		ProcessingTime:    time.Duration(pb.GetProcessingTimeMs()) * time.Millisecond,
		InputSize:         protoconv.FromProtoImageSize(pb.GetInputSize()),
		OutputSize:        protoconv.FromProtoImageSize(pb.GetOutputSize()),
		Model:             pb.GetModel(),
		Operation:         operationFromProto[pb.GetOperation()],
	}
	if faces := pb.GetFaces(); len(faces) > 0 {
		m.Faces = make([]FaceInfo, len(faces))
		for i, f := range faces {
			m.Faces[i] = fromProtoFaceInfo(f)
		}
	}

	if pb.MaskProvided != nil {
		v := pb.GetMaskProvided()
		m.MaskProvided = &v
	}

	return m
}

// Result is the outcome of a face-fixing call: the processed image and
// the metadata the server reported for it.
type Result struct {
	File     rootpullersdk.File
	Metadata Metadata
}

// RestoreOptions tunes a RestoreFace call. Nil keeps all defaults.
type RestoreOptions struct {
	// Upscale is the upscaling factor, 2 or 4 (0 = server default 2).
	Upscale int
	// FidelityWeight balances quality against fidelity, 0.0-1.0 (default
	// 0.5). Nil keeps the server default; 0 is meaningful.
	FidelityWeight *float32
	// HasAligned marks the input as already face-aligned.
	HasAligned bool
	// OnlyCenterFace processes only the center face.
	OnlyCenterFace bool
	// EnhanceBackground enhances the background with RealESRGAN. Nil
	// keeps the server default (true).
	EnhanceBackground *bool
	// Format selects the output encoding.
	Format rootpullersdk.ImageFormat
}

func (o *RestoreOptions) toProto() (*facefixerpb.RestoreFaceRequest_Params, error) {
	format, err := toProtoFormat(o.Format)
	if err != nil {
		return nil, err
	}

	msg := &facefixerpb.RestoreFaceRequest_Params{
		FidelityWeight:    o.FidelityWeight,
		EnhanceBackground: o.EnhanceBackground,
		Format:            format,
	}
	if o.Upscale > 0 {
		msg.Upscale = protoconv.Int32Ptr(&o.Upscale)
	}

	if o.HasAligned {
		msg.HasAligned = &o.HasAligned
	}

	if o.OnlyCenterFace {
		msg.OnlyCenterFace = &o.OnlyCenterFace
	}

	return msg, nil
}

// ColorizeOptions tunes a ColorizeFace call. Nil keeps all defaults.
type ColorizeOptions struct {
	// Upscale is the upscaling factor, 2 or 4 (0 = server default 2).
	Upscale int
	// FidelityWeight balances quality against fidelity, 0.0-1.0 (default
	// 0.5). Nil keeps the server default; 0 is meaningful.
	FidelityWeight *float32
	// OnlyCenterFace processes only the center face.
	OnlyCenterFace bool
	// EnhanceBackground enhances the background with RealESRGAN. Nil
	// keeps the server default (true).
	EnhanceBackground *bool
	// Format selects the output encoding.
	Format rootpullersdk.ImageFormat
}

func (o *ColorizeOptions) toProto() (*facefixerpb.ColorizeFaceRequest_Params, error) {
	format, err := toProtoFormat(o.Format)
	if err != nil {
		return nil, err
	}

	msg := &facefixerpb.ColorizeFaceRequest_Params{
		FidelityWeight:    o.FidelityWeight,
		EnhanceBackground: o.EnhanceBackground,
		Format:            format,
	}
	if o.Upscale > 0 {
		msg.Upscale = protoconv.Int32Ptr(&o.Upscale)
	}

	if o.OnlyCenterFace {
		msg.OnlyCenterFace = &o.OnlyCenterFace
	}

	return msg, nil
}

// InpaintOptions tunes an InpaintFace call. Nil keeps all defaults.
type InpaintOptions struct {
	// Upscale is the upscaling factor, 2 or 4 (0 = server default 2).
	Upscale int
	// FidelityWeight balances quality against fidelity, 0.0-1.0 (default
	// 0.5). Nil keeps the server default; 0 is meaningful.
	FidelityWeight *float32
	// OnlyCenterFace processes only the center face.
	OnlyCenterFace bool
	// EnhanceBackground enhances the background with RealESRGAN. Nil
	// keeps the server default (true).
	EnhanceBackground *bool
	// Format selects the output encoding.
	Format rootpullersdk.ImageFormat
}

func (o *InpaintOptions) toProto() (*facefixerpb.InpaintFaceRequest_Params, error) {
	format, err := toProtoFormat(o.Format)
	if err != nil {
		return nil, err
	}

	msg := &facefixerpb.InpaintFaceRequest_Params{
		FidelityWeight:    o.FidelityWeight,
		EnhanceBackground: o.EnhanceBackground,
		Format:            format,
	}
	if o.Upscale > 0 {
		msg.Upscale = protoconv.Int32Ptr(&o.Upscale)
	}

	if o.OnlyCenterFace {
		msg.OnlyCenterFace = &o.OnlyCenterFace
	}

	return msg, nil
}

// toProtoFormat maps a facade format to the proto optional enum, nil when
// unspecified so the server default applies.
func toProtoFormat(f rootpullersdk.ImageFormat) (*commonpb.ImageFormat, error) {
	format, ok := protoconv.ToProtoImageFormat(f)
	if !ok {
		return nil, apierr.New(connect.CodeInvalidArgument, fmt.Sprintf("unknown image format %q", f), "", 0, nil)
	}

	if format == commonpb.ImageFormat_IMAGE_FORMAT_UNSPECIFIED {
		return nil, nil
	}

	return &format, nil
}

// collectResult drives one face-fixing bidi stream to completion and
// assembles the metadata and file frames into a Result. extract splits a
// response frame into its oneof arms; at most one of the two returns is
// non-nil.
func collectResult[Req, Resp any](
	stream *connect.BidiStreamForClient[Req, Resp],
	procedure string,
	frames iter.Seq2[*Req, error],
	extract func(*Resp) (*facefixerpb.RestoreMetadata, *commonpb.FileChunk),
) (*Result, error) {
	var (
		meta      Metadata
		collector streamio.FileCollector
	)

	err := streamio.UploadCollect(stream, procedure, frames, func(resp *Resp) error {
		m, f := extract(resp)
		switch {
		case m != nil:
			meta = fromProtoMetadata(m)
		case f != nil:
			collector.Add(f)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	files, err := collector.Files()
	if err != nil {
		return nil, err
	}

	if len(files) == 0 {
		return nil, apierr.New(connect.CodeInternal, "server returned no image", procedure, 0, nil)
	}

	return &Result{File: files[0], Metadata: meta}, nil
}

// RestoreFace calls FaceFixerService/RestoreFace: it sends the params
// frame first, then the image chunks, in that order, and half-closes. It
// returns the restored faces alongside the restoration metadata.
func (s *Service) RestoreFace(ctx context.Context, image rootpullersdk.Upload, opts *RestoreOptions) (*Result, error) {
	if opts == nil {
		opts = &RestoreOptions{}
	}

	pb, err := opts.toProto()
	if err != nil {
		return nil, err
	}

	procedure := facefixerconnect.FaceFixerServiceRestoreFaceProcedure
	stream := s.rpc.RestoreFace(ctx)

	frames := streamio.Frames(
		&facefixerpb.RestoreFaceRequest{Request: &facefixerpb.RestoreFaceRequest_Params_{Params: pb}},
		streamio.FileChunkFrames(image, func(chunk *commonpb.FileChunk) *facefixerpb.RestoreFaceRequest {
			return &facefixerpb.RestoreFaceRequest{Request: &facefixerpb.RestoreFaceRequest_File{File: chunk}}
		}),
	)

	return collectResult(stream, procedure, frames, func(resp *facefixerpb.RestoreFaceResponse) (*facefixerpb.RestoreMetadata, *commonpb.FileChunk) {
		return resp.GetMetadata(), resp.GetFile()
	})
}

// ColorizeFace calls FaceFixerService/ColorizeFace: it sends the params
// frame first, then the image chunks, in that order, and half-closes. It
// returns the colorized result alongside the processing metadata.
func (s *Service) ColorizeFace(ctx context.Context, image rootpullersdk.Upload, opts *ColorizeOptions) (*Result, error) {
	if opts == nil {
		opts = &ColorizeOptions{}
	}

	pb, err := opts.toProto()
	if err != nil {
		return nil, err
	}

	procedure := facefixerconnect.FaceFixerServiceColorizeFaceProcedure
	stream := s.rpc.ColorizeFace(ctx)

	frames := streamio.Frames(
		&facefixerpb.ColorizeFaceRequest{Request: &facefixerpb.ColorizeFaceRequest_Params_{Params: pb}},
		streamio.FileChunkFrames(image, func(chunk *commonpb.FileChunk) *facefixerpb.ColorizeFaceRequest {
			return &facefixerpb.ColorizeFaceRequest{Request: &facefixerpb.ColorizeFaceRequest_File{File: chunk}}
		}),
	)

	return collectResult(stream, procedure, frames, func(resp *facefixerpb.ColorizeFaceResponse) (*facefixerpb.RestoreMetadata, *commonpb.FileChunk) {
		return resp.GetMetadata(), resp.GetFile()
	})
}

// InpaintFace calls FaceFixerService/InpaintFace: it sends the params
// frame first, then the image chunks, then the binary mask chunks (white
// marks the regions to restore), strictly in that order, and half-closes.
// It returns the inpainted result alongside the processing metadata.
func (s *Service) InpaintFace(ctx context.Context, image, mask rootpullersdk.Upload, opts *InpaintOptions) (*Result, error) {
	if opts == nil {
		opts = &InpaintOptions{}
	}

	pb, err := opts.toProto()
	if err != nil {
		return nil, err
	}

	procedure := facefixerconnect.FaceFixerServiceInpaintFaceProcedure
	stream := s.rpc.InpaintFace(ctx)

	frames := streamio.Frames(
		&facefixerpb.InpaintFaceRequest{Request: &facefixerpb.InpaintFaceRequest_Params_{Params: pb}},
		streamio.FileChunkFrames(image, func(chunk *commonpb.FileChunk) *facefixerpb.InpaintFaceRequest {
			return &facefixerpb.InpaintFaceRequest{Request: &facefixerpb.InpaintFaceRequest_File{File: chunk}}
		}),
		streamio.FileChunkFrames(mask, func(chunk *commonpb.FileChunk) *facefixerpb.InpaintFaceRequest {
			return &facefixerpb.InpaintFaceRequest{Request: &facefixerpb.InpaintFaceRequest_MaskFile{MaskFile: chunk}}
		}),
	)

	return collectResult(stream, procedure, frames, func(resp *facefixerpb.InpaintFaceResponse) (*facefixerpb.RestoreMetadata, *commonpb.FileChunk) {
		return resp.GetMetadata(), resp.GetFile()
	})
}
