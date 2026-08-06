// Package painter wraps com.entwico.rootpuller.painter.
// ImagePainterService: AI image generation, editing, and outpainting on
// the Google Imagen and OpenAI backends.
package painter

import (
	"context"
	"iter"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"
	painterpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/painter"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/painter/painterconnect"
	"github.com/entwico/rootpuller-sdk/internal/streamio"
)

// Service calls the ImagePainterService.
type Service struct {
	rpc painterconnect.ImagePainterServiceClient
}

// Option configures a Service at construction.
type Option func(*Service)

// NewService builds an ImagePainterService client on the sdk connection.
func NewService(sdk *rootpullersdk.Client, opts ...Option) *Service {
	core := sdk.TransportCore()

	s := &Service{rpc: painterconnect.NewImagePainterServiceClient(core.HTTPClient, core.BaseURL, core.ClientOpts...)}
	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Generate calls ImagePainterService/GenerateImage: it sends exactly one
// params frame, half-closes (there is no upload), and collects the
// response stream. Progress frames go to opts.OnProgress when set; the
// metadata frame and the reassembled images form the Result.
func (s *Service) Generate(ctx context.Context, prompt string, opts *GenerateOptions) (*Result, error) {
	if opts == nil {
		opts = &GenerateOptions{}
	}

	params, err := opts.toProto(prompt)
	if err != nil {
		return nil, err
	}

	procedure := painterconnect.ImagePainterServiceGenerateImageProcedure
	stream := s.rpc.GenerateImage(ctx)

	frames := streamio.Frames(
		&painterpb.GenerateImageRequest{Request: &painterpb.GenerateImageRequest_Params_{Params: params}},
	)

	var (
		collector streamio.FileCollector
		meta      Metadata
	)

	err = streamio.UploadCollect(stream, procedure, frames, func(resp *painterpb.GenerateImageResponse) error {
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

	procedure := painterconnect.ImagePainterServiceEditImageProcedure
	stream := s.rpc.EditImage(ctx)

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

	err = streamio.UploadCollect(stream, procedure, frames, func(resp *painterpb.EditImageResponse) error {
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
	stream := s.rpc.OutpaintImage(ctx)

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

	err = streamio.UploadCollect(stream, procedure, frames, func(resp *painterpb.OutpaintImageResponse) error {
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

func buildResult(collector *streamio.FileCollector, meta Metadata) (*Result, error) {
	files, err := collector.Files()
	if err != nil {
		return nil, err
	}

	return &Result{Images: files, Metadata: meta}, nil
}
