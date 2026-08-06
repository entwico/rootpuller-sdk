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

	"github.com/entwico/rootpuller-sdk/apierror"
	"github.com/entwico/rootpuller-sdk/common"
	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"
	facefixerpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/facefixer"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/facefixer/facefixerconnect"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
	"github.com/entwico/rootpuller-sdk/internal/streamio"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// Client calls the FaceFixerService. Obtain one from rootpuller.Client.
type Client struct {
	rpc facefixerconnect.FaceFixerServiceClient
}

// NewFromCore is the internal constructor used by rootpuller.New.
func NewFromCore(core *transport.Core) *Client {
	return &Client{rpc: facefixerconnect.NewFaceFixerServiceClient(core.HTTPClient, core.BaseURL, core.ClientOpts...)}
}

// Landmarks are the five facial keypoints detected for a face.
type Landmarks struct {
	LeftEye    common.Point
	RightEye   common.Point
	Nose       common.Point
	LeftMouth  common.Point
	RightMouth common.Point
}

// FaceInfo describes one face the server detected and processed.
type FaceInfo struct {
	// OriginalBBox is the face bounding box in the input image.
	OriginalBBox common.BoundingBox
	// Confidence is the detection confidence.
	Confidence float32
	// Landmarks are the detected facial keypoints, nil when the server
	// did not report them.
	Landmarks *Landmarks
	// RestoredBBox is the face bounding box in the output image, nil when
	// the server did not report it.
	RestoredBBox *common.BoundingBox
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
	InputSize common.ImageSize
	// OutputSize is the output image size.
	OutputSize common.ImageSize
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
	File     common.File
	Metadata Metadata
}

// RestoreParams configures RestoreFace. Nil pointer fields keep the
// server defaults.
type RestoreParams struct {
	// Upscale is the upscaling factor, 2 or 4 (default 2).
	Upscale *int
	// FidelityWeight balances quality against fidelity, 0.0-1.0 (default
	// 0.5).
	FidelityWeight *float32
	// HasAligned marks the input as already face-aligned (default false).
	HasAligned *bool
	// OnlyCenterFace processes only the center face (default false).
	OnlyCenterFace *bool
	// EnhanceBackground enhances the background with RealESRGAN (default
	// true).
	EnhanceBackground *bool
	// Format selects the output encoding.
	Format common.ImageFormat
}

func (p *RestoreParams) toProto() (*facefixerpb.RestoreFaceRequest_Params, error) {
	format, err := toProtoFormat(p.Format)
	if err != nil {
		return nil, err
	}

	return &facefixerpb.RestoreFaceRequest_Params{
		Upscale:           protoconv.Int32Ptr(p.Upscale),
		FidelityWeight:    p.FidelityWeight,
		HasAligned:        p.HasAligned,
		OnlyCenterFace:    p.OnlyCenterFace,
		EnhanceBackground: p.EnhanceBackground,
		Format:            format,
	}, nil
}

// ColorizeParams configures ColorizeFace. Nil pointer fields keep the
// server defaults.
type ColorizeParams struct {
	// Upscale is the upscaling factor, 2 or 4 (default 2).
	Upscale *int
	// FidelityWeight balances quality against fidelity, 0.0-1.0 (default
	// 0.5).
	FidelityWeight *float32
	// OnlyCenterFace processes only the center face (default false).
	OnlyCenterFace *bool
	// EnhanceBackground enhances the background with RealESRGAN (default
	// true).
	EnhanceBackground *bool
	// Format selects the output encoding.
	Format common.ImageFormat
}

func (p *ColorizeParams) toProto() (*facefixerpb.ColorizeFaceRequest_Params, error) {
	format, err := toProtoFormat(p.Format)
	if err != nil {
		return nil, err
	}

	return &facefixerpb.ColorizeFaceRequest_Params{
		Upscale:           protoconv.Int32Ptr(p.Upscale),
		FidelityWeight:    p.FidelityWeight,
		OnlyCenterFace:    p.OnlyCenterFace,
		EnhanceBackground: p.EnhanceBackground,
		Format:            format,
	}, nil
}

// InpaintParams configures InpaintFace. Nil pointer fields keep the
// server defaults.
type InpaintParams struct {
	// Upscale is the upscaling factor, 2 or 4 (default 2).
	Upscale *int
	// FidelityWeight balances quality against fidelity, 0.0-1.0 (default
	// 0.5).
	FidelityWeight *float32
	// OnlyCenterFace processes only the center face (default false).
	OnlyCenterFace *bool
	// EnhanceBackground enhances the background with RealESRGAN (default
	// true).
	EnhanceBackground *bool
	// Format selects the output encoding.
	Format common.ImageFormat
}

func (p *InpaintParams) toProto() (*facefixerpb.InpaintFaceRequest_Params, error) {
	format, err := toProtoFormat(p.Format)
	if err != nil {
		return nil, err
	}

	return &facefixerpb.InpaintFaceRequest_Params{
		Upscale:           protoconv.Int32Ptr(p.Upscale),
		FidelityWeight:    p.FidelityWeight,
		OnlyCenterFace:    p.OnlyCenterFace,
		EnhanceBackground: p.EnhanceBackground,
		Format:            format,
	}, nil
}

// toProtoFormat maps a facade format to the proto optional enum, nil when
// unspecified so the server default applies.
func toProtoFormat(f common.ImageFormat) (*commonpb.ImageFormat, error) {
	format, ok := protoconv.ToProtoImageFormat(f)
	if !ok {
		return nil, apierror.New(connect.CodeInvalidArgument, fmt.Sprintf("unknown image format %q", f), "", 0, nil)
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
		return nil, apierror.New(connect.CodeInternal, "server returned no image", procedure, 0, nil)
	}

	return &Result{File: files[0], Metadata: meta}, nil
}

// RestoreFace calls FaceFixerService/RestoreFace: streams the image up,
// then returns the restored faces alongside the restoration metadata.
func (c *Client) RestoreFace(ctx context.Context, params *RestoreParams, image common.Upload) (*Result, error) {
	pb, err := params.toProto()
	if err != nil {
		return nil, err
	}

	procedure := facefixerconnect.FaceFixerServiceRestoreFaceProcedure
	stream := c.rpc.RestoreFace(ctx)

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

// ColorizeFace calls FaceFixerService/ColorizeFace: streams the image up,
// then returns the colorized result alongside the processing metadata.
func (c *Client) ColorizeFace(ctx context.Context, params *ColorizeParams, image common.Upload) (*Result, error) {
	pb, err := params.toProto()
	if err != nil {
		return nil, err
	}

	procedure := facefixerconnect.FaceFixerServiceColorizeFaceProcedure
	stream := c.rpc.ColorizeFace(ctx)

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

// InpaintFace calls FaceFixerService/InpaintFace: streams the image and
// the binary mask (white marks the regions to restore) up, then returns
// the inpainted result alongside the processing metadata.
func (c *Client) InpaintFace(ctx context.Context, params *InpaintParams, image common.Upload, mask common.Upload) (*Result, error) {
	pb, err := params.toProto()
	if err != nil {
		return nil, err
	}

	procedure := facefixerconnect.FaceFixerServiceInpaintFaceProcedure
	stream := c.rpc.InpaintFace(ctx)

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
