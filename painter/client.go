// Package painter wraps com.entwico.rootpuller.painter.
// ImagePainterService: AI image generation, editing, and outpainting on
// the Google and OpenAI backends, plus generation and editing with
// self-hosted open-weight models on the comfy backend.
package painter

import (
	"context"
	"fmt"
	"iter"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"
	painterpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/painter"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/painter/painterconnect"
	"github.com/entwico/rootpuller-sdk/internal/streamio"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// Service calls the ImagePainterService.
type Service struct {
	rpc               painterconnect.ImagePainterServiceClient
	comfyUIDeployment string
}

// Option configures a Service at construction.
type Option func(*Service)

// WithComfyUIDeployment routes this service's comfy-backend calls at a
// named ComfyUI deployment. A per-call
// rootpullersdk.ContextWithComfyUIDeployment overrides it. Leave unset to use
// whichever deployment the server treats as default.
func WithComfyUIDeployment(name string) Option {
	return func(s *Service) { s.comfyUIDeployment = name }
}

// NewService builds an ImagePainterService client on the sdk connection.
func NewService(sdk *rootpullersdk.Client, opts ...Option) *Service {
	core := sdk.TransportCore()

	s := &Service{rpc: painterconnect.NewImagePainterServiceClient(core.HTTPClient, core.BaseURL, core.ClientOpts...)}
	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Generate calls ImagePainterService/GenerateImage: it sends the params
// frame, then each of opts.References in order (comfy backend only), and
// half-closes, then collects the response stream. Progress frames go to opts.OnProgress when set; the
// metadata frame and the reassembled images form the Result.
func (s *Service) Generate(ctx context.Context, prompt string, opts *GenerateOptions) (*Result, error) {
	if opts == nil {
		opts = &GenerateOptions{}
	}

	params, err := opts.toProto(prompt)
	if err != nil {
		return nil, err
	}

	ctx = transport.EnsureComfyUIDeployment(ctx, s.comfyUIDeployment)
	procedure := painterconnect.ImagePainterServiceGenerateImageProcedure

	frames := streamio.Frames(
		&painterpb.GenerateImageRequest{Request: &painterpb.GenerateImageRequest_Params_{Params: params}},
		referenceFrames(opts.References, func(chunk *commonpb.FileChunk) *painterpb.GenerateImageRequest {
			return &painterpb.GenerateImageRequest{Request: &painterpb.GenerateImageRequest_ReferenceImage{ReferenceImage: chunk}}
		})...,
	)

	var (
		collector streamio.FileCollector
		meta      Metadata
	)

	err = streamio.UploadCollect(ctx, s.rpc.GenerateImage, procedure, frames, func(resp *painterpb.GenerateImageResponse) error {
		switch r := resp.GetResponse().(type) {
		case *painterpb.GenerateImageResponse_Progress:
			if opts.OnProgress != nil {
				opts.OnProgress(progressFromProto(r.Progress))
			}
		case *painterpb.GenerateImageResponse_Metadata:
			meta = metadataFromProto(r.Metadata)
		case *painterpb.GenerateImageResponse_File:
			collector.Add(r.File)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return buildResult(&collector, meta)
}

// Edit calls ImagePainterService/EditImage: it sends the params frame
// first, then the input image chunks, then — when opts.Mask is non-nil —
// the mask image chunks, strictly in that order (the server enforces it),
// and half-closes. Progress frames go to opts.OnProgress when set; the
// metadata frame and the reassembled images form the Result.
func (s *Service) Edit(ctx context.Context, prompt string, image rootpullersdk.Upload, opts *EditOptions) (*Result, error) {
	if opts == nil {
		opts = &EditOptions{}
	}

	params, err := opts.toProto(prompt)
	if err != nil {
		return nil, err
	}

	ctx = transport.EnsureComfyUIDeployment(ctx, s.comfyUIDeployment)
	procedure := painterconnect.ImagePainterServiceEditImageProcedure

	tails := []iter.Seq2[*painterpb.EditImageRequest, error]{
		streamio.FileChunkFrames(image, func(chunk *commonpb.FileChunk) *painterpb.EditImageRequest {
			return &painterpb.EditImageRequest{Request: &painterpb.EditImageRequest_InputImage{InputImage: chunk}}
		}),
	}
	if opts.Mask != nil {
		tails = append(tails, streamio.FileChunkFrames(*opts.Mask, func(chunk *commonpb.FileChunk) *painterpb.EditImageRequest {
			return &painterpb.EditImageRequest{Request: &painterpb.EditImageRequest_MaskImage{MaskImage: chunk}}
		}))
	}

	frames := streamio.Frames(
		&painterpb.EditImageRequest{Request: &painterpb.EditImageRequest_Params_{Params: params}},
		tails...,
	)

	var (
		collector streamio.FileCollector
		meta      Metadata
	)

	err = streamio.UploadCollect(ctx, s.rpc.EditImage, procedure, frames, func(resp *painterpb.EditImageResponse) error {
		switch r := resp.GetResponse().(type) {
		case *painterpb.EditImageResponse_Progress:
			if opts.OnProgress != nil {
				opts.OnProgress(progressFromProto(r.Progress))
			}
		case *painterpb.EditImageResponse_Metadata:
			meta = metadataFromProto(r.Metadata)
		case *painterpb.EditImageResponse_File:
			collector.Add(r.File)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return buildResult(&collector, meta)
}

// Outpaint calls ImagePainterService/OutpaintImage: it sends the params
// frame first, then the input image chunks, in that order, and
// half-closes. Progress frames go to opts.OnProgress when set; the
// metadata frame and the reassembled images form the Result.
func (s *Service) Outpaint(ctx context.Context, prompt string, image rootpullersdk.Upload, opts *OutpaintOptions) (*Result, error) {
	if opts == nil {
		opts = &OutpaintOptions{}
	}

	params, err := opts.toProto(prompt)
	if err != nil {
		return nil, err
	}

	procedure := painterconnect.ImagePainterServiceOutpaintImageProcedure

	frames := streamio.Frames(
		&painterpb.OutpaintImageRequest{Request: &painterpb.OutpaintImageRequest_Params_{Params: params}},
		streamio.FileChunkFrames(image, func(chunk *commonpb.FileChunk) *painterpb.OutpaintImageRequest {
			return &painterpb.OutpaintImageRequest{Request: &painterpb.OutpaintImageRequest_InputImage{InputImage: chunk}}
		}),
	)

	var (
		collector streamio.FileCollector
		meta      Metadata
	)

	err = streamio.UploadCollect(ctx, s.rpc.OutpaintImage, procedure, frames, func(resp *painterpb.OutpaintImageResponse) error {
		switch r := resp.GetResponse().(type) {
		case *painterpb.OutpaintImageResponse_Progress:
			if opts.OnProgress != nil {
				opts.OnProgress(progressFromProto(r.Progress))
			}
		case *painterpb.OutpaintImageResponse_Metadata:
			meta = metadataFromProto(r.Metadata)
		case *painterpb.OutpaintImageResponse_File:
			collector.Add(r.File)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return buildResult(&collector, meta)
}

// RefineFaces redraws the faces in image at high resolution and blends them
// back, leaving every other pixel unchanged; the result has image's size.
// The prompt is optional extra guidance (e.g. skin texture) and may be
// empty. Faces are detected unless opts.FaceBoxes are given; when none is
// found the call fails with FailedPrecondition. opts and opts.Backend are
// required: RefineFacesComfy is the only backend.
func (s *Service) RefineFaces(
	ctx context.Context,
	prompt string,
	image rootpullersdk.Upload,
	opts *RefineFacesOptions,
) (*Result, error) {
	if opts == nil {
		opts = &RefineFacesOptions{}
	}

	params, err := opts.toProto(prompt)
	if err != nil {
		return nil, err
	}

	ctx = transport.EnsureComfyUIDeployment(ctx, s.comfyUIDeployment)
	procedure := painterconnect.ImagePainterServiceRefineFacesProcedure

	tails := append(
		[]iter.Seq2[*painterpb.RefineFacesRequest, error]{
			streamio.FileChunkFrames(image, func(chunk *commonpb.FileChunk) *painterpb.RefineFacesRequest {
				return &painterpb.RefineFacesRequest{Request: &painterpb.RefineFacesRequest_InputImage{InputImage: chunk}}
			}),
		},
		referenceFrames(opts.References, func(chunk *commonpb.FileChunk) *painterpb.RefineFacesRequest {
			return &painterpb.RefineFacesRequest{Request: &painterpb.RefineFacesRequest_ReferenceImage{ReferenceImage: chunk}}
		})...,
	)

	frames := streamio.Frames(
		&painterpb.RefineFacesRequest{Request: &painterpb.RefineFacesRequest_Params_{Params: params}},
		tails...,
	)

	var (
		collector streamio.FileCollector
		meta      Metadata
	)

	err = streamio.UploadCollect(ctx, s.rpc.RefineFaces, procedure, frames, func(resp *painterpb.RefineFacesResponse) error {
		switch r := resp.GetResponse().(type) {
		case *painterpb.RefineFacesResponse_Progress:
			if opts.OnProgress != nil {
				opts.OnProgress(progressFromProto(r.Progress))
			}
		case *painterpb.RefineFacesResponse_Metadata:
			meta = metadataFromProto(r.Metadata)
		case *painterpb.RefineFacesResponse_File:
			collector.Add(r.File)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return buildResult(&collector, meta)
}

// referenceFrames streams each reference as its own named file. The server
// starts a new reference whenever the chunk name changes, so names are made
// unique by position: two uploads both called "photo.jpg" stay two images.
func referenceFrames[R any](refs []rootpullersdk.Upload, wrap func(*commonpb.FileChunk) *R) []iter.Seq2[*R, error] {
	frames := make([]iter.Seq2[*R, error], 0, len(refs))

	for i, ref := range refs {
		ref.Name = fmt.Sprintf("ref_%d_%s", i+1, ref.Name)
		frames = append(frames, streamio.FileChunkFrames(ref, wrap))
	}

	return frames
}

func buildResult(collector *streamio.FileCollector, meta Metadata) (*Result, error) {
	files, err := collector.Files()
	if err != nil {
		return nil, err
	}

	return &Result{Images: files, Metadata: meta}, nil
}
